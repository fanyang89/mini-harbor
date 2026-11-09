// Package legacyimport reads old SQLite metadata for the standalone migrator.
package legacyimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fanyang89/mini-harbor/internal/organization"
	_ "modernc.org/sqlite"
)

func Read(ctx context.Context, path string) ([]organization.Organization, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("source must be a regular SQLite file")
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// One read snapshot includes integrity checking and committed WAL records.
	// immutable=1 would hide WAL, so it must not be used here.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var integrity string
	if err := tx.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return nil, err
	}
	if integrity != "ok" {
		return nil, fmt.Errorf("source integrity check failed: %s", integrity)
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, display_name, description, CAST(created_at AS TEXT), CAST(updated_at AS TEXT) FROM organizations ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []organization.Organization{}
	for rows.Next() {
		var item organization.Organization
		var created, updated string
		if err := rows.Scan(&item.Name, &item.DisplayName, &item.Description, &created, &updated); err != nil {
			return nil, err
		}
		if !organization.ValidName(item.Name) || len(item.DisplayName) > 100 || len(item.Description) > 500 {
			return nil, fmt.Errorf("invalid organization %q", item.Name)
		}
		if item.CreatedAt, err = legacyTime(created); err != nil {
			return nil, err
		}
		if item.UpdatedAt, err = legacyTime(updated); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

var timestampPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}[ T][0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})?$`)

func legacyTime(value string) (time.Time, error) {
	if !timestampPattern.MatchString(value) {
		return time.Time{}, fmt.Errorf("unsupported legacy timestamp %q", value)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported legacy timestamp %q", value)
}
