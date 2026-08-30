package storage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStore_BindsAccountAndReplacesScans(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.BindAccount("account-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAll([]StoredMessage{{ID: "old"}, {ID: "keep"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceAll([]StoredMessage{{ID: "keep"}, {ID: "new"}}); err != nil {
		t.Fatal(err)
	}
	count, err := store.CountAll()
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count after replacement = %d, want 2", count)
	}
	if err := store.BindAccount("account-b"); err == nil || !strings.Contains(err.Error(), "account-a") {
		t.Fatalf("account mismatch error = %v", err)
	}
}

func TestStore_RejectsUnownedLegacyRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Upsert(StoredMessage{ID: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindAccount("account-a"); err == nil || !strings.Contains(err.Error(), "no Gmail account identity") {
		t.Fatalf("legacy bind error = %v", err)
	}
}
