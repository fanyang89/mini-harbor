package organization

import (
	"errors"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestImportOrganizations(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	now := time.Date(2026, 11, 9, 7, 0, 0, 123456789, time.UTC)
	source := []Organization{{Name: "platform", DisplayName: "研发 ' Platform", Description: "Shared images", CreatedAt: now, UpdatedAt: now}}
	report, err := ImportOrganizations(t.Context(), source, cluster.URLs, true)
	if err != nil || report.New != 1 || !report.Initialize || report.Verified {
		t.Fatalf("preview: %+v, %v", report, err)
	}
	probe, err := newStore(cluster.URLs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = probe.Close() })
	tables, err := probe.query(t.Context(), [][]any{{"SELECT name FROM sqlite_master WHERE type='table'"}})
	if err != nil || len(tables[0].Values) != 0 {
		t.Fatalf("preview changed schema: %+v, %v", tables, err)
	}
	report, err = ImportOrganizations(t.Context(), source, cluster.URLs, false)
	if err != nil || !report.Verified {
		t.Fatalf("import: %+v, %v", report, err)
	}
	got, err := probe.Get(t.Context(), "platform")
	if err != nil || got != source[0] {
		t.Fatalf("read-back: %+v, %v", got, err)
	}
	if _, err := probe.Create(t.Context(), "existing", "Keep", "Unrelated row"); err != nil {
		t.Fatal(err)
	}
	report, err = ImportOrganizations(t.Context(), source, cluster.URLs, false)
	if err != nil || report.Identical != 1 || report.New != 0 || !report.Verified {
		t.Fatalf("repeat: %+v, %v", report, err)
	}
	if _, err := probe.Update(t.Context(), "platform", "Changed", "Keep target edit"); err != nil {
		t.Fatal(err)
	}
	report, err = ImportOrganizations(t.Context(), source, cluster.URLs, false)
	if err == nil || len(report.Conflicts) != 1 {
		t.Fatalf("conflict: %+v, %v", report, err)
	}
	newRow := source[0]
	newRow.Name = "before-conflict"
	// Simulate a conflict appearing after preflight. The first insert must roll
	// back together with the conflicting row on the actual rqlite server.
	if err := probe.importRows(t.Context(), []Organization{newRow, source[0]}); err == nil {
		t.Fatal("expected transaction conflict")
	}
	if _, err := probe.Get(t.Context(), "before-conflict"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial import: %v", err)
	}
	items, err := probe.List(t.Context())
	if err != nil || len(items) != 2 || items[1].DisplayName != "Changed" {
		t.Fatalf("target overwritten: %+v, %v", items, err)
	}
	if _, err := probe.write(t.Context(), [][]any{{"INSERT INTO schema_migrations VALUES (999)"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportOrganizations(t.Context(), source, cluster.URLs, true); err == nil {
		t.Fatal("accepted future schema")
	}
}
