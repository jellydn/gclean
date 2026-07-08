package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// auth.go — gclean login / logout. Until RealClient ships + OAuth lands,
// these commands only validate credentials.json and remove token.json.
// They never make a network call.

// newLoginCmd: scaffolded OAuth flow. Verifies credentials.json exists at
// credentialsPath() and prints setup steps if missing.
func newLoginCmd(out, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate with Gmail via OAuth2 and store token locally",
		Long:  "Reads credentials.json from GCLEAN_CREDENTIALS_PATH (or ~/.config/gclean/credentials.json) and starts the OAuth browser flow. The token lands at ~/.config/gclean/token.json.",
		RunE: func(cmd *cobra.Command, args []string) error {
			p := credentialsPath()
			if _, err := os.Stat(p); err != nil {
				_, _ = fmt.Fprintf(errOut, "gclean login: %s not found.\n\n", p)
				_, _ = fmt.Fprintln(errOut, "Setup steps:")
				_, _ = fmt.Fprintln(errOut, "  1. Create a Google Cloud Console project: https://console.cloud.google.com/")
				_, _ = fmt.Fprintln(errOut, "  2. Enable the Gmail API and the People API.")
				_, _ = fmt.Fprintln(errOut, "  3. Create an OAuth Desktop client.")
				_, _ = fmt.Fprintln(errOut, "  4. Download client_secret.json and save it to "+p)
				_, _ = fmt.Fprintln(errOut, "  5. Re-run `gclean login`.")
				_, _ = fmt.Fprintln(errOut)
				_, _ = fmt.Fprintln(errOut, "Until then, drive the pipeline with --fixtures on scan/clean/dry-run.")
				return errors.New("credentials.json missing")
			}
			_, _ = fmt.Fprintf(out, "credentials.json found at %s\n", p)
			_, _ = fmt.Fprintln(out, "OAuth flow: scaffolded only — full browser round-trip lands in session 2.")
			return nil
		},
	}
	return cmd
}

// newLogoutCmd: removes token.json. Best-effort; no error on missing file.
func newLogoutCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the locally stored OAuth token",
		RunE: func(cmd *cobra.Command, args []string) error {
			tok := filepath.Join(filepath.Dir(credentialsPath()), "token.json")
			if err := os.Remove(tok); err != nil && !os.IsNotExist(err) {
				return err
			}
			_, _ = fmt.Fprintln(out, "Logged out (token.json removed if present).")
			return nil
		},
	}
}
