package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

const imageBatchSize = 8

var errImageUsage = errors.New("Image usage storage failed")

type repositoryImagesResponse struct {
	Name   string           `json:"name"`
	Images []registry.Image `json:"images"`
}

// A batch uses the browser's catalog snapshot, avoiding another full catalog
// scan per page. The shared semaphore also bounds concurrent legacy requests.
func (s *server) imageBatch(w http.ResponseWriter, r *http.Request) {
	names := r.URL.Query()["repository"]
	if len(names) == 0 || len(names) > imageBatchSize {
		writeError(w, http.StatusBadRequest, "Specify between 1 and 8 repositories")
		return
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !validRepository(name) || seen[name] {
			writeError(w, http.StatusBadRequest, "Invalid or duplicate repository name")
			return
		}
		seen[name] = true
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	results := make([]repositoryImagesResponse, len(names))
	var workers sync.WaitGroup
	var once sync.Once
	var firstError error
	for i, name := range names {
		workers.Go(func() {
			images, err := s.repositoryImages(ctx, name)
			if registryErr, ok := errors.AsType[*registry.Error](err); ok && registryErr.StatusCode == http.StatusNotFound {
				// Catalog links can outlive the last manifest in a repository.
				images, err = []registry.Image{}, nil
			}
			if err != nil {
				once.Do(func() { firstError = err; cancel() })
				return
			}
			results[i] = repositoryImagesResponse{Name: name, Images: images}
		})
	}
	workers.Wait()
	if firstError != nil {
		s.registryError(w, firstError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": results})
}

func (s *server) repositoryImages(ctx context.Context, name string) ([]registry.Image, error) {
	select {
	case s.imageSlots <- struct{}{}:
		defer func() { <-s.imageSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	images, err := s.registry.Images(ctx, name)
	if err != nil {
		return nil, err
	}
	if images == nil {
		images = []registry.Image{}
	}
	if s.imageUsage != nil {
		for i := range images {
			lastUsed, ok, err := s.imageUsage.LastUsed(name, images[i].Digest)
			if err != nil {
				slog.Warn("read image usage", "repository", name, "digest", images[i].Digest, "error", err)
				return nil, errImageUsage
			}
			if ok {
				images[i].LastUsed = lastUsed.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return images, nil
}
