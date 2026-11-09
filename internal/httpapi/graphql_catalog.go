package httpapi

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

type graphPageInfo struct {
	EndCursor   string
	HasNextPage bool
}
type graphImageConnection struct {
	Nodes      []*graphImage
	TotalCount int32
	PageInfo   graphPageInfo
}
type graphRepositoryConnection struct {
	Nodes      []string
	TotalCount int32
	PageInfo   graphPageInfo
}
type graphImageFilter struct{ Search, Organization *string }
type graphImageArgs struct {
	First     int32
	After     string
	Filter    *graphImageFilter
	Sort      string
	Direction string
}
type imagePosition struct {
	Repository string
	Tag        string
	Digest     string
	Value      int64
	Missing    bool
}
type catalogCursor struct {
	Version  int
	Scope    string
	Position imagePosition
}

func encodeCursor(scope string, position imagePosition) string {
	data, _ := json.Marshal(catalogCursor{1, scope, position})
	return base64.RawURLEncoding.EncodeToString(data)
}
func decodeCursor(value, scope string) (imagePosition, error) {
	var cursor catalogCursor
	if len(value) > 4096 {
		return cursor.Position, invalidGraph("Invalid cursor")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Version != 1 || cursor.Scope != scope || cursor.Position.Repository == "" {
		return cursor.Position, invalidGraph("Cursor does not match this query")
	}
	return cursor.Position, nil
}
func cursorScope(parts ...string) string {
	data, _ := json.Marshal(parts)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func validPageSize(first int32) error {
	if first < 1 || first > 100 {
		return invalidGraph("first must be between 1 and 100")
	}
	return nil
}

func (r *graphResolver) catalogRepositories(ctx context.Context) ([]string, error) {
	request := ctx.Value(graphRequestKey{}).(*graphRequest)
	request.repositoriesOnce.Do(func() {
		names, err := r.server.registry.Repositories(ctx)
		request.repositoriesErr = graphRegistryError(err)
		request.repositories = slices.Clone(names)
		slices.Sort(request.repositories)
		request.repositories = slices.Compact(request.repositories)
	})
	return request.repositories, request.repositoriesErr
}

func (r *graphResolver) Repositories(ctx context.Context, args struct {
	First         int32
	After, Search string
}) (*graphRepositoryConnection, error) {
	if err := validPageSize(args.First); err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(args.Search))
	scope := cursorScope("repositories", search)
	var after string
	if args.After != "" {
		position, err := decodeCursor(args.After, scope)
		if err != nil {
			return nil, err
		}
		after = position.Repository
	}
	names, err := r.catalogRepositories(ctx)
	if err != nil {
		return nil, err
	}
	result := &graphRepositoryConnection{Nodes: []string{}}
	for _, name := range names {
		if !strings.Contains(name, search) {
			continue
		}
		result.TotalCount++
		if name <= after {
			continue
		}
		if len(result.Nodes) == int(args.First) {
			result.PageInfo.HasNextPage = true
			continue
		}
		result.Nodes = append(result.Nodes, name)
	}
	if len(result.Nodes) > 0 {
		result.PageInfo.EndCursor = encodeCursor(scope, imagePosition{Repository: result.Nodes[len(result.Nodes)-1]})
	}
	return result, nil
}

func (r *graphResolver) catalogImages(ctx context.Context) ([]*graphImage, error) {
	request := ctx.Value(graphRequestKey{}).(*graphRequest)
	request.imagesOnce.Do(func() {
		request.images, request.imagesErr = r.cachedCatalog(ctx, func() ([]*graphImage, error) {
			names, err := r.catalogRepositories(ctx)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			groups := make([][]*graphImage, len(names))
			jobs := make(chan int)
			var workers sync.WaitGroup
			var failed sync.Once
			var scanErr error
			// Fixed worker count avoids one waiting goroutine per repository. The
			// existing per-server semaphore also bounds all simultaneous requests.
			for range min(4, len(names)) {
				workers.Go(func() {
					for i := range jobs {
						images, err := r.server.repositoryImages(ctx, names[i])
						if e, ok := errors.AsType[*registry.Error](err); ok && e.StatusCode == http.StatusNotFound {
							continue
						}
						if err != nil {
							failed.Do(func() { scanErr = graphRegistryError(err); cancel() })
							return
						}
						for _, image := range images {
							groups[i] = append(groups[i], imageGraph(names[i], image))
						}
					}
				})
			}
		feed:
			for i := range names {
				select {
				case jobs <- i:
				case <-ctx.Done():
					break feed
				}
			}
			close(jobs)
			workers.Wait()
			if scanErr != nil {
				return nil, scanErr
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			images := []*graphImage{}
			for _, group := range groups {
				images = append(images, group...)
			}
			return images, nil
		})
	})
	return request.images, request.imagesErr
}

func imageSortPosition(image *graphImage, sort string) imagePosition {
	p := imagePosition{Repository: image.Repository, Tag: image.Tag, Digest: image.Digest}
	if sort == "SIZE" {
		p.Value = int64(image.Size)
		return p
	}
	value := image.LastUsed
	if sort == "CREATED" {
		value = image.Created
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	p.Missing = err != nil
	if err == nil {
		p.Value = timestamp.UnixNano()
	}
	return p
}
func comparePositions(a, b imagePosition, direction string) int {
	if a.Missing != b.Missing {
		if a.Missing {
			return 1
		}
		return -1
	}
	if !a.Missing {
		order := cmp.Compare(a.Value, b.Value)
		if direction == "DESC" {
			order = -order
		}
		if order != 0 {
			return order
		}
	}
	return cmp.Or(cmp.Compare(a.Repository, b.Repository), cmp.Compare(a.Tag, b.Tag), cmp.Compare(a.Digest, b.Digest))
}
func (r *graphResolver) Images(ctx context.Context, args graphImageArgs) (*graphImageConnection, error) {
	if err := validPageSize(args.First); err != nil {
		return nil, err
	}
	var search, organization string
	if args.Filter != nil {
		if args.Filter.Search != nil {
			search = strings.ToLower(strings.TrimSpace(*args.Filter.Search))
		}
		if args.Filter.Organization != nil {
			organization = strings.TrimSpace(*args.Filter.Organization)
		}
	}
	if len(search) > 256 || (organization != "" && !validRepository(organization)) {
		return nil, invalidGraph("Invalid image filter")
	}
	scope := cursorScope("images", search, organization, args.Sort, args.Direction)
	var after imagePosition
	if args.After != "" {
		var err error
		after, err = decodeCursor(args.After, scope)
		if err != nil {
			return nil, err
		}
	}
	images, err := r.catalogImages(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]*graphImage, 0, len(images))
	for _, image := range images {
		if organization != "" && !strings.HasPrefix(image.Repository, organization+"/") {
			continue
		}
		if strings.HasSuffix(search, "/") {
			if !strings.HasPrefix(image.Repository, search) {
				continue
			}
		} else if !strings.Contains(strings.ToLower(strings.Join([]string{image.Repository, image.Tag, image.Digest, image.Architecture, image.OS}, " ")), search) {
			continue
		}
		filtered = append(filtered, image)
	}
	slices.SortFunc(filtered, func(a, b *graphImage) int {
		return comparePositions(imageSortPosition(a, args.Sort), imageSortPosition(b, args.Sort), args.Direction)
	})
	result := &graphImageConnection{Nodes: []*graphImage{}, TotalCount: int32(len(filtered))}
	for _, image := range filtered {
		if args.After != "" && comparePositions(imageSortPosition(image, args.Sort), after, args.Direction) <= 0 {
			continue
		}
		if len(result.Nodes) == int(args.First) {
			result.PageInfo.HasNextPage = true
			break
		}
		result.Nodes = append(result.Nodes, image)
	}
	if len(result.Nodes) > 0 {
		result.PageInfo.EndCursor = encodeCursor(scope, imageSortPosition(result.Nodes[len(result.Nodes)-1], args.Sort))
	}
	return result, nil
}
