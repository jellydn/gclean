# STRUCTURE

Current repository layout and where to make common changes.

## Top level

```text
gclean/
├── AGENTS.md
├── README.md
├── LICENSE
├── go.mod / go.sum
├── justfile / Makefile
├── .pre-commit-config.yaml
├── .github/workflows/lint-emails.yml
├── cmd/gclean/main.go
├── internal/
├── testdata/fixtures/
├── scripts/
├── .plans/implement-notes.md
└── .planning/codebase/   # this map
```

The current source/test/fixture/script inventory is 37 files under `cmd`, `internal`, `testdata`, and `scripts`, totalling approximately 5,330 lines.

## `cmd/gclean/`

- `main.go` — process entry point; configures `slog` and invokes `cli.Build()`.

## `internal/cli/`

- `cli.go` — root Cobra command, command registration, client selection, path helpers, and byte formatting.
- `auth.go` — `login` and `logout`; OAuth browser flow and token removal.
- `pipeline.go` — `scan`, `stats`, `dry-run`, `clean`, `purge`, `undo`; pipeline construction and output.
- `meta.go` — `rules`, `config`, and experimental `tui` commands.
- `insights.go` — `sender`, `attachments`, `newsletters`, `receipts`, and TUI-selection persistence.
- `demo.go` — fixture-free terminal preview using `storage.SenderSafety`.
- `dev.go` — one-shot/watch development loop.
- `cli_test.go` — command registration, fixture integration, safety gate, demo, sender, and dev tests.

CLI constructor convention: `newXCmd(out, errOut io.Writer) *cobra.Command`. Tests inject `bytes.Buffer` instances through `Build(stdout, stderr)`.

## `internal/engine/`

- `classifier.go` — deterministic junk classification from sender/domain, headers, and Gmail labels.
- `protector.go` — configurable keep/protection rules and recent/contact/label handling.
- `evaluator.go` — rule DSL parser and predicate matcher.
- `planner.go` — ordered safety decision tree and dry-run report construction.
- `pipeline.go` — composable scan, plan, and apply stages.
- `classifier_test.go`, `protector_test.go`, `evaluator_test.go`, `planner_test.go` — pure logic tests.

The classifier/protector/evaluator/planner operate in memory. `pipeline.go` is the orchestration exception and depends on storage plus a narrow `Gmailer` interface.

## `internal/gmailclient/`

- `client.go` — shared Gmail client interface.
- `fake.go` — JSON fixture loader, basic Gmail query matching, and in-memory trash state.
- `real.go` — credential/token-backed Gmail service, read-only metadata listing, message mapping, and mutation stubs.
- `oauth.go` — OAuth config, callback server, token persistence, browser opening.
- `fake_test.go`, `real_test.go` — fake behavior and real-client construction/stub tests.

## `internal/storage/`

- `sqlite.go` — schema, connection lifecycle, row persistence, verdict updates, and trash/restore storage operations.
- `stats.go` — single-pass aggregations and largest-message query.
- `sendersafety.go` — `SenderSafety` data shape for the TUI.
- `undocache.go` — versioned JSON undo cache with SHA-256 record checksum.

## `internal/config/`

- `config.go` — path resolution, default config creation, YAML document shape, and compilation.
- `yaml.go` — thin `yaml.v3` unmarshalling wrapper.

## `internal/models/`

- `models.go` — `Message`, `Sender`, `Classified`, `Decision`, `Verdict`, stats/report structures, and stable reason-code strings.

## `internal/defang/`

- `defang.go` — `MkEmail(local, domain)` runtime address construction used to avoid raw email literals in source.

## `internal/tui/`

- `app.go` — Bubble Tea model, keyboard handling, rendering, selection statistics, and program wrapper.
- `app_test.go` — model/update/view tests without requiring an interactive terminal.

## Fixtures and scripts

- `testdata/fixtures/messages.json` — 40-message Gmail-shaped fixture corpus; current checks report 60 `@` characters and no `[email protected]` placeholder.
- `testdata/fixtures/messages.README.md` — fixture diversity and obfuscation-defense notes.
- `scripts/lint-email-literals.sh` — source lint for non-test Go/JSON email literals.

## Where to add changes

| Change | Location |
| --- | --- |
| New Cobra command | New/grouped file under `internal/cli/`, register in `Build`, extend `TestBuild_Help` |
| Classification signal | `internal/engine/classifier.go` plus classifier tests |
| Keep/protection rule | `internal/engine/protector.go` plus protector/planner tests |
| DSL predicate | `internal/engine/evaluator.go`, config docs, evaluator tests |
| Planner safety behavior | `internal/engine/planner.go` plus planner tests |
| Gmail operation | `gmailclient.Client`, `RealClient`, `FakeClient`, and boundary tests |
| SQLite field/rollup | `internal/storage/sqlite.go` or `stats.go`; consider migrations before release |
| TUI interaction | `internal/tui/app.go` and `app_test.go` |
| New user path | `internal/cli/cli.go` or the relevant command group; use existing env helpers |

## Naming and file conventions

- Go files are lower-case descriptive names; tests use the matching `_test.go` suffix.
- CLI constructors use `new<Name>Cmd`.
- Stable reason codes are exported constants in `models.go`.
- Storage methods describe the operation (`Upsert`, `SetVerdict`, `Aggregations`, `MarkTrashed`).
- User-facing output is written through injected `io.Writer` values.
