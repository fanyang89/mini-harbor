package s3

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

type walkObject struct {
	Key          string
	Size         int64
	LastModified string
}

type walkPrefix struct{ Prefix string }

type walkResponse struct {
	XMLName               xml.Name `xml:"ListBucketResult"`
	IsTruncated           bool
	NextContinuationToken string `xml:",omitempty"`
	Contents              []walkObject
	CommonPrefixes        []walkPrefix
}

func orderedWalkFixture(t *testing.T, root string) (*driver, *atomic.Int64, *atomic.Int64, *atomic.Bool) {
	t.Helper()
	keys := []string{
		"repos/foo/_manifests/tags/v1/current/link",
		"repos/foo/_manifests/tags/v1-extra/current/link",
		"repos/foo/_manifests/tags/v2/current/link",
		"repos/foo-bar/_manifests/tags/latest/current/link",
		"repos/foo.bar/_manifests/tags/latest/current/link",
		"repos/z/_manifests/tags/latest/current/link",
	}
	base := strings.Trim(root, "/")
	if base != "" {
		base += "/"
	}
	for i := range keys {
		keys[i] = base + keys[i]
	}
	slices.Sort(keys)
	lists, heads, fail := &atomic.Int64{}, &atomic.Int64{}, &atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/test/")
		if r.Method == http.MethodHead {
			heads.Add(1)
			if !slices.Contains(keys, key) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", "71")
			w.Header().Set("Last-Modified", "Mon, 09 Nov 2026 00:00:00 GMT")
			return
		}
		lists.Add(1)
		query := r.URL.Query()
		if query.Has("start-after") {
			t.Error("S3 scan must not use raw-key StartAfter for directory pagination")
		}
		if fail.Load() && query.Get("continuation-token") != "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `<Error><Code>InvalidArgument</Code><Message>injected page failure</Message></Error>`)
			return
		}
		var objects []walkObject
		var prefixes []walkPrefix
		seen := map[string]bool{}
		for _, key := range keys {
			if !strings.HasPrefix(key, query.Get("prefix")) {
				continue
			}
			if query.Get("delimiter") == "/" {
				relative := strings.TrimPrefix(key, query.Get("prefix"))
				if segment, _, found := strings.Cut(relative, "/"); found {
					prefix := query.Get("prefix") + segment + "/"
					if !seen[prefix] {
						prefixes = append(prefixes, walkPrefix{Prefix: prefix})
						seen[prefix] = true
					}
					continue
				}
			}
			objects = append(objects, walkObject{Key: key, Size: 71, LastModified: "2026-11-09T00:00:00Z"})
		}
		response := walkResponse{CommonPrefixes: prefixes}
		offset, _ := strconv.Atoi(query.Get("continuation-token"))
		pageSize := 2
		if query.Get("max-keys") == "1" {
			pageSize = 1
		}
		end := min(offset+pageSize, len(objects))
		response.Contents = objects[offset:end]
		if end < len(objects) {
			response.IsTruncated = true
			response.NextContinuationToken = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/xml")
		if err := xml.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := New(t.Context(), DriverParameters{AccessKey: "test", SecretKey: "test", Bucket: "test",
		Region: "garage", RegionEndpoint: server.URL, ForcePathStyle: true, V4Auth: true, RootDirectory: root})
	if err != nil {
		t.Fatal(err)
	}
	return client.StorageDriver.(*driver), lists, heads, fail
}

func TestOrderedWalkMatchesDirectoryWalker(t *testing.T) {
	for _, root := range []string{"registry", "/registry/", ""} {
		for _, hint := range []string{"", "/before", "/repos/foo/_manifests", "/repos/foo/_manifests/tags/v1", "/zz"} {
			t.Run(root+hint, func(t *testing.T) {
				d, lists, heads, _ := orderedWalkFixture(t, root)
				var got, want []string
				options := storagedriver.WithStartAfterHint(hint)
				err := d.Walk(t.Context(), "/repos", func(info storagedriver.FileInfo) error {
					got = append(got, fmt.Sprintf("%s:%t:%d", info.Path(), info.IsDir(), info.Size()))
					return nil
				}, options)
				if err != nil {
					t.Fatal(err)
				}
				if lists.Load() != 3 || heads.Load() != 0 {
					t.Fatalf("got %d lists and %d HEADs; want three batch pages, no per-entry HEAD", lists.Load(), heads.Load())
				}
				err = storagedriver.WalkFallback(t.Context(), d, "/repos", func(info storagedriver.FileInfo) error {
					want = append(want, fmt.Sprintf("%s:%t:%d", info.Path(), info.IsDir(), info.Size()))
					return nil
				}, options)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(got, want) {
					t.Fatalf("got %v; fallback %v", got, want)
				}
			})
		}
	}
}

func TestOrderedWalkSkipAndPagination(t *testing.T) {
	d, _, _, _ := orderedWalkFixture(t, "registry")
	var got []string
	err := d.Walk(t.Context(), "/repos", func(info storagedriver.FileInfo) error {
		got = append(got, info.Path())
		return storagedriver.ErrSkipDir
	})
	if err != nil || !slices.Equal(got, []string{"/repos/foo", "/repos/foo-bar", "/repos/foo.bar", "/repos/z"}) {
		t.Fatalf("skip must preserve sibling prefixes: %v, %v", got, err)
	}
	var tags []string
	last := ""
	root := "/repos/foo/_manifests/tags"
	for range 4 {
		found := ""
		err := d.Walk(t.Context(), root, func(info storagedriver.FileInfo) error {
			if info.IsDir() && strings.Count(strings.TrimPrefix(info.Path(), root+"/"), "/") == 0 {
				found = info.Path()
				return storagedriver.ErrFilledBuffer
			}
			return nil
		}, storagedriver.WithStartAfterHint(last))
		if err != nil {
			t.Fatal(err)
		}
		if found == "" {
			break
		}
		tags = append(tags, strings.TrimPrefix(found, root+"/"))
		last = found
	}
	if !slices.Equal(tags, []string{"v1", "v1-extra", "v2"}) {
		t.Fatalf("paginated tags: %v", tags)
	}
}

func TestOrderedWalkFailureAndFallback(t *testing.T) {
	d, _, _, fail := orderedWalkFixture(t, "registry")
	fail.Store(true)
	calls := 0
	err := d.Walk(t.Context(), "/repos", func(storagedriver.FileInfo) error { calls++; return nil })
	if err == nil || calls != 0 {
		t.Fatalf("failed listing published partial result: %v, calls=%d", err, calls)
	}
	fail.Store(false)
	var got []string
	err = d.walkOrderedLimit(t.Context(), "/repos", func(info storagedriver.FileInfo) error {
		got = append(got, info.Path())
		return storagedriver.ErrSkipDir
	}, 1)
	if err != nil || !slices.Equal(got, []string{"/repos/foo", "/repos/foo-bar", "/repos/foo.bar", "/repos/z"}) {
		t.Fatalf("bounded fallback: %v, %v", got, err)
	}
	sentinel := errors.New("callback failure")
	if err := d.Walk(t.Context(), "/repos", func(storagedriver.FileInfo) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("lost callback error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = d.Walk(ctx, "/repos", func(storagedriver.FileInfo) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
