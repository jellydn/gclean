package engine

import (
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
