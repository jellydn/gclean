package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// UndoCache persists pre-trash message records so `gclean undo` can restore
// them. It lives in storage (not cli) so the engine pipeline can write it as
// a stage without importing os/env. The CLI still owns the *path* (via
// GCLEAN_UNDO_CACHE); the engine passes that path in.
const undoCacheVersion = 1

type undoCache struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Records  []StoredMessage `json:"records"`
}

// checksumRecords hashes the canonical JSON of the records so a partial write
// or external tampering is detected before the records are re-inserted.
func checksumRecords(recs []StoredMessage) (string, error) {
	payload, err := json.Marshal(recs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// SaveUndoCache writes the pre-trash records to path with an integrity tag.
func SaveUndoCache(path string, recs []StoredMessage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sum, err := checksumRecords(recs)
	if err != nil {
		return err
	}
	c := undoCache{Version: undoCacheVersion, Checksum: sum, Records: recs}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// LoadUndoCache reads pre-trash records, verifying the integrity tag. A
// missing file is not an error (nothing to undo). A checksum mismatch or
// unsupported version is — a corrupt cache must not silently re-upsert
// strange rows.
func LoadUndoCache(path string) ([]StoredMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c undoCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Checksum != "" {
		if c.Version != undoCacheVersion {
			return nil, fmt.Errorf("undo cache version %d unsupported (want %d)", c.Version, undoCacheVersion)
		}
		want, err := checksumRecords(c.Records)
		if err != nil {
			return nil, err
		}
		if want != c.Checksum {
			return nil, errors.New("undo cache checksum mismatch: file may be corrupt or partially written")
		}
	}
	return c.Records, nil
}
