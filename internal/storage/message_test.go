package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gclean/internal/models"
)

func TestFromClassified_MapsMessageAndVerdict(t *testing.T) {
	c := &models.Classified{
		Message: &models.Message{
			ID:       "m1",
			ThreadID: "t1",
			Sender:   models.Sender{Email: "a@example.com", Name: "A", IsContact: true},
			Subject:  "hello",
			Date:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Size:     42,
			Labels:   []string{"INBOX", "IMPORTANT"},
			Headers:  map[string]string{"List-Unsubscribe": "<x>"},
		},
		IsJunk:     true,
		ReasonCode: models.ReasonNewsletter,
	}

	got := FromClassified(c, models.VerdictDelete)
	if got.ID != "m1" || got.ThreadID != "t1" {
		t.Fatalf("identity fields not mapped: %+v", got)
	}
	if got.SenderEmail != "a@example.com" || got.SenderName != "A" || !got.IsContact {
		t.Fatalf("sender fields not mapped: %+v", got)
	}
	if got.Subject != "hello" || got.Size != 42 {
		t.Fatalf("subject/size not mapped: %+v", got)
	}
	if got.Date != "2026-01-02T03:04:05Z" {
		t.Fatalf("date not mapped to RFC3339: %q", got.Date)
	}
	if got.Labels != "INBOX,IMPORTANT" {
		t.Fatalf("labels not joined: %q", got.Labels)
	}
	var headers map[string]string
	if err := json.Unmarshal([]byte(got.Headers), &headers); err != nil {
		t.Fatalf("headers not valid JSON: %v", err)
	}
	if headers["List-Unsubscribe"] != "<x>" {
		t.Fatalf("headers not round-tripped: %v", headers)
	}
	if got.IsJunk != true || got.JunkReason != models.ReasonNewsletter {
		t.Fatalf("classification not mapped: %+v", got)
	}
	if got.Verdict != int(models.VerdictDelete) {
		t.Fatalf("verdict not mapped: %d", got.Verdict)
	}
}

func TestFromClassified_SurvivesEmptySlices(t *testing.T) {
	c := &models.Classified{
		Message: &models.Message{
			ID:      "m2",
			Date:    time.Now(),
			Labels:  nil,
			Headers: nil,
		},
	}
	got := FromClassified(c, models.VerdictKeep)
	if strings.Contains(got.Labels, ",") && got.Labels != "" {
		t.Fatalf("empty labels should stay empty, got %q", got.Labels)
	}
	if got.Headers == "" {
		t.Fatalf("empty headers should still serialize to JSON, got empty string")
	}
}

func TestUpsertConflictPreservesPlannerVerdict(t *testing.T) {
	store, err := Open(t.TempDir() + "/gclean.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	message := StoredMessage{ID: "m1", Subject: "before", Verdict: int(models.VerdictKeep)}
	if err := store.Upsert(message); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVerdict("m1", int(models.VerdictProtected), "protect:contact", true); err != nil {
		t.Fatal(err)
	}

	message.Subject = "after"
	if err := store.Upsert(message); err != nil {
		t.Fatal(err)
	}

	var subject, reasons string
	var verdict, protected int
	if err := store.db.QueryRow(`SELECT subject, verdict, verdict_reasons, protected FROM messages WHERE id=?`, "m1").Scan(&subject, &verdict, &reasons, &protected); err != nil {
		t.Fatal(err)
	}
	if subject != "after" {
		t.Fatalf("subject = %q, want refreshed metadata", subject)
	}
	if verdict != int(models.VerdictProtected) || reasons != "protect:contact" || protected != 1 {
		t.Fatalf("planner stamp = (%d, %q, %d), want protected verdict preserved", verdict, reasons, protected)
	}
}
