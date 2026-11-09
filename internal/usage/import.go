package usage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Record is the legacy JSON format. Live services use rqlite instead.
type Record = record

// ReadLegacy reads a frozen directory without modifying source files.
func ReadLegacy(root string) ([]Record, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var records []Record
	store := &Store{root: root}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			return nil, err
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		if record.Repository == "" || record.Digest == "" || record.LastUsed.IsZero() || filepath.Base(store.path(record.Repository, record.Digest)) != entry.Name() {
			return nil, fmt.Errorf("invalid usage record %s", entry.Name())
		}
		records = append(records, record)
	}
	return records, nil
}
