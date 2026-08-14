package gmailclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

var oauthScopes = []string{gmail.GmailReadonlyScope, gmail.GmailModifyScope}

const oauthListenHost = "localhost"

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
	b, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, oauthScopes...)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	cfg.RedirectURL = redirectURL
	return cfg, nil
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
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}

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

// AuthCodeServer is a minimal HTTP server that captures the OAuth authorization
// code from the localhost callback. It shuts down automatically after receiving
// the code or on timeout.
type AuthCodeServer struct {
	server   *http.Server
	listener net.Listener
	redirect string
	code     chan string
	errCh    chan error
}

// NewAuthCodeServer starts listening on an available localhost port and
// returns a server ready to receive the callback. The redirect URI always uses
// the registered localhost hostname, even when the listener resolves it to an
// IPv4 or IPv6 loopback address internally.
func NewAuthCodeServer() (*AuthCodeServer, error) {
	listener, err := net.Listen("tcp", oauthListenHost+":0")
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth callback: %w", err)
	}
	s := &AuthCodeServer{
		listener: listener,
		redirect: "http://" + oauthListenHost + ":" + strconv.Itoa(listener.Addr().(*net.TCPAddr).Port),
		code:     make(chan string, 1),
		errCh:    make(chan error, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
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
