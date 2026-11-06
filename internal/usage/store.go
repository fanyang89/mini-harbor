package usage

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	root string
	mu   sync.Mutex
}

type record struct {
	Repository string    `json:"repository"`
	Digest     string    `json:"digest"`
	LastUsed   time.Time `json:"lastUsed"`
}

func Open(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("image usage root is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create image usage root: %w", err)
	}
	return &Store{root: root}, nil
}

func (s *Store) Record(repository, digest string, usedAt time.Time) error {
	if repository == "" || digest == "" || usedAt.IsZero() {
		return errors.New("image usage record is incomplete")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if previous, ok, err := s.lastUsed(repository, digest); err != nil {
		return err
	} else if ok && !usedAt.After(previous) {
		return nil
	}

	entry := record{Repository: repository, Digest: digest, LastUsed: usedAt.UTC()}
	temporary, err := os.CreateTemp(s.root, ".usage-*")
	if err != nil {
		return fmt.Errorf("create image usage record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set image usage permissions: %w", err)
	}
	if err := json.NewEncoder(temporary).Encode(entry); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode image usage record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync image usage record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close image usage record: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path(repository, digest)); err != nil {
		return fmt.Errorf("replace image usage record: %w", err)
	}
	return nil
}

func (s *Store) LastUsed(repository, digest string) (time.Time, bool, error) {
	if repository == "" || digest == "" {
		return time.Time{}, false, nil
	}
	return s.lastUsed(repository, digest)
}

func (s *Store) lastUsed(repository, digest string) (time.Time, bool, error) {
	data, err := os.ReadFile(s.path(repository, digest))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read image usage record: %w", err)
	}
	var entry record
	if err := json.Unmarshal(data, &entry); err != nil {
		return time.Time{}, false, fmt.Errorf("decode image usage record: %w", err)
	}
	if entry.Repository != repository || entry.Digest != digest || entry.LastUsed.IsZero() {
		return time.Time{}, false, errors.New("image usage record does not match manifest")
	}
	return entry.LastUsed, true, nil
}

func (s *Store) path(repository, digest string) string {
	key := sha256.Sum256([]byte(repository + "\x00" + digest))
	return filepath.Join(s.root, fmt.Sprintf("%x.json", key))
}
