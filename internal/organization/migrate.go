package organization

import (
	"context"
	"encoding/json"
	"fmt"
)

// Each migration is submitted with its version marker in one transaction.
// DDL must be idempotent: multiple application instances can start together.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS organizations (
		id TEXT PRIMARY KEY NOT NULL,
		display_name TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS image_usage (
		repository TEXT NOT NULL,
		digest TEXT NOT NULL,
		last_used INTEGER NOT NULL,
		PRIMARY KEY (repository, digest)
	)`,
	`CREATE TABLE IF NOT EXISTS cleanup_plans (
		id TEXT PRIMARY KEY NOT NULL,
		preview TEXT NOT NULL,
		expires_at INTEGER NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending',
		execution TEXT NOT NULL DEFAULT ''
	)`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.write(ctx, [][]any{{"CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY NOT NULL)"}}); err != nil {
		return err
	}
	results, err := s.query(ctx, [][]any{{"SELECT COALESCE(MAX(version), 0) FROM schema_migrations"}})
	if err != nil {
		return err
	}
	if len(results[0].Values) != 1 || len(results[0].Values[0]) != 1 {
		return fmt.Errorf("invalid schema version response")
	}
	var version int
	if err := json.Unmarshal(results[0].Values[0][0], &version); err != nil {
		return err
	}
	if version < 0 || version > len(migrations) {
		return fmt.Errorf("unsupported schema version %d", version)
	}
	for i := version; i < len(migrations); i++ {
		if _, err := s.write(ctx, [][]any{{migrations[i]}, {"INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)", i + 1}}); err != nil {
			return err
		}
	}
	return nil
}
