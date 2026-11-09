package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestMigrationCommand(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE organizations (id TEXT PRIMARY KEY NOT NULL, display_name TEXT NOT NULL, description TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organizations VALUES ('platform', 'Platform', 'Shared', '2026-11-09 15:00:00.123456789+08:00', '2026-11-09 07:01:00.000000001+00:00')`); err != nil {
		t.Fatal(err)
	}
	args := []string{"--source", path, "--rqlite-urls", cluster.URLs[0]}
	for _, dry := range []bool{true, false, false} {
		var stdout, stderr bytes.Buffer
		flags := append([]string{}, args...)
		if dry {
			flags = append(flags, "--dry-run")
		}
		if code := run(t.Context(), flags, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d: %s", code, &stderr)
		}
		var report organization.ImportReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Source != 1 || report.Verified == dry {
			t.Fatalf("report: %+v", report)
		}
	}
	store, err := organization.Open(t.Context(), cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item, err := store.Get(t.Context(), "platform")
	if err != nil || item.CreatedAt.Hour() != 7 || item.CreatedAt.Nanosecond() != 123456789 || item.UpdatedAt.Nanosecond() != 1 {
		t.Fatalf("read-back: %+v, %v", item, err)
	}
}

func TestMigrationCommandArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--source", "x", "--timeout=0"}, {"--unknown"}, {"--source", "x", "extra"}} {
		var out bytes.Buffer
		if code := run(t.Context(), args, &out, &out); code != 2 {
			t.Fatalf("args %v: exit %d, %s", args, code, &out)
		}
	}
	var out bytes.Buffer
	if code := run(t.Context(), []string{"--help"}, &out, &out); code != 0 {
		t.Fatal(code)
	}
}
