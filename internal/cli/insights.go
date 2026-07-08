package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"gclean/internal/models"
	"gclean/internal/storage"
)

// insights.go — gclean sender / attachments / newsletters / receipts +
// the TUI-selection save. Pure-read operations on the local SQLite store;
// none of these mutate state in Gmail or on disk beyond the TUI's selection
// file (the only "save" is saveSelection, which writes
// ~/.config/gclean/tui-selection.json when the user commits a TUI choice).

// saveSelection writes the TUI's commit-time selection to a JSON file.
// Lives here (insights.go) because tui-selection.json is produced by the
// interaction in newTuiCmd (meta.go) but consumed — eventually — by
// `gclean clean`, which reads the file before applying only the selected
// senders. The two-cmd pattern naturally straddles meta.go and pipeline.go;
// the save helper sits with the other read-side insights commands.
func saveSelection(emails []string) error {
	path := filepath.Join(filepath.Dir(credentialsPath()), "tui-selection.json")
	b, _ := json.MarshalIndent(map[string]any{
		"selectors": emails,
		"ts":        time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

// sliceControl drives newsletters/receipts: print one row per classified
// message whose ReasonCode matches any of `reasons`.
func sliceControl(out io.Writer, reasons []string) error {
	store, err := storage.Open(storePath())
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	rows, err := store.AllClassified()
	if err != nil {
		return err
	}
	for _, c := range rows {
		for _, r := range reasons {
			if c.ReasonCode == r {
				_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", c.Message.ID, c.Message.Sender.Email, truncate(c.Message.Subject, 60), humanBytes(c.Message.Size))
				break
			}
		}
	}
	return nil
}

// truncate shortens a string and adds an ellipsis on overflow. Used for
// rendering subjects in fixed-width tabwriter columns.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// --- Subcommand constructors -------------------------------------------

func newSenderCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "sender [address]",
		Short: "Per-sender insights: count, storage, safe-to-delete split",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			senders, err := store.SendersByVolume(50)
			if err != nil {
				return err
			}
			filter := ""
			if len(args) > 0 {
				filter = args[0]
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "SENDER\tMESSAGES\tSTORAGE")
			for _, s := range senders {
				if filter != "" && !strings.Contains(strings.ToLower(s.Email), strings.ToLower(filter)) {
					continue
				}
				_, _ = fmt.Fprintf(tw, "%s\t%d\t%s\n", s.Email, s.Count, humanBytes(s.Bytes))
			}
			_ = tw.Flush()
			return nil
		},
	}
}

func newAttachmentsCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "attachments",
		Short: "List the largest messages (likely attachment-heavy)",
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := storage.Open(storePath())
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			minBytes := int64(1) << 20 // 1MB threshold
			rows, err := store.LargestAttachments(minBytes, 50)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tSENDER\tSUBJECT\tSIZE\tDATE")
			for _, r := range rows {
				dateStr := r.Date
				if len(dateStr) > 10 {
					dateStr = dateStr[:10]
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.SenderEmail, truncate(r.Subject, 60), humanBytes(r.Size), dateStr)
			}
			_ = tw.Flush()
			return nil
		},
	}
}

func newNewslettersCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "newsletters",
		Short: "List all classified newsletter/mailing-list messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sliceControl(out, []string{models.ReasonNewsletter, models.ReasonMailingList})
		},
	}
}

func newReceiptsCmd(out, errOut io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "receipts",
		Short: "List all messages auto-classified as receipts/invoices",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sliceControl(out, []string{models.ReasonStripe, models.ReasonAWSBilling})
		},
	}
}
