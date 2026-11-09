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
	// Cache creation must not advance the durable schema version. Older deployed
	// Gateway binaries reject unknown versions during startup.
	results, err := a.query(t.Context(), [][]any{{"SELECT MAX(version) FROM schema_migrations"}})
	if err != nil || string(results[0].Values[0][0]) != "3" {
		t.Fatalf("cache changed durable schema version: %v %v", results, err)
	}
}
