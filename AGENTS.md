# gclean — agent guide

## Dev commands

```bash
just check          # default gate: vet + build + lint + test
just check-quick    # vet + build + test (skip lint scripts)
go test ./...           # all tests (no CGO, pure-Go SQLite)
go test ./internal/engine/   # single package
go test -run TestScanCommand_DevFixturePipeline ./internal/cli/   # integration test
```

There IS a Makefile and CI (`.github/workflows/lint-emails.yml`). `just` is the preferred runner; `make` mirrors it. Pre-commit (`.pre-commit-config.yaml`) runs `go vet`, `go build`, `golangci-lint`, plus the email-literal lint on staged Go files.

## Email literals are FORBIDDEN in non-test source

CI and a pre-commit hook (`scripts/lint-email-literals.sh`) reject any raw `local@domain` literal in non-test `*.go`/`*.json`. Cloudflare's source-pass silently rewrites such literals into `[email protected]` (no `@`), breaking domain extraction and equality checks.

Always assemble addresses at runtime:

```go
addr := defang.MkEmail("noreply", "example.com") // "noreply@example.com"
```

`MkEmail` lives in `internal/defang` and is intentionally non-test code so production fixture loaders can use it too. This is the single most likely thing to trip a PR.

## OAuth and real Gmail read path

Real Gmail requires `credentials.json` at `~/.config/gclean/credentials.json` (or `$GCLEAN_CREDENTIALS_PATH`). Run `gclean login` to complete the browser-based OAuth flow; the token is stored at `~/.config/gclean/token.json` by default or at `$GCLEAN_TOKEN_PATH`.

`gclean scan` can then use the real Gmail client without `--fixtures`. The real mutation adapter supports Trash, restore, and purge calls, but local reconciliation and live-account validation are still being hardened. Use `--fixtures` for every local end-to-end cleanup flow:

```bash
# End-to-end local dev flow:
GCLEAN_DB_PATH=$(mktemp -d)/gclean.db
gclean scan  --fixtures testdata/fixtures/messages.json
gclean stats
gclean dry-run
gclean clean --yes --fixtures testdata/fixtures/messages.json
gclean undo  --fixtures testdata/fixtures/messages.json
```

`RealClient.ListMessages` in `internal/gmailclient/real.go` is implemented for paginated metadata reads and classification headers. `TrashMessages` and `RestoreFromTrash` use retrying individual Gmail calls; `EmptyTrash` paginates the Trash label and batch-deletes up to 1,000 IDs per request. The engine's local reconciliation and end-to-end real-Gmail cleanup tests remain the next safety layer. Swap implementations at the `gmailclient.Client` interface seam.

## Key env vars

| Var                       | Default                             | Purpose           |
| ------------------------- | ----------------------------------- | ----------------- |
| `GCLEAN_DB_PATH`          | `~/.config/gclean/gclean.db`        | SQLite db path    |
| `GCLEAN_CREDENTIALS_PATH` | `~/.config/gclean/credentials.json` | Gmail OAuth client credentials |
| `GCLEAN_TOKEN_PATH`       | `~/.config/gclean/token.json`       | Persisted Gmail OAuth token |
| `GCLEAN_CONFIG_PATH`      | `~/.config/gclean/config.yaml`      | YAML rule config  |
| `GCLEAN_UNDO_CACHE`       | `~/.config/gclean/undo-cache.json`  | Pre-trash records |

## Safety invariants

- `--yes` required before `clean` or `purge` modifies state
- Planner refuses to delete a non-junk message even if a delete rule matches (PRD §15, `internal/engine/planner.go:99-107`)
- `clean` moves to Trash (recoverable); only `purge` empties Trash permanently
- Undo cache (`~/.config/gclean/undo-cache.json`) preserves pre-trash records

## Architecture

```
cmd/gclean/main.go         — slog setup, calls cli.Build()
internal/cli/              — Cobra command tree (thin handlers; pipeline adapters in pipeline.go)
internal/engine/           — classifier, protector, evaluator (DSL), planner, pipeline (stages)
internal/gmailclient/      — Client interface + FakeClient + OAuth-backed RealClient
internal/storage/          — SQLite via modernc.org/sqlite (no CGO) + undo-cache IO
internal/config/           — YAML via yaml.v3 (not Viper)
internal/defang/           — MkEmail (runtime email assembly, defeats obfuscation)
internal/models/           — cross-package types
internal/tui/              — experimental Bubble Tea UI
testdata/fixtures/         — message fixture corpus
```

The scan→plan→trash flow is the `engine.Pipeline` seam (`internal/engine/pipeline.go`): composable `Stage`s (Scan → Plan → Apply). CLI handlers build a `Pipeline` and run the stage slice they need. `Plan` does no Gmail I/O; `Apply` is the only Gmail-mutating stage.

Fixtures (`testdata/fixtures/messages.json`) are real Gmail-shaped JSON, used by both `--fixtures` and `NewFakeClientFromMessages()` in tests.

## Config

First run auto-creates `~/.config/gclean/config.yaml` with defaults. Config DSL: `key:value key:value` predicates. Supported: `has:`, `category:`, `from:`, `older_than:` (Nd only), `larger_than:` (B/KB/MB/GB). Commas tolerated as separators.

## Testing notes

- Engine tests are pure/table-driven (no I/O, no clocks)
- CLI tests (`cli_test.go`) are integration tests that run scan→stats→dry-run→clean against fixtures with a temp `GCLEAN_DB_PATH`
- FakeClient tests exercise the Client interface without network
- `cli.Build(stdout, stderr io.Writer)` injects test buffers — use `bytes.Buffer` and `cmd.SetArgs()`
