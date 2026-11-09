package organization

import (
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestSharedCatalogCacheGenerationAndExpiry(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	a, err := Open(t.Context(), cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(t.Context(), cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := a.PrepareCatalogCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.PrepareCatalogCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	generation, payload, err := a.LoadCatalog(t.Context(), "test")
	if err != nil || generation != 0 || payload != "" {
		t.Fatal(generation, payload, err)
	}
	if err := a.SaveCatalog(t.Context(), "test", generation, "snapshot", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, payload, err = b.LoadCatalog(t.Context(), "test")
	if err != nil || payload != "snapshot" {
		t.Fatal(payload, err)
	}
	if err := b.InvalidateCatalog(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveCatalog(t.Context(), "test", generation, "stale", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	generation, payload, err = b.LoadCatalog(t.Context(), "test")
	if err != nil || generation != 1 || payload != "" {
		t.Fatal(generation, payload, err)
	}
	if err := a.SaveCatalog(t.Context(), "test", generation, "expired", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_, payload, err = b.LoadCatalog(t.Context(), "test")
	if err != nil || payload != "" {
		t.Fatal(payload, err)
	}
	stale, err := b.LoadStaleCatalog(t.Context(), "test")
	if err != nil || stale != "expired" {
		t.Fatal("bounded stale snapshot unavailable", stale, err)
	}
	if err := a.SaveCatalog(t.Context(), "test", generation, "too old", time.Now().Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err = b.LoadStaleCatalog(t.Context(), "test")
	if err != nil || stale != "" {
		t.Fatal("served unbounded stale snapshot", stale, err)
	}
	claimed, err := a.ClaimCatalogRefresh(t.Context(), "test", "owner-a")
	if err != nil || !claimed {
		t.Fatal("first lease failed", claimed, err)
	}
	claimed, err = b.ClaimCatalogRefresh(t.Context(), "test", "owner-b")
	if err != nil || claimed {
		t.Fatal("concurrent lease acquired", claimed, err)
	}
	if err := b.ReleaseCatalogRefresh(t.Context(), "test", "owner-b"); err != nil {
		t.Fatal(err)
	}
	claimed, err = b.ClaimCatalogRefresh(t.Context(), "test", "owner-b")
	if err != nil || claimed {
		t.Fatal("wrong owner released lease", claimed, err)
	}
	if err := a.ReleaseCatalogRefresh(t.Context(), "test", "owner-a"); err != nil {
		t.Fatal(err)
	}
	claimed, err = b.ClaimCatalogRefresh(t.Context(), "test", "owner-b")
	if err != nil || !claimed {
		t.Fatal("released lease unavailable", claimed, err)
	}
	if _, err := a.write(t.Context(), [][]any{{"UPDATE catalog_refresh_v1 SET expires_at=0 WHERE id=?", "test"}}); err != nil {
		t.Fatal(err)
	}
	claimed, err = a.ClaimCatalogRefresh(t.Context(), "test", "owner-c")
	if err != nil || !claimed {
		t.Fatal("expired owner lease did not recover", claimed, err)
	}
	if err := b.ReleaseCatalogRefresh(t.Context(), "test", "owner-b"); err != nil {
		t.Fatal(err)
	}
	claimed, err = b.ClaimCatalogRefresh(t.Context(), "test", "owner-b")
	if err != nil || claimed {
		t.Fatal("old owner released replacement lease", claimed, err)
	}
	// Cache creation must not advance the durable schema version. Older deployed
	// Gateway binaries reject unknown versions during startup.
	results, err := a.query(t.Context(), [][]any{{"SELECT MAX(version) FROM schema_migrations"}})
	if err != nil || string(results[0].Values[0][0]) != "3" {
		t.Fatalf("cache changed durable schema version: %v %v", results, err)
	}
}
