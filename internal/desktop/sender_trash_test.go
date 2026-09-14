package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
