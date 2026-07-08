# gclean — Gmail Clean CLI

Developer-first CLI to reclaim Gmail storage safely.
Preserve important conversations, identify newsletters / notifications / marketing,
recover storage. Safe by default. Dry-run first. Trash before purge.

## Status

This scaffold implements the **local pipeline end-to-end against fixture data**:

- `gclean scan --fixtures testdata/fixtures/messages.json` pulls messages through
  the (fake) Gmail client, classifies each per the §7 rules, persists metadata to
  local SQLite.
- `gclean stats` renders the §5 example output (total, storage, reclaim, top senders,
  newsletter/notification counts, by-category, by-year).
- `gclean dry-run` walks the keep→archive→delete plan with the §15
  "refuse to delete non-junk even when a delete rule matches" safety invariant.
- `gclean clean --yes --fixtures …` moves the delete cohort to Trash
  (in-memory for the FakeClient, real Gmail once OAuth lands).
- `gclean purge --yes` empties Trash. `gclean undo` restores the last clean batch.

OAuth + real Gmail is intentionally **not yet wired**. Until `credentials.json` is
present and `gclean login` runs the full OAuth dance, `--fixtures` drives the
end-to-end pipeline locally. The seam is `internal/gmailclient.Client` — swap the
fake for a real implementation and the rest of the codebase doesn't change.

## Build & test

```bash
go mod init gclean          # if you haven't yet
go mod tidy                  # pulls cobra, sqlite, yaml
go build ./...
go test ./...
```

Deps:
- `github.com/spf13/cobra`
- `gopkg.in/yaml.v3`
- `modernc.org/sqlite` (pure-Go SQLite, no CGO required)

We intentionally skipped Viper for the scaffold: it adds 30+ transitive deps for
a single config file. Swapping in Viper is a 1-file change in `internal/config/`
and called out in §17 of the roadmap.

## Dev workflow with fixtures

```bash
# Drive an end-to-end scan against the bundled 40-message fixture corpus.
export GCLEAN_DB_PATH=$(mktemp -d)/gclean.db
gclean scan  --fixtures testdata/fixtures/messages.json
gclean stats
gclean dry-run
gclean clean --yes --fixtures testdata/fixtures/messages.json
gclean undo  --fixtures testdata/fixtures/messages.json

# Experimental interactive UI: per-sender checkbox list with safe-to-delete counts.
# Press Space to toggle, Enter to commit, q to quit. Selection is written to
# ~/.config/gclean/tui-selection.json (wiring into `gclean clean` next session).
gclean tui
```

## Safety model (PRD §15)

- Dry-run by default
- Trash, never permanently delete from `clean`
- `--yes` gate before any state-changing command
- The planner refuses to delete a non-junk message even if a delete rule
  matches — explicit safety-check in `internal/engine/planner.go`
- Local-only by default; no bodies ever loaded
- OAuth flow will request offline gmail.readonly + gmail.modify only

## Roadmap → next session

- `gclean login` complete OAuth browser flow with localhost callback
- `google.golang.org/api/gmail/v1` RealClient implementation of `Trash`/`Restore`/`Empty`
- `gclean tui` Bubble Tea UI for §12 (toggle senders, see reclaim before action)
- People-API enrichment (`IsContact`) on scan
- Per-message rate-limited batcher for `clean`
- `gclean rules` editor (currently show-only)
- `gclean report` analytics export

See PRD §16 for the long-horizon list.

## Layout

```
cmd/gclean/main.go          Entry point, slog setup
internal/cli/               Cobra command graph (every §9 command)
internal/config/            YAML config (path resolution, parse, compile)
internal/engine/            classifier, protector, evaluator (rules DSL), planner
internal/gmailclient/       Client interface + FakeClient + RealClient stub
internal/models/            Cross-package types
internal/storage/           modernc/sqlite schema + stats aggregator + sender-safety rollup
internal/tui/               Bubble Tea checkbox UI for `gclean tui` (EXPERIMENTAL)
testdata/fixtures/          40-message sample corpus for local dev
```
