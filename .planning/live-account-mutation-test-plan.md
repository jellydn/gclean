# Live-Account Integration Test Plan: Real Gmail Mutation Path

Runbook for exercising `gclean clean` → `gclean undo` → `gclean purge` against a **real**
Gmail account, verifying the retrying mutation adapter, the undo-cache reconciliation
layer, and the safety invariants end to end. Companion to
[`real-gmail-test-plan.md`](real-gmail-test-plan.md), which covers the read path and
overall pipeline; this plan drills into the mutation path only.

> **Destructive**: `clean` and `undo` are recoverable (Trash), `purge` is permanent
> and cannot be undone by Gmail's server-side Undo. Run this against a **dedicated test
> account** first. On the primary account, back up with Google Takeout before `purge`.

## Implementation under test (what the adapter actually does)

| Operation | CLI path | Adapter behavior (`internal/gmailclient/real.go`) |
| --------- | -------- | ------------------------------------------------- |
| `clean --yes` | `newCleanCmd` → `engine.Pipeline.ApplyStages` | **Order**: write undo cache atomically (fatal on error, refuses to overwrite a non-empty cache) → `TrashMessages` → `MarkTrashed` (SQLite). `TrashMessages` calls `Users.Messages.Trash` per ID, sequentially, with retry. |
| `undo` | `newUndoCmd` | Load undo cache → `RestoreFromTrash` (per-ID `Users.Messages.Untrash`, retried; returns the actually-restored subset, **404 = permanently deleted → skipped**, not an error) → re-insert only the restored subset via `RestoreTrashed` (SQLite) → remove cache file. Prints `Restored N messages from Trash.` |
| `purge --yes` | `newPurgeCmd` | `EmptyTrash`: paginate `LabelIds("TRASH")` at 1,000/page, then `Messages.BatchDelete` in chunks of 1,000, each retried. Afterwards the CLI deletes the undo cache. |

**Retry policy** (`retryMutation`): up to `maxMutationAttempts = 3` attempts per call.
On a retryable failure (`googleapi.Error` with code `429` or `>= 500`, or a transport
`*url.Error`) the wait is computed by `retryDelay`: the server's `Retry-After` header
is honored when present (delta-seconds or HTTP-date, capped at
`maxRetryAfterWait = 60s`); otherwise Google's recommended jittered exponential
backoff applies (`backoffBase = 1s` doubling, jitter up to 1s, capped at
`backoffCap = 32s`). Non-retryable 4xx (auth, missing message) fail immediately —
no masking.

**Safety gates**: `--yes` required by `clean`/`purge`; §15 planner refuses to delete
non-junk; undo cache is written **before** any Gmail mutation and a cache-write failure
aborts the clean; the cache refuses to be overwritten until undone/purged.

## Prerequisites

1. **Test account** (recommended) — a throwaway Gmail account you don't care about.
   The plan targets `a12pct@gmail.com` per the existing plan; substitute your own.
2. **OAuth** — `credentials.json` at `~/.config/gclean/credentials.json` (Desktop-app
   client with `gmail.readonly` + `gmail.modify` scopes), then `gclean login`. The
   callback server now binds an allocated loopback port, so two logins can run back to
   back without a port collision.
3. **Fresh local state** per scenario:

```bash
export GCLEAN_DB_PATH=$(mktemp -d)/gclean.db
export GCLEAN_UNDO_CACHE=$(mktemp -d)/undo-cache.json
export GCLEAN_SELECTION_PATH=$(mktemp -d)/tui-selection.json   # empty = unrestricted
gclean scan          # seed the local store
gclean dry-run       # confirm the cohort before touching anything
```

4. **Baseline gate before starting** (and re-run after any code change prompted by a
   finding): `./scripts/lint-email-literals.sh && go vet ./... && go build ./... && go test ./...`

## Fixture strategy

The classifier needs marketing-shaped mail to produce a predictable delete cohort.
Send each test message with a unique `[gclean-test] T<NN>` subject prefix from a
disposable sender, including a `List-Unsubscribe` header (e.g. via a mailing-list or by
drafting messages with headers through `gmail.users.messages.insert`). Then every
verification is a Gmail search:

- `subject:[gclean-test] T01 in:anywhere` — where the message is now
- `subject:[gclean-test] T01 label:trash` — in Trash
- `label:trash` — everything in Trash (for purge counts)

Record the Gmail message IDs (`gmail.users.messages.list` or the SQLite store) before
mutating, and compare IDs after, rather than trusting counts alone.

## Safety gates — verify first, before any mutation

| # | Check | Command | Expect |
| - | ----- | ------- | ------ |
| G1 | `--yes` gate (clean) | `gclean clean` | `Refusing to clean without --yes…`, exit non-zero, no Gmail change |
| G2 | `--yes` gate (purge) | `gclean purge` | `Refusing to purge without --yes…`, exit non-zero, Trash untouched |
| G3 | §15 non-junk protection | `gclean dry-run` | every `VerdictDelete` message is classified junk; no protected/starred/important/contact/recent message in the delete cohort |
| G4 | Cache-write failure aborts clean | point `GCLEAN_UNDO_CACHE` at an unwritable path, then `gclean clean --yes` | clean fails **before** Gmail mutation; `label:trash` count unchanged; error names the cache |
| G5 | Cache overwrite protection | run a successful `clean --yes`, then immediately `clean --yes` again with a new cohort | second run fails (`undo cache already exists…`); `label:trash` count reflects only the first cohort |

## Test cases

### TC-01 — Trash a single delete candidate

1. Seed one `[gclean-test] T01` marketing message; `gclean scan && gclean dry-run` lists it.
2. `gclean clean --yes`
3. **Expect**: exit 0; `subject:[gclean-test] T01 label:trash` returns the message;
   `GCLEAN_UNDO_CACHE` exists with exactly 1 record (`version: 1`, valid `checksum`);
   `gclean stats` shows the reclaimed estimate drop.

### TC-02 — Trash a batch of candidates

1. Seed ≥ 15 `[gclean-test]` messages across 3–4 junk senders; dry-run lists all.
2. `gclean clean --yes`
3. **Expect**: exit 0; all seeded IDs in `label:trash`; undo cache has 15 records; no
   `trash message n/m` error in the log.

### TC-03 — Retry on transient failure

A reliable real 429 is hard to provoke; unit tests (`real_test.go`) cover the retry loop
deterministically. On the live account, do a large `clean` (50+ messages) and watch
`slog` stderr output: backoff/retry only if quota pressure appears. **Expect**: the run
completes; if a 429/5xx or transport error occurs, the same call is retried (max 3) and
the final error (if any) reports the exact `n/m` message that failed. Non-retryable
4xx (e.g. a bad message ID) must fail immediately with the API error — that path is
worth a manual negative test: craft the cohort to include a nonexistent ID (see TC-05).

### TC-04 — Undo restores exactly the cohort

1. Precondition: TC-01 or TC-02 completed (cache present, N messages in Trash).
2. `gclean undo`
3. **Expect**: exit 0; `Restored N messages from Trash.`; `subject:[gclean-test] … in:anywhere`
   returns the messages (no longer `label:trash`); `gclean scan` shows them re-ingested;
   the undo cache file is removed.

### TC-05 — Undo idempotency / partial-failure safety

1. After a `clean`, manually restore one message out-of-band (Gmail UI or
   `users.messages.untrash`), so the cache still lists it.
2. `gclean undo`
3. **Expect**: undo completes, restores the remaining messages, and re-inserts all
   local rows (the SQLite upsert is idempotent by message ID). The run must not fail
   the whole batch over one already-restored message. Note: the API docs describe
   `untrash` as "removes the specified message from the trash" but don't specify the
   response for a message that isn't in Trash — if Gmail rejects it (e.g. 4xx), record
   that as a finding: the adapter would abort the whole undo at that point, and
   `RestoreFromTrash` may need to treat a per-message rejection as a warning instead of
   a hard error.

### TC-06 — Purge empties Trash (permanent)

1. Precondition: ≥ 1 message in Trash (seed via `clean --yes`, or
   `gmail.users.messages.insert` with `labelIds: ["TRASH"]`).
2. `gclean purge --yes`
3. **Expect**: exit 0; `Trash emptied…`; `label:trash` count 0 for the seeded IDs;
   `subject:[gclean-test] … in:anywhere` returns nothing (permanent). Undo cache removed.

### TC-07 — Purge > 1,000 messages (pagination + batch delete)

1. Seed > 1,000 messages into Trash. Seeding via the API is quota-bound: under the
   May 2026 quota model each `messages.insert` costs 25 units against a
   **6,000 units/min/user** budget, so 1,000 inserts need ~5 minutes of pacing (or
   reuse an account that already has a large Trash). `messages.insert` with
   `labelIds: ["TRASH"]` avoids the send quota entirely.
2. `gclean purge --yes`
3. **Expect**: exit 0; Trash count 0; `slog` shows the pagination/batch boundaries
   (~2 API calls per 1,000 IDs; `list`=5 + `batchDelete`=50 units, well under the
   per-minute budget); no duplicate/missing IDs. This exercises the
   `mutationBatchSize = 1000` chunking plus `NextPageToken` loop.

### TC-08 — Clean respects the TUI selection (if a selection file exists)

1. Seed two junk senders, A and B; write `GCLEAN_SELECTION_PATH` with only A selected
   (see `internal/storage/selection.go` format; legacy `selectors`/`ts` files load too).
2. `gclean dry-run` and `gclean clean --yes`
3. **Expect**: only A's messages are trashed; B's messages get a `selection_excluded`
   keep verdict and remain in the Inbox; the printed cohort summary names A only.

### TC-09 — Concurrent/back-to-back logins (OAuth lifecycle)

1. `gclean login` twice in quick succession (or from two shells).
2. **Expect**: both callback servers bind successfully (allocated loopback port), each
   redirect uses its own port, both tokens persist to `GCLEAN_TOKEN_PATH`. A fixed-port
   regression would surface as `address already in use`.

### TC-10 — Undo after a real partial purge (stale-cache recovery) ⚠️ safety check

**Why**: the most dangerous reconcile case. A partial/interrupted `purge` permanently
   deletes some cohort messages while leaving others in Trash, and the undo cache may
   still reference the deleted IDs. `gclean undo` must recover the survivors, skip the
   permanently-deleted IDs without aborting (404 = not restorable), and **never
   re-insert ghosts** into the local store. Regression counterpart of
   `TestUndo_AfterPartialPurgeSkipsDeletedRestoresSurvivors` and
   `TestRealClient_RestoreFromTrash_404SkipsDeleted`.

1. Seed ≥ 2 `[gclean-test] T10` marketing messages; `gclean scan && gclean dry-run`
   lists them all.
2. `gclean clean --yes` → all N in Trash; `cat $GCLEAN_UNDO_CACHE` shows N records.
3. Reproduce the partial-purge server state deterministically: permanently delete a
   subset K (e.g. the first one) out-of-band via the Gmail API
   (`gmail.users.messages.delete`) or Gmail UI "Delete forever". The K messages are
   now 404s — exactly what a partial `EmptyTrash` leaves behind. The remaining N−K
   stay in Trash.
   - Optional, if you can provoke a real mid-purge failure (e.g. quota 429 on a large
     Trash): run `gclean purge --yes` and let it fail partway; otherwise the
     out-of-band deletes above are the state to reconcile.
4. `gclean undo`
5. **Expect**:
   - exit 0; `Restored N−K messages from Trash.`
   - Gmail-side: `subject:[gclean-test] T10 in:anywhere` returns the N−K survivors
     (back in Inbox); the K deleted IDs return nothing (permanent).
   - Local store: `select id from messages where subject like '[gclean-test]%'`
     returns **exactly** the N−K survivors — **no ghost rows** for the deleted K.
   - Undo cache file removed.
6. **Negative assertion**: undo must not abort with a 404; the deleted IDs must never
   appear in the local store.

## Verification tooling

- **Gmail-side**: `label:trash`, `in:anywhere`, `subject:` searches; compare message IDs
  (recorded before) rather than counts.
- **Local store**: `sqlite3 $GCLEAN_DB_PATH 'select id, sender_email, verdict from messages where sender_email like "%[gclean-test]"'` (adjust schema as needed).
- **Undo cache**: `cat $GCLEAN_UNDO_CACHE` — expect `version: 1`, matching `checksum`,
  records matching the trash cohort; a non-empty cache blocks the next clean (G5).
- **Logs**: mutation progress (`listed page`, `fetched metadata`, retry/backoff) goes to
  stderr via `slog`; capture `2> gclean.log` and grep for `failed after` to confirm
  retry behavior.

## Cleanup & rollback

- Every `clean` is reversible with `undo` (same machine, same `GCLEAN_UNDO_CACHE`).
- `purge` is **not** reversible — always run `purge` against a throwaway account, and
  never before TC-04/TC-05 pass.
- Between scenarios: `gclean undo` (or restore via Gmail UI), delete
  `GCLEAN_DB_PATH`/`GCLEAN_UNDO_CACHE`/selection file, re-`scan`.

## Exit criteria

- [ ] G1–G5 safety gates pass with no Gmail-side side effects on failure paths
- [ ] TC-01…TC-10 pass on a live account; IDs verified on the Gmail side
- [ ] TC-10: after out-of-band permanent deletion, `undo` restores only the survivors
      (no ghost rows, no 404 abort, cache removed)
- [ ] `label:trash` count after TC-07 = 0 (batching + pagination correct)
- [ ] Undo cache lifecycle verified: created atomically before mutation, non-empty
      blocks re-clean, removed by `undo` and `purge`
- [ ] Re-run `./scripts/lint-email-literals.sh && go vet ./... && go build ./... && go test ./...`
      after any code change from findings

## Risks & open questions

- **Quota**: under the May 2026 quota model the binding limit is 6,000 units per
  minute per user (per method: `list`=5, `get`=20, `trash`=20, `untrash`=5,
  `batchDelete`=50, `insert`=25). A big `scan` or TC-07 seeding can exhaust the
  per-minute budget; pace seeding and expect 429s in the logs. The adapter honors
  `Retry-After` on 429/5xx (capped at 60s) and otherwise uses Google's recommended
  jittered exponential backoff (1s doubling, max 32s), 3 attempts per call — worst
  case ≈ 2 minutes of waiting before a clean "try later" error. If sustained 429s
  still appear, revisit `maxMutationAttempts` and the 60s Retry-After cap with
  observed quota behavior. Note the read path (`ListMessages`) has no retry yet.
- **Cohort quality**: `IsContact` (People API) and `REPLIED` enrichment remain gaps, so
  the delete cohort is what it is; mutation correctness (TC-01…TC-07) is independent of
  those, but G3's "no contact/replied deletions" can only be spot-checked manually.
- **Undo across machines**: the cache lives in `~/.config/gclean`; running `clean` on one
  machine and `undo` on another needs `GCLEAN_UNDO_CACHE` pointed at the same file.
