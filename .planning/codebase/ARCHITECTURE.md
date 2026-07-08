# ARCHITECTURE

System design: how pieces fit together, why the seams are where they are, and how data flows from Gmail → display → Trash.

## High-Level Pattern

**Thin Cobra CLI over pure-functional engine, with persistent metadata store.**

- `internal/cli` is **handlers only** — opens sqlite, calls engine/storage, formats output. The safety-critical logic is **deliberately not** here.
- `internal/engine` is **pure & deterministic**: classifier, protector, evaluator (rule DSL), planner. No I/O, no clocks beyond `time.Now()` when explicitly passed in for `recent_days` (`internal/engine/protector.go:88`). Every byte here can be exercised against fixtures.
- `internal/storage` owns SQLite + statistics aggregations + sender-safety rollup.
- `internal/gmailclient` is the Gmail interface seam — every command depends on `gmailclient.Client`, never directly on `RealClient` or `FakeClient`. Swap implementations at wiring time (`resolveClient` at `internal/cli/cli.go:64`).
- `internal/config` owns YAML I/O; `engine.RuleConfig` is the post-parse form used by the planner.
- `internal/models` is the universal vocabulary (Message, Classified, Decision, Verdict, StatsReport, DryRunReport).
- `internal/tui` is the experimental Bubble Tea UI on top of `storage.SenderSafety`.

## Safety-Critical Seam

`engine.Plan` (`internal/engine/planner.go:69`) is the single function that decides what gets deleted. Per-message order of operations:

1. Sender domain matches `ignore:` list → `VerdictProtected` (`internal/engine/planner.go:86`).
2. `Protect()` returns a reason → `VerdictProtected` (line 94).
3. `Config.Keep` rule matches → `VerdictKeep` (line 104).
4. `Config.Archive` rule matches → `VerdictArchive` (line 113).
5. `Config.Delete` rule matches **AND** classified as junk → `VerdictDelete` (line 122). **If a delete rule matches but the message is not junk, the planner refuses → `VerdictKeep` with reason `delete_rule_refused_non_junk`** (PRD §15; see `internal/engine/planner.go:123-130`).
6. Default → `VerdictKeep` (line 145).

This is the load-bearing safety invariant.

## Data Flow: Scan Pipeline

```
$ gclean scan --fixtures testdata/fixtures/messages.json
                                            │
                  ┌─────────────────────────┴───────────────────────┐
                  │  cli.newScanCmd (internal/cli/cli.go:216)        │
                  │  resolveClient(...) ...                         │
                  └─────────────────────────┬───────────────────────┘
                                            │
            ┌───────────────────────────────┴───────────────────────────────┐
            │  FakeClient.ListMessages("")   (internal/gmailclient/fake.go)  │
            │   → parses JSON fixture into []*models.Message                │
            └───────────────────────────────┬───────────────────────────────┘
                                            │
            ┌───────────────────────────────┴───────────────────────────────┐
            │  engine.Classify(m)  (internal/engine/classifier.go:24)       │
            │   Returns models.Classified{Message,IsJunk,ReasonCode}       │
            └───────────────────────────────┬───────────────────────────────┘
                                            │
            ┌───────────────────────────────┴───────────────────────────────┐
            │  storage.Store.Upsert(StoredMessage{...})                    │
            │    (internal/storage/sqlite.go:65)                           │
            └──────────────────────────────────────────────────────────────┘
```

`engine.Classify` priority (`internal/engine/classifier.go:24-86`):

1. From-local `noreply`/`no-reply` prefix (checked BEFORE vendor match — so `noreply@github.com` tags as `noreply`, not `github_notification`).
2. Known vendor domains (github, stripe, aws, azure, gitlab, atlassian/jira, slack, social networks).
3. RFC822 header signals (`List-Unsubscribe`, `List-ID`, `Precedence:bulk/list/junk`, `Auto-Submitted`).
4. Gmail category labels (`CATEGORY_PROMOTIONS`, `_SOCIAL`, `_UPDATES`, `_FORUMS`).

## Data Flow: Dry-Run / Clean Pipeline

```
$ gclean dry-run     (or `gclean clean --yes --fixtures X`)
        │
        ├─ storage.Open
        ├─ config.Load + doc.CompileFull  → engine.RuleConfig + engine.KeepConfig
        ├─ engine.Pipeline{Store,Client,Keep,Rules,CachePath}
        ├─ Pipeline.Run(Pipeline.PlanStages()...)   # load → Plan → SetVerdict (no Gmail I/O)
        │      → []models.Decision + models.DryRunReport
        │
        └─ If clean (not dry-run): Pipeline.Run(Pipeline.ApplyStages()...)
                ├─ collect VerdictDelete IDs and StoredMessage records
                ├─ client.TrashMessages(ids)   ← only network call
                ├─ storage.MarkTrashed(ids)
                └─ storage.SaveUndoCache(records) → ~/.config/gclean/undo-cache.json
```

The plan/apply split lives in `engine.Pipeline` (`internal/engine/pipeline.go`): `PlanStages` sets verdicts but performs no Gmail I/O; `ApplyStages` is the only Gmail-mutating stage (trash + undo cache). The CLI handlers in `internal/cli/pipeline.go` are thin adapters that run the right stage slice.

`gclean undo` (`internal/cli/pipeline.go`) reads `~/.config/gclean/undo-cache.json` via `storage.LoadUndoCache`, calls `client.RestoreFromTrash(ids)`, then `storage.RestoreTrashed(records)` — best-effort because Gmail's 30-day window is server-side.

## Configuration Loading Lifecycle

`config.Load()` (`internal/config/config.go:65`):

1. Resolve path via `DefaultPath()` (env var → XDG → `~/.config`).
2. If the file doesn't exist, write `defaultConfig` and continue (idempotent — does not re-stat).
3. Read + `yaml.Unmarshal` into `Document` (`internal/config/yaml.go:3`).
4. Caller `.Compile()` converts `Document.Delete[]string` and `Document.Archive[]string` into `[]engine.Rule` via `engine.ParseRule`. `Document.Keep` stays as `engine.KeepConfig` (it does not go through the DSL — it's a struct that the planner reads directly).

## `gclean dev` Watch-Mode State Machine

`internal/cli/dev.go:97-180`. Two files (fixture + config) watched on a polling loop. State variables per file:

```
lastFixtureMtime   configSeen bool   // overall: have we ever gotten a valid mtime?
lastConfigMtime    fixtureSeen bool
wasFixtureMissing  wasConfigMissing bool
```

Iteration triggers on `fixtureChanged || configChanged`.

### Key invariants baked in:

- **Missing fixture is NON-FATAL** — log once on the present→missing transition (`"fixture missing; will keep polling; recreate it to resume"`), keep polling, log again on reappearance. Contributors can recreate the file without restarting `gclean dev`.
- **Auto-created config is NOT treated as a user change** — `config.Load()` writes the config file during the first iteration's `dry-run`. The watch loop pre-sets `lastConfigMtime` to the current mtime on the first valid sight, so the new mtime on the next tick is recognized as the pre-existing baseline (not a new change).
- **Config is OPTIONAL** — if the file is missing at startup, the first iteration's `dry-run` creates it. The auto-create is absorbed because `lastConfigMtime` is already pre-set.
- **Deleted-then-recreated config still triggers a re-run** — when the file goes missing, `lastConfigMtime` resets to zero; when it reappears, the change check fires (because `configSeen` is already true and we DON'T re-pre-set).
- **Single iteration failure doesn't abort watch mode** — logged at `errOut` and the next tick tries again.
- **SIGINT/SIGTERM cleanly cancels** — `context.WithCancel` + `signal.Notify` handler; no hard `os.Exit`.

## Client Wiring Rule

`cli.resolveClient` (`internal/cli/cli.go:64`):

- If `--fixtures <path>` is non-empty → `NewFakeClient(path)`.
- Else → `NewRealClient(credsPath)`. The real client still returns `ErrNotImplemented` from every method, so until OAuth lands only `--fixtures` is functional end-to-end.

`login` only checks `credentials.json` exists (`internal/cli/cli.go:184`); it does not run a browser flow.

## Cobra Command Tree

`cli.Build(stdout, stderr)` (`internal/cli/cli.go:37`) returns a root command with 17 subcommands registered via `AddCommand`:

`login, logout, scan, stats, dry-run, clean, purge, undo, rules, config, sender, attachments, newsletters, receipts, tui, demo, dev`.

- Every subcommand takes `out, errOut io.Writer` (test buffers) — see `cli.Build(nil, nil)` from `cmd/gclean/main.go:15`.
- Every state-changing subcommand (`clean`, `purge`) requires `--yes`. `gclean dev` requires no `--yes` because it doesn't change state in Gmail — but in CI/tests `--watch=false` makes it deterministic.
- `gclean tui` mentions "EXPERIMENTAL" in both `Use`/`Short/Long` strings so the contract is honest (`internal/cli/meta.go` `newTuiCmd`).

## Dry-Run Output Shape (per PRD §5)

`dry-run` prints (`internal/cli/pipeline.go` `newDryRunCmd`):

```
──────────────────────────
Safe to delete    N messages
Recover           HumanBytes
Will keep         N (contacts, starred, important, replied, recent, ignored)
Will archive      N
──────────────────────────
Nothing changes.

Sample deletes: (first 10)
Top delete senders: (top 10, by count)
Recover by reason: (top 10)
```

## Why Storage Holds Verdicts, Not Engine

Verdicts are written to `messages.verdict` (`internal/storage/sqlite.go:23`) by both dry-run and clean. This means:

- The `gclean sender` query filters by `verdict = ?` to find delete-eligible senders.
- Re-planning after a config edit re-reads `verdict_reasons`, but the verdict itself is recomputed each dry-run/clean.
- `RestoreTrashed` upserts full `StoredMessage` records back, preserving `verdict` if present.

## Test Doubles and Other Adapters

- `FakeClient`: full I/O stand-in (`internal/gmailclient/fake.go`).
- `RealClient`: pass-through stub that always errors (`internal/gmailclient/real.go`).
- `NewFakeClientFromMessages` (`internal/gmailclient/fake.go:35`): in-memory variant for unit tests that don't want to parse a file.
- `defang.MkEmail(local, domain)` (`internal/defang/defang.go`): not a test double, but the project's only sanctioned way to assemble an `@`-bearing string in source — defeats the Cloudflare email-obfuscation source-pass that has previously rewritten literals into `[email protected]` placeholders. Used by `gclean demo`, every CLI integration test fixture, and any production loader option.

## Layering Rules (informal)

| May import    | `models` | `engine` | `storage` | `gmailclient` | `config` | `tui` | `cli` |
| ------------- | :------: | :------: | :-------: | :-----------: | :------: | :---: | :---: |
| `cmd/gclean`  |    –     |    –     |     –     |       –       |    –     |   –   |   ✓   |
| `cli`         |    ✓     |    ✓     |     ✓     |       ✓       |    ✓     |   ✓   | self  |
| `tui`         |    ✓     |    –     |     ✓     |       –       |    –     |   –   |   –   |
| `config`      |    –     |    ✓     |     –     |       –       |   self   |   –   |   –   |
| `storage`     |    ✓     |    –     |   self    |       –       |    –     |   –   |   –   |
| `gmailclient` |    ✓     |    –     |     –     |     self      |    –     |   –   |   –   |
| `engine`      |    ✓     |   self   |     ✓     |       –       |    –     |   –   |   –   |
| `models`      |   self   |    –     |     –     |       –       |    –     |   –   |   –   |

`engine`'s classifier/protector/planner/evaluator stay pure (in-memory slices, no I/O). The `engine.Pipeline` stages legitimately import `storage` and depend on `engine.Gmailer` (a subset of `gmailclient.Client`), so engine never imports `gmailclient` directly. `engine` does NOT import `config` or `cli`.
`cli` is the only package that imports everything else.
