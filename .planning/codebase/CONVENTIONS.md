# CONVENTIONS

Code style, patterns, idioms, and error-handling consistent with the existing codebase. New code should follow the same patterns.

## Module Path & Layout

- Module root: `gclean` (`go.mod:1`)
- Package layout: `cmd/gclean` for the binary entry, every implementation package under `internal/`.
- One package per concern (`engine`, `storage`, `config`, `gmailclient`, `models`, `tui`, `cli`).
- Each file in `internal/cli/` declares ONE or more Cobra subcommands; constructors are `newXxxCmd(out, errOut io.Writer) *cobra.Command`.

## Imports

- Standard library first, third-party next, `gclean/...` last — Go default. The `internal/cli/cli.go` import block follows this.
- Single-purpose packages (e.g. `gmailclient` imports nothing from `gclean` aside from `models`).
- `internal/engine` does NOT import `config` or `cli`. It imports `models` and `storage` (the `Pipeline` stages orchestrate SQLite + Gmail I/O via the `engine.Gmailer` subset of `gmailclient.Client`, so engine never imports `gmailclient` directly). The classifier/protector/planner/evaluator remain pure (no I/O); only `engine.Pipeline` touches storage.
- `internal/cli/dev.go` imports `gclean/internal/config` because it polls `config.DefaultPath()` — the only place `cli` pulls in `config` for a non-Load/Compile path.

## Error Handling

- Wrap with `%w` (`fmt.Errorf("open %s: %w", path, err)` style) all the way down — see `internal/storage/sqlite.go:48` and `internal/cli/cli.go:120`.
- Sentinels live in the package that owns the failure mode: `gmailclient.ErrCredentialsMissing` and `gmailclient.ErrNotImplemented` (`internal/gmailclient/real.go:11,30`). CLI checks these at the boundary and renders actionable messages.
- `cli.newLoginCmd` returns a fresh `errors.New("credentials.json missing")` so the error is non-zero exit but its source is the message body.
- `gclean clean` / `gclean purge` use `errors.New("confirmation required")` after printing a hint to `errOut` (`internal/cli/cli.go:354,422`).
- `gclean dev` does NOT use `errors.New` for the watch loop — instead, single-iteration failures are logged to `errOut` and the loop continues; only SIGINT/SIGTERM cleanly cancel.
- Cobra root uses `SilenceErrors: true` and `SilenceUsage: true` (`internal/cli/cli.go:42-44`) so the binary, not Cobra, owns the error surface (`cmd/gclean/main.go:16-17`).

## Logging

- `slog` via stdlib. `cmd/gclean/main.go:13` sets a text-handler logger at `LevelInfo` writing to `os.Stderr`.
- No logging calls inside `internal/engine/*` — the engine is silent; `Plan` reports go up through `Decision.Reasons` and not via `slog`.
- The CLI surface writes via `fmt.Fprintln(out/errOut, ...)` directly. `slog` is reserved for the binary's setup phase.

## Cobra Usage

- Every subcommand sets `Use`, `Short`, `Long` where the help text contributes value.
- State-changing subcommands declare `--yes bool` AND surface the gate via errOut (see `newCleanCmd` in `internal/cli/cli.go:331`).
- The `--fixtures` flag is the universal dev/test seam. Implemented by `resolveClient` choosing `FakeClient` whenever it's non-empty (`internal/cli/cli.go:65`).
- `RunE` (not `Run`) per consistent convention — error returned by handler is wrapped at the CLI level.
- `gclean tui` mentions "EXPERIMENTAL" in both `Use`/`Short/Long` strings so the contract is honest (`internal/cli/cli.go:622`).
- `gclean dev` uses `--watch BOOL` to switch between watch and one-shot modes; one-shot is what tests use for determinism.

## Output Formatting

- `text/tabwriter` for all tabular subcommands (stats, sender, attachments, demo).
- Sizes via local `humanBytes()` in `internal/cli/cli.go:152` (`1024`-base, KMGTPE units). Don't reach for `humanize` — it's transitively present but not directly imported.
- Bytes summed to/from `int64` consistently — see `models.SenderVolume.Bytes`, `models.StatsReport.EstimatedStorage`, `models.DryRunReport.RecoverBytes`.

## Reason Codes

- Stable string constants in `internal/models/models.go`. Add new ones by appending, never reordering (comment at the top of the constants block).
- Reasons have prefixes that identify their source: `protect:`, `config_keep:`, `config_archive:`, `config_delete:`, `ignored_domain`, `delete_rule_refused_non_junk`, `default_keep`. Consumers can grep on these prefixes for "what blocked the delete?".
- Hard-coded reason literals are forbidden in consumers; they use `models.ReasonXxx` constants only.

## StatsReport / DryRunReport Construction

- `models.DryRunReport` is built INSIDE `engine.Plan` (`internal/engine/planner.go:78`) so report fields are kept in lock-step with the same iteration loop — never reconstruct it from `[]Decision` after the fact.
- `models.StatsReport.LargestSender` picks the single sender with the highest COUNT (`internal/storage/stats.go:73`), not the highest BYTES — matches Gmail's storage-reclaim framing in PRD §5.

## DSL Grammar

- `=` predicate form `key:value`, separated by spaces OR commas (commas tolerated to be forgiving in config authoring) — `internal/engine/evaluator.go:31-36`.
- Only `Nd` suffix for durations, only `B|KB|MB|GB` for sizes (case-insensitive). Anything else fails to parse and the rule is silently dropped by the planner — `matchPredicate` returns `false` for unknown keys, see `internal/engine/evaluator.go:118`.
- An empty rule never matches (`Rule.Matches` returns `false` for `len(Predicates)==0`).

## The "@" Obfuscation Defense (Load-Bearing)

- **Never write `local@domain` as a literal in source** (non-test `*.go`, non-`testdata` `*.json`).
- All `@`-bearing strings in source must be assembled at runtime via `defang.MkEmail(local, domain) string` (`internal/defang/defang.go`).
- This is enforced by `scripts/lint-email-literals.sh`:
  - Scans `*.go` and `*.json` recursively,
  - Excludes `*_test.go`, `testdata/`, `vendor/`, `.git/`, `.plans/`,
  - Filters out lines starting with `//` (Go comment lines),
  - Exits 1 on any remaining `local@domain.tld` match,
  - Reports `file:line:snippet` context for every offender.

  Cloudflare's email-obfuscation source pass silently rewrites matching literals into `[email protected]` placeholders. The rewrite removes the `@`, which breaks `extractDomain` (`internal/engine/classifier.go:111`), `matchQuery` substring lookups (`internal/gmailclient/fake.go:96`), and `GROUP BY sender_email` SQL queries (`internal/storage/sendersafety.go:24`). The lint catches this BEFORE the rewrite can land in an install.

  Existing helpers that should be the source-of-truth when adding addresses:
  - `defang.MkEmail(local, domain)` — single `@`-bearing string. Use in production code, demo commands, and tests.
  - Test fixtures build inline struct literals with `JSON tags` and `defang.MkEmail` for `Sender.Email` — see `TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders` and `TestDevCommand_OneShotMode_RendersPipeline` for the canonical pattern.

## Comment Style

- File headers explain **why** the file exists, not what's in it. Examples:
  - `internal/cli/cli.go:1-5`: explains that handlers are intentionally thin.
  - `internal/cli/dev.go:1-26`: documents polling-vs-fsnotify rationale and Ctrl+C handling.
  - `internal/engine/evaluator.go:11-26`: documents the **grammar**, not the parser mechanics.
  - `internal/gmailclient/real.go:14-22`: explains why methods return `ErrNotImplemented`.
- Decision-relevant invariants are called out with `// SAFETY:` or quoted PRD reference (e.g. `PRD §15`, `§6`) — grep-friendly.
- Avoid decorative separators (`// =====` etc.) — the file header comment is enough.

## Test Conventions

- Place unit tests next to the source file with the same `_test.go` suffix.
- Place integration tests in `internal/cli/cli_test.go` (single shared file, since the CLI surface is one package).
- Engine tests are table-driven, pure (no I/O, no clocks beyond what's passed).
- CLI integration tests use `t.Setenv("GCLEAN_DB_PATH", filepath.Join(t.TempDir(), "gclean.db"))` for sandboxing.
- Always exercise the production chain in CLI tests (`cli.Build` + `SetArgs` + `Execute`) — don't reach into package-internal helpers.
- Build synthetic inline fixture JSON in `t.TempDir()` rather than referring to `testdata/...` — see `TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders` for the canonical pattern and rationale (Cloudflare obfuscation has corrupted the on-disk fixture's `sender.email` fields).

## Verification Before Merge (Project Workflow)

`just check` runs:

1. `go vet ./...`
2. `go build ./...`
3. `./scripts/lint-email-literals.sh`
4. `go test ./...`

`just e2e` runs the full scan→stats→dry-run→clean→undo pipeline against bundled fixtures using a temp `GCLEAN_DB_PATH`.

Pre-commit hooks (`prek`) run vet + build + golangci-lint + email-literal lint on staged Go files.
