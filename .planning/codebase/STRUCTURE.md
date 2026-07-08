# STRUCTURE

Directory map, key locations, and naming conventions.

## Top-Level Layout

```
gclean/
├── AGENTS.md                     ← contributor guide (dev commands, env vars, safety invariants)
├── README.md                     ← user-facing overview + roadmap
├── LICENSE
├── go.mod / go.sum
├── justfile                      ← `just check` / `just e2e` macros
├── Makefile                      ← minimal mirrors of justfile targets
├── .pre-commit-config.yaml       ← pre-commit / prek hooks (vet, build, lint, etc.)
│
├── cmd/
│   └── gclean/
│       └── main.go                 ← binary entry: slog setup → cli.Build() → ExecuteContext
│
├── internal/
│   ├── cli/                       ← Cobra command handlers (thin)
│   ├── config/                    ← YAML read/write + path resolution
│   ├── engine/                    ← classifier + protector + planner + DSL evaluator (pure) + pipeline (stages)
│   ├── gmailclient/               ← Client interface + FakeClient + RealClient stub
│   ├── models/                    ← universal message/decision/stats types
│   ├── defang/                    ← MkEmail (runtime email assembly, defeats obfuscation)
│   ├── storage/                   ← SQLite + stats aggregator + sender-safety rollup + undo-cache
│   └── tui/                       ← Bubble Tea checkbox UI (EXPERIMENTAL)
│
├── testdata/
│   └── fixtures/
│       ├── messages.json         ← 40-message sample corpus (de-corrupted 2026-07-08; 30 distinct senders)
│       └── messages.README.md    ← sibling doc: structural requirements + obfuscation-defense notes
│
├── scripts/
│   └── lint-email-literals.sh    ← custom obfuscation-defense lint
│
├── .plans/
│   └── implement-notes.md        ← dated entries: findings + resolutions
│
└── .planning/
    └── codebase/                  ← THIS directory (codemap skill output)
```

## File Inventory (current)

```
 260 internal/cli/pipeline.go         ← scan/stats/dry-run/clean/purge/undo as thin adapters over engine.Pipeline
 242 internal/cli/dev.go              ← gclean dev with watch mode + config polling + non-fatal missing
165 internal/cli/insights.go         ← sender/attachments/newsletters/receipts + tui-selection save (NEW, post-split)
126 internal/cli/cli.go              ← Build + AddCommand wiring + resolveClient + storePath + credentialsPath + humanBytes (was 842; split-refactor 2026-07-08)
123 internal/cli/meta.go             ← rules/config/tui + printKeep/printRules (NEW, post-split)
123 internal/cli/demo.go             ← gclean demo (self-contained TUI preview)
 70 internal/cli/auth.go             ← login/logout (NEW, post-split)
416 internal/cli/cli_test.go         ← 6 integration tests
293 internal/tui/app_test.go
287 internal/storage/sqlite.go
263 internal/tui/app.go
170 internal/engine/classifier.go
156 internal/gmailclient/fake.go
154 internal/engine/classifier_test.go
151 internal/engine/planner.go
151 internal/engine/evaluator.go
149 internal/storage/stats.go
148 internal/engine/planner_test.go
132 internal/models/models.go
120 internal/config/config.go
120 internal/engine/evaluator_test.go
105 internal/engine/protector.go
 84 internal/engine/protector_test.go
 72 internal/storage/sendersafety.go
 53 internal/gmailclient/fake_test.go
 51 internal/gmailclient/real.go
 31 internal/gmailclient/client.go
 75 internal/engine/pipeline.go        ← engine.Pipeline + Stage interface (scan→plan→trash seam)
```

## internal/cli/ — Cobra Command Tree

| File                       | Purpose                                                                                                                                                                                                                                                                                                                                                                                                  |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cli.go` (126 lines)       | Root command + `Build(stdout, stderr)` + cross-cutting helpers (`resolveClient`, `storePath`, `credentialsPath`, `humanBytes`). Slim after the 2026-07-08 split-refactor (`CONCERNS.md #6`). Package doc lists the per-file layout for future contributors.                                                                                                                                              |
| `auth.go` (70 lines)       | `newLoginCmd` + `newLogoutCmd`. Until RealClient ships, these commands only validate credentials.json and remove token.json — never network.                                                                                                                                                                                                                                                             |
| `pipeline.go` (~260 lines) | The action surface: `newScanCmd` + `newStatsCmd` + `newDryRunCmd` + `newCleanCmd` + `newPurgeCmd` + `newUndoCmd`. Each is a thin adapter that opens the store, resolves the client + config, and runs a slice of `engine.Pipeline` stages (`buildPipeline` + `ScanStages`/`PlanStages`/`ApplyStages`). Top-N rendering helpers (`kv`/`topN`) live here; undo-cache path resolution (`defaultCache`) too. |
| `insights.go` (165 lines)  | Read-only reporting commands: `newSenderCmd` + `newAttachmentsCmd` + `newNewslettersCmd` + `newReceiptsCmd` (all tabwriter-wrapped); plus `sliceControl` + `truncate` + `saveSelection` (writes tui-selection.json when the TUI commits a selection).                                                                                                                                                    |
| `meta.go` (120 lines)      | Inspecting/UI: `newRulesCmd` + `newConfigCmd` + `newTuiCmd` (experimental Bubble Tea UI) + `printKeep` + `printRules`.                                                                                                                                                                                                                                                                                   |
| `demo.go` (123 lines)      | Self-contained render preview of `gclean tui` using `defang.MkEmail`-built addresses — the demo doubles as a structural-reverse document of `storage.SenderSafety`.                                                                                                                                                                                                                                      |
| `dev.go` (242 lines)       | `gclean dev` subcommand for develop-mode. Three flags: `--fixtures PATH`, `--watch BOOL`, `--interval DURATION`. Polling-based watcher; traps SIGINT/SIGTERM via `context.WithCancel`; tracks both fixture AND config mtime with state-transition logging; auto-create of config is absorbed by pre-setting `lastConfigMtime`.                                                                           |
| `cli_test.go` (416 lines)  | Integration tests: drives the full pipeline end-to-end against fixtures, with substrings asserting rendered output. **6 tests**: `TestBuild_Help`, `TestScanCommand_DevFixturePipeline`, `TestCleanCommand_RefusesWithoutYes`, `TestDemoCommand_RendersExpectedOutput`, `TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`, `TestDevCommand_OneShotMode_RendersPipeline`.                 |

### Naming inside `cli.go` and `cli/dev.go`

- `newXxxCmd(out, errOut io.Writer) *cobra.Command` — constructor pattern; every subcommand gets one such function. The signature lets tests inject `bytes.Buffer` instead of real streams.
- Each constructor wires flags inside its scope. State-changing subcommands declare `--yes bool`. `gclean dev` declares `--fixtures` / `--watch` / `--interval`.

## internal/config/ — YAML I/O

| File                    | Purpose                                                                                                                                                                                               |
| ----------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `config.go` (120 lines) | `Document` struct, `DefaultPath()` (env-aware XDG fallback), `Load()` (auto-creates `defaultConfig` on first run; idempotent), `Compile()` (calls `engine.ParseRule` for every delete/archive entry). |
| `yaml.go`               | Thin wrapper around `gopkg.in/yaml.v3` — single place to swap libraries later.                                                                                                                        |

## internal/engine/ — Pure Logic (no I/O)

| File                        | Purpose                                                                                                                                                                                                                                                                                      |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `classifier.go` (170 lines) | Junk classification by sender domain + headers + Gmail labels. Returns strongest `models.ReasonCode`.                                                                                                                                                                                        |
| `protector.go` (105 lines)  | §6 keep rules: starred/important/sent → replied → contacts → recent → whitelist.                                                                                                                                                                                                             |
| `planner.go` (151 lines)    | `Plan()` — the single function that decides what's deleted. The non-junk refusal lives here (`internal/engine/planner.go:99-107`).                                                                                                                                                           |
| `evaluator.go` (151 lines)  | DSL parser (`ParseRule`), matcher (`Rule.Matches`), units (`ParseDuration`, `ParseByteSize`).                                                                                                                                                                                                |
| `pipeline.go` (~75 lines)   | `Pipeline` + `Stage` interface — the scan→plan→trash seam. `ScanStages` (fetch→classify→upsert), `PlanStages` (planner + verdicts, no Gmail I/O), `ApplyStages` (the only Gmail-mutating stage: trash + undo cache). `engine.Gmailer` is the subset of `gmailclient.Client` the stages need. |
| `*_test.go` (3 files)       | Table-driven classifier/protector/planner tests (no I/O, no clocks outside fixtures).                                                                                                                                                                                                        |

## internal/gmailclient/ — Gmail Boundary

| File                   | Purpose                                                                                                                                                                                                                     |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `client.go` (31 lines) | `Client` interface (4 methods). Sole seam between gclean and Gmail.                                                                                                                                                         |
| `fake.go` (156 lines)  | `FakeClient` reads a JSON fixture file; `TrashMessages/EmptyTrash/Restore...` mutate in-memory state only. Plus `matchQuery` supporting `from:`, `subject:`, `label:`, `category:`, `has:` — fallback to subject substring. |
| `real.go` (51 lines)   | `RealClient` stub: every method returns `ErrNotImplemented`. Real OAuth + `google.golang.org/api/gmail/v1` land in the next session.                                                                                        |
| `fake_test.go`         | Query-matching tests for the fake's `matchQuery` slice of Gmail query grammar.                                                                                                                                              |

## internal/models/

| File                    | Purpose                                                                                                                                                                                              |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `models.go` (132 lines) | All cross-package types: `Message`, `Sender`, `Classified`, `Verdict` (Keep/Delete/Archive/Protected iota), `Decision`, `StatsReport`, `SenderVolume`, `DryRunReport`. Reason code string constants. |

## internal/storage/ — SQLite + Aggregations

| File                         | Purpose                                                                                                                                                                                                                                                                         |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `sqlite.go` (287 lines)      | Schema (`messages` table + 4 indexes), CRUD (`Upsert`, `SetVerdict`, `AllClassified`, `MarkTrashed`, `RestoreTrashed`, `CountAll`, `DeleteMessageIDs`).                                                                                                                         |
| `stats.go` (~197 lines)      | `Aggregations()` — single full-table scan producing the §5 `StatsReport`, the per-sender `BySender` ranking, and the per-sender `SendersSafe` split. Also `LargestAttachments`. Replaced the old `Aggregate`/`PotentialReclaimOf`/`SendersByVolume` trio (CONCERNS.md #15/#16). |
| `sendersafety.go` (72 lines) | `SenderSafety` struct + `sortByBytesDesc`/`sortByDeleteBytesDesc` helpers. Consumed via `Aggregations().SendersSafe`; used by `gclean sender` and the TUI.                                                                                                                      |
| `undocache.go` (new)         | Undo-cache IO: `SaveUndoCache`/`LoadUndoCache` with SHA-256 `checksum` integrity. Written by the engine Apply stage, read by `gclean undo`.                                                                                                                                     |

## internal/tui/ — Bubble Tea UI

| File                      | Purpose                                                                                                                                                                         |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `app.go` (263 lines)      | Model + Update + View. Reads `[]storage.SenderSafety`, manages a checkbox list, optionally persists selection to `~/.config/gclean/tui-selection.json` via `cli.saveSelection`. |
| `app_test.go` (293 lines) | TUI model tests (state transitions, selection persistence).                                                                                                                     |

## testdata/fixtures/

| File                 | Purpose                                                                                                                                                                                                                                                                                                                                                                 |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `messages.json`      | 40-message Gmail-shaped corpus. As of 2026-07-08 the `sender.email` and `List-Unsubscribe` mailto fields are real `local@domain` literals (≈30 distinct senders), paired with each row's `name` field and aligned with the classifier's vendor table. Locked by `TestMessagesJSON_HasNoPlaceholder` (>=30 distinct senders + bytes.Contains `[email protected]` check). |
| `messages.README.md` | Sibling markdown documenting the fixture's structure, sender/name mapping, the Cloudflare obfuscation defense (rebuild via `defang.MkEmail`-style runtime-join), and structural constraints the fixture must satisfy. JSON has no comment syntax so a sibling file is the cheapest ground-truth doc.                                                                    |

## scripts/

| File                     | Purpose                                                                                                                                                                                                         |
| ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `lint-email-literals.sh` | Rejects raw `local@domain.tld` literals in non-test `*.go` and `*.json` (excludes `testdata/`, vendor, `.git/`, `.plans`, and lines starting with `//`). Wired into Makefile, justfile, pre-commit-config.yaml. |

## .plans/

| File                 | Purpose                                                                                                                                                                       |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `implement-notes.md` | Dated log of design decisions, findings, and resolutions. Currently includes the synthetic-fixture sender test, the dev command, and the obfuscation-defense pattern entries. |

## .planning/codebase/ — this directory

Files written by the codemap skill (`STACK.md`, `INTEGRATIONS.md`, `ARCHITECTURE.md`, `STRUCTURE.md`, `CONVENTIONS.md`, `TESTING.md`, `CONCERNS.md`). **Refreshed** from scratch on this turn to capture `gclean dev`, the synthetic-fixture patterns, and the new tests.

## Naming Conventions

| Style                                                                                                                                                                                                                       | Where                        |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| `Cobra` subcommand constructor `newXxxCmd(out, errOut io.Writer) *cobra.Command`                                                                                                                                            | `internal/cli/*.go`          |
| `storage` method names mirror the column they read/affect (`Upsert`, `SetVerdict`, `AllClassified`, `Aggregations`, `LargestAttachments`, `MarkTrashed`, `RestoreTrashed`)                                                  | `internal/storage/*.go`      |
| `engine` reason codes are stable, exported string constants; add new codes by append, never reorder                                                                                                                         | `internal/models/models.go`  |
| Yaml field names match the struct field names (yaml.v3 defaults to lowercase keys)                                                                                                                                          | `internal/config/config.go`  |
| Reason codes are referenced as `models.ReasonXxx` etc., never as string literals in consumers                                                                                                                               | everywhere                   |
| Reason codes flowing from `Plan()` come back as `Decision.Reasons []string` with prefixes `ignored_domain`, `protect:`, `config_keep:`, `config_archive:`, `config_delete:`, `delete_rule_refused_non_junk`, `default_keep` | `internal/engine/planner.go` |
| Test names follow `TestX_DoesY_WhenZ` or `TestX_SubjectsY` (e.g. `TestDevCommand_OneShotMode_RendersPipeline`, `TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`)                                           | `internal/cli/cli_test.go`   |

## Where To Add Things

| Need to add…                                                           | Where                                                                                                                                                                          |
| ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| A new CLI subcommand                                                   | new file in `internal/cli/`, register it in `Build()`'s `AddCommand` list (`internal/cli/cli.go:50`), **extend `TestBuild_Help`'s substring list** so a future drop is caught. |
| A new fake-only sample row for demo purposes                           | `internal/cli/demo.go` (constructed at runtime via `defang.MkEmail`).                                                                                                          |
| A new pure-logic check (classification, protection, planner edge case) | new table-driven test in `internal/engine/<file>_test.go`; helpers must not depend on `time.Now` directly — accept a clock or use a fixture date.                              |
| A new SQL aggregation                                                  | new method on `*storage.Store` in `internal/storage/stats.go`.                                                                                                                 |
| A new DSL predicate                                                    | add a case in `engine.matchPredicate` (`internal/engine/evaluator.go`); document in `AGENTS.md` Config section.                                                                |
| A new reason code                                                      | append a constant to `internal/models/models.go`; the comment there says "Add new codes by appending; never reorder."                                                          |
| A new file to watch in `gclean dev`                                    | extend the polling loop in `internal/cli/dev.go`; follow the existing `lastXxxMtime + xxxSeen + wasXxxMissing` state triple.                                                   |
