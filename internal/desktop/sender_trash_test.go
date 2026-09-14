package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gclean/internal/storage"
)

func TestDesktopSenderTrashRequiresExactCurrentPreview(t *testing.T) {
	app, fake := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	sender := fakeAddress("offers")
	var preview senderTrashPreviewResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/preview", senderTrashRequest{Sender: strings.ToUpper(sender)}, &preview, http.StatusOK)
	if preview.Sender != sender || preview.Count != 1 || preview.PreviewID == "" {
		t.Fatalf("preview = %+v", preview)
	}

	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/trash", senderTrashRequest{
		Sender: sender, PreviewID: "stale", Confirmation: senderTrashConfirmation,
	}, &apiError, http.StatusConflict)
	if len(fake.TrashedIDs()) != 0 {
		t.Fatalf("stale preview trashed messages: %v", fake.TrashedIDs())
	}

	var result actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/trash", senderTrashRequest{
		Sender: sender, PreviewID: preview.PreviewID, Confirmation: senderTrashConfirmation,
	}, &result, http.StatusOK)
	if result.Count != 1 || len(fake.TrashedIDs()) != 1 {
		t.Fatalf("result = %+v, trashed = %v", result, fake.TrashedIDs())
	}
}

func TestDesktopSenderTrashConfirmationDirectCleanupAndRestore(t *testing.T) {
	app, fake := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	sender := fakeAddress("offers")
	var preview senderTrashPreviewResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/preview", senderTrashRequest{Sender: sender}, &preview, http.StatusOK)

	for _, confirmation := range []string{"", "MOVE PERSON MAIL TO TRASH"} {
		var apiError map[string]string
		doAPI(t, app, server.URL, http.MethodPost, "/api/sender/trash", senderTrashRequest{
			Sender: sender, PreviewID: preview.PreviewID, Confirmation: confirmation,
		}, &apiError, http.StatusBadRequest)
	}
	if trashed := fake.TrashedIDs(); len(trashed) != 0 {
		t.Fatalf("rejected confirmations changed Gmail: %v", trashed)
	}
	if count, err := app.store.CountAll(); err != nil || count != 0 {
		t.Fatalf("store after rejected confirmations: count=%d error=%v", count, err)
	}

	var trashed actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/trash", senderTrashRequest{
		Sender: sender, PreviewID: preview.PreviewID, Confirmation: senderTrashConfirmation,
	}, &trashed, http.StatusOK)
	if trashed.Count != 1 || len(fake.TrashedIDs()) != 1 || fake.TrashedIDs()[0] != "junk" {
		t.Fatalf("direct trash = %+v, Gmail IDs = %v", trashed, fake.TrashedIDs())
	}

	var restored actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/restore", actionRequest{Confirmation: restoreConfirmation}, &restored, http.StatusOK)
	if restored.Count != 1 || len(fake.TrashedIDs()) != 0 {
		t.Fatalf("restore = %+v, Gmail IDs = %v", restored, fake.TrashedIDs())
	}
	records, err := app.store.AllClassified()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Message.ID != "junk" {
		t.Fatalf("restored store records = %+v, want only junk", records)
	}
	cached, err := storage.LoadUndoCache(app.cfg.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 0 {
		t.Fatalf("undo cache after restore = %+v, want empty", cached)
	}
}

func TestDesktopSenderTrashRejectsQuerySyntax(t *testing.T) {
	app, _ := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodPost, "/api/sender/preview", senderTrashRequest{
		Sender: "user@example.com OR from:other@example.com",
	}, &apiError, http.StatusBadRequest)
	if !strings.Contains(apiError["error"], "plain email address") {
		t.Fatalf("error = %q", apiError["error"])
	}
}

func fakeAddress(local string) string {
	return local + "@example.com"
}
