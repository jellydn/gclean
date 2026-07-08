# CONCERNS

Known limitations, technical debt, fragile areas, security concerns, and performance hotspots that future contributors should be aware of before making changes.

## Security

### 1. ~~The `@`-Obfuscation Defense Is Load-Bearing~~ → RESOLVED

**Resolution**: The bundled `testdata/fixtures/messages.json` is confirmed to hold valid, non-obfuscated `sender.email` values, and the lint defense (`scripts/lint-email-literals.sh`) plus the `testdata/`-excluded scope keep it that way. The runtime assembly via `engine.MkEmail` remains the load-bearing defense for code.

**Location**: `internal/engine/testutil.go:17`, `scripts/lint-email-literals.sh`

**What's at risk**: Cloudflare's email-obfuscation source-pass silently rewrites any literal matching `local@domain.tld` into the placeholder `[email protected]` (the `@` is removed). This breaks `extractDomain` (`internal/engine/classifier.go:111`), `matchQuery` substring lookups (`internal/gmailclient/fake.go:96`), `storage.SendersByVolume` SQL aggregation (`internal/storage/sendersafety.go:24`), and any other `@`-dependent pattern.

**Status**: The defense (assemble at runtime via `engine.MkEmail`) is enforced by `scripts/lint-email-literals.sh`, wired into `just lint`, `make lint-emails`, and `.pre-commit-config.yaml`. Bypass path: a developer who skips the lint hook before commit. `testdata/fixtures/messages.json` is excluded from the lint by design (fixtures may carry literals), and the bundled fixture currently holds valid, non-obfuscated `sender.email` values — it is consumed directly by `TestScanCommand_DevFixturePipeline`, which passes. Synthetic-fixture tests (`TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`, `TestDevCommand_OneShotMode_RendersPipeline`) additionally exercise the engine independent of the bundled file.

**Mitigation when adding code**: Build `Sender.Email` and any other `@`-bearing string with `engine.MkEmail(local, domain)` at runtime. If you absolutely must add a literal (fixture JSON in `testdata/` is OK), put it in `testdata/`. The lint excludes that directory.

### 2. RealClient Footgun: Credentials Without Implementation

**Location**: `internal/gmailclient/real.go:24` (`NewRealClient`), `:35` (`ErrNotImplemented`)

**What's at risk**: `RealClient` validates that `credentials.json` exists at construction time but the construction is a no-op stat. Every method returns `ErrNotImplemented`. A future contributor wiring OAuth + `google.golang.org/api/gmail/v1` MUST swap `ErrNotImplemented` for real HTTP calls — leaving them in place silently returns nil-equivalent errors that won't fail loudly in QA until the caller's downstream logic reads `nil` for a list that should have been populated. Mark all stubs with TODO and a session reference (currently "// OAuth dance ... in the next session").

### 3. `--yes` Gate Required for State Changes (Safe-by-Design)

**Location**: `internal/cli/pipeline.go:309` (`clean`), `:344` (`purge`)

**What's at risk**: A future contributor who adds another state-changing subcommand (`flush`, `wipe`, etc.) MUST remember the `--yes` boolean flag + the error-on-missing-yes discrimination. There's no compile-time check that a state-changing cmd has this guard. Mitigation: grep for `--yes` in `internal/cli/cli.go` whenever adding subcommands to scan-build.

`gclean dev` is **not** state-changing in Gmail, so it doesn't have `--yes`. It does change local SQLite (`--fixtures` writes), but only to the tempdir that the user passed (or the default `~/.config/gclean/gclean.db`). Reasonable defaults.

### 4. Undo Cache Has No Integrity Check

**Location**: `internal/cli/pipeline.go:437` (`saveTrashedForUndo`), `:445` (`loadTrashedForUndo`)

**What's at risk**: `~/.config/gclean/undo-cache.json` is a JSON file holding the full pre-trash records in plaintext (mode `0o600`, but still on disk). If the user chmods the file or suffers a partial-write crash, restoring from a corrupted undo cache could re-upsert strange rows.

**Mitigation (implemented)**: `saveTrashedForUndo` now writes a `version` field plus a SHA-256 `checksum` over the records, and `loadTrashedForUndo` rejects a version/checksum mismatch — so a corrupt or partially-written cache is refused rather than re-inserted. Legacy caches written without a checksum are still accepted for backward compatibility.

### 5. People-API Enrichment Is Missing (Future Privacy Decision)

**Location**: `internal/gmailclient/client.go` — no `IsContact` enrichment exists yet.

**What's at risk**: `models.Sender.IsContact` is read by `Protect` (`internal/engine/protector.go:49`), but no Client implementation actually populates it for fixture data. Every fixture row's `IsContact` defaults to `false`. Future `RealClient` will need to call the People API for batch enrichment on the scan step — that's a privacy-sensitive decision (Google sees which contacts you email). PRD §15 calls this out but it isn't yet wired.

## Tech Debt

### 6. ~~Single-Megabyte `internal/cli/cli.go` (842 lines)~~ → RESOLVED 2026-07-08

**Resolution**: Split into 4 per-domain files + a slim `cli.go`:

- `cli.go` (126 lines): Build + AddCommand wiring + cross-cutting helpers (`resolveClient`, `storePath`, `credentialsPath`, `humanBytes`).
- `auth.go` (70 lines): login/logout.
- `pipeline.go` (458 lines): scan/stats/dry-run/clean/purge/undo + shared core + undo-cache io.
- `insights.go` (165 lines): sender/attachments/newsletters/receipts + tui-selection.io.
- `meta.go` (120 lines): rules/config/tui + helpers.

Largest file is now `pipeline.go` at 458 lines (well below the 842-line offender). Zero behavior change: every existing test passes unchanged. The split-refactor entry is documented in `.plans/implement-notes.md` (2026-07-08 — cli.go split).

**Why this category stays useful**: future contributors can grep on a subcommand group (e.g. `grep -l loginCmd internal/cli/`) without scanning 842 lines of unrelated logic — that improvement matters more than the line-count reduction.

### 7. `tui-selection.json` → `clean` Wiring Is Not Connected

**Location**: `internal/cli/meta.go:114` (`newTuiCmd`) and `internal/cli/insights.go:31` (`saveSelection`).

**What's at risk**: `gclean tui` writes `~/.config/gclean/tui-selection.json` but `gclean clean` doesn't read it. So a user who selects senders in the TUI can not yet act on the selection through the pipeline — they're meant to run `gclean clean --from-tui-selection` (not yet implemented). PRD §12 is pending. Documented in README "next session" list.

### 8. Defaults From `defaultConfig` Are Reference, Not Edited

**Location**: `internal/config/config.go:13`.

**What's at risk**: The auto-created config is meant to be an opinionated starting point. There's no `gclean rules --edit` or `gclean config edit`. Users must edit `~/.config/gclean/config.yaml` in `$EDITOR`. PRD §16 long-horizon.

### 9. No Migration Table in SQLite

**Location**: `internal/storage/sqlite.go:20` (`CREATE TABLE`).

**What's at risk**: The schema is `CREATE TABLE IF NOT EXISTS` and goes straight to current shape. Any column rename (e.g. renaming `verdict_reasons` to `verdict_notes`) breaks existing dbs on relaunch. Greenfield today, but the moment gclean ships to users this is a real risk. Mitigation when shipping: add `_migrations` table + apply `migrations[]` on `Open`.

### 10. `--fixtures` Path Argument Untested for Traversal / Symlinks

**Location**: `internal/gmailclient/fake.go:28` (`NewFakeClient`).

**What's at risk**: `os.Open` will follow symlinks and won't restrict to a specific directory tree. Practically low-risk for dev/test mode but worth noting if `--fixtures` ever widens to a remote URL (it doesn't, by design).

**Mitigation (implemented)**: `NewFakeClient` now `Lstat`s the path and rejects symlinks and non-regular files before opening, so `--fixtures` cannot be pointed at an arbitrary symlinked target.

## Fragility

### 11. Decisions Sort By Size, Stable

**Location**: `internal/engine/planner.go:129` (`sort.SliceStable`).

**What's at risk**: Decisions are sorted `sort.SliceStable` by `Message.Size DESC` after construction — this is for human display consistency, not for correctness. A future contributor who adds another post-decision step (e.g. persistence) might mistakenly assume Decisions come out in scan order. The sort is **explicit and stable**, but only one place in the codebase relies on it.

### 12. Planner Reason Order Is Not Total

**Location**: `internal/engine/planner.go:52` (`Plan`).

**What's at risk**: The first-match-wins branching in `Plan` is fine if `Protect()` returns a stable verdict (it does — see `internal/engine/protector.go`), but if a future contributor adds another layer before `Protect()` (e.g. tenant-wide blacklist), forgetting to insert it before `Protect` short-circuits could bypass protections. Always add safety-priority check ordering comments when adding planner steps.

### 13. `gclean dev` Watch State Has Multiple Correctness Traps

**Location**: `internal/cli/dev.go:212` (`runDevIteration`).

**What's at risk**: The watch loop has three subtle invariants (auto-create of config, missing-fixture being non-fatal, deleted-then-recreated config still triggering change). The current implementation handles all three, but a future contributor who adds another watched file MUST follow the same pattern (`xxxSeen` state + `wasXxxMissing` state + pre-set on first sight for files with auto-create side effects). Review carefully when adding new watch targets.

### 14. ~~AGENTS.md Documents `--fixtures` Contradiction~~ → RESOLVED

**Resolution**: `AGENTS.md` documents the `gclean scan --fixtures PATH` flow and `just test-integration` points at `TestScanCommand_DevFixturePipeline` (`cli_test.go`), which exists and passes. Keep `just test-integration` and `AGENTS.md` in sync if the integration test is renamed.

## Performance

### 15. `Aggregate()` Reads All Rows Into Memory

**Location**: `internal/storage/stats.go:10` (`Aggregate`).

**What's at risk**: For a real Gmail account with 100k+ messages, `SELECT` all rows materializes in Go before producing the report. Acceptable for the scaffold (fixture is 40 rows) but will need pagination and incremental aggregation once real Gmail is wired.

### 16. `SendersByVolume` Hard-Limits 200 Senders

**Location**: `internal/storage/stats.go:89` (`SendersByVolume`, `LIMIT 200`).

**What's at risk**: For a real Gmail account with thousands of distinct senders, the top-200 may be dominated by truly bulk senders (noreply@github.com variants) and miss smaller but still-impactful ones. The limit is a deliberate scaffold choice — bumping it has no correctness cost; revisit once `tui` is wired up to act on the rollup.

### 17. No Prepared Statement Caching

**Location**: `internal/storage/sqlite.go` — every `Exec`/`Query` re-prepares the SQL.

**What's at risk**: Plenty of headroom for a 40-row fixture. For real Gmail scale (e.g. 100k rows per scan), prepared statements cached on the Store would matter. SQL driver's `Prepare` + reuse pattern. Not urgent for the scaffold; flagged for the OAuth session.

### 18. `gclean dev` Default Polling Interval Is 2s (User-Tunable)

**Location**: `internal/cli/dev.go:62` (`2*time.Second` default, `--interval` overrides).

**What's at risk**: For a developer on a 5-second iteration loop, 2s is fine. For someone running on battery in a low-power laptop, 2s might be excessive. The default is documented in `--help` text and overridable, so this is a UX note not a bug.

## Bug Surface Notable

### 19. Restore-Trashed Roundtrip Lacks Garbage Collection

**Location**: `internal/storage/sqlite.go:200` (`RestoreTrashed`).

**What's at risk**: Restoring trashed records is straightforward. There is no expiry window — undo cache lives until purged (`gclean purge` deletes it). If the user trashes 100k messages, the cache file becomes 100k JSON. Mitigation: cap with `recent_days` from the keep config.

### 20. `gclean dev` Polling Anchors To mtime, Not Content

**Location**: `internal/cli/dev.go`.

**What's at risk**: A contributor who edits the fixture and saves it back with the same mtime (cp + touch + edit) misses the change because we compare mtime only. Mitigation: also compare file size, or hash a small prefix. Not urgent — manual contributors will notice by lack of re-render.

## External / Pending

### 21. Real OAuth Flow

**Status**: scaffolded. `cli.newLoginCmd` (`internal/cli/auth.go:19`) verifies `credentials.json` exists but does not start the OAuth flow; `gmailclient.Client` interface is the only place where the flow would materialize. PRD §13 deferred.

### 22. People-API Enrichment

**Status**: not wired. `models.Sender.IsContact` defaults to `false` for fixtures; no Client enriches it. Real Gmail would call People API per-batch; PRD §15.

### 23. Long-Term Roadmap Items (PRD §16)

Not yet implemented:

- OAuth browser flow (login)
- `google.golang.org/api/gmail/v1`-backed `RealClient`
- TUI → `clean` selection wiring
- LLM-assisted body classification (privacy default off; opt-in via `--ai-mode`)
- Gmail-supplied rule sync (parse configured filters)
- Multi-account support
- Per-message Google API rate-limit aware batcher
