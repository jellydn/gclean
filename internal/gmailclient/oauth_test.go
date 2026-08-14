package gmailclient

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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
	server, err := NewAuthCodeServer()
	if err != nil {
		t.Fatalf("NewAuthCodeServer: %v", err)
	}
	defer func() { _ = server.Close() }()

	if !strings.HasPrefix(server.RedirectURL(), "http://localhost:") {
		t.Fatalf("redirect URL = %q, want localhost port", server.RedirectURL())
	}
	response, err := http.Get(server.RedirectURL() + "/?code=authorization-code")
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
