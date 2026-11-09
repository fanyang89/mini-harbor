package organization

import (
	"context"
	"fmt"

	"github.com/fanyang89/mini-harbor/internal/usage"
)

// ImportUsage imports bounded batches with monotonic timestamps. An interrupted
// import can be previewed and resumed; later live usage is never overwritten.
func ImportUsage(ctx context.Context, records []usage.Record, endpoints []string, dryRun bool) error {
	s, err := newStore(endpoints)
	if err != nil {
		return err
	}
	defer s.Close()
	if dryRun {
		_, err := s.query(ctx, [][]any{{"SELECT 1"}})
		return err
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	for start := 0; start < len(records); start += 200 {
		var statements [][]any
		for _, record := range records[start:min(start+200, len(records))] {
			statements = append(statements, []any{`INSERT INTO image_usage VALUES (?, ?, ?)
				ON CONFLICT(repository, digest) DO UPDATE SET last_used=MAX(last_used, excluded.last_used)`, record.Repository, record.Digest, record.LastUsed.UnixNano()})
		}
		if _, err := s.write(ctx, statements); err != nil {
			return err
		}
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		latest, ok, err := s.lastUsed(ctx, record.Repository, record.Digest)
		if err != nil {
			return err
		}
		if !ok || latest.Before(record.LastUsed) {
			return fmt.Errorf("usage verification failed for %s@%s", record.Repository, record.Digest)
		}
	}
	return nil
}
