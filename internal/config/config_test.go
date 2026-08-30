package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveRoundTripsExistingCLIConfigSecurely(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("GCLEAN_CONFIG_PATH", path)
	legacy := []byte("keep:\n  contacts: true\n  recent_days: 30\ndelete:\n  - 'has:unsubscribe older_than:30d'\narchive: []\nignore:\n  - example.org\n")
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	document, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	document.Keep.Starred = true
	if err := Save(document); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded, document) {
		t.Fatalf("reloaded config = %+v, want %+v", reloaded, document)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveRejectsInvalidRulesWithoutReplacingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("GCLEAN_CONFIG_PATH", path)
	want := []byte("existing")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(Document{Delete: []string{"unknown:value"}}); err == nil {
		t.Fatal("Save should reject an invalid rule")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("invalid save replaced config: %q", got)
	}
}
