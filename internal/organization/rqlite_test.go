package organization

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteDoesNotRetryUnknownOutcome(t *testing.T) {
	for _, failure := range []string{"disconnect", "truncated", "unavailable", "raft-error", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			var writes atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writes.Add(1)
				_, _ = io.WriteString(w, `{"results":[{}]}`)
			}))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/db/query" {
					_, _ = io.WriteString(w, `{"results":[{"values":[[1]]}]}`)
					return
				}
				writes.Add(1)
				switch failure {
				case "disconnect":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
				case "truncated":
					_, _ = io.WriteString(w, `{"results":[`)
				case "unavailable":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "raft-error":
					_, _ = io.WriteString(w, `{"error":"leadership lost while committing log"}`)
				case "redirect":
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
				}
			}))
			defer server.Close()
			store := &Store{endpoints: []string{server.URL, target.URL}, client: &http.Client{
				Timeout:       time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}}
			_, err := store.Create(t.Context(), "platform", "Platform", "")
			if !errors.Is(err, ErrOutcomeUnknown) {
				t.Fatalf("error = %v", err)
			}
			if got := writes.Load(); got != 1 {
				t.Fatalf("write was submitted %d times", got)
			}
		})
	}
}

func TestReadEndpointFallbackAndCancellation(t *testing.T) {
	var failed, successful atomic.Int32
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer offline.Close()
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		successful.Add(1)
		if r.URL.Query().Get("level") != "linearizable" {
			t.Error("missing linearizable read")
		}
		_, _ = io.WriteString(w, `{"results":[{}]}`)
	}))
	defer online.Close()
	store := &Store{endpoints: []string{offline.URL, online.URL}, client: online.Client()}
	for range 2 {
		items, err := store.List(t.Context())
		if err != nil || items == nil || len(items) != 0 {
			t.Fatalf("list: %v, %v", items, err)
		}
	}
	if failed.Load() != 1 || successful.Load() != 2 {
		t.Fatal("working endpoint was not retained")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	if successful.Load() != 2 {
		t.Fatal("canceled request reached server")
	}
}

func TestHTTP200SQLErrorsAndOmittedZero(t *testing.T) {
	for _, test := range []struct {
		name, response string
		want           error
	}{
		{"duplicate", `{"results":[{"error_v2":{"message":"duplicate","sqlite":{"extended_code":1555}}}]}`, ErrExists},
		{"missing", `{"results":[{}]}`, ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/db/query" {
					_, _ = io.WriteString(w, `{"results":[{"values":[[1]]}]}`)
					return
				}
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			store := &Store{endpoints: []string{server.URL}, client: server.Client()}
			var err error
			if test.name == "duplicate" {
				_, err = store.Create(t.Context(), "platform", "Platform", "")
			} else {
				err = store.Delete(t.Context(), "missing")
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v; want %v", err, test.want)
			}
		})
	}
}

func TestOpenRejectsInvalidEndpoints(t *testing.T) {
	for _, endpoints := range [][]string{nil, {""}, {"file:metadata.db"}, {"http://localhost/db/query"}, {"http://user:password@localhost"}, {"http://localhost?x=y"}} {
		if _, err := Open(t.Context(), endpoints...); err == nil {
			t.Fatalf("accepted endpoints %v", endpoints)
		}
	}
}
