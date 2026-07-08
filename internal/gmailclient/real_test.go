package gmailclient

import (
	"os"
	"testing"
)

func TestNewRealClient_MissingPath(t *testing.T) {
	_, err := NewRealClient("")
	if err != ErrCredentialsMissing {
		t.Fatalf("want ErrCredentialsMissing, got %v", err)
	}
}

func TestNewRealClient_MissingCredentials(t *testing.T) {
	tmp := t.TempDir()
	_, err := NewRealClient(tmp + "/nonexistent.json")
	if err == nil {
		t.Fatal("want error for missing credentials file, got nil")
	}
}

func TestRealClient_TrashMethods_NotImplemented(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("GCLEAN_TOKEN_PATH", tmp+"/token.json")

	creds := tmp + "/credentials.json"
	if err := os.WriteFile(creds, []byte(`{"installed":{"client_id":"x","client_secret":"y","redirect_uris":["http://localhost"],"auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp+"/token.json", []byte(`{"access_token":"x","token_type":"Bearer","expiry":"2099-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := NewRealClient(creds)
	if err != nil {
		t.Fatalf("construction should succeed with valid files, got %v", err)
	}
	if c == nil {
		t.Fatal("construction returned nil client")
	}

	if err := c.TrashMessages([]string{"m1"}); err != ErrNotImplemented {
		t.Errorf("TrashMessages: want ErrNotImplemented, got %v", err)
	}
	if err := c.EmptyTrash(); err != ErrNotImplemented {
		t.Errorf("EmptyTrash: want ErrNotImplemented, got %v", err)
	}
	if err := c.RestoreFromTrash([]string{"m1"}); err != ErrNotImplemented {
		t.Errorf("RestoreFromTrash: want ErrNotImplemented, got %v", err)
	}
}
