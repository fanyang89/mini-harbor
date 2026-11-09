package organization

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// PrepareCatalogCache creates disposable cache state independently of durable
// schema versions. Older Web/Gateway binaries can still open the database.
func (s *Store) PrepareCatalogCache(ctx context.Context) error {
	_, err := s.write(ctx, [][]any{{`CREATE TABLE IF NOT EXISTS catalog_cache_v1 (
		id TEXT PRIMARY KEY NOT NULL,
		generation INTEGER NOT NULL DEFAULT 0,
		payload TEXT NOT NULL DEFAULT '',
		expires_at INTEGER NOT NULL DEFAULT 0
	)`}})
	return err
}

func (s *Store) LoadCatalog(ctx context.Context, key string) (int64, string, error) {
	results, err := s.query(ctx, [][]any{{`SELECT generation,
		CASE WHEN expires_at > ? THEN payload ELSE '' END
		FROM catalog_cache_v1 WHERE id=?`, time.Now().UnixNano(), key}})
	if err != nil {
		return 0, "", err
	}
	if len(results[0].Values) == 0 {
		return 0, "", nil
	}
	row := results[0].Values[0]
	if len(row) != 2 {
		return 0, "", errors.New("invalid catalog cache response")
	}
	var generation int64
	var payload string
	if err := json.Unmarshal(row[0], &generation); err != nil {
		return 0, "", err
	}
	if err := json.Unmarshal(row[1], &payload); err != nil {
		return 0, "", err
	}
	return generation, payload, nil
}

// Generation fencing prevents an in-flight scan from repopulating a cache that
// was invalidated by a concurrent write or explicit refresh.
func (s *Store) SaveCatalog(ctx context.Context, key string, generation int64, payload string, expires time.Time) error {
	_, err := s.write(ctx, [][]any{{`INSERT INTO catalog_cache_v1(id, generation, payload, expires_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload, expires_at=excluded.expires_at
		WHERE catalog_cache_v1.generation=excluded.generation`, key, generation, payload, expires.UnixNano()}})
	return err
}

func (s *Store) InvalidateCatalog(ctx context.Context, key string) error {
	_, err := s.write(ctx, [][]any{{`INSERT INTO catalog_cache_v1(id, generation) VALUES (?, 1)
		ON CONFLICT(id) DO UPDATE SET generation=generation+1, payload='', expires_at=0`, key}})
	return err
}
