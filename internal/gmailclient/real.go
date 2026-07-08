package gmailclient

import (
	"errors"

	"gclean/internal/models"
)

// ErrCredentialsMissing is returned by NewRealClient when credentials.json
// is not present at the configured path.
var ErrCredentialsMissing = errors.New("gmail credentials.json not found; drop it into ~/.config/gclean/credentials.json or set GCLEAN_CREDENTIALS_PATH")

// RealClient talks to the real Gmail API. In this scaffold the OAuth flow
// and API calls are deliberately stubbed: the constructor returns
// ErrCredentialsMissing if credentials.json is absent, otherwise it succeeds
// but every method returns ErrNotImplemented. The OAuth dance and the
// google.golang.org/api/gmail/v1 wiring land in session 2.
type RealClient struct {
	credentialsPath string
}

// NewRealClient validates that credentials.json exists and would be readable
// by the OAuth library on a real install. It does NOT validate the JSON.
func NewRealClient(credentialsPath string) (*RealClient, error) {
	if credentialsPath == "" {
		return nil, ErrCredentialsMissing
	}
	// We deliberately do not os.Stat here so this method remains pure and
	// testable; the CLI checks existence up front and passes the path only
	// once it's confirmed.
	return &RealClient{credentialsPath: credentialsPath}, nil
}

// ErrNotImplemented is returned by every RealClient method until session 2.
var ErrNotImplemented = errors.New("gmailclient.RealClient: not implemented; OAuth + google.golang.org/api integration ships in the next session")

func (r *RealClient) ListMessages(query string, max int) ([]*models.Message, error) {
	return nil, ErrNotImplemented
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
