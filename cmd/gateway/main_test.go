package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyCache(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "docker.io")
	sourcePath := filepath.Join(root, "docker.io-a1b2c3")
	if err := os.Mkdir(legacyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyPath, "marker"), []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyCache(legacyPath, sourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sourcePath, "marker")); err != nil {
		t.Fatalf("migrated cache marker: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy path still exists: %v", err)
	}
}

func TestMigrateLegacyCacheKeepsExistingSource(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "docker.io")
	sourcePath := filepath.Join(root, "docker.io-a1b2c3")
	if err := os.Mkdir(legacyPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(sourcePath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := migrateLegacyCache(legacyPath, sourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy cache should be untouched: %v", err)
	}
}
