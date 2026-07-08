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

## OAuth is NOT wired

Real Gmail requires `credentials.json` at `~/.config/gclean/credentials.json` (or `$GCLEAN_CREDENTIALS_PATH`). Until then, every command MUST be driven with `--fixtures`:

```bash
# End-to-end local dev flow:
GCLEAN_DB_PATH=$(mktemp -d)/gclean.db
gclean scan  --fixtures testdata/fixtures/messages.json
gclean stats
gclean dry-run
gclean clean --yes --fixtures testdata/fixtures/messages.json
gclean undo  --fixtures testdata/fixtures/messages.json
```

`RealClient` in `internal/gmailclient/real.go` is a stub — all methods return `ErrNotImplemented`. Swap at the `gmailclient.Client` interface seam.

## Key env vars

| Var                       | Default                             | Purpose           |
| ------------------------- | ----------------------------------- | ----------------- |
| `GCLEAN_DB_PATH`          | `~/.config/gclean/gclean.db`        | SQLite db path    |
| `GCLEAN_CREDENTIALS_PATH` | `~/.config/gclean/credentials.json` | Gmail OAuth creds |
| `GCLEAN_CONFIG_PATH`      | `~/.config/gclean/config.yaml`      | YAML rule config  |
| `GCLEAN_UNDO_CACHE`       | `~/.config/gclean/undo-cache.json`  | Pre-trash records |

## Safety invariants

- `--yes` required before `clean` or `purge` modifies state
- Planner refuses to delete a non-junk message even if a delete rule matches (PRD §15, `internal/engine/planner.go` ~L99)
- `clean` moves to Trash (recoverable); only `purge` empties Trash permanently
- Undo cache (`~/.config/gclean/undo-cache.json`) preserves pre-trash records

## Architecture

```
cmd/gclean/main.go         — slog setup, calls cli.Build()
internal/cli/              — Cobra command tree (thin handlers)
internal/engine/           — classifier, protector, evaluator (DSL), planner (pure, no I/O)
internal/gmailclient/      — Client interface + FakeClient + RealClient stub
internal/storage/          — SQLite via modernc.org/sqlite (no CGO)
internal/config/           — YAML via yaml.v3 (not Viper)
internal/models/           — cross-package types
internal/tui/              — experimental Bubble Tea UI
testdata/fixtures/         — message fixture corpus
```

Fixtures (`testdata/fixtures/messages.json`) are real Gmail-shaped JSON, used by both `--fixtures` and `NewFakeClientFromMessages()` in tests.

## Config

First run auto-creates `~/.config/gclean/config.yaml` with defaults. Config DSL: `key:value key:value` predicates. Supported: `has:`, `category:`, `from:`, `older_than:` (Nd only), `larger_than:` (B/KB/MB/GB). Commas tolerated as separators.

## Testing notes

- Engine tests are pure/table-driven (no I/O, no clocks)
- CLI tests (`cli_test.go`) are integration tests that run scan→stats→dry-run→clean against fixtures with a temp `GCLEAN_DB_PATH`
- FakeClient tests exercise the Client interface without network
- `cli.Build(stdout, stderr io.Writer)` injects test buffers — use `bytes.Buffer` and `cmd.SetArgs()`
