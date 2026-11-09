package organization

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestConcurrentMigrationsAndTransactionRollback(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	for range 3 {
		wg.Go(func() {
			store, err := Open(t.Context(), cluster.URLs...)
			if err == nil {
				err = store.Close()
			}
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(t.Context(), cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	versions, err := store.query(t.Context(), [][]any{{"SELECT version FROM schema_migrations ORDER BY version"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(versions[0].Values) != len(migrations) {
		t.Fatalf("unexpected versions: %+v", versions)
	}
	for i, row := range versions[0].Values {
		if len(row) != 1 || string(row[0]) != fmt.Sprint(i+1) {
			t.Fatalf("unexpected migration sequence: %+v", versions)
		}
	}
	_, err = store.write(t.Context(), [][]any{
		{"INSERT INTO organizations VALUES (?, ?, ?, ?, ?)", "rollback", "Rollback", "", "2026-11-09T00:00:00Z", "2026-11-09T00:00:00Z"},
		{"INSERT INTO schema_migrations (version) VALUES (1)"},
	})
	if err == nil || errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("expected definitive transaction failure: %v", err)
	}
	if _, err := store.Get(t.Context(), "rollback"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("transaction did not roll back: %v", err)
	}
	if _, err := store.write(t.Context(), [][]any{{"INSERT INTO schema_migrations (version) VALUES (999)"}}); err != nil {
		t.Fatal(err)
	}
	if newer, err := Open(t.Context(), cluster.URLs...); err == nil {
		_ = newer.Close()
		t.Fatal("accepted unsupported future schema")
	} else if !strings.Contains(err.Error(), "unsupported schema version 999") {
		t.Fatal(err)
	}
}
