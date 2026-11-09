package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

type testRefreshStore struct {
	testCatalogStore
	owner  string
	claims chan struct{}
	saved  chan struct{}
}

func (s *testRefreshStore) LoadStaleCatalog(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.expires) < 2*time.Minute {
		return s.payload, nil
	}
	return "", nil
}
func (s *testRefreshStore) ClaimCatalogRefresh(_ context.Context, _, owner string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims <- struct{}{}
	if s.owner != "" {
		return false, nil
	}
	s.owner = owner
	return true, nil
}
func (s *testRefreshStore) ReleaseCatalogRefresh(_ context.Context, _, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == owner {
		s.owner = ""
	}
	return nil
}
func (s *testRefreshStore) SaveCatalog(ctx context.Context, key string, generation int64, payload string, expires time.Time) error {
	err := s.testCatalogStore.SaveCatalog(ctx, key, generation, payload, expires)
	s.saved <- struct{}{}
	return err
}

func TestBackgroundRefreshSharesLeaseAndServesBoundedStaleData(t *testing.T) {
	store := &testRefreshStore{claims: make(chan struct{}, 10), saved: make(chan struct{}, 10)}
	old, _ := json.Marshal([]*graphImage{{Repository: "team/app", Tag: "old"}})
	store.payload, store.expires = string(old), time.Now().Add(-time.Second)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(ctx context.Context, _ string) ([]registry.Image, error) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return []registry.Image{{Tag: "new"}}, nil
	}}
	a := New(client, store, nil, "registry", t.TempDir(), nil)
	b := New(client, store, nil, "registry", t.TempDir(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopped := make(chan struct{}, 2)
	for _, handler := range []*Handler{a, b} {
		go func() { handler.RunCatalogRefresh(ctx); stopped <- struct{}{} }()
	}
	for range 2 {
		select {
		case <-store.claims:
		case <-time.After(2 * time.Second):
			t.Fatal("background workers did not claim")
		}
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not start")
	}
	for _, handler := range []*Handler{a, b} {
		readCtx, stop := context.WithTimeout(t.Context(), time.Second)
		response, result := graphTestRequest(readCtx, handler, "GET", cachedQuery, nil)
		stop()
		requireGraphOK(t, response, result)
		if !strings.Contains(response.Body.String(), `"tag":"old"`) {
			t.Fatal("read did not use stale snapshot", response.Body.String())
		}
		if !strings.Contains(response.Header().Get("Server-Timing"), `desc="stale-cache"`) {
			t.Fatal("missing stale timing diagnostic")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate background scans", calls.Load())
	}
	close(release)
	select {
	case <-store.saved:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not publish")
	}
	response, result := graphTestRequest(t.Context(), b, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if !strings.Contains(response.Body.String(), `"tag":"new"`) {
		t.Fatal("new snapshot not visible across replicas")
	}
	if !strings.Contains(response.Header().Get("Server-Timing"), `desc="shared-cache"`) {
		t.Fatal("missing cache timing diagnostic")
	}
	cancel()
	for range 2 {
		select {
		case <-stopped:
		case <-time.After(2 * time.Second):
			t.Fatal("background worker did not stop")
		}
	}
	store.mu.Lock()
	if store.owner != "" {
		t.Error("refresh lease was not released")
	}
	store.payload, store.expires = string(old), time.Now().Add(-time.Second)
	store.mu.Unlock()
	response, result = graphTestRequest(t.Context(), a, "GET", cachedQuery, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 2 {
		t.Fatal("served stale data without an active background worker")
	}
}
