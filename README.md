# gclean — Gmail cleaner for desktop and terminal

Cross-platform desktop workflow and developer-friendly CLI to reclaim Gmail storage safely.
Preserve important conversations, identify newsletters / notifications / marketing,
recover storage. Safe by default. Dry-run first. Trash before purge.

## Desktop app

The desktop UI ships inside the same single `gclean` binary. It runs on a
random loopback-only port and opens in the default browser, so there is no
Electron runtime, native webview dependency, or background cloud service.

```bash
go build -o gclean ./cmd/gclean
./gclean desktop

# Safe no-network demo against the bundled corpus:
GCLEAN_DB_PATH=$(mktemp -d)/gclean.db ./gclean desktop \
  --fixtures testdata/fixtures/messages.json
```

The workflow is scan metadata → review storage estimates and planner-approved
senders/messages → filter the cohort → type an explicit confirmation → move to
Trash. The last batch can be restored. Permanent Empty Trash is hidden unless
the app is started with `--allow-purge`, and requires a separate full-access
OAuth grant plus an irreversible-action confirmation.

The built-in **Settings** page provides safe keep defaults, validated cleanup
and archive rules, protected domains, guided OAuth credential import/status,
and local path diagnostics. Cleanup settings are persisted atomically to the
same `config.yaml` used by the CLI; secret and token contents are never shown.

See [Desktop setup and packaging](docs/desktop.md) for Google Cloud Console,
security, cross-platform builds, and platform-specific launch notes.
Pull requests and `main` builds produce short-lived portable workflow
artifacts. SemVer tags (`v1.2.3`) publish macOS, Linux, and Windows archives
plus `SHA256SUMS` to GitHub Releases. Container images are intentionally not
published because this is a loopback desktop application, not a network
service.

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
  in-memory through the FakeClient. The real client retries Trash/restore
  calls and reconciles partial mutations against Gmail's actual state.
- `gclean purge --yes` empties Trash. `gclean undo` restores the last clean batch
  when using the fixture client.

OAuth login and real Gmail read/write support are now implemented. Run
`gclean login` to authorize a desktop OAuth client with the least-privilege
`gmail.modify` scope, then `gclean scan` can fetch
Gmail metadata without `--fixtures`. The real client retries individual Trash and
restore calls and empties Trash by paginating the Trash label and batch-deleting
up to 1,000 IDs per request when separately authorized for full access. Mutations
honor the server's `Retry-After` hint and reconcile partial
failures against Gmail's actual state via `InTrash`, so a partially-applied
`clean`/`purge` trims the undo cache and local store instead of drifting. The seam
is `internal/gmailclient.Client`, so the fixture client remains available for
safe local end-to-end testing.

OAuth requests offline access for refreshable desktop sessions. Local metadata
and recovery batches are account-bound; gclean refuses to merge or restore them
under a different Gmail account.

For real Gmail setup, provide `credentials.json` at
`~/.config/gclean/credentials.json` or set `GCLEAN_CREDENTIALS_PATH`, run
`gclean login`, and use `GCLEAN_TOKEN_PATH` to override the token location when
needed. Tokens are stored with restrictive permissions.

The real metadata scan requests the headers used by classification, including
`List-Unsubscribe`, `List-ID`, `Precedence`, and `Auto-Submitted`.

The local fixture workflow remains the recommended way to exercise cleanup
while live-account end-to-end validation is completed.

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

Real `clean`, `undo`, and `purge` now call the Gmail mutation adapter and
reconcile partial failures via `InTrash`: after a partial mutation the undo
cache and local store are trimmed to Gmail's actual state, and a `purge` that
permanently deletes messages leaves `undo` able to skip those IDs (404) instead
of aborting or re-inserting ghosts. Use the fixture flow below first:
real-account validation is destructive and still pending before broad
production use.

## Safety model (PRD §15)

- Dry-run by default
- Trash, never permanently delete from `clean`
- `--yes` gate before any state-changing command
- The planner refuses to delete a non-junk message even if a delete rule
  matches — explicit safety-check in `internal/engine/planner.go`
- Local-only by default; no bodies ever loaded
- Default OAuth requests only `gmail.modify` (metadata reads + Trash/restore)
- Permanent purge requires a separate `login --allow-permanent-delete` grant
  and an explicit runtime `desktop --allow-purge` opt-in

## Roadmap → next session

- ~~Reconcile local SQLite and undo-cache state after partial or interrupted real Gmail mutations~~ — done (InTrash reconcile)
- Live-account end-to-end validation (TC-01…TC-10 in `.planning/live-account-mutation-test-plan.md`)
- People-API enrichment (`IsContact`) on scan
- Native signed/notarized installer bundles (the portable single binary and
  browser-hosted desktop UI are available now)
- Per-message rate-limited batcher for `clean`
- Richer rule-builder presets beyond the validated desktop advanced editor
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
internal/desktop/           Loopback API + embedded responsive desktop UI
internal/tui/               Bubble Tea checkbox UI for `gclean tui` (EXPERIMENTAL)
testdata/fixtures/          40-message sample corpus for local dev
```
