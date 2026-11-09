package organization

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Record keeps the latest timestamp even when gateways report out of order.
func (s *Store) Record(repository, digest string, usedAt time.Time) error {
	if repository == "" || digest == "" || usedAt.IsZero() {
		return errors.New("image usage record is incomplete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := s.write(ctx, [][]any{{`INSERT INTO image_usage VALUES (?, ?, ?)
		ON CONFLICT(repository, digest) DO UPDATE SET last_used=MAX(last_used, excluded.last_used)`, repository, digest, usedAt.UnixNano()}})
	return err
}

func (s *Store) LastUsed(repository, digest string) (time.Time, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.lastUsed(ctx, repository, digest)
}

func (s *Store) lastUsed(ctx context.Context, repository, digest string) (time.Time, bool, error) {
	r, err := s.query(ctx, [][]any{{"SELECT last_used FROM image_usage WHERE repository=? AND digest=?", repository, digest}})
	if err != nil {
		return time.Time{}, false, err
	}
	if len(r[0].Values) == 0 {
		return time.Time{}, false, nil
	}
	var timestamp int64
	if err := json.Unmarshal(r[0].Values[0][0], &timestamp); err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(0, timestamp).UTC(), true, nil
}

func (s *Store) SavePlan(ctx context.Context, id, preview string, expires time.Time) error {
	_, err := s.write(ctx, [][]any{
		{"DELETE FROM cleanup_plans WHERE expires_at <= ? AND state != 'running'", time.Now().UnixNano()},
		{"INSERT INTO cleanup_plans(id, preview, expires_at) VALUES (?, ?, ?)", id, preview, expires.UnixNano()},
	})
	return err
}

// ClaimPlan atomically allows only one cleanup execution across all replicas.
// A running plan never expires: after a crash an operator must reconcile partial
// registry deletions before releasing it. A lease cannot fence Registry DELETE.
func (s *Store) ClaimPlan(ctx context.Context, id string, now time.Time) (preview, execution, state string, claimed bool, err error) {
	r, err := s.write(ctx, [][]any{
		{`UPDATE cleanup_plans SET state='running' WHERE id=? AND state='pending' AND expires_at>?
		AND NOT EXISTS (SELECT 1 FROM cleanup_plans WHERE state='running')`, id, now.UnixNano()},
		{"SELECT preview, execution, state, expires_at FROM cleanup_plans WHERE id=?", id},
	})
	if err != nil {
		return "", "", "", false, err
	}
	if len(r[1].Values) == 0 {
		return "", "", "missing", false, nil
	}
	var expires int64
	for i, target := range []any{&preview, &execution, &state, &expires} {
		if err := json.Unmarshal(r[1].Values[0][i], target); err != nil {
			return "", "", "", false, err
		}
	}
	claimed = r[0].RowsAffected == 1
	if !claimed && state != "running" && expires <= now.UnixNano() {
		state = "expired"
	}
	return preview, execution, state, claimed, nil
}

func (s *Store) CompletePlan(ctx context.Context, id, execution string, expires time.Time) error {
	r, err := s.write(ctx, [][]any{{"UPDATE cleanup_plans SET state='done', execution=?, expires_at=? WHERE id=? AND state='running'", execution, expires.UnixNano(), id}})
	if err != nil {
		return err
	}
	if r[0].RowsAffected != 1 {
		return errors.New("cleanup plan is not running")
	}
	return nil
}
