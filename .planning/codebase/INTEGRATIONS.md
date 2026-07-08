# INTEGRATIONS

Third-party / external surfaces the project talks to. The project is deliberately **deeply stubbed** at the Gmail boundary — every interface below is either scaffold-first or local-only.

## Gmail API (NOT YET WIRED)

- **Status**: Stubbed. The OAuth browser flow, and the `google.golang.org/api/gmail/v1` wiring, are deferred to a later session.
- **Interface seam**: `gmailclient.Client` (`internal/gmailclient/client.go:16`) — four methods: `ListMessages(query, max)`, `TrashMessages(ids)`, `EmptyTrash()`, `RestoreFromTrash(ids)`.
- **Implementation that runs today**: `gmailclient.FakeClient` (`internal/gmailclient/fake.go`). Loads a JSON fixture file, mutates in-memory trash state. No network.
- **Implementation scaffolded, not active**: `gmailclient.RealClient.NewRealClient` (`internal/gmailclient/real.go:23`) accepts a path string and returns `ErrNotImplemented` from every method until session 2.
- **OAuth scopes called out in PRD/AGENTS.md**: `gmail.readonly` + `gmail.modify` only. Localhost callback flow.

## Local Filesystem

| Path                                             | Owner                                                                                          | Purpose                                                                                                            |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `~/.config/gclean/config.yaml`                   | `config.Load()` (`internal/config/config.go:65`)                                               | YAML rules. Auto-created on first run; auto-create is also polled by `gclean dev` (`internal/cli/dev.go:124-139`). |
| `<GCLEAN_DB_PATH or ~/.config/gclean/gclean.db>` | `storage.Open()` (`internal/storage/sqlite.go:46`)                                             | Local SQLite metadata store. Created/opened on `gclean scan` / `dev` / etc.                                        |
| `~/.config/gclean/undo-cache.json`               | `storage.SaveUndoCache()` (`internal/storage/undocache.go`), written by the engine Apply stage | Pre-trash records so `gclean undo` can restore them within the 30-day Gmail window.                                |
| `~/.config/gclean/tui-selection.json`            | `cli.saveSelection()` (`internal/cli/cli.go:589`)                                              | Output of `gclean tui` Bubble Tea UI; written under `selectors` + `ts` keys.                                       |
| `~/.config/gclean/token.json` (would be)         | `gmailclient.RealClient` later                                                                 | OAuth token (not yet written).                                                                                     |
| `~/.config/gclean/credentials.json`              | user-supplied                                                                                  | OAuth client_secret from Google Cloud Console — required for real Gmail, absent in scaffold.                       |

The `defaultCache` / `credentialsPath` / `storePath` helpers in `internal/cli/cli.go` centralize path resolution and respect the override env vars listed in `STACK.md` so test code can sandbox everything into a `$(mktemp -d)`.

## YAML Config (Internal DSL)

- Format: `gopkg.in/yaml.v3` (`internal/config/yaml.go:3`).
- Schema (`internal/config/config.go:41`): `Document{Keep KeepConfig; Delete []string; Archive []string; Ignore []string}` with `KeepConfig` (`internal/engine/protector.go:21`) carrying boolean toggles + `recent_days`.
- Prelude (`defaultConfig` in `internal/config/config.go:13`): ships with `keep.contacts/replied/starred/important/sent_by_user/recent_days:365` and three example delete rules + one archive + one ignore.
- DSL parser (`internal/engine/evaluator.go`): predicates `has:`, `category:`, `from:`, `older_than:Xd`, `larger_than:XB|KB|MB|GB` joined by whitespace or comma.

## SQLite via modernc.org/sqlite

- Driver import: `_ "modernc.org/sqlite"` (`internal/storage/sqlite.go:14`).
- CGO-free — important for cross-compiles and CI portability.
- Schema detailed in `STACK.md`. Storage layer exposes `Upsert`, `SetVerdict`, `AllClassified`, `DeleteMessageIDs`, `MarkTrashed`, `RestoreTrashed`, `CountAll`, `Aggregations` (one scan → StatsReport + BySender + SendersSafe), `LargestAttachments`, `SaveUndoCache`, `LoadUndoCache`.

## OAuth2 (scaffolded, not active)

- Endpoints: Google OAuth2 authorization + token exchange + refresh. Both flows would land in `internal/gmailclient/real.go`'s future incarnation.
- Login entry point: `cli.newLoginCmd` (`internal/cli/cli.go:184`) currently checks `credentials.json` exists and prints setup steps if missing — it does **not** start a browser flow yet.
- `cli.newLogoutCmd` removes `token.json` only.

## Bubble Tea / Lip Gloss (TUI)

- Models live in `internal/tui/app.go`. The `gclean tui` command (`internal/cli/cli.go:622`) reads sender rows from SQLite, hands them to `tui.Run(tui.NewModel(safeties))`, and persists the selection via `saveSelection`.
- Keymap (declared in `--help`): arrows/j/k move, space toggle, a select-all junk, n clear, enter commit, q quit.

## `gclean dev` Watch Loop (developer-facing integration with the local FS)

- Polls both the fixture file (default `testdata/fixtures/messages.json`) and the config file (default `~/.config/gclean/config.yaml`) for mtime changes (`internal/cli/dev.go:97-180`).
- Default interval: 2s (`-d time.Second * 2`).
- Non-fatal missing files: log once per state transition, keep polling (`wasFixtureMissing`, `wasConfigMissing` state vars).
- Config auto-create absorbed: `config.Load()` would write the file during the first iteration's `dry-run`; the watch loop pre-sets `lastConfigMtime` on first valid sight so the auto-created mtime isn't misread as a user-driven change.
- Triggers a `scan + stats + dry-run` pipeline (via `Build() + SetArgs() + Execute()`) on either file's mtime change.

## No External APIs Today

- No telemetry, no analytics, no log-shipping, no payment, no email-sending, no LLM APIs.
- The only network calls gclean can currently make are inside `RealClient` (which always returns `ErrNotImplemented`).
