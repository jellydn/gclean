package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelection_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selection.json")
	want := []string{"a" + "@" + "example.com", "b" + "@" + "example.com"}
	if err := SaveSelection(path, want); err != nil {
		t.Fatalf("SaveSelection: %v", err)
	}
	got, err := LoadSelection(path)
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("selection = %v, want %v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat selection: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("selection permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadSelection_LegacyFormatAndNormalization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selection.json")
	data := `{"selectors":[" a@example.com ","a@example.com",""],"ts":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSelection(path)
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}
	if len(got) != 1 || got[0] != "a@example.com" {
		t.Fatalf("selection = %v, want one normalized sender", got)
	}
}

func TestLoadSelection_MissingIsUnrestricted(t *testing.T) {
	got, err := LoadSelection(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("LoadSelection: %v", err)
	}
	if got != nil {
		t.Fatalf("missing selection = %v, want nil", got)
	}
}

func TestLoadSelection_RejectsMalformedOrEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selection.json")
	if err := os.WriteFile(path, []byte(`{"senders":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSelection(path); err == nil {
		t.Fatal("empty selection should fail")
	}
}
