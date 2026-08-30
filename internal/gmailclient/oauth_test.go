package gmailclient

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/gmail/v1"
)

func TestOAuthScopes_DefaultIsModifyAndPurgeIsOptIn(t *testing.T) {
	credentials := `{"installed":{"client_id":"id","client_secret":"secret","auth_uri":"https://example.invalid/auth","token_uri":"https://example.invalid/token","redirect_uris":["http://localhost"]}}`
	path := filepath.Join(t.TempDir(), "credentials.json")
	if err := os.WriteFile(path, []byte(credentials), 0o600); err != nil {
		t.Fatal(err)
	}
	defaultConfig, err := LoadConfigWithRedirectAndPurge(path, "http://localhost/callback", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaultConfig.Scopes) != 1 || defaultConfig.Scopes[0] != gmail.GmailModifyScope {
		t.Fatalf("default scopes = %v, want only gmail.modify", defaultConfig.Scopes)
	}
	purgeConfig, err := LoadConfigWithRedirectAndPurge(path, "http://localhost/callback", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(purgeConfig.Scopes) != 2 || purgeConfig.Scopes[1] != gmail.MailGoogleComScope {
		t.Fatalf("purge scopes = %v, want modify + full access", purgeConfig.Scopes)
	}
}

func TestAuthCodeServer_WaitForCodeReceivesCode(t *testing.T) {
	server := &AuthCodeServer{code: make(chan string, 1), errCh: make(chan error, 1)}
	server.code <- "authorization-code"

	got, err := server.WaitForCode(time.Second)
	if err != nil {
		t.Fatalf("WaitForCode returned error: %v", err)
	}
	if got != "authorization-code" {
		t.Fatalf("WaitForCode returned %q, want authorization-code", got)
	}
}

func TestAuthCodeServer_WaitForCodeTimesOut(t *testing.T) {
	server := &AuthCodeServer{code: make(chan string, 1), errCh: make(chan error, 1)}

	start := time.Now()
	_, err := server.WaitForCode(5 * time.Millisecond)
	if err == nil {
		t.Fatal("WaitForCode should return an error after timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("WaitForCode took too long to time out: %v", elapsed)
	}
}

func TestAuthCodeServer_UsesAvailableLocalhostPort(t *testing.T) {
	server, err := NewAuthCodeServer("expected-state")
	if err != nil {
		t.Fatalf("NewAuthCodeServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	if !strings.HasPrefix(server.RedirectURL(), "http://localhost:") {
		t.Fatalf("redirect URL = %q, want localhost port", server.RedirectURL())
	}
	response, err := http.Get(server.RedirectURL() + "/?code=authorization-code&state=expected-state")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", response.StatusCode)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	got, err := server.WaitForCode(time.Second)
	if err != nil {
		t.Fatalf("WaitForCode: %v", err)
	}
	if got != "authorization-code" {
		t.Fatalf("code = %q, want authorization-code", got)
	}
}

func TestAuthCodeServer_RejectsMismatchedState(t *testing.T) {
	server, err := NewAuthCodeServer("expected")
	if err != nil {
		t.Fatalf("NewAuthCodeServer: %v", err)
	}
	defer func() { _ = server.Close() }()
	response, err := http.Get(server.RedirectURL() + "/?code=authorization-code&state=wrong")
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400", response.StatusCode)
	}
}
