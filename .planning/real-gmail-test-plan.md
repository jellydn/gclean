# Plan: Real-Gmail end-to-end testing for a12pct@gmail.com

## Goal

Get the **full safe-by-default pipeline** (`scan → stats → dry-run → clean → undo`) running against the user's real Gmail account (`a12pct@gmail.com`), then verify each stage's output and safety invariants against real data.

## Current state (verified against source)

| Stage                | Code path                                                                                                | Status                                                                                                                                                                                                                                                                |
| -------------------- | -------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gclean login`       | `cli/auth.go` → `gmailclient/oauth.go`                                                                   | **Implemented** — full OAuth2 browser flow, loopback callback server on `localhost:8080`, token saved to `~/.config/gclean/token.json` or `$GCLEAN_TOKEN_PATH`                                                                                                                                |
| `gclean scan` (real) | `cli/pipeline.go:newScanCmd` → `resolveClient` → `gmailclient.NewRealClient` → `RealClient.ListMessages` | **Implemented for metadata reads** — `ListMessages` paginates via `gmail.Users.Messages.List` and fetches `From/To/Cc/Subject/Date` plus `List-Unsubscribe/List-ID/Precedence/Auto-Submitted`; the remaining real-data gaps are contact/replied enrichment |
| `gclean stats`       | `cli/pipeline.go:newStatsCmd` → `storage.Store.Aggregations`                                             | **Implemented** — reads from local SQLite, no Gmail I/O                                                                                                                                                                                                               |
| `gclean dry-run`     | `cli/pipeline.go:newDryRunCmd` → `engine.Pipeline.PlanStages`                                            | **Implemented** — runs `engine.Plan` (offline, no Gmail I/O), enforces §15 non-junk protection                                                                                                                                                                        |
| `gclean clean`       | `cli/pipeline.go:newCleanCmd` → `engine.Pipeline.ApplyStages` → `RealClient.TrashMessages`               | **Implemented in adapter** — individual Trash calls retry transient Gmail errors; local reconciliation still needs end-to-end validation                                                                                                                                 |
| `gclean undo`        | `cli/pipeline.go:newUndoCmd` → `RealClient.RestoreFromTrash`                                             | **Implemented in adapter** — individual Untrash calls retry transient Gmail errors; local restore reconciliation still needs end-to-end validation                                                                                                                       |
| `gclean purge`       | `cli/pipeline.go:newPurgeCmd` → `RealClient.EmptyTrash`                                                  | **Implemented in adapter** — paginates `TRASH` and batch-deletes up to 1,000 IDs per request; destructive live validation remains pending                                                                                                                               |

### Two enrichment gaps that remain against real data

1. **`IsContact` never set** — `mapGmailMessage` (`internal/gmailclient/real.go`) hard-codes `IsContact: false`. The People API (`people.people.get` or `people.people.connections` batch-lookup) is not wired, so `Protect()`'s contacts rule (PRD §6) is a no-op.
2. **`REPLIED` label never produced** — `Protect()` checks for a `REPLIED` label (`protector.go`), but `ListMessages` does not fetch thread-level reply metadata. Without this, replied-to messages lose that protection signal.

### What already works correctly against real data

- Gmail `LabelIds` (including `STARRED`, `IMPORTANT`, `SENT`, `CATEGORY_PROMOTIONS`, etc.) flow through `m.LabelIds` → `Labels` → classifier/protector.
- Metadata headers required by the classifier (`List-Unsubscribe`, `List-ID`, `Precedence`, and `Auto-Submitted`) are requested by the real client.
- Pagination: `ListMessages` loops pages of 500, stops at `max` (0 = all).
- The §15 safety invariant (refuse to delete non-junk) is enforced in `engine.Plan` at `internal/engine/planner.go:79-83`.

### Risk: full-mailbox scan

`scan` passes query `""` (all messages). For a real Gmail account this paginates 500 at a time. This is fine for testing but should be noted — a production run on a multi-decade account may be slow.

## Phase 1 — Verify `scan` against real Gmail (read path)

**Goal**: Verify the implemented `gclean login` + `gclean scan` path against real Gmail and document any remaining metadata/enrichment gaps.

### 1a. OAuth credentials setup (no code change)

1. Go to https://console.cloud.google.com/
2. Create/select a project, enable the **Gmail API**
3. Create an **OAuth 2.0 Client ID** (Desktop app type)
4. Download `credentials.json`
5. Save to: `~/.config/gclean/credentials.json`
6. Add scopes — the code already requests `gmail.GmailReadonlyScope` + `gmail.GmailModifyScope` (`oauth.go:20`):

```go
oauthScopes = []string{gmail.GmailReadonlyScope, gmail.GmailModifyScope}
```

7. Run `gclean login` — browser opens, approve, token saved to `~/.config/gclean/token.json`

### 1b. Fix `ListMessages` to fetch classifier-required headers

`internal/gmailclient/real.go` now fetches:

```go
full, err := r.service.Users.Messages.Get("me", m.Id).Format("metadata").MetadataHeaders(
    "From", "To", "Cc", "Subject", "Date",
    "List-Unsubscribe", "List-ID", "Precedence", "Auto-Submitted",
).Do()
```

The four classifier headers are now present in the real read path, so the next real-Gmail test should verify their values are mapped into `models.Message.Headers` and influence classification as expected.

### 1c. (Optional, Phase 1 stretch) Enrich `IsContact` via People API

`mapGmailMessage` (`real.go:117`) sets `IsContact: false` unconditionally. The People API is already a `go.mod` dependency (`cloud.google.com/go/auth`, `google.golang.org/api`). Add a batch lookup:

```go
peopleSvc, _ := people.NewService(ctx, option.WithTokenSource(ts))
// batch-get contacts: people.connections.list, then map email→true
```

This can be **deferred to Phase 1.5** — without it, the `contacts: true` keep rule simply won't fire, and messages from contacts will rely on the other protect signals (starred, important, replied, recent_days). Safe but less protective.

## Phase 2 — Validate and harden the write path (trash + undo + purge)

**Goal**: Make `clean`, `undo`, and `purge` mutate real Gmail.

### 2a. Implement `TrashMessages`

`internal/gmailclient/real.go` now uses retrying `gmail.Users.Messages.Trash` calls (moves to Trash, recoverable). The API call is intentionally sequential so partial progress and the failing message remain deterministic; the adapter reports the `n/m` position in the returned error.

```go
func (r *RealClient) TrashMessages(ids []string) error {
    bs := r.service.NewBatchService()
    const batchSize = 100
    for i := 0; i < len(ids); i += batchSize {
        end := i + batchSize
        if end > len(ids) { end = len(ids) }
        batch := ids[i:end]
        reqs := make([]*gmail.Message, len(batch))
        for j, id := range batch {
            reqs[j] = &gmail.Message{Id: id}
        }
        // Actually: use Users.Messages.Trash per ID, or threads
    }
}
```

Actually, the Gmail API's batch endpoint is the right tool here. The `google-golang-api` library supports `service.NewBatchService().Add(...)`. But the simpler and more correct approach for moving to Trash is per-message `Users.Messages.Trash` with a small batched loop.

**File**: `internal/gmailclient/real.go:91-93`

### 2b. Implement `RestoreFromTrash`

`real.go` — uses `gmail.Users.Messages.Untrash` with the same transient-error retry policy.

**File**: `internal/gmailclient/real.go:97-99`

### 2c. Implement `EmptyTrash`

`real.go` — paginates `LabelIds("TRASH")` and calls `BatchDelete` in chunks of up to 1,000 IDs, with retries for transient failures.

**File**: `internal/gmailclient/real.go:101-103`

### 2d. Local reconciliation and live validation

The adapter can report partial progress, but `engine.Pipeline` still performs Gmail mutation, SQLite deletion, and undo-cache persistence as separate operations. Add reconciliation and failure-injection tests before broad live-account use. The existing 100ms/200ms retry backoff is intentionally conservative and should be revisited with observed quota behaviour.

## Phase 3 — End-to-end test against real data

### 3a. Verify read path (no Gmail mutation)

```bash
export GCLEAN_DB_PATH=$(mktemp -d)/gclean.db
gclean login                    # OAuth browser flow
gclean scan                     # fetch + classify + persist to SQLite
gclean stats                    # verify storage analytics
gclean dry-run                  # verify §15 safety invariants + delete cohort
```

**Check**: `dry-run` should show a non-zero "Safe to delete" count only after 1b (header enrichment). Before 1b, expect 0 deletes.

**Check**: Verify no `local@domain` literals were introduced in any new code — run `./scripts/lint-email-literals.sh` before every commit.

### 3b. Verify write path (Gmail mutation — **destructive**)

> **Safety first**: `clean` moves to Trash (recoverable). `purge` is permanent. Never run `purge` until `undo` has been verified.

```bash
gclean clean --yes              # moves delete cohort to Trash
gclean undo                     # restores from Trash via undo cache
gclean purge --yes              # empties Trash (permanent)
```

**Check after `clean`**: `gclean stats` should show reduced estimated storage; "Potential reclaim" should decrease.

**Check after `undo`**: Run `gclean scan` again — the untrashed messages should reappear.

**Check after `purge`**: Only run this after verifying `clean` + `undo` both work correctly.

## Phase 4 — Edge cases & verification

### 4a. Large mailbox

If `a12pct@gmail.com` has thousands of messages, `scan` with no query fetches all. Consider adding a `--query` flag to `scan` to narrow (e.g., `scan --query "label:inbox"`), but this is a UX improvement, not required for testing.

### 4b. Gmail API quota

Gmail imposes per-second and per-day quotas. The `google-golang-api` library has built-in retry with exponential backoff for 429/5xx. Verify behavior under load with a large mailbox.

### 4c. People API enrichment (if implemented in 1b/1c)

If `IsContact` enrichment is added, verify that protected contacts don't appear in the delete cohort. Test with a contact who also sends noreply-style automated mail.

## Verification checklist

- [ ] `./scripts/lint-email-literals.sh` passes (no raw `local@domain` in non-test `.go`/`.json`)
- [ ] `go vet ./...` clean
- [ ] `go build ./...` clean
- [ ] `go test ./...` — all existing tests still pass
- [ ] `gclean login` succeeds against `a12pct@gmail.com`
- [ ] `gclean scan` (no `--fixtures`) pulls real messages from Gmail
- [ ] `gclean stats` shows real Gmail analytics
- [ ] `gclean dry-run` shows a non-zero delete cohort
- [ ] `gclean clean --yes` moves junk to Trash on the real account
- [ ] `gclean undo` restores the trashed messages
- [ ] `gclean purge --yes` empties Trash permanently (only after undo verified)
