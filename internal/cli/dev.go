package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gclean/internal/config"

	"github.com/spf13/cobra"
)

// newDevCmd returns the `gclean dev` subcommand. It runs the
// `scan + stats + dry-run` pipeline against a JSON fixture file. In
// watch mode (the default), it polls the fixture's mtime every
// `interval` and re-runs the pipeline whenever the file changes —
// designed for iterating on fixture data and watching the pipeline
// output update in real time.
//
// Polling-based: no new dependencies (intentional; fsnotify would be a
// better fit for sub-second feedback, but adds a dep and a moving part
// for a tool that is only used in development). Default interval is 2s
// which is well below human iteration speed.
//
// Default fixture: testdata/fixtures/messages.json (relative to the
// process's CWD). For repos where the gclean binary is invoked from a
// different working directory, pass --fixtures with an absolute or
// explicitly-relative path.
//
// Press Ctrl+C to exit watch mode. The SIGINT/SIGTERM handler cancels
// the polling context cleanly.
func newDevCmd(out, errOut io.Writer) *cobra.Command {
	var (
		fixturesPath string
		watch        bool
		interval     time.Duration
	)
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run the gclean pipeline in develop mode with file watching",
		Long: "Develop mode for iterating on the gclean fixture. " +
			"Runs scan + stats + dry-run against a fixture file and " +
			"(in watch mode) re-runs on each change. " +
			"Default fixture: testdata/fixtures/messages.json. " +
			"Press Ctrl+C to exit watch mode.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if fixturesPath == "" {
				fixturesPath = "testdata/fixtures/messages.json"
			}
			if watch {
				return runDevWatch(out, errOut, fixturesPath, interval)
			}
			return runDevOnce(out, errOut, fixturesPath)
		},
	}
	cmd.Flags().StringVar(&fixturesPath, "fixtures", "", "Path to a JSON fixtures file (default: testdata/fixtures/messages.json)")
	cmd.Flags().BoolVar(&watch, "watch", true, "Re-run the pipeline on fixture changes")
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "Polling interval in watch mode")
	return cmd
}

// runDevOnce runs the pipeline a single time and exits. Used when
// --watch=false (and by the smoke test for one-shot assertions).
func runDevOnce(out, errOut io.Writer, fixturesPath string) error {
	_, _ = fmt.Fprintf(out, "=== gclean dev (one-shot) ===\n")
	return runDevIteration(out, errOut, fixturesPath)
}

// runDevWatch runs the pipeline in watch mode: polls BOTH the fixture
// file AND the config file (~/.config/gclean/config.yaml or
// GCLEAN_CONFIG_PATH) every `interval`, re-runs the pipeline on
// EITHER change, and exits cleanly on SIGINT/SIGTERM.
//
// Missing-fixture is NON-FATAL — the loop logs a warning on the
// present→missing transition and keeps polling, so a contributor who
// deletes the fixture during a refactor can recreate it and have the
// watch pick up the new mtime without restarting. Reappearance is
// logged once on the missing→present transition; intermediate missing
// ticks are silent.
//
// The config file is OPTIONAL — config.Load() auto-creates it on
// first call via writeDefault() (see internal/config/config.go), so
// missing-config at startup is also non-fatal: the first iteration's
// dry-run will create the file, and the watch loop absorbs the
// auto-create mtime change (see the !configSeen pre-set below) so it
// does NOT trigger a redundant second iteration. The user only sees
// a reappearance log on the next tick.
func runDevWatch(out, errOut io.Writer, fixturesPath string, interval time.Duration) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// SIGINT/SIGTERM handler — cancels the polling context so the loop
	// exits cleanly without a hard os.Exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		_, _ = fmt.Fprintf(errOut, "\ngclean dev: received %v, shutting down...\n", sig)
		cancel()
	}()

	// Resolve the config path ONCE at startup. A running Go process
	// can't see mid-session changes to the parent shell's env vars
	// (GCLEAN_CONFIG_PATH) anyway, so polling DefaultPath() per tick
	// is unnecessary. The file is OPTIONAL (see docstring + auto-create
	// note above).
	configPath, _ := config.DefaultPath()

	_, _ = fmt.Fprintf(out, "=== gclean dev (watching %s and %s, polling every %s, Ctrl+C to exit) ===\n", fixturesPath, configPath, interval)

	var (
		lastFixtureMtime  time.Time
		lastConfigMtime   time.Time
		fixtureSeen       bool // have we ever seen the fixture with a valid mtime?
		configSeen        bool // have we ever seen the config with a valid mtime?
		wasFixtureMissing bool
		wasConfigMissing  bool
	)
	iter := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// --- Fixture check -------------------------------------------------
		// Fixture is REQUIRED. The first valid mtime naturally triggers
		// an iteration (lastFixtureMtime starts at zero, any valid mtime
		// differs). No pre-set needed for the first sight — the
		// fixture-change check below fires automatically.
		fixtureMtime, fixtureErr := getMtime(fixturesPath)
		if fixtureErr != nil {
			if !wasFixtureMissing {
				_, _ = fmt.Fprintf(errOut, "gclean dev: stat %s: %v (fixture missing; will keep polling; recreate it to resume)\n", fixturesPath, fixtureErr)
				wasFixtureMissing = true
			}
			lastFixtureMtime = time.Time{}
		} else {
			if wasFixtureMissing {
				_, _ = fmt.Fprintf(errOut, "gclean dev: fixture reappeared at %s; resuming\n", fixturesPath)
				wasFixtureMissing = false
			}
			fixtureSeen = true
		}

		// --- Config check --------------------------------------------------
		// Config is OPTIONAL. The first valid mtime is PRE-SET to absorb
		// the auto-create mtime change: the first iteration's dry-run
		// calls config.Load() which auto-creates the file via
		// writeDefault(), and the resulting mtime would otherwise be
		// misread as a user-driven change on the next tick.
		//
		// The deleted-then-recreated case still works correctly: when
		// the file goes missing, lastConfigMtime resets to zero; when
		// it reappears, the change check fires (configSeen is already
		// true, so we DON'T re-pre-set).
		configMtime, configErr := getMtime(configPath)
		if configErr != nil {
			if !wasConfigMissing {
				_, _ = fmt.Fprintf(errOut, "gclean dev: stat %s: %v (config missing; the first iteration's dry-run will auto-create it via config.Load())\n", configPath, configErr)
				wasConfigMissing = true
			}
			lastConfigMtime = time.Time{}
		} else {
			if wasConfigMissing {
				_, _ = fmt.Fprintf(errOut, "gclean dev: config reappeared at %s\n", configPath)
				wasConfigMissing = false
			}
			if !configSeen {
				lastConfigMtime = configMtime
				configSeen = true
			}
		}

		// --- Trigger iteration on EITHER file's mtime change --------------
		fixtureChanged := fixtureSeen && !fixtureMtime.Equal(lastFixtureMtime)
		configChanged := configSeen && !configMtime.Equal(lastConfigMtime)
		if fixtureChanged || configChanged {
			if fixtureSeen {
				lastFixtureMtime = fixtureMtime
			}
			if configSeen {
				lastConfigMtime = configMtime
			}
			iter++
			_, _ = fmt.Fprintf(out, "\n--- iteration %d (%s) ---\n", iter, time.Now().Format("15:04:05"))
			if err := runDevIteration(out, errOut, fixturesPath); err != nil {
				// Don't abort watch mode on a single iteration failure
				// (e.g. transient DB lock); just log and try again on
				// the next tick.
				_, _ = fmt.Fprintf(errOut, "gclean dev: iteration %d failed: %v\n", iter, err)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// runDevIteration runs the scan + stats + dry-run pipeline once.
// Each subcommand is invoked via Build() + SetArgs() + Execute() so
// the dev command goes through the same code path the user would hit
// from the command line (no shortcuts around Cobra's command setup).
func runDevIteration(out, errOut io.Writer, fixturesPath string) error {
	for _, args := range [][]string{
		{"scan", "--fixtures", fixturesPath},
		{"stats"},
		{"dry-run"},
	} {
		if err := runDevSubcommand(out, errOut, args); err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}
		_, _ = fmt.Fprintln(out)
	}
	return nil
}

// runDevSubcommand runs a single gclean subcommand with the given
// args, writing stdout/stderr to the given writers.
func runDevSubcommand(out, errOut io.Writer, args []string) error {
	cmd := Build(out, errOut)
	cmd.SetArgs(args)
	return cmd.Execute()
}

// getMtime returns the file's modification time, or a zero time +
// error if the file does not exist.
func getMtime(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
