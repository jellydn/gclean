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
  in-memory through the FakeClient. The real client now supports retrying Trash
  and restore calls; local reconciliation remains under active hardening.
- `gclean purge --yes` empties Trash. `gclean undo` restores the last clean batch
  when using the fixture client.

OAuth login and real Gmail read/write support are now implemented. Run
`gclean login` to authorize a desktop OAuth client, then `gclean scan` can fetch
Gmail metadata without `--fixtures`. The real client retries individual Trash and
restore calls and empties Trash by paginating the Trash label and batch-deleting
up to 1,000 IDs per request. The seam is `internal/gmailclient.Client`, so the
fixture client remains available for safe local end-to-end testing.

For real Gmail setup, provide `credentials.json` at
`~/.config/gclean/credentials.json` or set `GCLEAN_CREDENTIALS_PATH`, run
`gclean login`, and use `GCLEAN_TOKEN_PATH` to override the token location when
needed. Tokens are stored with restrictive permissions.

The real metadata scan requests the headers used by classification, including
`List-Unsubscribe`, `List-ID`, `Precedence`, and `Auto-Submitted`.

The local fixture workflow remains the recommended way to exercise cleanup
while local reconciliation and live-account end-to-end validation are completed.

The seam is `internal/gmailclient.Client` — the fake and real implementations
can be swapped without changing the engine or storage layers.

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
# ~/.config/gclean/tui-selection.json (the selected senders constrain dry-run and clean).
gclean tui
```

## Real Gmail read workflow

```bash
# Store the local database somewhere explicit for a test run.
export GCLEAN_DB_PATH=$(mktemp -d)/gclean.db

gclean login       # browser-based OAuth; saves a local token
gclean scan        # reads Gmail metadata through the real client
gclean stats
gclean dry-run     # preview only; does not modify Gmail
```

Real `clean`, `undo`, and `purge` now call the Gmail mutation adapter. Use the
fixture flow below first: real-account validation is destructive, and the local
reconciliation path is still being hardened before broad production use.

## Safety model (PRD §15)

- Dry-run by default
- Trash, never permanently delete from `clean`
- `--yes` gate before any state-changing command
- The planner refuses to delete a non-junk message even if a delete rule
  matches — explicit safety-check in `internal/engine/planner.go`
- Local-only by default; no bodies ever loaded
- OAuth flow will request offline gmail.readonly + gmail.modify only

## Roadmap → next session

- Reconcile local SQLite and undo-cache state after partial or interrupted real Gmail mutations
- People-API enrichment (`IsContact`) on scan
- `gclean tui` Bubble Tea UI for §12 (toggle senders, see reclaim before action)
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
internal/gmailclient/       Client interface + FakeClient + OAuth-backed RealClient
internal/models/            Cross-package types
internal/storage/           modernc/sqlite schema + stats aggregator + sender-safety rollup
internal/tui/               Bubble Tea checkbox UI for `gclean tui` (EXPERIMENTAL)
testdata/fixtures/          40-message sample corpus for local dev
```
