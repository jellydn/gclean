# ARCHITECTURE

`gclean` is a safety-oriented CLI organised around a Gmail client seam, a pure decision engine, and a local metadata store.

## Layer model

```text
cmd/gclean
    ↓
internal/cli  ───────→ config
    ↓      ↓             ↓
engine ←──┴───────→ storage
    ↓                  ↑
models          gmailclient
                       ↑
                 FakeClient / RealClient

internal/tui reads storage.SenderSafety
```

- `cmd/gclean` owns process startup and top-level error presentation.
- `internal/cli` owns Cobra wiring, environment/path resolution, orchestration, and output formatting.
- `internal/config` owns YAML I/O and compiles user rules into engine types.
- `internal/engine` owns classification, protection, rule evaluation, planning, and the scan/plan/apply pipeline stages.
- `internal/storage` owns SQLite metadata, aggregations, and undo-cache serialization.
- `internal/gmailclient` owns the Gmail API boundary and its fake.
- `internal/models` contains the shared vocabulary.
- `internal/tui` is an experimental presentation layer over sender safety rows.

## Entry points

- Binary: `cmd/gclean/main.go:12-21`.
- Root command: `internal/cli/cli.go:37-78`.
- Client selection: `internal/cli/cli.go:82-88`; `--fixtures` selects `FakeClient`, otherwise `RealClient` is constructed.
- Command groups: `internal/cli/auth.go`, `pipeline.go`, `meta.go`, `insights.go`, `demo.go`, and `dev.go`.

## Scan flow

```text
scan --fixtures PATH
  → cli.newScanCmd
  → resolveClient
  → FakeClient.ListMessages / RealClient.ListMessages
  → engine.Pipeline.ScanStages
  → Classify each models.Message
  → storage.Store.Upsert
  → SQLite messages table
```

The scan stage is `engine.Pipeline.fetchAndClassify` (`internal/engine/pipeline.go:94-121`). The classifier prioritises noreply local parts, known vendor domains, RFC822 bulk/list headers, then Gmail category labels (`internal/engine/classifier.go:15-86`).

## Planning flow

```text
dry-run or clean
  → config.Load
  → Document.CompileFull
  → storage.AllClassified
  → engine.Plan
  → storage.SetVerdict
  → report rendered by CLI
```

`engine.Plan` is the safety-critical decision seam (`internal/engine/planner.go:40-145`). Its order is:

1. ignored sender domain → protected;
2. `Protect` result → protected;
3. keep rule → keep;
4. archive rule → archive;
5. delete rule only deletes classified junk;
6. otherwise keep.

A matching delete rule on a non-junk message produces `delete_rule_refused_non_junk`, preserving the project's central safety invariant.

## Apply flow

`clean --yes` runs `PlanStages()` followed by `ApplyStages()` (`internal/cli/pipeline.go:197-240`). `engine.Pipeline.applyTrash` collects delete decisions, calls `Client.TrashMessages`, removes the rows from SQLite, and writes an integrity-checked undo cache (`internal/engine/pipeline.go:143-190`). This is the only pipeline stage allowed to mutate Gmail.

The current real client still returns `ErrNotImplemented` for mutation. The fake client models the operation in memory, so fixture-driven local flows can exercise the command shape without network access.

`undo` loads the cache, calls `RestoreFromTrash`, restores records into SQLite, and removes the cache (`internal/cli/pipeline.go:273-315`). `purge` calls `EmptyTrash` and removes the cache, but the real operation is currently stubbed.

## Reporting flow

`storage.Store.Aggregations()` performs one full table scan and produces:

- `models.StatsReport` for `stats`;
- sender volume rows for `sender`;
- `SenderSafety` rows for the TUI;
- category/year, newsletter, notification, attachment, and reclaim rollups.

Read-only command handlers in `internal/cli/insights.go` and `pipeline.go` format these values with `text/tabwriter` and `humanBytes`.

## Development watcher

`gclean dev` (`internal/cli/dev.go`) invokes the real Cobra command path for `scan`, `stats`, and `dry-run`. In one-shot mode it runs once; in watch mode it polls fixture and config mtimes, handles missing files as recoverable states, and cancels on SIGINT/SIGTERM.

## Abstractions and invariants

- `gmailclient.Client` keeps network implementation replaceable.
- `engine.Gmailer` narrows that dependency for pipeline stages without importing the concrete Gmail package.
- `engine.Stage` is a function type; `Pipeline.Run` executes stages in order and stops on error.
- `models.Message` intentionally excludes bodies.
- `clean` and `purge` require explicit `--yes`.
- The planner refuses to delete non-junk messages even if a delete rule matches.
- Runtime email construction is centralised in `internal/defang` to protect source integrity.
