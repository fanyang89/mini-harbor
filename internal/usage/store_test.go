package usage

import (
	"testing"
	"time"
)

func TestStorePersistsLatestUsage(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	latest := time.Date(2026, time.August, 22, 10, 30, 0, 123, time.FixedZone("test", 8*60*60))
	if err := store.Record("team/app", "sha256:abcd", latest); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("team/app", "sha256:abcd", latest.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := reopened.LastUsed("team/app", "sha256:abcd")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || !got.Equal(latest) || got.Location() != time.UTC {
		t.Fatalf("last used = %v, %v; want %v, true", got, ok, latest.UTC())
	}
}

func TestStoreReturnsMissingUsage(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.LastUsed("team/app", "sha256:missing"); err != nil || ok || !got.IsZero() {
		t.Fatalf("last used = %v, %v, %v; want zero, false, nil", got, ok, err)
	}
}
