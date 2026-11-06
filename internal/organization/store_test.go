package organization

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreCRUDAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mini-harbor.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	created, err := store.Create(ctx, "platform", "Platform", "Shared platform images")
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "platform" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("unexpected organization: %#v", created)
	}
	if _, err := store.Create(ctx, "platform", "Duplicate", ""); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate error = %v", err)
	}

	updated, err := store.Update(ctx, "platform", "Core Platform", "Production images")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Core Platform" || updated.Description != "Production images" {
		t.Fatalf("unexpected update: %#v", updated)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	organizations, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(organizations) != 1 || organizations[0].Name != "platform" {
		t.Fatalf("organizations = %#v", organizations)
	}
	if err := store.Delete(ctx, "platform"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "platform"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted error = %v", err)
	}
}

func TestValidName(t *testing.T) {
	for _, name := range []string{"platform", "team-one", "team.one", "team_one"} {
		if !ValidName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}
	for _, name := range []string{"", "Platform", "team/one", "-team"} {
		if ValidName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}
