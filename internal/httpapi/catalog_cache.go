package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/maypok86/otter/v2"
)

const catalogCacheLifetime = 30 * time.Second
const maxCatalogCacheBytes = 4 << 20

type catalogStore interface {
	LoadCatalog(context.Context, string) (int64, string, error)
	SaveCatalog(context.Context, string, int64, string, time.Time) error
	InvalidateCatalog(context.Context, string) error
}

type catalogLocalKey struct {
	generation int64
	digest     [32]byte
	scan       bool
	fresh      bool
}

type catalogSnapshot struct {
	images []*graphImage
	bytes  uint32
	source string
}

func newCatalogCache() *otter.Cache[catalogLocalKey, catalogSnapshot] {
	return otter.Must(&otter.Options[catalogLocalKey, catalogSnapshot]{
		MaximumWeight: maxCatalogCacheBytes,
		Weigher:       func(_ catalogLocalKey, value catalogSnapshot) uint32 { return max(1, value.bytes) },
		ExpiryCalculator: otter.ExpiryWritingFunc(func(entry otter.Entry[catalogLocalKey, catalogSnapshot]) time.Duration {
			// Scan results only coalesce current callers. Shared state must approve
			// every subsequent read, including expiry and cross-replica invalidation.
			if entry.Key.scan {
				// Otter treats non-positive durations as preserving the deadline.
				return time.Nanosecond
			}
			return catalogCacheLifetime
		}),
	})
}

// Close releases cache maintenance workers after requests and refresh have stopped.
func (h *Handler) Close() {
	if h.server.catalogCache != nil {
		h.server.catalogCache.StopAllGoroutines()
	}
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

// Only a payload just authorized by shared storage may address the decoded cache.
// Content addressing also handles refreshes that do not change the generation.
func (s *server) decodeCatalog(ctx context.Context, payload string) (catalogSnapshot, error) {
	if payload == "" || len(payload) > maxCatalogCacheBytes {
		return catalogSnapshot{}, otter.ErrNotFound
	}
	return s.catalogCache.Get(ctx, catalogLocalKey{digest: sha256.Sum256([]byte(payload))},
		otter.LoaderFunc[catalogLocalKey, catalogSnapshot](func(context.Context, catalogLocalKey) (catalogSnapshot, error) {
			var images []*graphImage
			if err := json.Unmarshal([]byte(payload), &images); err != nil {
				return catalogSnapshot{}, err
			}
			if images == nil {
				return catalogSnapshot{}, otter.ErrNotFound
			}
			return catalogSnapshot{images: images, bytes: uint32(len(payload)), source: "shared-cache"}, nil
		}))
}

func (r *graphResolver) cachedCatalog(ctx context.Context, build func(context.Context) ([]*graphImage, error)) ([]*graphImage, error) {
	s := r.server
	store, ok := s.organizations.(catalogStore)
	if !ok {
		return build(ctx)
	}
	request := ctx.Value(graphRequestKey{}).(*graphRequest)
	if request.fresh {
		s.invalidateCatalog(ctx)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		generation, payload, err := store.LoadCatalog(ctx, s.catalogKey())
		if err != nil {
			if request.background || ctx.Err() != nil {
				return nil, err
			}
			slog.Warn("read catalog cache; using live scan", "error", err)
			return build(ctx)
		}
		if !request.fresh {
			if snapshot, err := s.decodeCatalog(ctx, payload); err == nil {
				request.catalogSource = snapshot.source
				return snapshot.images, nil
			}
			if !request.background && s.catalogBackground.Load() {
				if refreshStore, ok := store.(catalogRefreshStore); ok {
					stale, err := refreshStore.LoadStaleCatalog(ctx, s.catalogKey())
					if err == nil {
						if snapshot, err := s.decodeCatalog(ctx, stale); err == nil {
							request.catalogSource = "stale-cache"
							return snapshot.images, nil
						}
					}
				}
			}
		}
		snapshot, err := s.catalogCache.Get(ctx, catalogLocalKey{generation: generation, scan: true, fresh: request.fresh},
			otter.LoaderFunc[catalogLocalKey, catalogSnapshot](func(loadCtx context.Context, _ catalogLocalKey) (catalogSnapshot, error) {
				if request.background {
					release, claimed, err := claimCatalogRefresh(loadCtx, store.(catalogRefreshStore), s.catalogKey())
					if err != nil {
						return catalogSnapshot{}, err
					}
					if !claimed {
						return catalogSnapshot{}, otter.ErrNotFound
					}
					defer release()
				}
				// Another replica may have published or invalidated before this load.
				generation, payload, err := store.LoadCatalog(loadCtx, s.catalogKey())
				if err != nil && request.background {
					return catalogSnapshot{}, err
				}
				if err == nil && !request.fresh {
					// Do not recursively call this cache from its loader.
					var images []*graphImage
					if len(payload) <= maxCatalogCacheBytes && json.Unmarshal([]byte(payload), &images) == nil && images != nil {
						return catalogSnapshot{images: images, source: "shared-cache"}, nil
					}
				}
				canSave := err == nil
				images, err := build(loadCtx)
				if err != nil {
					return catalogSnapshot{}, err
				}
				if err := loadCtx.Err(); err != nil {
					return catalogSnapshot{}, err
				}
				payloadBytes, err := json.Marshal(images)
				if canSave && err == nil && len(payloadBytes) <= maxCatalogCacheBytes {
					if err := store.SaveCatalog(loadCtx, s.catalogKey(), generation, string(payloadBytes), time.Now().Add(catalogCacheLifetime)); err != nil {
						slog.Warn("save catalog cache; returning live result", "error", err)
					}
				}
				return catalogSnapshot{images: images, source: "live-scan"}, nil
			}))
		if errors.Is(err, otter.ErrNotFound) {
			if request.background {
				return nil, nil
			}
			// A foreground reader can join a worker that lost the cluster lease.
			continue
		}
		if err != nil {
			return nil, err
		}
		request.catalogSource = snapshot.source
		return snapshot.images, nil
	}
}
