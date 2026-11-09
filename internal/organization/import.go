package organization

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ImportReport describes a comparison against the target before any writes.
type ImportReport struct {
	Source     int      `json:"source"`
	New        int      `json:"new"`
	Identical  int      `json:"identical"`
	Conflicts  []string `json:"conflicts"`
	Initialize bool     `json:"initialize"`
	Verified   bool     `json:"verified"`
}

// Identical conflicts are no-ops. A conflicting edit violates the NOT NULL key
// and rolls back the whole batch, even if the edit happened after preflight.
const importInsert = `INSERT INTO organizations (` + columns + `) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET id=NULL WHERE
 organizations.display_name IS NOT excluded.display_name OR
 organizations.description IS NOT excluded.description OR
 organizations.created_at IS NOT excluded.created_at OR
 organizations.updated_at IS NOT excluded.updated_at`

// ImportOrganizations imports a validated legacy snapshot. Dry runs never write to
// rqlite; real imports initialize a fresh target, import atomically, and verify.
// Submitted writes are not replayed on ambiguous responses.
func ImportOrganizations(ctx context.Context, source []Organization, endpoints []string, dryRun bool) (ImportReport, error) {
	report := ImportReport{Conflicts: []string{}}
	seen := make(map[string]bool, len(source))
	for _, item := range source {
		if err := validate(item.Name, item.DisplayName, item.Description); err != nil {
			return report, err
		}
		if seen[item.Name] {
			return report, fmt.Errorf("duplicate source organization %q", item.Name)
		}
		seen[item.Name] = true
	}
	report.Source = len(source)
	store, err := newStore(endpoints)
	if err != nil {
		return report, err
	}
	defer store.Close()
	// Imports can be larger than ordinary organization CRUD requests.
	store.client.Timeout = 30 * time.Second
	tables, err := store.query(ctx, [][]any{{"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"}})
	if err != nil {
		return report, err
	}
	report.Initialize = len(tables[0].Values) == 0
	var target []Organization
	if !report.Initialize {
		versions, err := store.query(ctx, [][]any{{"SELECT version FROM schema_migrations ORDER BY version"}})
		if err != nil {
			return report, err
		}
		if len(versions[0].Values) == 0 || len(versions[0].Values) > len(migrations) {
			return report, errors.New("unsupported target schema version")
		}
		for i, row := range versions[0].Values {
			if len(row) != 1 || string(row[0]) != fmt.Sprint(i+1) {
				return report, errors.New("unsupported target schema version sequence")
			}
		}
		target, err = store.List(ctx)
		if err != nil {
			return report, err
		}
	}
	report.New, report.Identical, report.Conflicts = compareImport(source, target)
	if len(report.Conflicts) != 0 {
		return report, errors.New("conflicting organizations; target data was not changed")
	}
	if dryRun {
		return report, nil
	}
	if report.Initialize {
		if err := store.migrate(ctx); err != nil {
			return report, err
		}
	}
	if len(source) > 0 {
		if err := store.importRows(ctx, source); err != nil {
			return report, err
		}
	}
	target, err = store.List(ctx)
	if err != nil {
		return report, fmt.Errorf("verify imported data: %w", err)
	}
	missing, _, conflicts := compareImport(source, target)
	if missing != 0 || len(conflicts) != 0 {
		return report, errors.New("post-import verification failed; inspect target before retrying")
	}
	report.Verified = true
	return report, nil
}

func (s *Store) importRows(ctx context.Context, items []Organization) error {
	statements := make([][]any, 0, len(items))
	for _, item := range items {
		statements = append(statements, []any{importInsert, item.Name, item.DisplayName, item.Description,
			item.CreatedAt.UTC().Format(time.RFC3339Nano), item.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	_, err := s.write(ctx, statements)
	return err
}

func compareImport(source, target []Organization) (missing, identical int, conflicts []string) {
	conflicts = []string{}
	byName := make(map[string]Organization, len(target))
	for _, item := range target {
		byName[item.Name] = item
	}
	for _, item := range source {
		existing, ok := byName[item.Name]
		switch {
		case !ok:
			missing++
		case existing.DisplayName == item.DisplayName && existing.Description == item.Description && existing.CreatedAt.Equal(item.CreatedAt) && existing.UpdatedAt.Equal(item.UpdatedAt):
			identical++
		default:
			conflicts = append(conflicts, item.Name)
		}
	}
	return
}
