// Command gclean is the Gmail Clean CLI.
package main

import (
	"context"
	"log/slog"
	"os"

	"gclean/internal/cli"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := cli.Build(nil, nil).ExecuteContext(context.Background()); err != nil {
		// Cobra's first-line error print is silenced (SilenceErrors=true).
		// Surface the message via stderr so the user sees what went wrong.
		_, _ = os.Stderr.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
}
