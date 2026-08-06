# CONCERNS

Current limitations, risks, and fragile areas. This document describes the repository as it exists now, not the older scaffold state.

## Highest-priority incomplete integration

### 1. Real Gmail cleanup still needs reconciliation hardening

- **Location:** `internal/gmailclient/real.go`, `internal/engine/pipeline.go:143-190`
- `RealClient` now implements retrying Trash and restore calls plus paginated Trash listing and 1,000-ID batch deletion for purge.
- The remaining risk is cross-system recovery: a partial Gmail mutation or process failure can occur before SQLite deletion and undo-cache persistence complete.
- Keep the fixture flow as the default local verification path while adding explicit reconciliation, partial-failure tests, and carefully controlled live-account validation.

### 2. OAuth callback server has a fixed port

- **Location:** `internal/gmailclient/oauth.go:20-29, 96-151`
- The callback listens on `localhost:8080`. A second login process or another local service can occupy that port.
- Desktop OAuth redirect semantics and the registered hostname are intentionally documented in the source; changing the host without updating the OAuth configuration can break login.
- A future implementation should consider selecting an available loopback port and passing the exact redirect URI through the OAuth config, with a regression test for the callback lifecycle.

## Safety and security

### 3. Undo cache is plaintext local metadata

- **Location:** `internal/storage/undocache.go`
- The cache is mode `0600` and has a SHA-256 integrity checksum, but it still stores sender, subject, headers, and message metadata in plaintext.
- The checksum detects corruption/tampering, not confidentiality or an attacker who can rewrite both records and checksum. Keep permissions, atomic writes, and path validation under review.

### 4. State-changing command coverage is incomplete

- **Location:** `internal/cli/pipeline.go:197-315`
- `clean` and `purge` require `--yes`, and the planner refuses non-junk deletes. However, there is no compile-time mechanism that forces a future mutating command to add the same gate.
- Add integration tests around the real Gmail mutation adapter, including partial failures, retries, local reconciliation, and repeated-command idempotency.

### 5. Contact protection is modeled but not enriched

- **Location:** `internal/models/models.go:28-34`, `internal/engine/protector.go:62-65`
- `Sender.IsContact` affects protection, but no People API or equivalent enrichment currently populates it for real scans. False negatives could reduce the keep cohort unless Gmail data already supplies the field.
- Adding contact enrichment is a privacy and API-scope decision, not merely a mechanical feature.

### 6. TUI selection is not connected to cleanup

- **Location:** `internal/cli/meta.go:114-158`, `internal/cli/insights.go:20-36`
- `gclean tui` writes selected senders to `tui-selection.json`, but `clean` does not consume that file. The UI is therefore advisory and cannot yet constrain the applied cohort.
- Do not imply that committing a TUI selection changes Gmail until this wiring is implemented and tested.

## Persistence and correctness

### 7. SQLite schema has no migration history

- **Location:** `internal/storage/sqlite.go:22-41`
- `storage.Open` applies an inline `CREATE TABLE IF NOT EXISTS` script with no schema version or migration table.
- Column changes, constraints, or index changes after release could leave existing databases incompatible. Introduce explicit migrations before distributing persistent databases broadly.

### 8. Store and cache operations are not atomic as a whole

- **Location:** `internal/engine/pipeline.go:143-190`
- Applying a clean performs Gmail mutation, SQLite deletion, and cache writing as separate operations. A process crash or API/storage failure between them can leave those systems out of sync.
- The cache warning is intentionally non-fatal, which favours continued cleanup but can weaken undo guarantees. Real Gmail mutation should define rollback/reconciliation behavior explicitly.

### 9. Planner uses current wall clock internally

- **Location:** `internal/engine/protector.go:79-83`, `internal/engine/evaluator.go:91-98`
- Recent-window and `older_than` decisions use `time.Now()`/`time.Since()`. Tests use wide date margins, but exact boundary behavior can vary between runs.
- Injecting a clock or evaluation time would improve determinism before adding boundary-sensitive features.

### 10. Decision ordering is presentation-driven

- **Location:** `internal/engine/planner.go:133-136`
- `Plan` sorts decisions by descending message size after calculating them. Consumers must not assume the output preserves scan/database order.
- Keep the sort documented or return an explicitly named presentation collection if additional callers appear.

## Performance and scale

### 11. Aggregations scan the entire messages table

- **Location:** `internal/storage/stats.go:23-135`
- `Aggregations()` reads every row to build stats, sender volume, and sender safety in one pass. This is a good locality trade-off for the fixture corpus, but a large mailbox will increase latency and memory use.
- Consider database-side aggregation, pagination/chunking, or incremental rollups when real Gmail sync is enabled.

### 12. Gmail metadata listing and mutation calls need quota-aware control

- **Location:** `internal/gmailclient/real.go:58-91`
- The API list response is followed by an individual metadata `Get` for every message. The code logs page and 100-message progress, but there is no rate-limit/backoff or batch request layer yet.
- Large accounts still need bounded concurrency or batching for metadata reads, cancellation, and quota-aware progress reporting; mutation calls now have bounded retries and purge batching.

### 13. Undo cache grows with the full cleanup cohort

- **Location:** `internal/storage/undocache.go`, `internal/engine/pipeline.go:153-186`
- Every trashed record is serialised to one JSON file. Large cleanups can create large writes and expensive restore loads.
- Consider chunking, a database-backed cache, or an explicit size/retention policy once real cleanup is enabled.

### 14. Development watcher is mtime-based

- **Location:** `internal/cli/dev.go:92-205`
- Watch mode detects fixture/config changes through modification times. A change that preserves mtime can be missed, and the loop is intentionally not covered by deterministic tests.
- The current state machine handles missing/reappearing files and config auto-creation; preserve those invariants when adding watched inputs.

## Resolved protections to preserve

### 15. Email-literal obfuscation defense

- **Location:** `internal/defang/defang.go`, `scripts/lint-email-literals.sh`
- Non-test Go/JSON source is linted for raw email literals; runtime construction uses `defang.MkEmail`.
- The current fixture check reports no `[email protected]` placeholder and 60 `@` characters. Do not weaken the lint scope or replace runtime assembly with literals.

### 16. Fixture path validation

- **Location:** `internal/gmailclient/fake.go:27-49`
- `NewFakeClient` uses `Lstat` and rejects symlinks and non-regular files before opening the fixture. Preserve this boundary if fixtures ever accept broader inputs.

### 17. Undo-cache integrity check

- **Location:** `internal/storage/undocache.go:49-82`
- Version and checksum validation rejects unsupported/corrupt cache content. Preserve the validation before restoring rows.

## Documentation synchronization

**Resolved in the current working tree.** `README.md` and `AGENTS.md` now document the implemented OAuth login flow, real Gmail metadata reads, retrying Trash/restore calls, paginated purge batching, and `GCLEAN_TOKEN_PATH`. `.planning/real-gmail-test-plan.md` also reflects the `localhost:8080` callback and classifier metadata headers fetched by `RealClient`.Keep these documents synchronized when local reconciliation, contact enrichment, or other pending integrations land.
 This map intentionally continues to track the remaining implementation concerns above rather than treating documentation updates as feature completion.
