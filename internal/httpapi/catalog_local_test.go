package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

func TestCatalogLocalCacheRequiresSharedAuthorization(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(context.Context, string) ([]registry.Image, error) {
		calls.Add(1)
		return []registry.Image{{Tag: "live"}}, nil
	}}
	store := &testCatalogStore{}
	h := New(client, store, nil, "registry", t.TempDir(), nil)
	t.Cleanup(h.Close)
	read := func(tag string) {
		t.Helper()
		response, result := graphTestRequest(t.Context(), h, "GET", cachedQuery, nil)
		requireGraphOK(t, response, result)
		if !strings.Contains(response.Body.String(), `"tag":"`+tag+`"`) {
			t.Fatal(response.Body.String())
		}
	}
	for _, tag := range []string{"first", "replacement"} {
		payload, _ := json.Marshal([]*graphImage{{Repository: "team/app", Tag: tag}})
		if err := store.SaveCatalog(t.Context(), "", 0, string(payload), time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		read(tag)
		read(tag)
	}
	if calls.Load() != 0 {
		t.Fatal("shared snapshots were rescanned")
	}
	store.mu.Lock()
	store.expires = time.Now().Add(-time.Second)
	store.mu.Unlock()
	read("live")
	if calls.Load() != 1 {
		t.Fatal("local cache bypassed shared expiry")
	}
	store.mu.Lock()
	store.payload = "invalid JSON"
	store.mu.Unlock()
	read("live")
	if calls.Load() != 2 {
		t.Fatal("malformed shared data did not trigger recovery")
	}
}

func TestCatalogOtterCoalescesConcurrentScans(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(ctx context.Context, _ string) ([]registry.Image, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []registry.Image{{Tag: "latest"}}, nil
	}}
	h := New(client, &testCatalogStore{}, nil, "registry", t.TempDir(), nil)
	t.Cleanup(h.Close)
	var readers sync.WaitGroup
	for range 16 {
		readers.Go(func() {
			response, result := graphTestRequest(t.Context(), h, "GET", cachedQuery, nil)
			if response.Code != 200 || len(result.Errors) != 0 {
				t.Error(response.Body.String())
			}
		})
	}
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, result := graphTestRequest(ctx, h, "GET", cachedQuery, nil)
	if len(result.Errors) == 0 {
		t.Error("canceled caller succeeded")
	}
	close(release)
	readers.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate scans", calls.Load())
	}
}

func TestCatalogOversizedScansAreNotRetained(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(context.Context, string) ([]registry.Image, error) {
		calls.Add(1)
		return []registry.Image{{Tag: strings.Repeat("x", maxCatalogCacheBytes)}}, nil
	}}
	store := &testCatalogStore{}
	h := New(client, store, nil, "registry", t.TempDir(), nil)
	t.Cleanup(h.Close)
	for range 2 {
		ctx := context.WithValue(t.Context(), graphRequestKey{}, &graphRequest{})
		images, err := (&graphResolver{server: h.server}).catalogImages(ctx)
		if err != nil || len(images) != 1 {
			t.Fatal(len(images), err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("oversized scan retained locally")
	}
	if store.payload != "" {
		t.Fatal("oversized scan saved to shared cache")
	}
}

func TestCatalogFreshReadDoesNotJoinInvalidatedLoad(t *testing.T) {
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
			return []registry.Image{{Tag: "old"}}, nil
		}
		return []registry.Image{{Tag: "new"}}, nil
	}}
	store := &testCatalogStore{}
	h := New(client, store, nil, "registry", t.TempDir(), nil)
	t.Cleanup(h.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		graphTestRequest(ctx, h, "GET", cachedQuery, nil)
	}()
	<-entered
	freshCtx := context.WithValue(ctx, graphRequestKey{}, &graphRequest{fresh: true})
	images, err := (&graphResolver{server: h.server}).catalogImages(freshCtx)
	close(release)
	<-done
	if err != nil || len(images) != 1 || images[0].Tag != "new" {
		t.Fatal(images, err)
	}
	_, payload, err := store.LoadCatalog(t.Context(), "")
	if err != nil || !strings.Contains(payload, `"Tag":"new"`) {
		t.Fatal("old scan replaced fresh snapshot", payload, err)
	}
}
