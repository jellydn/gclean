package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// UndoCache persists pre-trash message records so `gclean undo` can restore
// them. It lives in storage (not cli) so the engine pipeline can write it as
// a stage without importing os/env. The CLI still owns the *path* (via
// GCLEAN_UNDO_CACHE); the engine passes that path in.
const undoCacheVersion = 2

// UndoBatch is the account-bound recovery unit created before moving messages
// to Trash.
type UndoBatch struct {
	Account string          `json:"account"`
	Records []StoredMessage `json:"records"`
}

type undoCache struct {
	Version  int             `json:"version"`
	Checksum string          `json:"checksum"`
	Account  string          `json:"account,omitempty"`
	Records  []StoredMessage `json:"records"`
}

// checksumBatch hashes the account and records so tampering with either is
// detected before Gmail or the local database is mutated.
func checksumBatch(account string, recs []StoredMessage) (string, error) {
	payload, err := json.Marshal(UndoBatch{Account: account, Records: recs})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// SaveUndoCache writes the pre-trash records to path with an integrity tag.
// It writes and syncs a temporary file before renaming it into place so a
// crash cannot leave a partially-written cache at the canonical path. It
// refuses to overwrite a non-empty existing cache.
func SaveUndoCache(path string, recs []StoredMessage) error {
	return SaveUndoCacheForAccount(path, "", recs)
}

func SaveUndoCacheForAccount(path, account string, recs []StoredMessage) error {
	return writeUndoCache(path, account, recs, false)
}

// ReplaceUndoCache overwrites an existing undo cache. It is used to trim the
// records to the subset that actually reached Trash after a partial mutation
// (or that remain in Trash after a partial restore/purge), so `gclean undo`
// only ever touches the messages that really need it. The write is still
// atomic.
func ReplaceUndoCache(path string, recs []StoredMessage) error {
	return ReplaceUndoCacheForAccount(path, "", recs)
}

func ReplaceUndoCacheForAccount(path, account string, recs []StoredMessage) error {
	return writeUndoCache(path, account, recs, true)
}

// ReplaceOrRemoveUndoCache rewrites the cache to the given records, or
// removes the file entirely when no records remain. An empty-records cache
// file would block a retried `clean` (SaveUndoCache refuses to overwrite a
// non-empty file), so after a reconcile that leaves nothing in Trash the
// correct end state is "no cache at all", not "a cache with zero records".
func ReplaceOrRemoveUndoCache(path string, recs []StoredMessage) error {
	return ReplaceOrRemoveUndoCacheForAccount(path, "", recs)
}

func ReplaceOrRemoveUndoCacheForAccount(path, account string, recs []StoredMessage) error {
	if len(recs) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return ReplaceUndoCacheForAccount(path, account, recs)
}

func writeUndoCache(path, account string, recs []StoredMessage, overwrite bool) error {
	if !overwrite {
		if info, err := os.Stat(path); err == nil {
			if info.Size() > 0 {
				return fmt.Errorf("undo cache already exists at %s; run `gclean undo` or `gclean purge` first", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	sum, err := checksumBatch(account, recs)
	if err != nil {
		return err
	}
	c := undoCache{Version: undoCacheVersion, Checksum: sum, Account: account, Records: recs}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o600, ".undo-cache-*")
}

// LoadUndoCache reads pre-trash records, verifying the integrity tag. A
// missing file is not an error (nothing to undo). A checksum mismatch or
// unsupported version is — a corrupt cache must not silently re-upsert
// strange rows.
func LoadUndoCache(path string) ([]StoredMessage, error) {
	batch, err := LoadUndoBatch(path)
	return batch.Records, err
}

// LoadUndoBatch reads and verifies the account-bound undo batch. Version 1
// files remain readable but have an empty account and are rejected by
// ValidateUndoAccount before production mutations.
func LoadUndoBatch(path string) (UndoBatch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return UndoBatch{}, nil
		}
		return UndoBatch{}, err
	}
	var c undoCache
	if err := json.Unmarshal(data, &c); err != nil {
		return UndoBatch{}, err
	}
	if c.Checksum != "" {
		var want string
		switch c.Version {
		case 1:
			payload, err := json.Marshal(c.Records)
			if err != nil {
				return UndoBatch{}, err
			}
			sum := sha256.Sum256(payload)
			want = hex.EncodeToString(sum[:])
		case undoCacheVersion:
			var err error
			want, err = checksumBatch(c.Account, c.Records)
			if err != nil {
				return UndoBatch{}, err
			}
		default:
			return UndoBatch{}, fmt.Errorf("undo cache version %d unsupported (want %d)", c.Version, undoCacheVersion)
		}
		if want != c.Checksum {
			return UndoBatch{}, errors.New("undo cache checksum mismatch: file may be corrupt or partially written")
		}
	}
	return UndoBatch{Account: c.Account, Records: c.Records}, nil
}

// ValidateUndoAccount prevents a cache from one Gmail account (or an unowned
// legacy cache) from being consumed while another account is authenticated.
func ValidateUndoAccount(path, account string) error {
	if account == "" {
		return nil
	}
	batch, err := LoadUndoBatch(path)
	if err != nil || len(batch.Records) == 0 {
		return err
	}
	if batch.Account == "" {
		return errors.New("undo cache predates account binding; restore it with the previous gclean version or remove it before continuing")
	}
	if batch.Account != account {
		return fmt.Errorf("undo cache belongs to Gmail account %q, not %q; switch accounts before restoring or purging", batch.Account, account)
	}
	return nil
}
