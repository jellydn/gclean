package engine

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gclean/internal/defang"
	"gclean/internal/gmailclient"
	"gclean/internal/models"
	"gclean/internal/storage"
)

func TestNormalizeSenderAddress(t *testing.T) {
	address := defang.MkEmail("Notifications+Product", "Example.COM")
	got, err := NormalizeSenderAddress("  " + address + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ToLower(address); got != want {
		t.Fatalf("NormalizeSenderAddress() = %q, want %q", got, want)
	}

	for _, input := range []string{"", "example.com", "Name <" + address + ">", "user@localhost", "user@example.com OR from:other@example.com"} {
		if _, err := NormalizeSenderAddress(input); err == nil {
			t.Errorf("NormalizeSenderAddress(%q) error = nil", input)
		}
	}
}

func TestPreviewSenderTrashExactMatchesAfterBroadGmailQuery(t *testing.T) {
	wanted := defang.MkEmail("notice", "example.com")
	lookalike := defang.MkEmail("othernotice", "example.com")
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	client := gmailclient.NewFakeClientFromMessages([]*models.Message{
		{ID: "wanted", Sender: models.Sender{Email: wanted}, Subject: "Wanted", Date: old, Size: 20},
		{ID: "spam", Sender: models.Sender{Email: wanted}, Subject: "Spam", Date: old, Size: 5, Labels: []string{"SPAM"}},
		{ID: "already-trashed", Sender: models.Sender{Email: wanted}, Subject: "Trashed", Date: old, Size: 80, Labels: []string{"TRASH"}},
		{ID: "lookalike", Sender: models.Sender{Email: lookalike}, Subject: "Must stay", Date: old, Size: 40},
	})

	preview, err := PreviewSenderTrash(client, "fixture", strings.ToUpper(wanted))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Sender != wanted || preview.Query != `from:"`+wanted+`" -in:trash` || preview.Count != 2 || preview.Bytes != 25 {
		t.Fatalf("preview = %+v", preview)
	}
	if len(preview.records) != 2 || preview.records[0].ID != "wanted" || preview.records[1].ID != "spam" {
		t.Fatalf("records = %+v, want exact sender outside Trash", preview.records)
	}

	store, err := storage.Open(filepath.Join(t.TempDir(), "gclean.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	journal := &Reconciler{Store: store, CachePath: filepath.Join(t.TempDir(), "undo.json"), Account: "fixture", Client: client}
	outcome, err := ApplySenderTrash(journal, preview)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Moved) != 2 || outcome.Moved[0] != "wanted" || outcome.Moved[1] != "spam" {
		t.Fatalf("moved = %v, want wanted and spam", outcome.Moved)
	}
	if trashed := client.TrashedIDs(); len(trashed) != 2 {
		t.Fatalf("trashed = %v, want two exact sender messages", trashed)
	}
}

func TestApplySenderTrashReconcilesPartialFailureWithoutPriorScan(t *testing.T) {
	sender := defang.MkEmail("receipts", "example.com")
	old := time.Date(2019, 4, 3, 2, 1, 0, 0, time.UTC)
	reader := gmailclient.NewFakeClientFromMessages([]*models.Message{
		{ID: "receipt-small", Sender: models.Sender{Email: sender}, Subject: "Small", Date: old, Size: 13},
		{ID: "receipt-large", Sender: models.Sender{Email: sender}, Subject: "Large", Date: old, Size: 89},
	})
	preview, err := PreviewSenderTrash(reader, "fixture", sender)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Count != 2 {
		t.Fatalf("preview count = %d, want 2", preview.Count)
	}

	store, err := storage.Open(filepath.Join(t.TempDir(), "gclean.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	cachePath := filepath.Join(t.TempDir(), "undo.json")
	client := &journalClient{
		trashed:  map[string]bool{},
		trashIDs: []string{"receipt-large"},
		trashErr: errors.New("second message rejected"),
	}
	journal := &Reconciler{Store: store, CachePath: cachePath, Account: "fixture", Client: client}

	outcome, err := ApplySenderTrash(journal, preview)
	if err == nil {
		t.Fatal("ApplySenderTrash() error = nil, want partial mutation error")
	}
	if len(outcome.Moved) != 1 || outcome.Moved[0] != "receipt-large" {
		t.Fatalf("moved = %v, want only receipt-large", outcome.Moved)
	}
	cached, err := storage.LoadUndoCache(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].ID != "receipt-large" {
		t.Fatalf("cache = %+v, want only receipt-large", cached)
	}
	if count, err := store.CountAll(); err != nil || count != 0 {
		t.Fatalf("unscanned store after partial trash: count=%d error=%v", count, err)
	}
}

func TestSenderTrashPreviewIDIgnoresListOrder(t *testing.T) {
	sender := defang.MkEmail("notice", "example.com")
	a := []storage.StoredMessage{{ID: "a"}, {ID: "b"}}
	b := []storage.StoredMessage{{ID: "b"}, {ID: "a"}}
	if senderTrashPreviewID("fixture", sender, a) != senderTrashPreviewID("fixture", sender, b) {
		t.Fatal("preview ID changed with list order")
	}
	if senderTrashPreviewID("other-account", sender, a) == senderTrashPreviewID("fixture", sender, a) {
		t.Fatal("preview ID did not change with Gmail account")
	}
}
