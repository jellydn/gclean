package gmailclient

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"gclean/internal/models"
	"google.golang.org/api/gmail/v1"
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

// ErrNotImplemented is returned by trash-mutating methods until Milestone 2.
var ErrNotImplemented = errors.New("gmailclient.RealClient: not implemented; trash operations ship in the next session")

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
		for _, m := range resp.Messages {
			if max > 0 && len(out) >= max {
				return out, nil
			}
			full, err := r.service.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders("From", "To", "Cc", "Subject", "Date").Do()
			if err != nil {
				return nil, fmt.Errorf("get message %s: %w", m.Id, err)
			}
			msg := mapGmailMessage(full)
			out = append(out, msg)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

func (r *RealClient) TrashMessages(ids []string) error {
	return ErrNotImplemented
}

func (r *RealClient) EmptyTrash() error {
	return ErrNotImplemented
}

func (r *RealClient) RestoreFromTrash(ids []string) error {
	return ErrNotImplemented
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
	var to []string
	if toHdr, ok := headers["To"]; ok {
		parts := strings.Split(toHdr, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				to = append(to, p)
			}
		}
	}
	if ccHdr, ok := headers["Cc"]; ok {
		parts := strings.Split(ccHdr, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				to = append(to, p)
			}
		}
	}
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
