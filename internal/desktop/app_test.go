package desktop

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gclean/internal/config"
	"gclean/internal/defang"
	"gclean/internal/engine"
	"gclean/internal/gmailclient"
	"gclean/internal/models"
	"gclean/internal/storage"
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

func TestDesktopRefusesCrossAccountRestoreWithoutLosingUndo(t *testing.T) {
	app, fake := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	var scan actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/scan", map[string]any{}, &scan, http.StatusOK)
	var state stateResponse
	doAPI(t, app, server.URL, http.MethodGet, "/api/state", nil, &state, http.StatusOK)
	var trashed actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/trash", actionRequest{Confirmation: trashConfirmation, PreviewID: state.PreviewID}, &trashed, http.StatusOK)

	fake.SetAccountEmail("different-account")
	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodGet, "/api/state", nil, &apiError, http.StatusInternalServerError)
	if !strings.Contains(apiError["error"], "belongs to Gmail account") {
		t.Fatalf("cross-account state error = %q", apiError["error"])
	}
	apiError = nil
	doAPI(t, app, server.URL, http.MethodPost, "/api/restore", actionRequest{Confirmation: restoreConfirmation}, &apiError, http.StatusInternalServerError)
	if !strings.Contains(apiError["error"], "undo cache belongs") {
		t.Fatalf("cross-account error = %q", apiError["error"])
	}
	batch, err := storage.LoadUndoBatch(app.cfg.CachePath)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Account != "fixture" || len(batch.Records) != trashed.Count || len(fake.TrashedIDs()) != trashed.Count {
		t.Fatalf("undo was changed after mismatch: batch=%+v trash=%v", batch, fake.TrashedIDs())
	}
}

func TestDesktopRejectsReboundHostAndCrossOriginMutation(t *testing.T) {
	app, _ := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	rebound, err := http.NewRequest(http.MethodGet, server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	rebound.Host = "attacker.invalid"
	response, err := http.DefaultClient.Do(rebound)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("rebound Host status = %d, want 403", response.StatusCode)
	}

	payload := bytes.NewBufferString("{}")
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/scan", payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Gclean-Token", app.token)
	request.Header.Set("Origin", "https://attacker.invalid")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status = %d, want 403", response.StatusCode)
	}
}

func TestSettingsAPIValidatesPersistsAndResetsCLIConfig(t *testing.T) {
	app, _ := newTestApp(t, false)
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	var settings settingsResponse
	doAPI(t, app, server.URL, http.MethodGet, "/api/settings", nil, &settings, http.StatusOK)
	if settings.Version != 1 || settings.Keep.RecentDays != 0 {
		t.Fatalf("initial settings = %+v", settings)
	}

	invalid := settingsRequest{Version: 1, Keep: settings.Keep, Delete: []string{"unknown:value"}}
	var apiError map[string]string
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings", invalid, &apiError, http.StatusBadRequest)
	if !strings.Contains(apiError["error"], "unknown key") {
		t.Fatalf("invalid rule error = %q", apiError["error"])
	}

	invalid = settingsRequest{Version: 1, Keep: engine.KeepConfig{RecentDays: 3651}}
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings", invalid, &apiError, http.StatusBadRequest)

	want := settingsRequest{
		Version: 1,
		Keep:    engine.KeepConfig{Contacts: true, Starred: true, RecentDays: 120},
		Delete:  []string{"has:unsubscribe older_than:365d"},
		Archive: []string{"subject:receipt"},
		Ignore:  []string{"example.org"},
	}
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings", want, &settings, http.StatusOK)
	if settings.Keep.RecentDays != 120 || len(settings.Delete) != 1 || settings.Ignore[0] != "example.org" {
		t.Fatalf("saved settings = %+v", settings)
	}
	doAPI(t, app, server.URL, http.MethodGet, "/api/settings", nil, &settings, http.StatusOK)
	if settings.Keep.RecentDays != 120 {
		t.Fatalf("settings did not persist through loader: %+v", settings)
	}

	doAPI(t, app, server.URL, http.MethodPost, "/api/settings/reset", map[string]any{}, &settings, http.StatusOK)
	if settings.Keep != engine.DefaultKeep() || len(settings.Delete) != 3 {
		t.Fatalf("reset settings = %+v", settings)
	}
}

func TestValidatedSettingsRejectsMalformedDomains(t *testing.T) {
	for _, domain := range []string{".", "foo..example", "-invalid.example", "invalid-.example", "not_a_domain.example"} {
		t.Run(domain, func(t *testing.T) {
			_, err := validatedSettings(settingsRequest{Keep: engine.DefaultKeep(), Ignore: []string{domain}})
			if err == nil || !strings.Contains(err.Error(), "domain name only") {
				t.Fatalf("validatedSettings(%q) error = %v, want domain validation error", domain, err)
			}
		})
	}
}

func TestSettingsCredentialsImportValidatesAndNeverReturnsSecrets(t *testing.T) {
	tmp := t.TempDir()
	tokenPath := filepath.Join(tmp, "token.json")
	t.Setenv("GCLEAN_TOKEN_PATH", tokenPath)
	document := config.Document{Keep: engine.DefaultKeep()}
	credentialsPath := filepath.Join(tmp, "credentials.json")
	app, err := New(Config{
		StorePath: filepath.Join(tmp, "desktop.db"), CachePath: filepath.Join(tmp, "undo.json"),
		ConfigPath: filepath.Join(tmp, "config.yaml"), CredentialsPath: credentialsPath,
		LoadConfig: func() (config.Document, error) { return document, nil },
		SaveConfig: func(updated config.Document) error { document = updated; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close() }()
	server := httptest.NewServer(app.Handler())
	defer server.Close()

	var apiError map[string]string
	if err := os.WriteFile(tokenPath, []byte(`{"access_token":"existing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings/credentials", credentialsRequest{Credentials: json.RawMessage(`{"web":{}}`)}, &apiError, http.StatusBadRequest)
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("invalid credentials removed existing token: %v", err)
	}

	const secret = "private-client-value"
	credentials := json.RawMessage(`{"installed":{"client_id":"id","client_secret":"` + secret + `","auth_uri":"https://example.invalid/auth","token_uri":"https://example.invalid/token","redirect_uris":["http://localhost"]}}`)
	app.authMu.Lock()
	app.auth = authStatus{State: "waiting"}
	app.authMu.Unlock()
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings/credentials", credentialsRequest{Credentials: credentials}, &apiError, http.StatusConflict)
	if _, err := os.Stat(tokenPath); err != nil {
		t.Fatalf("credentials changed during OAuth and removed token: %v", err)
	}
	app.authMu.Lock()
	app.auth = authStatus{State: "idle"}
	app.authMu.Unlock()
	var result actionResponse
	doAPI(t, app, server.URL, http.MethodPost, "/api/settings/credentials", credentialsRequest{Credentials: credentials}, &result, http.StatusOK)
	if strings.Contains(result.Message, secret) {
		t.Fatal("credentials secret was returned by the API")
	}
	info, err := os.Stat(credentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %o, want 600", info.Mode().Perm())
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
	document := config.Document{
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
		LoadConfig:      func() (config.Document, error) { return document, nil },
		SaveConfig:      func(updated config.Document) error { document = updated; return nil },
		DefaultConfig:   config.DefaultDocument,
		ConfigPath:      filepath.Join(tmp, "config.yaml"),
		SelectionPath:   filepath.Join(tmp, "selection.json"),
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
		req.Header.Set("Origin", baseURL)
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
