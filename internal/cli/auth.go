package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"gclean/internal/gmailclient"
)

func newLoginCmd(out, errOut io.Writer) *cobra.Command {
	var allowPurge bool
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
				_, _ = fmt.Fprintln(errOut, "  2. Enable the Gmail API.")
				_, _ = fmt.Fprintln(errOut, "  3. Create an OAuth Desktop client (type: Desktop app).")
				_, _ = fmt.Fprintln(errOut, "  4. Download credentials.json and save it to "+p)
				_, _ = fmt.Fprintln(errOut, "  5. Re-run `gclean login`.")
				_, _ = fmt.Fprintln(errOut)
				_, _ = fmt.Fprintln(errOut, "Until then, drive the pipeline with --fixtures on scan/clean/dry-run.")
				return errors.New("credentials.json missing")
			}

			state, err := gmailclient.NewOAuthState()
			if err != nil {
				return fmt.Errorf("create OAuth state: %w", err)
			}
			server, err := gmailclient.NewAuthCodeServer(state)
			if err != nil {
				return fmt.Errorf("start callback server: %w", err)
			}
			defer func() { _ = server.Close() }()

			cfg, err := gmailclient.LoadConfigWithRedirectAndPurge(p, server.RedirectURL(), allowPurge)
			if err != nil {
				return fmt.Errorf("load credentials: %w", err)
			}
			authURL := gmailclient.AuthorizationURL(cfg, state)
			_, _ = fmt.Fprintln(out, "Opening browser for Gmail authentication...")
			if err := gmailclient.OpenBrowser(authURL); err != nil {
				_, _ = fmt.Fprintf(out, "Could not open browser automatically. Open this URL manually:\n%s\n", authURL)
			}

			code, err := server.WaitForCode(5 * time.Minute)
			if err != nil {
				return fmt.Errorf("auth flow failed: %w", err)
			}

			tok, err := cfg.Exchange(context.Background(), code)
			if err != nil {
				return fmt.Errorf("exchange code: %w", err)
			}

			if err := gmailclient.SaveTokenWithAuthorization(tok, allowPurge); err != nil {
				return fmt.Errorf("save token: %w", err)
			}

			_, _ = fmt.Fprintln(out, "Authentication successful. Token saved to "+gmailclient.TokenPath())
			return nil
		},
	}
	cmd.Flags().BoolVar(&allowPurge, "allow-permanent-delete", false, "Request full Gmail access required to empty Trash (not needed for scan, Trash, or restore)")
	return cmd
}

func newLogoutCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the locally stored OAuth token",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := gmailclient.RemoveToken(); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "Logged out (token.json removed if present).")
			return nil
		},
	}
}
