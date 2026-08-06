package gmailclient

import (
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
