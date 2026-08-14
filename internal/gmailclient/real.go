package gmailclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"gclean/internal/models"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// ErrCredentialsMissing is returned by NewRealClient when credentials.json
// is not present at the configured path.
var ErrCredentialsMissing = errors.New("gmail credentials.json not found; drop it into ~/.config/gclean/credentials.json or set GCLEAN_CREDENTIALS_PATH")

// RealClient talks to the real Gmail API.
type RealClient struct {
	credentialsPath string
	service         *gmail.Service
}

// NewRealClient validates that credentials.json exists, loads the persisted
// token, and builds an authenticated Gmail service. It returns
// ErrCredentialsMissing if the path is empty, and propagates I/O or auth
// errors otherwise.
func NewRealClient(credentialsPath string) (*RealClient, error) {
	if credentialsPath == "" {
		return nil, ErrCredentialsMissing
	}
	cfg, err := LoadConfig(credentialsPath)
	if err != nil {
		return nil, err
	}
	tok, err := LoadToken()
	if err != nil {
		return nil, fmt.Errorf("load token: %w (run `gclean login`)", err)
	}
	ctx := context.Background()
	ts := TokenSource(ctx, cfg, tok)
	svc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	return &RealClient{
		credentialsPath: credentialsPath,
		service:         svc,
	}, nil
}

const (
	mutationBatchSize   = 1000
	maxMutationAttempts = 3
	mutationRetryDelay  = 100 * time.Millisecond
)

func (r *RealClient) ListMessages(query string, max int) ([]*models.Message, error) {
	var out []*models.Message
	pageToken := ""
	for {
		listCall := r.service.Users.Messages.List("me").MaxResults(500).PageToken(pageToken)
		if query != "" {
			listCall.Q(query)
		}
		resp, err := listCall.Do()
		if err != nil {
			return nil, fmt.Errorf("list messages: %w", err)
		}
		slog.Info("listed page", "listed", len(resp.Messages), "fetched_so_far", len(out))
		for _, m := range resp.Messages {
			if max > 0 && len(out) >= max {
				return out, nil
			}
			full, err := r.service.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders(
				"From", "To", "Cc", "Subject", "Date",
				"List-Unsubscribe", "List-ID", "Precedence", "Auto-Submitted",
			).Do()
			if err != nil {
				return nil, fmt.Errorf("get message %s: %w", m.Id, err)
			}
			msg := mapGmailMessage(full)
			out = append(out, msg)
			if len(out)%100 == 0 {
				slog.Info("fetched metadata", "fetched_so_far", len(out))
			}
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	slog.Info("list complete", "total", len(out))
	return out, nil
}

func (r *RealClient) TrashMessages(ids []string) error {
	for i, id := range ids {
		if err := r.retryMutation("trash message "+id, func() error {
			_, err := r.service.Users.Messages.Trash("me", id).Do()
			return err
		}); err != nil {
			return fmt.Errorf("trash message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
	}
	return nil
}

func (r *RealClient) EmptyTrash() error {
	var (
		ids       []string
		pageToken string
	)
	for {
		call := r.service.Users.Messages.List("me").LabelIds("TRASH").MaxResults(mutationBatchSize)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		var resp *gmail.ListMessagesResponse
		if err := r.retryMutation("list trash", func() error {
			var err error
			resp, err = call.Do()
			return err
		}); err != nil {
			return err
		}
		for _, message := range resp.Messages {
			ids = append(ids, message.Id)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	for start := 0; start < len(ids); start += mutationBatchSize {
		end := min(start+mutationBatchSize, len(ids))
		batch := &gmail.BatchDeleteMessagesRequest{Ids: ids[start:end]}
		if err := r.retryMutation(fmt.Sprintf("empty trash batch %d-%d", start+1, end), func() error {
			return r.service.Users.Messages.BatchDelete("me", batch).Do()
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *RealClient) RestoreFromTrash(ids []string) error {
	for i, id := range ids {
		if err := r.retryMutation("restore message "+id, func() error {
			_, err := r.service.Users.Messages.Untrash("me", id).Do()
			return err
		}); err != nil {
			return fmt.Errorf("restore message %d/%d (%s): %w", i+1, len(ids), id, err)
		}
	}
	return nil
}

func (r *RealClient) retryMutation(operation string, fn func() error) error {
	for attempt := 1; attempt <= maxMutationAttempts; attempt++ {
		if err := fn(); err == nil {
			return nil
		} else if attempt == maxMutationAttempts || !isRetryableGmailError(err) {
			return fmt.Errorf("%s failed after %d attempt(s): %w", operation, attempt, err)
		}
		time.Sleep(mutationRetryDelay * time.Duration(1<<(attempt-1)))
	}
	return fmt.Errorf("%s failed", operation)
}

func isRetryableGmailError(err error) bool {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == 429 || apiErr.Code >= 500
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

func appendRecipients(to []string, header string) []string {
	if header == "" {
		return to
	}
	for _, recipient := range strings.Split(header, ",") {
		recipient = strings.TrimSpace(recipient)
		if recipient != "" {
			to = append(to, recipient)
		}
	}
	return to
}

func mapGmailMessage(m *gmail.Message) *models.Message {
	headers := make(map[string]string)
	for _, h := range m.Payload.Headers {
		headers[h.Name] = h.Value
	}
	var sender models.Sender
	if from, ok := headers["From"]; ok {
		addr, err := mail.ParseAddress(from)
		if err == nil {
			sender.Email = addr.Address
			if addr.Name != "" {
				sender.Name = addr.Name
			}
		} else {
			sender.Email = from
		}
	}
	to := appendRecipients(nil, headers["To"])
	to = appendRecipients(to, headers["Cc"])
	var date time.Time
	if m.InternalDate > 0 {
		date = time.UnixMilli(m.InternalDate)
	}
	return &models.Message{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		Sender:   sender,
		To:       to,
		Subject:  headers["Subject"],
		Date:     date,
		Size:     int64(m.SizeEstimate),
		Labels:   m.LabelIds,
		Headers:  headers,
		Snippet:  m.Snippet,
	}
}
