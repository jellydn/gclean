// Package desktop serves gclean's cross-platform graphical interface from a
// loopback-only HTTP server. The UI is embedded in the Go binary and delegates
// all planning and Gmail mutations to the existing engine/client seams.
package desktop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gclean/internal/config"
	"gclean/internal/engine"
	"gclean/internal/gmailclient"
	"gclean/internal/models"
	"gclean/internal/storage"
)

//go:embed assets/*
var assets embed.FS

const (
	trashConfirmation   = "MOVE TO TRASH"
	restoreConfirmation = "RESTORE"
	purgeConfirmation   = "EMPTY TRASH PERMANENTLY"
)

// Config contains process-owned paths and dependencies. Client is lazy so the
// setup page can launch before a user has completed OAuth.
type Config struct {
	StorePath       string
	CachePath       string
	ConfigPath      string
	SelectionPath   string
	CredentialsPath string
	FixturePath     string
	AllowPurge      bool
	Client          func() (gmailclient.Client, error)
	LoadConfig      func() (config.Document, error)
	SaveConfig      func(config.Document) error
	DefaultConfig   func() (config.Document, error)
}

// App owns one desktop session. Mutations are serialized and selected senders
// live only for the session; the preview signature prevents applying stale UI.
type App struct {
	cfg       Config
	store     *storage.Store
	token     string
	clientMu  sync.Mutex
	client    gmailclient.Client
	selectMu  sync.RWMutex
	selected  map[string]struct{}
	limited   bool
	operation sync.Mutex
	authMu    sync.RWMutex
	auth      authStatus
	originMu  sync.RWMutex
	host      string
}

type authStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

type senderRow struct {
	Email   string   `json:"email"`
	Count   int64    `json:"count"`
	Bytes   int64    `json:"bytes"`
	Reasons []string `json:"reasons"`
	Checked bool     `json:"checked"`
}

type messageRow struct {
	Sender  string `json:"sender"`
	Subject string `json:"subject"`
	Date    string `json:"date"`
	Bytes   int64  `json:"bytes"`
	Reason  string `json:"reason"`
}

type stateResponse struct {
	Authenticated bool                `json:"authenticated"`
	Credentials   bool                `json:"credentialsPresent"`
	FixtureMode   bool                `json:"fixtureMode"`
	PurgeAllowed  bool                `json:"purgeAllowed"`
	Auth          authStatus          `json:"auth"`
	Stats         models.StatsReport  `json:"stats"`
	Preview       models.DryRunReport `json:"preview"`
	PreviewID     string              `json:"previewId"`
	Senders       []senderRow         `json:"senders"`
	Messages      []messageRow        `json:"messages"`
	UndoCount     int                 `json:"undoCount"`
}

type actionRequest struct {
	Confirmation string   `json:"confirmation"`
	PreviewID    string   `json:"previewId"`
	Senders      []string `json:"senders"`
}

type actionResponse struct {
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
	AuthURL string `json:"authUrl,omitempty"`
}

type settingsRequest struct {
	Version int               `json:"version"`
	Keep    engine.KeepConfig `json:"keep"`
	Delete  []string          `json:"delete"`
	Archive []string          `json:"archive"`
	Ignore  []string          `json:"ignore"`
}

type settingsPaths struct {
	Config      string `json:"config"`
	Database    string `json:"database"`
	UndoCache   string `json:"undoCache"`
	Selection   string `json:"selection"`
	Credentials string `json:"credentials"`
	Token       string `json:"token"`
}

type settingsOAuth struct {
	CredentialsPresent        bool `json:"credentialsPresent"`
	TokenPresent              bool `json:"tokenPresent"`
	PermanentDeleteAuthorized bool `json:"permanentDeleteAuthorized"`
	PurgeEnabled              bool `json:"purgeEnabled"`
	FixtureMode               bool `json:"fixtureMode"`
}

type settingsResponse struct {
	Version       int               `json:"version"`
	Keep          engine.KeepConfig `json:"keep"`
	Delete        []string          `json:"delete"`
	Archive       []string          `json:"archive"`
	Ignore        []string          `json:"ignore"`
	Paths         settingsPaths     `json:"paths"`
	OAuth         settingsOAuth     `json:"oauth"`
	PathOverrides map[string]bool   `json:"pathOverrides"`
}

type credentialsRequest struct {
	Credentials json.RawMessage `json:"credentials"`
}

// New opens the metadata store and creates an isolated desktop session.
func New(cfg Config) (*App, error) {
	if cfg.LoadConfig == nil {
		cfg.LoadConfig = config.Load
	}
	if cfg.SaveConfig == nil {
		cfg.SaveConfig = config.Save
	}
	if cfg.DefaultConfig == nil {
		cfg.DefaultConfig = config.DefaultDocument
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath, _ = config.DefaultPath()
	}
	if dir := filepath.Dir(cfg.StorePath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create desktop data directory: %w", err)
		}
	}
	store, err := storage.Open(cfg.StorePath)
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("create session token: %w", err)
	}
	return &App{
		cfg:      cfg,
		store:    store,
		token:    base64.RawURLEncoding.EncodeToString(tokenBytes),
		selected: map[string]struct{}{},
		auth:     authStatus{State: "idle"},
	}, nil
}

func (a *App) Close() error { return a.store.Close() }

// Handler returns the secured application handler. API requests require the
// unguessable per-process token embedded into the same-origin page.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.index)
	mux.HandleFunc("GET /assets/", a.static)
	mux.HandleFunc("GET /api/state", a.api(a.getState))
	mux.HandleFunc("GET /api/settings", a.api(a.getSettings))
	mux.HandleFunc("POST /api/settings", a.api(a.saveSettings))
	mux.HandleFunc("POST /api/settings/reset", a.api(a.resetSettings))
	mux.HandleFunc("POST /api/settings/credentials", a.api(a.saveCredentials))
	mux.HandleFunc("POST /api/logout", a.api(a.logout))
	mux.HandleFunc("POST /api/scan", a.api(a.scan))
	mux.HandleFunc("POST /api/selection", a.api(a.selection))
	mux.HandleFunc("POST /api/trash", a.api(a.trash))
	mux.HandleFunc("POST /api/restore", a.api(a.restore))
	mux.HandleFunc("POST /api/purge", a.api(a.purge))
	mux.HandleFunc("POST /api/login", a.api(a.login))
	return a.validateHost(securityHeaders(mux))
}

// Serve listens only on IPv4 loopback and shuts down when ctx is cancelled.
func (a *App) Serve(ctx context.Context) (string, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second}
	url := "http://" + listener.Addr().String() + "/"
	a.originMu.Lock()
	a.host = listener.Addr().String()
	a.originMu.Unlock()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = listener.Close()
		}
	}()
	return url, nil
}

func (a *App) validateHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.originMu.RLock()
		expected := a.host
		a.originMu.RUnlock()
		if expected != "" {
			if r.Host != expected {
				http.Error(w, "invalid desktop host", http.StatusForbidden)
				return
			}
		} else {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil || host != "127.0.0.1" {
				http.Error(w, "invalid desktop host", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) index(w http.ResponseWriter, _ *http.Request) {
	body, err := assets.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "interface unavailable", http.StatusInternalServerError)
		return
	}
	body = []byte(strings.ReplaceAll(string(body), "{{SESSION_TOKEN}}", a.token))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(body)
}

func (a *App) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "assets/app.css" && name != "assets/app.js" {
		http.NotFound(w, r)
		return
	}
	body, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

func (a *App) api(next func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gclean-Token") != a.token {
			writeError(w, http.StatusForbidden, "invalid desktop session")
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		if r.Method == http.MethodPost && r.Header.Get("Origin") != "http://"+r.Host {
			writeError(w, http.StatusForbidden, "invalid desktop origin")
			return
		}
		if err := next(w, r); err != nil {
			var apiErr *statusError
			if errors.As(err, &apiErr) {
				writeError(w, apiErr.status, apiErr.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
		}
	}
}

func (a *App) getState(w http.ResponseWriter, _ *http.Request) error {
	state, err := a.buildState()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, state)
}

func (a *App) getSettings(w http.ResponseWriter, _ *http.Request) error {
	settings, err := a.buildSettings()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, settings)
}

func (a *App) buildSettings() (settingsResponse, error) {
	document, err := a.cfg.LoadConfig()
	if err != nil {
		return settingsResponse{}, err
	}
	_, credentialsErr := os.Stat(a.cfg.CredentialsPath)
	_, tokenErr := gmailclient.LoadToken()
	return settingsResponse{
		Version: 1,
		Keep:    document.Keep,
		Delete:  append([]string(nil), document.Delete...),
		Archive: append([]string(nil), document.Archive...),
		Ignore:  append([]string(nil), document.Ignore...),
		Paths: settingsPaths{
			Config: a.cfg.ConfigPath, Database: a.cfg.StorePath,
			UndoCache: a.cfg.CachePath, Selection: a.cfg.SelectionPath,
			Credentials: a.cfg.CredentialsPath, Token: gmailclient.TokenPath(),
		},
		OAuth: settingsOAuth{
			CredentialsPresent:        credentialsErr == nil || a.cfg.FixturePath != "",
			TokenPresent:              tokenErr == nil || a.cfg.FixturePath != "",
			PermanentDeleteAuthorized: gmailclient.PurgeAuthorized(),
			PurgeEnabled:              a.cfg.AllowPurge && (a.cfg.FixturePath != "" || gmailclient.PurgeAuthorized()),
			FixtureMode:               a.cfg.FixturePath != "",
		},
		PathOverrides: map[string]bool{
			"config":      os.Getenv("GCLEAN_CONFIG_PATH") != "",
			"database":    os.Getenv("GCLEAN_DB_PATH") != "",
			"undoCache":   os.Getenv("GCLEAN_UNDO_CACHE") != "",
			"selection":   os.Getenv("GCLEAN_SELECTION_PATH") != "",
			"credentials": os.Getenv("GCLEAN_CREDENTIALS_PATH") != "",
			"token":       os.Getenv("GCLEAN_TOKEN_PATH") != "",
		},
	}, nil
}

func (a *App) saveSettings(w http.ResponseWriter, r *http.Request) error {
	var request settingsRequest
	if err := decodeJSON(r, &request); err != nil {
		return err
	}
	if request.Version != 1 {
		return &statusError{http.StatusBadRequest, "unsupported settings version; reload the app"}
	}
	document, err := validatedSettings(request)
	if err != nil {
		return &statusError{http.StatusBadRequest, err.Error()}
	}
	if err := a.cfg.SaveConfig(document); err != nil {
		return err
	}
	settings, err := a.buildSettings()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, settings)
}

func (a *App) resetSettings(w http.ResponseWriter, r *http.Request) error {
	if err := decodeEmpty(r); err != nil {
		return err
	}
	document, err := a.cfg.DefaultConfig()
	if err != nil {
		return err
	}
	if err := a.cfg.SaveConfig(document); err != nil {
		return err
	}
	settings, err := a.buildSettings()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, settings)
}

func validatedSettings(request settingsRequest) (config.Document, error) {
	if request.Keep.RecentDays < 0 || request.Keep.RecentDays > 3650 {
		return config.Document{}, errors.New("recent protection must be between 0 and 3650 days")
	}
	normalize := func(name string, values []string) ([]string, error) {
		if len(values) > 100 {
			return nil, fmt.Errorf("%s supports at most 100 entries", name)
		}
		result := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if len(value) > 500 {
				return nil, fmt.Errorf("%s entries must be 500 characters or fewer", name)
			}
			result = append(result, value)
		}
		return result, nil
	}
	deleteRules, err := normalize("delete rules", request.Delete)
	if err != nil {
		return config.Document{}, err
	}
	archiveRules, err := normalize("archive rules", request.Archive)
	if err != nil {
		return config.Document{}, err
	}
	ignored, err := normalize("ignored domains", request.Ignore)
	if err != nil {
		return config.Document{}, err
	}
	for _, domain := range ignored {
		if len(strings.Fields(domain)) != 1 || strings.ContainsAny(domain, "@/:\\") {
			return config.Document{}, fmt.Errorf("ignored domain %q must be a domain name only", domain)
		}
	}
	document := config.Document{Keep: request.Keep, Delete: deleteRules, Archive: archiveRules, Ignore: ignored}
	if _, err := document.CompileFull(); err != nil {
		return config.Document{}, err
	}
	return document, nil
}

func (a *App) saveCredentials(w http.ResponseWriter, r *http.Request) error {
	if a.cfg.FixturePath != "" {
		return &statusError{http.StatusBadRequest, "OAuth credentials are not used in fixture mode"}
	}
	var request credentialsRequest
	if err := decodeJSON(r, &request); err != nil {
		return err
	}
	if len(request.Credentials) == 0 || len(request.Credentials) > 512<<10 {
		return &statusError{http.StatusBadRequest, "select a credentials JSON file smaller than 512 KB"}
	}
	if err := gmailclient.ValidateCredentials(request.Credentials); err != nil {
		return &statusError{http.StatusBadRequest, err.Error()}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	if a.authInProgress() {
		return &statusError{http.StatusConflict, "finish the current Google authorization before replacing credentials"}
	}
	if err := gmailclient.RemoveToken(); err != nil {
		return fmt.Errorf("disconnect old OAuth session: %w", err)
	}
	if err := gmailclient.SaveCredentials(a.cfg.CredentialsPath, request.Credentials); err != nil {
		return &statusError{http.StatusBadRequest, err.Error()}
	}
	a.clearClient()
	return writeJSON(w, http.StatusOK, actionResponse{Message: "OAuth credentials saved securely. Connect with Google to authorize this Desktop app."})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	if err := decodeEmpty(r); err != nil {
		return err
	}
	if a.cfg.FixturePath != "" {
		return &statusError{http.StatusBadRequest, "disconnect is not available in fixture mode"}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	if a.authInProgress() {
		return &statusError{http.StatusConflict, "finish the current Google authorization before disconnecting"}
	}
	if err := gmailclient.RemoveToken(); err != nil {
		return err
	}
	a.clearClient()
	a.authMu.Lock()
	a.auth = authStatus{State: "idle"}
	a.authMu.Unlock()
	return writeJSON(w, http.StatusOK, actionResponse{Message: "Disconnected. The local OAuth token was removed; credentials were kept."})
}

func (a *App) clearClient() {
	a.clientMu.Lock()
	a.client = nil
	a.clientMu.Unlock()
}

func (a *App) authInProgress() bool {
	a.authMu.RLock()
	defer a.authMu.RUnlock()
	return a.auth.State == "starting" || a.auth.State == "waiting"
}

func (a *App) buildState() (stateResponse, error) {
	p, err := a.plan()
	if err != nil {
		return stateResponse{}, err
	}
	agg, err := a.store.Aggregations()
	if err != nil {
		return stateResponse{}, err
	}
	records, err := storage.LoadUndoCache(a.cfg.CachePath)
	if err != nil {
		return stateResponse{}, fmt.Errorf("read undo cache: %w", err)
	}
	a.authMu.RLock()
	auth := a.auth
	a.authMu.RUnlock()
	_, credentialsErr := os.Stat(a.cfg.CredentialsPath)
	_, tokenErr := gmailclient.LoadToken()
	allDecisions, err := a.unrestrictedDecisions()
	if err != nil {
		return stateResponse{}, err
	}
	rows, messages := a.rows(allDecisions, p.Decisions())
	return stateResponse{
		Authenticated: tokenErr == nil || a.cfg.FixturePath != "",
		Credentials:   credentialsErr == nil || a.cfg.FixturePath != "",
		FixtureMode:   a.cfg.FixturePath != "",
		PurgeAllowed:  a.cfg.AllowPurge && (a.cfg.FixturePath != "" || gmailclient.PurgeAuthorized()),
		Auth:          auth,
		Stats:         agg.Report,
		Preview:       p.Report(),
		PreviewID:     previewID(p.Decisions()),
		Senders:       rows,
		Messages:      messages,
		UndoCount:     len(records),
	}, nil
}

func (a *App) rows(allDecisions, selectedDecisions []models.Decision) ([]senderRow, []messageRow) {
	type totals struct {
		count int64
		bytes int64
		why   map[string]struct{}
	}
	all := map[string]*totals{}
	for _, d := range allDecisions {
		if d.Verdict != models.VerdictDelete {
			continue
		}
		t := all[d.Message.Sender.Email]
		if t == nil {
			t = &totals{why: map[string]struct{}{}}
			all[d.Message.Sender.Email] = t
		}
		t.count++
		t.bytes += d.Message.Size
		t.why[d.Classified.ReasonCode] = struct{}{}
	}
	messages := make([]messageRow, 0, 50)
	for _, d := range selectedDecisions {
		if d.Verdict == models.VerdictDelete && len(messages) < 50 {
			messages = append(messages, messageRow{Sender: d.Message.Sender.Email, Subject: d.Message.Subject, Date: d.Message.Date.Format("2006-01-02"), Bytes: d.Message.Size, Reason: d.Classified.ReasonCode})
		}
	}
	a.selectMu.RLock()
	limited, selected := a.limited, a.selected
	a.selectMu.RUnlock()
	rows := make([]senderRow, 0, len(all))
	for email, t := range all {
		reasons := make([]string, 0, len(t.why))
		for reason := range t.why {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		_, checked := selected[email]
		rows = append(rows, senderRow{Email: email, Count: t.count, Bytes: t.bytes, Reasons: reasons, Checked: !limited || checked})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Bytes > rows[j].Bytes })
	return rows, messages
}

func (a *App) unrestrictedDecisions() ([]models.Decision, error) {
	doc, err := a.cfg.LoadConfig()
	if err != nil {
		return nil, err
	}
	compiled, err := doc.CompileFull()
	if err != nil {
		return nil, err
	}
	messages, err := a.store.AllClassified()
	if err != nil {
		return nil, err
	}
	decisions, _ := engine.Plan(engine.PlanInputs{Messages: messages, Keep: compiled.Keep, Config: compiled.Rules})
	return decisions, nil
}

func (a *App) plan() (*engine.Pipeline, error) {
	doc, err := a.cfg.LoadConfig()
	if err != nil {
		return nil, err
	}
	compiled, err := doc.CompileFull()
	if err != nil {
		return nil, err
	}
	a.selectMu.RLock()
	selected := make(map[string]struct{}, len(a.selected))
	for sender := range a.selected {
		selected[sender] = struct{}{}
	}
	limited := a.limited
	a.selectMu.RUnlock()
	p := &engine.Pipeline{
		Store:            a.store,
		Keep:             compiled.Keep,
		Rules:            compiled.Rules,
		CachePath:        a.cfg.CachePath,
		SelectedSenders:  selected,
		SelectionLimited: limited,
	}
	if err := p.Run(p.PlanStages()...); err != nil {
		return nil, err
	}
	return p, nil
}

func (a *App) scan(w http.ResponseWriter, r *http.Request) error {
	if err := decodeEmpty(r); err != nil {
		return err
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	client, err := a.getClient()
	if err != nil {
		return err
	}
	account, err := client.AccountEmail()
	if err != nil {
		return err
	}
	doc, err := a.cfg.LoadConfig()
	if err != nil {
		return err
	}
	compiled, err := doc.CompileFull()
	if err != nil {
		return err
	}
	p := &engine.Pipeline{Store: a.store, Client: client, Keep: compiled.Keep, Rules: compiled.Rules, Account: account, CachePath: a.cfg.CachePath}
	if err := p.Run(p.ScanStages()...); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponse{Message: fmt.Sprintf("Scanned %d message metadata records. Nothing was changed in Gmail.", p.Scanned()), Count: p.Scanned()})
}

func (a *App) selection(w http.ResponseWriter, r *http.Request) error {
	var req actionRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	a.selectMu.Lock()
	a.selected = make(map[string]struct{}, len(req.Senders))
	for _, sender := range req.Senders {
		if sender = strings.TrimSpace(sender); sender != "" {
			a.selected[sender] = struct{}{}
		}
	}
	a.limited = true
	a.selectMu.Unlock()
	state, err := a.buildState()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, state)
}

func (a *App) trash(w http.ResponseWriter, r *http.Request) error {
	var req actionRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if req.Confirmation != trashConfirmation {
		return &statusError{http.StatusBadRequest, "type MOVE TO TRASH to confirm"}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	lock, err := storage.AcquireMutationLock(a.cfg.CachePath)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	p, err := a.plan()
	if err != nil {
		return err
	}
	if p.Report().DeleteCount == 0 {
		return &statusError{http.StatusBadRequest, "select at least one cleanup candidate"}
	}
	if req.PreviewID == "" || req.PreviewID != previewID(p.Decisions()) {
		return &statusError{http.StatusConflict, "preview changed; review the refreshed selection before continuing"}
	}
	client, err := a.getClient()
	if err != nil {
		return err
	}
	account, err := client.AccountEmail()
	if err != nil {
		return err
	}
	p.Client = client
	p.Account = account
	p.MutationLockHeld = true
	if err := p.Run(p.ApplyStages()...); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponse{Message: fmt.Sprintf("Moved %d messages to Gmail Trash. They remain recoverable for up to 30 days.", len(p.TrashedIDs())), Count: len(p.TrashedIDs())})
}

func (a *App) restore(w http.ResponseWriter, r *http.Request) error {
	var req actionRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if req.Confirmation != restoreConfirmation {
		return &statusError{http.StatusBadRequest, "restore confirmation required"}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	records, err := storage.LoadUndoCache(a.cfg.CachePath)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return &statusError{http.StatusBadRequest, "there is no gclean batch to restore"}
	}
	client, err := a.getClient()
	if err != nil {
		return err
	}
	account, err := client.AccountEmail()
	if err != nil {
		return err
	}
	restored, err := (&engine.Reconciler{Store: a.store, CachePath: a.cfg.CachePath, Account: account}).Undo(client, records)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponse{Message: fmt.Sprintf("Restored %d messages from Trash.", restored), Count: restored})
}

func (a *App) purge(w http.ResponseWriter, r *http.Request) error {
	var req actionRequest
	if err := decodeJSON(r, &req); err != nil {
		return err
	}
	if !a.cfg.AllowPurge || (a.cfg.FixturePath == "" && !gmailclient.PurgeAuthorized()) {
		return &statusError{http.StatusForbidden, "permanent deletion is disabled; run login --allow-permanent-delete, then restart with --allow-purge"}
	}
	if req.Confirmation != purgeConfirmation {
		return &statusError{http.StatusBadRequest, "type EMPTY TRASH PERMANENTLY to confirm"}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	records, err := storage.LoadUndoCache(a.cfg.CachePath)
	if err != nil {
		return err
	}
	client, err := a.getClient()
	if err != nil {
		return err
	}
	account, err := client.AccountEmail()
	if err != nil {
		return err
	}
	if err := (&engine.Reconciler{CachePath: a.cfg.CachePath, Account: account}).Purge(client, records); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, actionResponse{Message: "Gmail Trash was emptied permanently. This cannot be undone."})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	if err := decodeEmpty(r); err != nil {
		return err
	}
	if a.cfg.FixturePath != "" {
		return &statusError{http.StatusBadRequest, "OAuth is not used in fixture mode"}
	}
	if !a.operation.TryLock() {
		return &statusError{http.StatusConflict, "another operation is already running"}
	}
	defer a.operation.Unlock()
	if _, err := os.Stat(a.cfg.CredentialsPath); err != nil {
		return &statusError{http.StatusBadRequest, "credentials.json is missing; follow the setup steps shown in the app"}
	}
	a.authMu.Lock()
	if a.auth.State == "starting" || a.auth.State == "waiting" {
		a.authMu.Unlock()
		return &statusError{http.StatusConflict, "OAuth login is already waiting for completion"}
	}
	a.auth = authStatus{State: "starting"}
	a.authMu.Unlock()
	oauthState, err := gmailclient.NewOAuthState()
	if err != nil {
		a.setAuthError(err)
		return err
	}
	callback, err := gmailclient.NewAuthCodeServer(oauthState)
	if err != nil {
		a.setAuthError(err)
		return err
	}
	// Desktop Connect is deliberately never an escalation path. Full access is
	// granted only by the separately named CLI login flag.
	cfg, err := gmailclient.LoadConfigWithRedirectAndPurge(a.cfg.CredentialsPath, callback.RedirectURL(), false)
	if err != nil {
		_ = callback.Close()
		a.setAuthError(err)
		return err
	}
	a.authMu.Lock()
	a.auth = authStatus{State: "waiting"}
	a.authMu.Unlock()
	authURL := gmailclient.AuthorizationURL(cfg, oauthState)
	go func() {
		defer func() { _ = callback.Close() }()
		code, waitErr := callback.WaitForCode(5 * time.Minute)
		if waitErr == nil {
			token, exchangeErr := cfg.Exchange(context.Background(), code)
			if exchangeErr != nil {
				waitErr = fmt.Errorf("exchange authorization: %w", exchangeErr)
			} else if saveErr := gmailclient.SaveTokenWithAuthorization(token, false); saveErr != nil {
				waitErr = fmt.Errorf("save token: %w", saveErr)
			}
		}
		a.authMu.Lock()
		if waitErr != nil {
			a.auth = authStatus{State: "error", Error: waitErr.Error()}
		} else {
			a.auth = authStatus{State: "complete"}
			a.clearClient()
		}
		a.authMu.Unlock()
	}()
	return writeJSON(w, http.StatusAccepted, actionResponse{Message: "Complete authorization in the Google window, then return here.", AuthURL: authURL})
}

func (a *App) setAuthError(err error) {
	a.authMu.Lock()
	a.auth = authStatus{State: "error", Error: err.Error()}
	a.authMu.Unlock()
}

func (a *App) getClient() (gmailclient.Client, error) {
	a.clientMu.Lock()
	defer a.clientMu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	if a.cfg.Client == nil {
		return nil, errors.New("Gmail client is not configured")
	}
	client, err := a.cfg.Client()
	if err != nil {
		return nil, err
	}
	a.client = client
	return client, nil
}

func previewID(decisions []models.Decision) string {
	h := sha256.New()
	for _, d := range decisions {
		if d.Verdict == models.VerdictDelete {
			_, _ = io.WriteString(h, d.Message.ID)
			_, _ = io.WriteString(h, "\x00")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, (1<<20)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &statusError{http.StatusBadRequest, "invalid request: " + err.Error()}
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return &statusError{http.StatusBadRequest, "request must contain one JSON value"}
	}
	return nil
}

func decodeEmpty(r *http.Request) error {
	var body map[string]any
	return decodeJSON(r, &body)
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	_ = writeJSON(w, status, map[string]string{"error": message})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
