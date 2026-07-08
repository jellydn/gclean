package engine

import (
	"testing"
	"time"

	"gclean/internal/models"
)

// MkEmail() is exported from internal/engine/testutil.go. Same package.

func TestProtect_Starred_SkippedWhenDisabledInConfig(t *testing.T) {
	m := &models.Message{
		Sender: models.Sender{Email: MkEmail("mailbox", "example.com")},
		Date:   time.Now().Add(-730 * 24 * time.Hour),
		Labels: []string{"STARRED"},
	}
	// Starred off in config: must NOT keep.
	r := Protect(m, KeepConfig{Starred: false}, nil)
	if r.Protected {
		t.Fatalf("expected not protected when starred=false, got %+v", r)
	}
	r = Protect(m, KeepConfig{Starred: true}, nil)
	if !r.Protected || r.Reason != models.ReasonStarred {
		t.Fatalf("expected protected by star, got %+v", r)
	}
}

func TestProtect_RecentWindow(t *testing.T) {
	m := &models.Message{
		Sender: models.Sender{Email: MkEmail("someone", "example.com")},
		Date:   time.Now().Add(-30 * 24 * time.Hour),
		Labels: []string{},
	}
	r := Protect(m, KeepConfig{RecentDays: 365}, nil)
	if !r.Protected || r.Reason != models.ReasonRecent {
		t.Fatalf("recent date must be protected, got %+v", r)
	}
	old := &models.Message{
		Sender: models.Sender{Email: MkEmail("old-msg", "example.com")},
		Date:   time.Now().Add(-400 * 24 * time.Hour),
		Labels: []string{},
	}
	r = Protect(old, KeepConfig{RecentDays: 365}, nil)
	if r.Protected {
		t.Fatalf("old (400d) email must NOT be protected, got %+v", r)
	}
}

func TestProtect_Contact(t *testing.T) {
	m := &models.Message{
		Sender: models.Sender{Email: MkEmail("alice", "example.com"), IsContact: true},
		Date:   time.Now().Add(-100 * 24 * time.Hour),
		Labels: []string{},
	}
	r := Protect(m, KeepConfig{RecentDays: 365, Contacts: true}, nil)
	if !r.Protected || r.Reason != models.ReasonContact {
		t.Fatalf("contact must be protected, got %+v", r)
	}
}

func TestProtect_Whitelist(t *testing.T) {
	m := &models.Message{
		Sender: models.Sender{Email: MkEmail("user", "bank.com")},
		Date:   time.Now().Add(-2000 * 24 * time.Hour),
		Labels: []string{},
	}
	r := Protect(m, KeepConfig{}, []string{"bank.com"})
	if !r.Protected || r.Reason != models.ReasonWhitelisted {
		t.Fatalf("whitelisted bank.com must be protected, got %+v", r)
	}
}

func TestProtect_SentByUser(t *testing.T) {
	m := &models.Message{
		Sender: models.Sender{Email: MkEmail("me-self", "example.com")},
		Date:   time.Now().Add(-100 * 24 * time.Hour),
		Labels: []string{"SENT"},
	}
	r := Protect(m, KeepConfig{SentByUser: true}, nil)
	if !r.Protected || r.Reason != models.ReasonSentByUser {
		t.Fatalf("SENT label must be protected, got %+v", r)
	}
}
