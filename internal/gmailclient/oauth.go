package gmailclient

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"gclean/internal/fileutil"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// gmail.modify includes metadata reads plus Trash and restore. It is the
// least-privilege default. Permanent deletion requires mail.google.com and is
// requested only when a user explicitly opts into purge authorization.
var oauthScopes = []string{gmail.GmailModifyScope}

const oauthListenHost = "localhost"

const authorizationProfileVersion = 1

type authorizationProfile struct {
	Version           int  `json:"version"`
	PermanentDeleteOK bool `json:"permanent_delete_ok"`
}

// tokenPath returns the path to the persisted OAuth token.
// Honors GCLEAN_TOKEN_PATH; falls back to ~/.config/gclean/token.json.
func tokenPath() string {
	if p := os.Getenv("GCLEAN_TOKEN_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gclean-token.json")
	}
	return filepath.Join(home, ".config", "gclean", "token.json")
}

// LoadConfig reads credentials.json and returns an oauth2.Config for the
// Gmail API. Callers that own a callback server should use
// LoadConfigWithRedirect so the exact allocated loopback URI is registered.
func LoadConfig(credentialsPath string) (*oauth2.Config, error) {
	return LoadConfigWithRedirect(credentialsPath, "http://"+oauthListenHost)
}

// LoadConfigWithRedirect reads credentials.json and sets the redirect URI
// used by the OAuth authorization and token-exchange requests.
func LoadConfigWithRedirect(credentialsPath, redirectURL string) (*oauth2.Config, error) {
	return LoadConfigWithRedirectAndPurge(credentialsPath, redirectURL, false)
}

// LoadConfigWithRedirectAndPurge optionally requests Gmail's full-access
// scope. Callers must expose this as an explicit destructive-capability opt-in.
func LoadConfigWithRedirectAndPurge(credentialsPath, redirectURL string, allowPurge bool) (*oauth2.Config, error) {
	b, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	scopes := oauthScopes
	if allowPurge {
		scopes = []string{gmail.GmailModifyScope, gmail.MailGoogleComScope}
	}
	cfg, err := google.ConfigFromJSON(b, scopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	cfg.RedirectURL = redirectURL
	return cfg, nil
}

// ValidateCredentials verifies a Google Desktop OAuth client file without
// persisting or exposing its contents.
func ValidateCredentials(data []byte) error {
	var envelope struct {
		Installed json.RawMessage `json:"installed"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Installed) == 0 {
		return errors.New("select a valid Google OAuth Desktop app credentials JSON file")
	}
	if _, err := google.ConfigFromJSON(data, gmail.GmailModifyScope); err != nil {
		return errors.New("the selected file is not a valid Google OAuth client configuration")
	}
	return nil
}

// SaveCredentials validates a Google Desktop OAuth client file and stores it
// atomically. Callers must never log or return the supplied JSON.
func SaveCredentials(path string, data []byte) error {
	if err := ValidateCredentials(data); err != nil {
		return err
	}
	if err := fileutil.WriteAtomic(path, data, 0o600, ".gclean-credentials-*"); err != nil {
		return fmt.Errorf("store OAuth credentials: %w", err)
	}
	return nil
}

// SaveToken persists an oauth2.Token to token.json with mode 0600.
func SaveToken(tok *oauth2.Token) error {
	p := tokenPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir token dir: %w", err)
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return fmt.Errorf("marshal token: %w", err)
	}
	if err := fileutil.WriteAtomic(p, b, 0o600, ".gclean-token-*"); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}

// SaveTokenWithAuthorization preserves an existing refresh token if Google's
// exchange omits it and records whether this login explicitly granted the
// permanent-delete scope. Missing legacy profiles are treated as no grant.
func SaveTokenWithAuthorization(tok *oauth2.Token, permanentDelete bool) error {
	if tok.RefreshToken == "" {
		if existing, err := LoadToken(); err == nil {
			tok.RefreshToken = existing.RefreshToken
		}
	}
	// Remove any previous grant marker first so an interrupted or failed login
	// cannot leave a stale permanent-delete capability attached to a new token.
	if err := os.Remove(authorizationProfilePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := SaveToken(tok); err != nil {
		return err
	}
	profile := authorizationProfile{Version: authorizationProfileVersion, PermanentDeleteOK: permanentDelete}
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	if err := fileutil.WriteAtomic(authorizationProfilePath(), data, 0o600, ".gclean-authorization-*"); err != nil {
		return fmt.Errorf("write authorization profile: %w", err)
	}
	return nil
}

// PurgeAuthorized reports whether the current token was created by an
// explicit permanent-delete login. Legacy tokens without a profile fail safe.
func PurgeAuthorized() bool {
	data, err := os.ReadFile(authorizationProfilePath())
	if err != nil {
		return false
	}
	var profile authorizationProfile
	return json.Unmarshal(data, &profile) == nil &&
		profile.Version == authorizationProfileVersion && profile.PermanentDeleteOK
}

// RemoveToken removes both OAuth credentials and the local scope profile.
func RemoveToken() error {
	for _, path := range []string{tokenPath(), authorizationProfilePath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func authorizationProfilePath() string { return tokenPath() + ".authorization.json" }

// TokenPath returns the configured token path for user-facing status output.
func TokenPath() string { return tokenPath() }

// LoadToken reads the persisted oauth2.Token from token.json.
func LoadToken() (*oauth2.Token, error) {
	p := tokenPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read token: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(b, &tok); err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	return &tok, nil
}

// TokenSource returns an oauth2.TokenSource that auto-refreshes using the
// provided config and token.
func TokenSource(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) oauth2.TokenSource {
	return cfg.TokenSource(ctx, tok)
}

// AuthorizationURL requests offline access and explicit consent so Google
// returns a refresh token for a persistent desktop session.
func AuthorizationURL(cfg *oauth2.Config, state string) string {
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// AuthCodeServer is a minimal HTTP server that captures the OAuth authorization
// code from the localhost callback. It shuts down automatically after receiving
// the code or on timeout.
type AuthCodeServer struct {
	server   *http.Server
	listener net.Listener
	redirect string
	state    string
	code     chan string
	errCh    chan error
}

// NewAuthCodeServer starts listening on an available localhost port and
// returns a server ready to receive the callback. The redirect URI always uses
// the registered localhost hostname, even when the listener resolves it to an
// IPv4 or IPv6 loopback address internally.
func NewAuthCodeServer(expectedState string) (*AuthCodeServer, error) {
	if expectedState == "" {
		return nil, errors.New("OAuth state must not be empty")
	}
	listener, err := net.Listen("tcp", oauthListenHost+":0")
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback: %w", err)
	}
	s := &AuthCodeServer{
		listener: listener,
		redirect: "http://" + oauthListenHost + ":" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
		state:    expectedState,
		code:     make(chan string, 1),
		errCh:    make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != s.state {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			select {
			case s.errCh <- fmt.Errorf("no code in callback"):
			default:
			}
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("Authentication successful. You can close this window."))
		select {
		case s.code <- code:
		default:
		}
	})
	s.server = &http.Server{Handler: mux}
	go func() {
		if err := s.server.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			select {
			case s.errCh <- err:
			default:
			}
		}
	}()
	return s, nil
}

// NewOAuthState returns an unguessable state value used to bind an OAuth
// callback to the process that initiated it.
func NewOAuthState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// RedirectURL returns the exact redirect URI registered for this callback
// server.
func (s *AuthCodeServer) RedirectURL() string { return s.redirect }

// WaitForCode blocks until the authorization code arrives, the server errors,
// or the timeout expires.
func (s *AuthCodeServer) WaitForCode(timeout time.Duration) (string, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case code := <-s.code:
		return code, nil
	case err := <-s.errCh:
		return "", err
	case <-timer.C:
		return "", fmt.Errorf("auth timeout after %v", timeout)
	}
}

// Close shuts down the callback server.
func (s *AuthCodeServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.server.Shutdown(ctx)
	if closeErr := s.listener.Close(); err == nil {
		err = closeErr
	}
	return err
}

// OpenBrowser opens the given URL in the user's default browser.
func OpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		if os.Getenv("DISPLAY") != "" {
			cmd = "xdg-open"
			args = []string{url}
		} else {
			return fmt.Errorf("no known browser opener for %s without DISPLAY", runtime.GOOS)
		}
	}
	if cmd == "" {
		return fmt.Errorf("no browser opener for %s", runtime.GOOS)
	}
	if len(args) == 0 {
		args = []string{url}
	}
	c := exec.Command(cmd, args...)
	return c.Start()
}
