# TESTING

Testing framework, patterns, structure, and known gaps.

## Framework

- **stdlib `testing`** only. No testify, no gomock, no assertion library — `t.Errorf` / `t.Fatalf` / `t.Run` directly.
- CI: `go test ./...` runs from `justfile` and pre-commit hooks. No separate test runner.
- Race detector: project does not yet run `go test -race` by default — bug fix #1 if any concurrent path proves flaky.

## Test Inventory (current)

```
internal/cli/cli_test.go
  Line 20:  TestBuild_Help
  Line 35:  TestScanCommand_DevFixturePipeline
  Line 86:  TestCleanCommand_RefusesWithoutYes
  Line 110: TestDemoCommand_RendersExpectedOutput
  Line 199: TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders
  Line 338: TestDevCommand_OneShotMode_RendersPipeline

internal/tui/app_test.go (293 lines)
internal/gmailclient/fake_test.go (53 lines)
internal/engine/classifier_test.go (154 lines)
internal/engine/evaluator_test.go (120 lines)
internal/engine/planner_test.go (148 lines)
internal/engine/protector_test.go (84 lines)
```

## Patterns

### Engine tests (unit, pure)

- All assertions treat `time.Now()` as out-of-scope. `Protect`'s `recent_days: N` rule uses `time.Now()` directly — when testing recency the fixture `Date` is set so the case is decision-deterministic regardless of clock.
- Helper: `defang.MkEmail(local, domain)` is used for every Sender.Email in test fixtures and demo data — assembly-time `@` defeats Cloudflare obfuscation.

### CLI tests (integration)

Pattern:

1. Build the CLI with `bytes.Buffer` capture: `out, errOut := &bytes.Buffer{}, &bytes.Buffer{}; root := cli.Build(out, errOut)`.
2. Inject flags via `root.SetArgs([]string{...})`.
3. Create a sandboxed `GCLEAN_DB_PATH`: `t.Setenv("GCLEAN_DB_PATH", filepath.Join(t.TempDir(), "gclean.db"))`.
4. Build a synthetic JSON fixture in `t.TempDir()` using an inline struct with `json:"..."` tags matching the expected shape and `defang.MkEmail` for every email.
5. Drive via `root.Execute()`.
6. Assert substrings in `out.String()` (and `errOut.String()` for the negative case).

### TestBuild_Help (registration lock)

Iterates a curated substring list — currently `login, logout, scan, stats, dry-run, clean, undo, purge, dev` — and asserts each appears in the root command's `--help` output. This locks `Build()`'s `AddCommand` list — a future refactor that drops a registered subcommand fails the test loudly. When adding a new subcommand, extend the list.

### TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders (the synthetic-fixture canonical pattern)

This test was added because the on-disk `testdata/fixtures/messages.json` is itself obfuscation-corrupt. The test:

1. Synthesizes 5 sample sender entries inline (each row has its own `defang.MkEmail`-built email).
2. Marshals to JSON, writes to `t.TempDir()/messages.json`.
3. Drives `gclean scan --fixtures <tempfile>` (full FakeClient chain).
4. Runs `gclean sender`.
5. Asserts: 5 expected addresses present, 3-column header regex matches the rendered output, row count == len(samples).

Tracks the on-disk-getting-corrupted gotcha in `.plans/implement-notes.md`.

### TestDevCommand_OneShotMode_RendersPipeline

Smoke-tests `gclean dev --watch=false`:

1. Inline synthetic fixture in `t.TempDir()`.
2. Sets `GCLEAN_DB_PATH` to `t.TempDir()/gclean.db`.
3. Drives `["dev", "--fixtures", tmpfix, "--watch=false"]`.
4. Asserts that the output contains the three expected sections: `Scanned N messages.`, `Total messages`, `Safe to delete`. Watch mode is **intentionally not tested**.

### TestScanCommand_DevFixturePipeline

The legacy integration test referenced in `justfile` and `just test-integration`. Drives the full scan→stats→dry-run pipeline end-to-end.

### TestCleanCommand_RefusesWithoutYes

Negative case: `gclean clean` without `--yes` fails with `errors.New("confirmation required")`.

### TestDemoCommand_RendersExpectedOutput

Drains a `bytes.Buffer` from `gclean demo` and asserts the table headers + at least one sample row.

## Untested Paths (Deliberate)

- `gmailclient.RealClient` is **deliberately not tested**. Every method returns `ErrNotImplemented`; tests would be tautological.
- `gclean dev` watch-mode loop is **intentionally not tested**. The polling + SIGINT/cancel + state-transition behavior is hard to assert deterministically without flakiness (filesystem mtime, signal injection). Tests cover only `--watch=false` one-shot mode. The watch loop invariants can be verified by hand-running `gclean dev` against a fixture and editing it.

## Coverage

There is **no enforced coverage target**. `go test -cover` is not wired into `just check` or pre-commit. The engine tests exercise every documented classification path (noreply prefix, vendor domain matches, header signals, Gmail categories), and every planner verdict branch (ignored, protected, keep, archive, delete-with-junk, delete-without-junk refusal, default keep).

## What's NOT Covered

- `clean` and `purge` roundtrip against the FakeClient — IDs flow through TrashedStates but not message bodies. End-to-end smoke is wired through `cli_test.go` for the dev-fixture path.
- `gclean rules` and `gclean config --op show` were hand-tested against the bundled fixture's `defaultConfig`. Quick-win: add `TableDriven` parse-of-config tests.
- `gclean purge` does not have a CLI integration test (it's state-changing without an undo; tested implicitly via the e2e `just e2e` recipe).
- The `scripts/lint-email-literals.sh` shell script has no Go-side coverage. A `lint_test.go` smoke test under a new `internal/scripts/scripts_test.go` could `os/exec` the script with a deliberately-broken fixture and assert exit=1 — not yet done.

## Conventions for New Tests

1. Place unit tests next to the source file with the same `_test.go` suffix.
2. Place integration tests in `internal/cli/cli_test.go` (single shared file, since the CLI surface is one package).
3. Name tests with a verb phrase: `TestX_DoesY_WhenZ` or `TestX_SubjectsY` (matches `TestDevCommand_OneShotMode_RendersPipeline`, `TestSenderCommand_SyntheticFixturePipeline_ShowsExpectedSenders`).
4. Add a synthetic inline fixture JSON for any CLI integration test that needs fixtures — do not call `os.ReadFile("testdata/fixtures/messages.json")`. Always build emails via `defang.MkEmail`.
5. Use `t.Setenv("GCLEAN_DB_PATH", ...)` rather than `os.Setenv` so the env is restored on `t.Cleanup` automatically.
6. Always exercise the production chain in CLI tests (`cli.Build` + `SetArgs` + `Execute`) — don't reach into the package-internal helpers.
7. When adding a new subcommand, extend `TestBuild_Help`'s substring list so the registration is locked.
