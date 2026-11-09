package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

type testCatalogStore struct {
	graphTestOrganizations
	mu         sync.Mutex
	generation int64
	payload    string
	expires    time.Time
	fail       bool
}

func (s *testCatalogStore) LoadCatalog(context.Context, string) (int64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return 0, "", errors.New("cache unavailable")
	}
	if time.Now().Before(s.expires) {
		return s.generation, s.payload, nil
	}
	return s.generation, "", nil
}
func (s *testCatalogStore) SaveCatalog(_ context.Context, _ string, generation int64, payload string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == s.generation {
		s.payload, s.expires = payload, expires
	}
	return nil
}
func (s *testCatalogStore) InvalidateCatalog(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.payload = ""
	return nil
}

const cachedQuery = `{images(first:1){nodes{tag size} totalCount pageInfo{endCursor hasNextPage}}}`

func TestCatalogCacheAcrossReplicasRefreshExpiryAndWrite(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(context.Context, string) ([]registry.Image, error) {
		calls.Add(1)
		return []registry.Image{{Tag: "latest", Size: 1 << 34}}, nil
	}}
	store := &testCatalogStore{}
	a, b := New(client, store, nil, "registry", t.TempDir(), nil), New(client, store, nil, "registry", t.TempDir(), nil)
	for _, h := range []http.Handler{a, b} {
		response, result := graphTestRequest(t.Context(), h, "GET", cachedQuery, nil)
		requireGraphOK(t, response, result)
	}
	if calls.Load() != 1 {
		t.Fatal("replicas did not share scan", calls.Load())
	}
	fresh := httptest.NewRequest("GET", "/graphql?"+url.Values{"query": {cachedQuery}}.Encode(), nil)
	fresh.Header.Set("Cache-Control", "no-cache")
	response := httptest.NewRecorder()
	b.ServeHTTP(response, fresh)
	if calls.Load() != 2 || strings.Contains(response.Body.String(), `"errors"`) {
		t.Fatal("refresh reused cache", response.Body.String())
	}
	store.mu.Lock()
	store.expires = time.Now().Add(-time.Second)
	store.mu.Unlock()
	response, result := graphTestRequest(t.Context(), a, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 3 {
		t.Fatal("expired cache reused")
	}
	response, result = graphTestRequest(t.Context(), b, "POST", `mutation{deleteTag(repository:"team/app",tag:"latest")}`, nil)
	requireGraphOK(t, response, result)
	response, result = graphTestRequest(t.Context(), a, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 4 {
		t.Fatal("write failed to invalidate other replica")
	}
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	response, result = graphTestRequest(t.Context(), a, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 5 {
		t.Fatal("cache outage did not fall back to live scan")
	}
}

func TestCatalogCanceledScanDoesNotPublish(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(ctx context.Context, _ string) ([]registry.Image, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []registry.Image{{Tag: "latest"}}, nil
	}}
	store := &testCatalogStore{}
	h := New(client, store, nil, "registry", t.TempDir(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { graphTestRequest(ctx, h, "GET", cachedQuery, nil); close(done) }()
	<-started
	waiter := make(chan struct{})
	go func() {
		response, result := graphTestRequest(t.Context(), h, "GET", cachedQuery, nil)
		if response.Code != 200 || len(result.Errors) > 0 {
			t.Error("waiting request failed", response.Body.String())
		}
		close(waiter)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("leader did not cancel")
	}
	select {
	case <-waiter:
	case <-time.After(2 * time.Second):
		t.Fatal("waiting request did not recover")
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected scan count", calls.Load())
	}
	response, result := graphTestRequest(t.Context(), h, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 2 {
		t.Fatal("successful retry was not cached")
	}
}

func TestCatalogInvalidationFencesRunningScan(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	client := &batchRegistry{imagesFunc: func(context.Context, string) ([]registry.Image, error) {
		close(started)
		<-release
		return []registry.Image{{Tag: "old"}}, nil
	}}
	store := &testCatalogStore{}
	h := New(client, store, nil, "registry", t.TempDir(), nil)
	done := make(chan struct{})
	go func() { graphTestRequest(t.Context(), h, "GET", cachedQuery, nil); close(done) }()
	<-started
	_ = store.InvalidateCatalog(t.Context(), "")
	close(release)
	<-done
	_, payload, err := store.LoadCatalog(t.Context(), "")
	if err != nil || payload != "" {
		t.Fatal("in-flight scan resurrected invalidated cache")
	}
}
