package cli

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"gclean/internal/config"
	"gclean/internal/desktop"
	"gclean/internal/gmailclient"
)

func newDesktopCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	var noBrowser bool
	var allowPurge bool
	cmd := &cobra.Command{
		Use:   "desktop",
		Short: "Launch the local graphical Gmail cleanup workflow",
		Long: "Starts a loopback-only desktop web interface embedded in the gclean binary. " +
			"The GUI scans metadata, previews and filters cleanup candidates, moves confirmed cohorts to Trash, and supports restore. Permanent purge is disabled unless explicitly enabled.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cachePath, err := defaultCache()
			if err != nil {
				return err
			}
			clientFactory := func() (gmailclient.Client, error) {
				return resolveClient(fixtures, credentialsPath())
			}
			configPath, err := config.DefaultPath()
			if err != nil {
				return err
			}
			app, err := desktop.New(desktop.Config{
				StorePath:       storePath(),
				CachePath:       cachePath,
				ConfigPath:      configPath,
				SelectionPath:   selectionPath(),
				CredentialsPath: credentialsPath(),
				FixturePath:     fixtures,
				AllowPurge:      allowPurge,
				Client:          clientFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = app.Close() }()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			url, err := app.Serve(ctx)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(out, "gclean desktop is ready at "+url)
			_, _ = fmt.Fprintln(out, "The server accepts connections only from this computer. Press Ctrl+C to stop.")
			if !noBrowser {
				if err := gmailclient.OpenBrowser(url); err != nil {
					_, _ = fmt.Fprintln(errOut, "Could not open a browser automatically; open the URL above.")
				}
			}
			<-ctx.Done()
			return nil
		},
	}
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to JSON fixtures for a no-network safe demo")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the local URL without opening a browser")
	cmd.Flags().BoolVar(&allowPurge, "allow-purge", false, "Enable the permanent Empty Trash control (requires login --allow-permanent-delete)")
	return cmd
}
