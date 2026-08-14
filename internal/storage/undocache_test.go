package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
