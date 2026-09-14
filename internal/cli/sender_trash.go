package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gclean/internal/engine"
	"gclean/internal/format"
	"gclean/internal/storage"
)

func newTrashSenderCmd(out, errOut io.Writer) *cobra.Command {
	var fixtures string
	var previewID string
	var yes bool
	cmd := &cobra.Command{
		Use:   "trash-sender <address>",
		Short: "Preview or Trash every message from one exact sender",
		Long: "Lists Gmail with a quoted from: query, then exact-matches the normalized From address before showing the count. " +
			"This direct action includes protected, starred, important, and recent mail from that sender. Without --yes, nothing changes. " +
			"With --yes, the displayed cohort moves to Trash and remains available through `gclean undo`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sender, err := engine.NormalizeSenderAddress(args[0])
			if err != nil {
				return err
			}
			client, err := resolveClient(fixtures, credentialsPath())
			if err != nil {
				return err
			}
			account, err := client.AccountEmail()
			if err != nil {
				return err
			}
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			cache, err := defaultCache()
			if err != nil {
				return err
			}
			var lock *storage.MutationLock
			if yes {
				lock, err = storage.AcquireMutationLock(cache)
				if err != nil {
					return err
				}
				defer func() { _ = lock.Unlock() }()
			}
			preview, err := engine.PreviewSenderTrash(client, account, sender)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Found %d messages from %s (%s estimated).\n", preview.Count, preview.Sender, format.HumanBytes(preview.Bytes))
			_, _ = fmt.Fprintln(out, "Warning: this direct sender action includes starred, important, recent, and otherwise protected mail from that sender.")
			if preview.Count == 0 {
				_, _ = fmt.Fprintln(out, "Nothing changed.")
				return nil
			}
			quoted := shellQuote(preview.Sender)
			if !yes {
				_, _ = fmt.Fprintf(out, "Nothing changed. Review this count, then run `gclean trash-sender %s --preview-id %s --yes` to move only this reviewed cohort to Trash.\n", quoted, preview.ID)
				return nil
			}
			if previewID == "" || previewID != preview.ID {
				return fmt.Errorf("sender preview changed or was not supplied; run `gclean trash-sender %s` again and use its --preview-id", quoted)
			}
			journal := &engine.Reconciler{
				Store:            store,
				CachePath:        cache,
				Account:          account,
				Client:           client,
				MutationLockHeld: true,
			}
			outcome, err := engine.ApplySenderTrash(journal, preview)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Moved %d messages from %s to Trash. Recover with `gclean undo`.\n", len(outcome.Moved), preview.Sender)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm moving the displayed exact-sender cohort to Trash")
	cmd.Flags().StringVar(&previewID, "preview-id", "", "Exact cohort ID printed by the read-only preview")
	cmd.Flags().StringVar(&fixtures, "fixtures", "", "Path to a JSON fixtures file (dev/test mode)")
	return cmd
}

// shellQuote wraps s in POSIX single quotes so copy-pasted commands keep a
// validated local-part character such as & or $ as one argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
