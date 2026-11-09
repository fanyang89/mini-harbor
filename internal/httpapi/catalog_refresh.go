package httpapi

import (
	"context"
	"crypto/rand"
	"log/slog"
	"time"
)

type catalogRefreshStore interface {
	catalogStore
	LoadStaleCatalog(context.Context, string) (string, error)
	ClaimCatalogRefresh(context.Context, string, string) (bool, error)
	ReleaseCatalogRefresh(context.Context, string, string) error
}

// RunCatalogRefresh keeps a shared browsing snapshot ready independently of page
// requests. The owner lease limits background scans to one across all replicas.
// Fresh requests and mutations retain their existing generation-fenced behavior.
func (h *Handler) RunCatalogRefresh(ctx context.Context) {
	if _, ok := h.server.organizations.(catalogRefreshStore); !ok {
		return
	}
	if !h.server.catalogBackground.CompareAndSwap(false, true) {
		return
	}
	defer h.server.catalogBackground.Store(false)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		scanCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		scanCtx = context.WithValue(scanCtx, graphRequestKey{}, &graphRequest{background: true})
		_, err := (&graphResolver{server: h.server}).catalogImages(scanCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("background catalog refresh", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func claimCatalogRefresh(ctx context.Context, store catalogRefreshStore, key string) (func(), bool, error) {
	owner := rand.Text()
	claimed, err := store.ClaimCatalogRefresh(ctx, key, owner)
	if err != nil || !claimed {
		return nil, false, err
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := store.ReleaseCatalogRefresh(releaseCtx, key, owner); err != nil {
			slog.Warn("release catalog refresh lease; will expire", "error", err)
		}
	}, true, nil
}
