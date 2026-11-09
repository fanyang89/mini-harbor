package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

type batchRegistry struct {
	fakeRegistry
	imagesFunc func(context.Context, string) ([]registry.Image, error)
}

func (b *batchRegistry) Images(ctx context.Context, name string) ([]registry.Image, error) {
	return b.imagesFunc(ctx, name)
}

func batchURL(names ...string) string {
	query := url.Values{}
	for _, name := range names {
		query.Add("repository", name)
	}
	return "/api/images?" + query.Encode()
}

func TestImageBatchValidationAndStaleRepository(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(_ context.Context, name string) ([]registry.Image, error) {
		calls.Add(1)
		if name == "stale" {
			return nil, &registry.Error{StatusCode: http.StatusNotFound}
		}
		return []registry.Image{{Tag: name, Digest: "sha256:abcd"}}, nil
	}}
	handler := New(client, nil, nil, "localhost", t.TempDir(), nil)
	for _, query := range []string{batchURL(), batchURL("../bad"), batchURL("same", "same"), batchURL("a", "b", "c", "d", "e", "f", "g", "h", "i")} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, query, nil))
		if response.Code != http.StatusBadRequest || calls.Load() != 0 {
			t.Fatalf("invalid batch reached registry: %d %s", response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, batchURL("nested/team/app", "stale", "last"), nil))
	var result struct {
		Repositories []repositoryImagesResponse `json:"repositories"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(result.Repositories) != 3 {
		t.Fatalf("batch failed: %d %s", response.Code, response.Body.String())
	}
	for i, name := range []string{"nested/team/app", "stale", "last"} {
		if result.Repositories[i].Name != name || result.Repositories[i].Images == nil {
			t.Fatalf("lost order or empty array: %+v", result.Repositories)
		}
	}
	if len(result.Repositories[1].Images) != 0 || result.Repositories[0].Images[0].Tag != "nested/team/app" {
		t.Fatalf("wrong images: %+v", result.Repositories)
	}
}

func TestImageBatchSharesConcurrencyLimitAndCancels(t *testing.T) {
	started := make(chan struct{}, 16)
	var active atomic.Int32
	client := &batchRegistry{imagesFunc: func(ctx context.Context, _ string) ([]registry.Image, error) {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	handler := New(client, nil, nil, "localhost", t.TempDir(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			request := httptest.NewRequest(http.MethodGet, batchURL("a", "b", "c", "d", "e", "f", "g", "h"), nil).WithContext(ctx)
			handler.ServeHTTP(httptest.NewRecorder(), request)
		})
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("more than four concurrent registry requests")
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left queued work running")
	}
	if active.Load() != 0 || len(started) != 0 {
		t.Fatal("queued work started after cancellation")
	}
}

func TestImageBatchFailureDoesNotPublishPartialResults(t *testing.T) {
	client := &batchRegistry{imagesFunc: func(_ context.Context, name string) ([]registry.Image, error) {
		if name == "bad" {
			return nil, errors.New("registry unavailable")
		}
		return []registry.Image{{Tag: "latest"}}, nil
	}}
	handler := New(client, nil, nil, "localhost", t.TempDir(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, batchURL("good", "bad"), nil))
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unexpected success: %d %s", response.Code, response.Body.String())
	}
}
