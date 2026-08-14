package gmailclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"gclean/internal/defang"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func TestNewRealClient_MissingPath(t *testing.T) {
	_, err := NewRealClient("")
	if err != ErrCredentialsMissing {
		t.Fatalf("want ErrCredentialsMissing, got %v", err)
	}
}

func TestNewRealClient_MissingCredentials(t *testing.T) {
	tmp := t.TempDir()
	_, err := NewRealClient(tmp + "/nonexistent.json")
	if err == nil {
		t.Fatal("want error for missing credentials, got nil")
	}
}

func TestMapGmailMessage_CombinesToAndCcRecipients(t *testing.T) {
	message := mapGmailMessage(&gmail.Message{
		Id:           "m1",
		InternalDate: 1_700_000_000_000,
		Payload: &gmail.MessagePart{Headers: []*gmail.MessagePartHeader{
			{Name: "From", Value: defang.MkEmail("sender", "example.com")},
			{Name: "To", Value: "first@example.com, second@example.com"},
			{Name: "Cc", Value: " third@example.com "},
		}},
	})

	want := []string{"first@example.com", "second@example.com", "third@example.com"}
	if len(message.To) != len(want) {
		t.Fatalf("got %d recipients, want %d: %v", len(message.To), len(want), message.To)
	}
	for i, recipient := range want {
		if message.To[i] != recipient {
			t.Errorf("recipient %d = %q, want %q", i, message.To[i], recipient)
		}
	}
}

func TestRealClient_TrashMessages_RetriesTransientErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/gmail/v1/users/me/messages/m1/trash" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "try again", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client := newHTTPTestClient(t, server)
	if err := client.TrashMessages([]string{"m1"}); err != nil {
		t.Fatalf("TrashMessages: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("request attempts = %d, want 2", got)
	}
}

func TestRealClient_RestoreFromTrash_UsesUntrash(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	if err := newHTTPTestClient(t, server).RestoreFromTrash([]string{"m1"}); err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	if gotPath != "/gmail/v1/users/me/messages/m1/untrash" {
		t.Fatalf("path = %q, want untrash endpoint", gotPath)
	}
}

func TestRealClient_EmptyTrash_BatchesAllPages(t *testing.T) {
	const total = mutationBatchSize + 1
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/gmail/v1/users/me/messages":
			if got := r.URL.Query().Get("labelIds"); got != "TRASH" {
				t.Errorf("labelIds = %q, want TRASH", got)
			}
			start, end := 0, mutationBatchSize
			if r.URL.Query().Get("pageToken") == "next" {
				start, end = mutationBatchSize, total
			}
			messages := make([]map[string]string, end-start)
			for i := range messages {
				messages[i] = map[string]string{"id": "trash-" + strconv.Itoa(start+i)}
			}
			response := map[string]any{"messages": messages}
			if start == 0 {
				response["nextPageToken"] = "next"
			}
			_ = json.NewEncoder(w).Encode(response)
		case r.Method == http.MethodPost && r.URL.Path == "/gmail/v1/users/me/messages/batchDelete":
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode batch body: %v", err)
				return
			}
			batchSizes = append(batchSizes, len(body.IDs))
			_, _ = fmt.Fprint(w, `{}`)
		default:
			http.Error(w, "unexpected request: "+path.Base(r.URL.Path), http.StatusNotFound)
		}
	}))
	defer server.Close()

	if err := newHTTPTestClient(t, server).EmptyTrash(); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if got, want := strings.TrimSpace(fmt.Sprint(batchSizes)), "[1000 1]"; got != want {
		t.Fatalf("batch sizes = %s, want %s", got, want)
	}
}

func newHTTPTestClient(t *testing.T, server *httptest.Server) *RealClient {
	t.Helper()
	transport := rewriteHostTransport{base: http.DefaultTransport, server: server}
	httpClient := &http.Client{Transport: transport}
	service, err := gmail.NewService(context.Background(),
		option.WithHTTPClient(httpClient),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new test Gmail service: %v", err)
	}
	return &RealClient{service: service}
}

type rewriteHostTransport struct {
	base   http.RoundTripper
	server *httptest.Server
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copyReq := req.Clone(req.Context())
	requestURL := *req.URL
	parsed, err := url.Parse(t.server.URL)
	if err != nil {
		return nil, err
	}
	requestURL.Scheme = parsed.Scheme
	requestURL.Host = parsed.Host
	copyReq.URL = &requestURL
	copyReq.Host = requestURL.Host
	return t.base.RoundTrip(copyReq)
}
