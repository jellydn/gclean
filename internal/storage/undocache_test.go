package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndoCache_AccountBindingAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	records := []StoredMessage{{ID: "m1"}}
	if err := SaveUndoCacheForAccount(path, "account-a", records); err != nil {
		t.Fatal(err)
	}
	batch, err := LoadUndoBatch(path)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Account != "account-a" || len(batch.Records) != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	if err := ValidateUndoAccount(path, "account-b"); err == nil || !strings.Contains(err.Error(), "account-a") {
		t.Fatalf("mismatch error = %v", err)
	}

	payload, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	legacy, err := json.Marshal(map[string]any{
		"version": 1, "checksum": hex.EncodeToString(sum[:]), "records": records,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUndoAccount(path, "account-a"); err == nil || !strings.Contains(err.Error(), "predates account binding") {
		t.Fatalf("legacy validation error = %v", err)
	}
}

func TestMutationLockRejectsConcurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo.json")
	first, err := AcquireMutationLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Unlock() }()
	if _, err := AcquireMutationLock(path); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock error = %v", err)
	}
}

func TestSaveUndoCache_RoundTripsAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "undo-cache.json")
	records := []StoredMessage{{
		ID:          "m1",
		SenderEmail: "sender" + "@" + "example.com",
		Subject:     "subject",
		Date:        "2026-01-01T00:00:00Z",
		Size:        42,
		IsJunk:      true,
	}}

	if err := SaveUndoCache(path, records); err != nil {
		t.Fatalf("SaveUndoCache: %v", err)
	}
	got, err := LoadUndoCache(path)
	if err != nil {
		t.Fatalf("LoadUndoCache: %v", err)
	}
	if len(got) != 1 || got[0].ID != records[0].ID || got[0].Subject != records[0].Subject {
		t.Fatalf("loaded records = %#v, want %#v", got, records)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache permissions = %o, want 600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read cache directory: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".undo-cache-") {
			t.Errorf("temporary cache file remains: %s", entry.Name())
		}
	}
}

func TestSaveUndoCache_DoesNotOverwriteExistingBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo-cache.json")
	if err := SaveUndoCache(path, []StoredMessage{{ID: "m1"}}); err != nil {
		t.Fatalf("initial SaveUndoCache: %v", err)
	}
	if err := SaveUndoCache(path, []StoredMessage{{ID: "m2"}}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second SaveUndoCache error = %v, want existing-cache error", err)
	}
	got, err := LoadUndoCache(path)
	if err != nil {
		t.Fatalf("LoadUndoCache after rejected overwrite: %v", err)
	}
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("cache after rejected overwrite = %#v, want m1", got)
	}
}

func TestReplaceOrRemoveUndoCache_ReplacesWhenNonEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo-cache.json")
	if err := SaveUndoCache(path, []StoredMessage{{ID: "m1"}}); err != nil {
		t.Fatalf("SaveUndoCache: %v", err)
	}
	// Replace branch: non-empty records overwrite the existing cache.
	if err := ReplaceOrRemoveUndoCache(path, []StoredMessage{{ID: "m2"}}); err != nil {
		t.Fatalf("ReplaceOrRemoveUndoCache: %v", err)
	}
	got, err := LoadUndoCache(path)
	if err != nil {
		t.Fatalf("LoadUndoCache: %v", err)
	}
	if len(got) != 1 || got[0].ID != "m2" {
		t.Fatalf("cache after replace = %#v, want m2", got)
	}
}

func TestReplaceOrRemoveUndoCache_RemovesWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo-cache.json")
	if err := SaveUndoCache(path, []StoredMessage{{ID: "m1"}}); err != nil {
		t.Fatalf("SaveUndoCache: %v", err)
	}
	// Remove branch: empty records delete the file entirely rather than
	// writing an empty-records cache that would block a retried clean.
	if err := ReplaceOrRemoveUndoCache(path, nil); err != nil {
		t.Fatalf("ReplaceOrRemoveUndoCache(nil): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cache file should be removed, stat err = %v", err)
	}
	// A removed cache loads as "nothing to undo" (missing file is not an error).
	got, err := LoadUndoCache(path)
	if err != nil {
		t.Fatalf("LoadUndoCache after remove: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cache after remove = %#v, want empty", got)
	}
}

func TestReplaceOrRemoveUndoCache_RemoveIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo-cache.json")
	// No file exists yet; removing an absent cache must not error.
	if err := ReplaceOrRemoveUndoCache(path, nil); err != nil {
		t.Fatalf("ReplaceOrRemoveUndoCache(nil) on absent file: %v", err)
	}
}

func TestLoadUndoCache_RejectsTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "undo-cache.json")
	if err := SaveUndoCache(path, []StoredMessage{{ID: "m1"}}); err != nil {
		t.Fatalf("SaveUndoCache: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	data = []byte(strings.Replace(string(data), `"m1"`, `"m2"`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("tamper cache: %v", err)
	}
	if _, err := LoadUndoCache(path); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("LoadUndoCache error = %v, want checksum mismatch", err)
	}
}
