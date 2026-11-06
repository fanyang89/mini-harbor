package organization

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fanyang89/mini-harbor/internal/ent"
	entorganization "github.com/fanyang89/mini-harbor/internal/ent/organization"
	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrExists   = errors.New("organization already exists")
	ErrNotFound = errors.New("organization not found")
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

type Organization struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"displayName"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Store struct {
	client *ent.Client
}

func Open(ctx context.Context, path string) (*Store, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	databaseURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	dsn := databaseURL.String() + "?_fk=1&_busy_timeout=5000&_journal_mode=WAL"
	client, err := ent.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open organization database: %w", err)
	}
	if err := client.Schema.Create(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("migrate organization database: %w", err)
	}
	return &Store{client: client}, nil
}

func (s *Store) Close() error {
	return s.client.Close()
}

func (s *Store) List(ctx context.Context) ([]Organization, error) {
	rows, err := s.client.Organization.Query().Order(ent.Asc(entorganization.FieldID)).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	organizations := make([]Organization, len(rows))
	for i, row := range rows {
		organizations[i] = fromEntity(row)
	}
	return organizations, nil
}

func (s *Store) Get(ctx context.Context, name string) (Organization, error) {
	row, err := s.client.Organization.Get(ctx, name)
	if ent.IsNotFound(err) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("get organization: %w", err)
	}
	return fromEntity(row), nil
}

func (s *Store) Create(ctx context.Context, name, displayName, description string) (Organization, error) {
	row, err := s.client.Organization.Create().
		SetID(name).
		SetDisplayName(displayName).
		SetDescription(description).
		Save(ctx)
	if ent.IsConstraintError(err) {
		return Organization{}, ErrExists
	}
	if err != nil {
		return Organization{}, fmt.Errorf("create organization: %w", err)
	}
	return fromEntity(row), nil
}

func (s *Store) Update(ctx context.Context, name, displayName, description string) (Organization, error) {
	row, err := s.client.Organization.UpdateOneID(name).
		SetDisplayName(displayName).
		SetDescription(description).
		Save(ctx)
	if ent.IsNotFound(err) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("update organization: %w", err)
	}
	return fromEntity(row), nil
}

func (s *Store) Delete(ctx context.Context, name string) error {
	if err := s.client.Organization.DeleteOneID(name).Exec(ctx); ent.IsNotFound(err) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("delete organization: %w", err)
	}
	return nil
}

func ValidName(name string) bool {
	return len(name) <= 63 && namePattern.MatchString(name)
}

func fromEntity(row *ent.Organization) Organization {
	return Organization{
		Name:        row.ID,
		DisplayName: row.DisplayName,
		Description: row.Description,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}
