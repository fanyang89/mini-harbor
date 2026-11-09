package legacyimport

import (
	"bytes"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadCommittedWALWithoutChangingSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy ?#.db")
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		`CREATE TABLE organizations (id TEXT PRIMARY KEY NOT NULL, display_name TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
	} {
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(t.Context(), "INSERT INTO organizations VALUES (?, ?, ?, ?, ?)", "platform", "研发 ' Platform", "Shared images", "2026-11-09 15:00:00.123456789+08:00", "2026-11-09 07:01:00.000000001+00:00")
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	mainBefore, walBefore := read(path), read(path+"-wal")
	items, err := Read(t.Context(), path)
	if err != nil || len(items) != 1 {
		t.Fatalf("source: %+v, %v", items, err)
	}
	want := time.Date(2026, 11, 9, 7, 0, 0, 123456789, time.UTC)
	if items[0].DisplayName != "研发 ' Platform" || !items[0].CreatedAt.Equal(want) || items[0].UpdatedAt.Nanosecond() != 1 {
		t.Fatalf("data lost: %+v", items[0])
	}
	if !bytes.Equal(mainBefore, read(path)) || !bytes.Equal(walBefore, read(path+"-wal")) {
		t.Fatal("source changed")
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE organizations SET created_at='invalid'"); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(t.Context(), path); err == nil {
		t.Fatal("accepted malformed time")
	}
}

func TestMissingSourceIsNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, err := Read(t.Context(), path); err == nil {
		t.Fatal("accepted missing source")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing source was created: %v", err)
	}
}

func TestLegacyTime(t *testing.T) {
	for _, value := range []string{"2026-11-09 07:00:00", "2026-11-09 15:00:00+08:00", "2026-11-09T07:00:00Z"} {
		got, err := legacyTime(value)
		if err != nil || got.Format(time.RFC3339Nano) != "2026-11-09T07:00:00Z" {
			t.Fatalf("%q: %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"", "2026-02-30 00:00:00", "garbage", "2026-11-09T00:00:00.1234567891Z"} {
		if _, err := legacyTime(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
