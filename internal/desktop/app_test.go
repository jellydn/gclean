package desktop

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gclean/internal/config"
	"gclean/internal/defang"
	"gclean/internal/engine"
	"gclean/internal/gmailclient"
	"gclean/internal/models"
)

func TestDesktopWorkflowRequiresPreviewAndSupportsRestore(t *testing.T) {
	app, fake := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	unauthorized, err := http.Get(server.URL + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d, want 403", unauthorized.StatusCode)
	}

	var scan actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/scan", map[string]any{}, &scan, http.StatusOK)
	if scan.Count != 2 {
		t.Fatalf("scan count = %d, want 2", scan.Count)
	}

	var state stateResponse
	doAPI(t, app, server.URL, http.MethodGet, "/api/state", nil, &state, http.StatusOK)
	if state.Preview.DeleteCount != 1 || len(state.Senders) != 1 {
		t.Fatalf("initial preview = %+v, senders = %+v", state.Preview, state.Senders)
	}

	// An explicit empty checkbox selection must mean delete nothing, not the
	// CLI's historical "no filter means all" behavior.
	doAPI(t, app, server.URL, http.MethodPost, "/api/selection", actionRequest{Senders: []string{}}, &state, http.StatusOK)
	if state.Preview.DeleteCount != 0 || len(state.Senders) != 1 || state.Senders[0].Checked {
		t.Fatalf("empty selection should preserve unchecked candidate: %+v", state)
	}

	doAPI(t, app, server.URL, http.MethodPost, "/api/selection", actionRequest{Senders: []string{state.Senders[0].Email}}, &state, http.StatusOK)
	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodPost, "/api/trash", actionRequest{Confirmation: trashConfirmation, PreviewID: "stale"}, &apiError, http.StatusConflict)

	var trashed actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/trash", actionRequest{Confirmation: trashConfirmation, PreviewID: state.PreviewID}, &trashed, http.StatusOK)
	if trashed.Count != 1 || len(fake.TrashedIDs()) != 1 {
		t.Fatalf("trash result = %+v, fake trash = %v", trashed, fake.TrashedIDs())
	}

	doAPI(t, app, server.URL, http.MethodGet, "/api/state", nil, &state, http.StatusOK)
	if state.UndoCount != 1 {
		t.Fatalf("undo count = %d, want 1", state.UndoCount)
	}
	var restored actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/restore", actionRequest{Confirmation: restoreConfirmation}, &restored, http.StatusOK)
	if restored.Count != 1 || len(fake.TrashedIDs()) != 0 {
		t.Fatalf("restore result = %+v, fake trash = %v", restored, fake.TrashedIDs())
	}
}

func TestDesktopPurgeIsDisabledByDefault(t *testing.T) {
	app, _ := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()
	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodPost, "/api/purge", actionRequest{Confirmation: purgeConfirmation}, &apiError, http.StatusForbidden)
	if apiError["error"] == "" {
		t.Fatal("purge denial should explain the safeguard")
	}
}

func newTestApp(t *testing.T, allowPurge bool) (*App, *gmailclient.FakeClient) {
	t.Helper()
	old := time.Now().AddDate(-3, 0, 0)
	junkSender := defang.MkEmail("offers", "example.com")
	personSender := defang.MkEmail("friend", "example.com")
	fake := gmailclient.NewFakeClientFromMessages([]*models.Message{
		{ID: "junk", Sender: models.Sender{Email: junkSender}, Subject: "Old offer", Date: old, Size: 4 << 20, Headers: map[string]string{"List-Unsubscribe": "yes"}},
		{ID: "person", Sender: models.Sender{Email: personSender}, Subject: "Important", Date: old, Size: 1 << 20, Labels: []string{"STARRED"}, Headers: map[string]string{}},
	})
	doc := config.Document{
		Keep:   engine.KeepConfig{Starred: true},
		Delete: []string{"has:unsubscribe"},
	}
	tmp := t.TempDir()
	app, err := New(Config{
		StorePath:       filepath.Join(tmp, "desktop.db"),
		CachePath:       filepath.Join(tmp, "undo.json"),
		CredentialsPath: filepath.Join(tmp, "credentials.json"),
		FixturePath:     "in-memory",
		AllowPurge:      allowPurge,
		Client:          func() (gmailclient.Client, error) { return fake, nil },
		LoadConfig:      func() (config.Document, error) { return doc, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, fake
}

func doAPI(t *testing.T, app *App, baseURL, method, path string, body, target any, wantStatus int) {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, baseURL+path, &payload)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Gclean-Token", app.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != wantStatus {
		var response any
		_ = json.NewDecoder(res.Body).Decode(&response)
		t.Fatalf("%s %s status = %d, want %d: %v", method, path, res.StatusCode, wantStatus, response)
	}
	if err := json.NewDecoder(res.Body).Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}
