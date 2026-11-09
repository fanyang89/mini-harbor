package organization

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type Store struct {
	endpoints []string
	client    *http.Client
	preferred atomic.Uint64
}

type result struct {
	Values       [][]json.RawMessage `json:"values"`
	RowsAffected int64               `json:"rows_affected"`
	Error        string              `json:"error"`
	ErrorV2      *struct {
		Message string `json:"message"`
		SQLite  struct {
			ExtendedCode int `json:"extended_code"`
		} `json:"sqlite"`
	} `json:"error_v2"`
}

type sqlError struct {
	message string
	code    int
}

func (e *sqlError) Error() string { return "rqlite SQL: " + e.message }

func Open(ctx context.Context, endpoints ...string) (*Store, error) {
	if len(endpoints) == 0 {
		return nil, errors.New("at least one rqlite endpoint is required")
	}
	s := &Store{client: &http.Client{
		Timeout:   3 * time.Second,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		// Following a redirect could resubmit a write or change its method.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, endpoint := range endpoints {
		u, err := url.Parse(strings.TrimSpace(endpoint))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, errors.New("rqlite endpoints must be HTTP(S) origins without credentials, query, or path")
		}
		s.endpoints = append(s.endpoints, strings.TrimRight(u.String(), "/"))
	}
	if err := s.migrate(ctx); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrate organization database: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { s.client.CloseIdleConnections(); return nil }

const queryPath = "/db/query?level=linearizable&linearizable_timeout=1s&timeout=1s"
const writePath = "/db/request?transaction&level=linearizable&timeout=1s"

func (s *Store) request(ctx context.Context, index int, path string, statements [][]any) ([]result, error) {
	body, err := json.Marshal(statements)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoints[index]+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rqlite HTTP status %d", resp.StatusCode)
	}
	var payload struct {
		result
		Results []result `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode rqlite response: %w", err)
	}
	for _, entry := range append([]result{payload.result}, payload.Results...) {
		if entry.ErrorV2 != nil {
			return nil, &sqlError{entry.ErrorV2.Message, entry.ErrorV2.SQLite.ExtendedCode}
		}
		if entry.Error != "" {
			return nil, &sqlError{message: entry.Error}
		}
	}
	if len(payload.Results) != len(statements) {
		return nil, errors.New("unexpected rqlite result count")
	}
	return payload.Results, nil
}

// query can retry because it never changes state. Each configured endpoint is
// attempted at most once, bounded by both the caller's context and HTTP timeout.
func (s *Store) query(ctx context.Context, statements [][]any) ([]result, error) {
	start := int(s.preferred.Load())
	var failures []error
	for offset := range len(s.endpoints) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index := (start + offset) % len(s.endpoints)
		results, err := s.request(ctx, index, queryPath, statements)
		if err == nil {
			s.preferred.Store(uint64(index))
			return results, nil
		}
		if sqlErr, ok := errors.AsType[*sqlError](err); ok && sqlErr.code != 0 {
			return nil, err
		}
		failures = append(failures, err)
	}
	return nil, fmt.Errorf("rqlite endpoints unavailable: %w", errors.Join(failures...))
}

func (s *Store) write(ctx context.Context, statements [][]any) ([]result, error) {
	// Select a quorum-ready endpoint before submitting the write. A node can
	// still fail after this probe, so never retry an uncertain submitted write.
	if _, err := s.query(ctx, [][]any{{"SELECT 1"}}); err != nil {
		return nil, err
	}
	results, err := s.request(ctx, int(s.preferred.Load()), writePath, statements)
	if err != nil {
		if sqlErr, ok := errors.AsType[*sqlError](err); ok && sqlErr.code != 0 {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", ErrOutcomeUnknown, err)
	}
	return results, nil
}
