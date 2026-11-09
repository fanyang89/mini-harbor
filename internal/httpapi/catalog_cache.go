package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

const catalogCacheLifetime = 30 * time.Second
const maxCatalogCacheBytes = 4 << 20

type catalogStore interface {
	LoadCatalog(context.Context, string) (int64, string, error)
	SaveCatalog(context.Context, string, int64, string, time.Time) error
	InvalidateCatalog(context.Context, string) error
}

func (s *server) catalogKey() string { return "graphql-images-v1:" + s.publicURL }

func (s *server) invalidateCatalog(ctx context.Context) {
	store, ok := s.organizations.(catalogStore)
	if !ok {
		return
	}
	// A canceled request may already have committed a Registry write.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := store.InvalidateCatalog(ctx, s.catalogKey()); err != nil {
		slog.Warn("invalidate catalog cache; expiry remains bounded", "error", err)
	}
}

func (r *graphResolver) cachedCatalog(ctx context.Context, build func() ([]*graphImage, error)) ([]*graphImage, error) {
	s := r.server
	store, ok := s.organizations.(catalogStore)
	if !ok {
		return build()
	}
	fresh := ctx.Value(graphRequestKey{}).(*graphRequest).fresh
	if fresh {
		s.invalidateCatalog(ctx)
	}
	for {
		generation, payload, err := store.LoadCatalog(ctx, s.catalogKey())
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			slog.Warn("read catalog cache; using live scan", "error", err)
			return build()
		}
		if !fresh && payload != "" && len(payload) <= maxCatalogCacheBytes {
			var images []*graphImage
			if err := json.Unmarshal([]byte(payload), &images); err == nil && images != nil {
				return images, nil
			}
		}
		s.catalogMu.Lock()
		if flight := s.catalogFlight; flight != nil {
			s.catalogMu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-flight:
				// Reload shared state: a canceled leader never publishes partial data.
				continue
			}
		}
		flight := make(chan struct{})
		s.catalogFlight = flight
		s.catalogMu.Unlock()
		defer func() {
			s.catalogMu.Lock()
			s.catalogFlight = nil
			close(flight)
			s.catalogMu.Unlock()
		}()
		// Another scan may have completed between the first read and taking the
		// flight slot. Recheck both payload and fencing generation before work.
		generation, payload, err = store.LoadCatalog(ctx, s.catalogKey())
		if err != nil {
			return build()
		}
		if !fresh && payload != "" && len(payload) <= maxCatalogCacheBytes {
			var images []*graphImage
			if json.Unmarshal([]byte(payload), &images) == nil && images != nil {
				return images, nil
			}
		}
		images, err := build()
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		payloadBytes, err := json.Marshal(images)
		if err == nil && len(payloadBytes) <= maxCatalogCacheBytes {
			if err := store.SaveCatalog(ctx, s.catalogKey(), generation, string(payloadBytes), time.Now().Add(catalogCacheLifetime)); err != nil {
				slog.Warn("save catalog cache; returning live result", "error", err)
			}
		}
		return images, nil
	}
}
