package engine

import (
	"testing"
	"time"

	"gclean/internal/models"
)

func TestParseRule_DeleteUnsubscribe(t *testing.T) {
	r, err := ParseRule("delete", "has:unsubscribe older_than:180d")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Predicates) != 2 {
		t.Fatalf("want 2 predicates, got %d", len(r.Predicates))
	}
	if r.Predicates[0].Key != "has" || r.Predicates[0].Value != "unsubscribe" {
		t.Errorf("p0 = %v", r.Predicates[0])
	}
	if r.Predicates[1].Key != "older_than" || r.Predicates[1].Value != "180d" {
		t.Errorf("p1 = %v", r.Predicates[1])
	}
}

func TestParseRule_CommaTolerant(t *testing.T) {
	r, err := ParseRule("delete", "from:noreply,older_than:90d")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Predicates) != 2 {
		t.Fatalf("comma-separated should yield 2, got %d", len(r.Predicates))
	}
}

func TestParseRule_Invalid(t *testing.T) {
	if _, err := ParseRule("delete", "garbage_no_colon"); err == nil {
		t.Fatal("expected error for missing colon")
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"0d":   0,
		"1d":   24 * time.Hour,
		"180d": 180 * 24 * time.Hour,
	}
	for in, want := range cases {
		got, err := ParseDuration(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
	if _, err := ParseDuration("24h"); err == nil {
		t.Error("hour suffix should be rejected")
	}
	if _, err := ParseDuration("bogus"); err == nil {
		t.Error("garbage must be rejected")
	}
}

func TestParseByteSize(t *testing.T) {
	cases := map[string]int64{
		"100B": 100,
		"1KB":  1024,
		"2MB":  2 << 20,
		"3GB":  3 << 30,
		"3gb":  3 << 30,
	}
	for in, want := range cases {
		got, err := ParseByteSize(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %d want %d", in, got, want)
		}
	}
	if _, err := ParseByteSize("42"); err == nil {
		t.Error("missing suffix must error")
	}
}

func TestRule_Matches_HasAndOlderThan(t *testing.T) {
	r, _ := ParseRule("delete", "has:unsubscribe older_than:180d")
	old := &models.Message{
		Sender:  models.Sender{Email: "[email protected]"},
		Date:    time.Now().Add(-400 * 24 * time.Hour),
		Headers: map[string]string{"List-Unsubscribe": "<mailto:[email protected]>"},
	}
	if !r.Matches(old) {
		t.Error("expected match: old + has:unsubscribe")
	}
	recent := &models.Message{
		Date:    time.Now().Add(-10 * 24 * time.Hour),
		Headers: map[string]string{"List-Unsubscribe": "x"},
	}
	if r.Matches(recent) {
		t.Error("expected miss: recent")
	}
	noHeader := &models.Message{
		Date:    time.Now().Add(-400 * 24 * time.Hour),
		Headers: map[string]string{},
	}
	if r.Matches(noHeader) {
		t.Error("expected miss: no unsubscribe header")
	}
}

func TestRule_Matches_EmptyRuleNeverMatches(t *testing.T) {
	r, _ := ParseRule("delete", "")
	if r.Matches(&models.Message{}) {
		t.Error("empty rule must never match")
	}
}
