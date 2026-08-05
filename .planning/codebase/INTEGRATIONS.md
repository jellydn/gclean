# INTEGRATIONS

External systems, local persistence surfaces, and integration seams in the current `gclean` implementation.

## Google Gmail API

**Status: partially implemented.**

- Interface boundary: `gmailclient.Client` in `internal/gmailclient/client.go:8-31`.
- Real implementation: `internal/gmailclient/real.go:21-52` constructs an authenticated Gmail service using credentials and a persisted OAuth token.
- Read path: `RealClient.ListMessages` lists message IDs, then fetches `metadata` format with `From`, `To`, `Cc`, `Subject`, `Date`, `List-Unsubscribe`, `List-ID`, `Precedence`, and `Auto-Submitted` headers (`internal/gmailclient/real.go:58-95`).
- Mapping: `mapGmailMessage` converts Gmail API data into `models.Message`, parses sender addresses with `net/mail`, combines To/Cc recipients, maps labels, and records the estimated size (`internal/gmailclient/real.go:109-160`).
- Mutation gap: `TrashMessages`, `EmptyTrash`, and `RestoreFromTrash` return `ErrNotImplemented` (`internal/gmailclient/real.go:98-106`).
- Fake implementation: `internal/gmailclient/fake.go` loads a local JSON array, filters basic Gmail-style queries, and models Trash/restore in memory.

## OAuth

OAuth is implemented as a local desktop flow:

- Credentials are loaded by `LoadConfig` in `internal/gmailclient/oauth.go:38-56`.
- Scopes are `gmail.readonly` and `gmail.modify` (`internal/gmailclient/oauth.go:27`).
- The callback server listens on `localhost:8080` (`internal/gmailclient/oauth.go:20-29`), captures an authorization code, and supports timeout/shutdown.
- `gclean login` starts the server, opens the browser, exchanges the code, and persists the token (`internal/cli/auth.go:17-73`).
- Tokens are written with mode `0600` and can be redirected with `GCLEAN_TOKEN_PATH` (`internal/gmailclient/oauth.go:31-79`).
- `gclean logout` removes the token next to the configured credentials path (`internal/cli/auth.go:76-89`).

The OAuth flow depends on a Google Cloud project with the Gmail API enabled and a desktop OAuth client credentials file; the CLI prints setup guidance when credentials are missing.

## Local SQLite persistence

`modernc.org/sqlite` backs `internal/storage/sqlite.go`. `storage.Open()` creates the `messages` table and indexes for sender, date, junk status, and verdict. The store persists message metadata and planning state, not message bodies.

Primary storage operations:

- `Upsert`, `AllClassified`, `SetVerdict`
- `MarkTrashed`, `RestoreTrashed`
- `Aggregations`, `LargestAttachments`
- `SaveUndoCache`, `LoadUndoCache` (the latter two are JSON files, not SQLite tables)

There is no migration table; schema setup is an inline `CREATE TABLE IF NOT EXISTS` script (`internal/storage/sqlite.go:22-41`).

## Filesystem integrations

| Surface | Owner | Purpose |
| --- | --- | --- |
| `GCLEAN_CONFIG_PATH` / XDG config path | `internal/config/config.go` | User-editable YAML rules and protection profile |
| `GCLEAN_DB_PATH` | `internal/cli/cli.go`, `internal/storage/sqlite.go` | Local metadata and verdict store |
| `GCLEAN_CREDENTIALS_PATH` | `internal/cli/cli.go` | User-supplied Google OAuth client JSON |
| `GCLEAN_TOKEN_PATH` | `internal/gmailclient/oauth.go` | OAuth token persistence |
| `GCLEAN_UNDO_CACHE` | `internal/cli/pipeline.go`, `internal/storage/undocache.go` | Integrity-checked pre-trash records |
| `~/.config/gclean/tui-selection.json` | `internal/cli/insights.go` | Experimental TUI selection output |
| `--fixtures PATH` | `internal/gmailclient/fake.go` | Local Gmail-shaped fixture input |

## Configuration DSL

`internal/config/config.go` parses YAML into `Document`; `Document.Compile()` converts delete/archive strings into `engine.Rule` values. `internal/engine/evaluator.go` supports:

- `has:<header>`
- `subject:<substring>`
- `category:<name>`
- `from:<substring>`
- `older_than:<Nd>`
- `larger_than:<NB|KB|MB|GB>`

Predicates separated by whitespace or commas are ANDed. Unknown predicates do not match.

## TUI integration

`internal/tui/app.go` wraps Bubble Tea and Lip Gloss. `gclean tui` reads `storage.SenderSafety` rows from SQLite, preselects senders with delete candidates, and saves selected sender addresses through `saveSelection` (`internal/cli/meta.go:114-158`, `internal/cli/insights.go:20-36`). The selection file is not yet consumed by `gclean clean`.

## No other external services

The repository contains no telemetry, analytics, payments, email-sending, LLM, webhook, or People API integration. `Sender.IsContact` is modeled and protected by the engine, but contact enrichment is not wired.
