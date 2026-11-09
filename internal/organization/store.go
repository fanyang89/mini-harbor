package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	ErrExists   = errors.New("organization already exists")
	ErrNotFound = errors.New("organization not found")
	// ErrOutcomeUnknown means a submitted write may have committed. Read back
	// the resource before deciding whether to retry the business operation.
	ErrOutcomeUnknown = errors.New("database write outcome unknown")
	namePattern       = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

type Organization struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

const columns = "id, display_name, description, created_at, updated_at"
const selectOne = "SELECT " + columns + " FROM organizations WHERE id=?"

func (s *Store) List(ctx context.Context) ([]Organization, error) {
	results, err := s.query(ctx, [][]any{{"SELECT " + columns + " FROM organizations ORDER BY id"}})
	if err != nil {
		return nil, err
	}
	items := make([]Organization, 0, len(results[0].Values))
	for _, row := range results[0].Values {
		item, err := decodeOrganization(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) Get(ctx context.Context, name string) (Organization, error) {
	results, err := s.query(ctx, [][]any{{selectOne, name}})
	if err != nil {
		return Organization{}, err
	}
	return oneOrganization(results[0])
}

func (s *Store) Create(ctx context.Context, name, displayName, description string) (Organization, error) {
	if err := validate(name, displayName, description); err != nil {
		return Organization{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	results, err := s.write(ctx, [][]any{
		{"INSERT INTO organizations (" + columns + ") VALUES (?, ?, ?, ?, ?)", name, displayName, description, now, now},
		{selectOne, name},
	})
	if err != nil {
		if sqlErr, ok := errors.AsType[*sqlError](err); ok && (sqlErr.code == 1555 || sqlErr.code == 2067) {
			return Organization{}, ErrExists
		}
		return Organization{}, err
	}
	return oneOrganization(results[1])
}

func (s *Store) Update(ctx context.Context, name, displayName, description string) (Organization, error) {
	if err := validate(name, displayName, description); err != nil {
		return Organization{}, err
	}
	results, err := s.write(ctx, [][]any{
		{"UPDATE organizations SET display_name=?, description=?, updated_at=? WHERE id=?", displayName, description, time.Now().UTC().Format(time.RFC3339Nano), name},
		{selectOne, name},
	})
	if err != nil {
		return Organization{}, err
	}
	return oneOrganization(results[1])
}

func (s *Store) Delete(ctx context.Context, name string) error {
	results, err := s.write(ctx, [][]any{{"DELETE FROM organizations WHERE id=?", name}})
	if err != nil {
		return err
	}
	if results[0].RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func ValidName(name string) bool {
	return len(name) <= 63 && namePattern.MatchString(name)
}

func validate(name, displayName, description string) error {
	if !ValidName(name) || len(displayName) > 100 || len(description) > 500 {
		return errors.New("invalid organization fields")
	}
	return nil
}

func oneOrganization(result result) (Organization, error) {
	if len(result.Values) == 0 {
		return Organization{}, ErrNotFound
	}
	if len(result.Values) != 1 {
		return Organization{}, errors.New("unexpected organization row count")
	}
	return decodeOrganization(result.Values[0])
}

func decodeOrganization(row []json.RawMessage) (Organization, error) {
	var item Organization
	if len(row) != 5 {
		return item, errors.New("invalid organization row")
	}
	for i, target := range []any{&item.Name, &item.DisplayName, &item.Description, &item.CreatedAt, &item.UpdatedAt} {
		if err := json.Unmarshal(row[i], target); err != nil {
			return Organization{}, fmt.Errorf("decode organization: %w", err)
		}
	}
	return item, nil
}
