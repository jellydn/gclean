# TESTING

Testing framework, current inventory, patterns, and known coverage gaps.

## Framework and commands

- Tests use only Go's standard `testing` package; there is no testify, gomock, or external assertion framework.
- Full suite: `go test ./...`.
- Fast project gate: `just check-quick` (`go vet`, `go build`, `go test`).
- Default gate: `just check` adds the email-literal lint and optional golangci-lint.
- Focused packages: `go test ./internal/engine/` and the CLI integration command in `justfile`.
- The project does not enforce coverage and does not run the race detector by default.

## Test inventory

- `internal/engine/classifier_test.go` — header, noreply, vendor-domain, category, personal-message, domain extraction, and header lookup behavior.
- `internal/engine/protector_test.go` — starred, recent, contact, whitelist, and sent-message protection.
- `internal/engine/evaluator_test.go` — rule parsing, comma tolerance, duration/size parsing, matching, and empty-rule behavior.
- `internal/engine/planner_test.go` — delete-only-junk safety, keep precedence, ignored domains, protection precedence, and archive decisions.
- `internal/gmailclient/fake_test.go` — fixture-free fake listing, query matching, trash, restore, and trashed-ID tracking.
- `internal/gmailclient/real_test.go` — missing credential handling, valid client construction from temporary credentials/token files, and mutation stubs.
- `internal/cli/cli_test.go` — root help registration, scan/stats/dry-run/clean integration, `--yes` refusal, demo output, synthetic sender pipeline, fixture integrity, and `dev --watch=false`.
- `internal/tui/app_test.go` — selection defaults, keyboard navigation, toggling, select-all/clear, commit/cancel, empty rows, resize, and rendered views.

## Test patterns

### Pure engine tests

Engine tests construct `models.Message` and `models.Classified` values directly. They assert exact verdicts, reason codes, and report counts. Date-based tests choose dates far enough from the current time to avoid boundary instability.

Email addresses in source are constructed with `defang.MkEmail` or a runtime join to avoid the repository's source-obfuscation issue.

### CLI integration tests

The canonical pattern is:

1. `t.TempDir()` for all runtime state.
2. `t.Setenv("GCLEAN_DB_PATH", ...)` to sandbox SQLite.
3. Construct a temporary JSON fixture when sender diversity matters.
4. Build through `cli.Build(&out, &errOut)`.
5. Set arguments with `cmd.SetArgs(...)`.
6. Call `cmd.Execute()` and assert user-visible output.

This intentionally exercises the production chain rather than calling internal helpers directly. `TestBuild_Help` also acts as a registration lock for key commands.

### TUI tests

`internal/tui/app_test.go` calls `Model.Update` directly with Bubble Tea messages and promotes the interface result back to the concrete `Model`. Rendering tests strip ANSI escape sequences before matching visible text.

### OAuth/Gmail tests

Real-client tests avoid network calls. They verify construction from temporary credential/token JSON and document that mutation methods currently return `ErrNotImplemented`. The fake client provides all local mutation behavior needed for fixture-driven tests.

## Current coverage gaps

- No live Gmail integration test or network contract test for `ListMessages`/`mapGmailMessage`.
- No end-to-end real OAuth browser callback test; callback behavior is unit-testable but currently covered only indirectly by construction/login code.
- Real `TrashMessages`, `EmptyTrash`, and `RestoreFromTrash` are stubs, so no real mutation test exists.
- `gclean dev` watch mode, signal handling, mtime transitions, and missing/reappearing files are not deterministically tested; only one-shot mode is covered.
- `gclean purge`, `undo` integrity failure paths, and TUI-selection-to-clean behavior lack complete CLI integration coverage.
- No coverage threshold, race test, fuzz test, or benchmark is wired into the project gate.
- The email-literal shell script is not tested from Go; it is validated by CI and pre-commit execution.

## Adding tests

- Place unit tests beside the implementation with the same package and `_test.go` suffix.
- Keep engine tests table-driven and I/O-free.
- Put CLI integration tests in `internal/cli/cli_test.go` unless a new package boundary makes a separate file clearer.
- Use runtime email assembly and synthetic fixtures rather than embedding fragile source literals.
- Assert safety behavior and user-visible output, not private implementation details.
- When adding a command, extend `TestBuild_Help`; when adding a planner branch, add a focused verdict/reason test.
