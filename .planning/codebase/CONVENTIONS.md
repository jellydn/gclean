# CONVENTIONS

Coding patterns and invariants to preserve when extending `gclean`.

## Go style and package boundaries

- Use standard `gofmt` formatting and conventional Go import grouping.
- Keep implementation packages under `internal/`; the only executable package is `cmd/gclean`.
- Use small packages by responsibility: `cli`, `config`, `defang`, `engine`, `gmailclient`, `models`, `storage`, and `tui`.
- `internal/cli` is the composition root. It may import the other application packages; lower layers should not import `cli`.
- `internal/engine`'s classifier, evaluator, protector, and planner are deterministic in-memory logic. Pipeline stages are the explicit I/O orchestration seam.
- `engine.Gmailer` is a local narrow interface so pipeline code does not depend on the concrete Gmail package.

## CLI conventions

- Build commands with `newXCmd(out, errOut io.Writer) *cobra.Command`.
- Use `RunE`, return errors, and set `SilenceErrors`/`SilenceUsage` on the root command (`internal/cli/cli.go:42-44`).
- Inject output writers rather than writing directly to process stdout/stderr; this makes CLI tests deterministic.
- Resolve paths through central helpers (`storePath`, `credentialsPath`, `defaultCache`) and respect the documented environment variables.
- Use `--fixtures` to select `FakeClient` for local development and tests.
- Any command that changes Gmail state must require `--yes`; print an actionable refusal before returning `errors.New("confirmation required")`.
- Use `text/tabwriter` for tables and the shared `humanBytes` formatter for byte values.

## Error handling

- Wrap lower-level failures with context and `%w`, for example `fmt.Errorf("list messages: %w", err)`.
- Keep sentinel errors in the owning package (`ErrCredentialsMissing`, `ErrNotImplemented`).
- Preserve actionable path/operation context in filesystem, config, SQLite, Gmail, and OAuth errors.
- Watch mode treats one iteration failure as recoverable and reports it to `errOut`; cancellation is handled through context/signals.
- Ignore errors only where the operation is intentionally best-effort or non-critical, such as output writes, closing resources, or cleanup of an already-absent cache.

## Safety conventions

- Keep `Plan`'s priority order explicit and documented. New rules must be placed relative to the existing safety checks, not appended casually.
- Never allow a matching delete rule to delete a non-junk message; preserve `delete_rule_refused_non_junk`.
- `clean` means recoverable Trash; permanent deletion belongs only to `purge`.
- Do not fetch or persist message bodies under the default metadata-only model.
- Preserve undo records before/while trashing and validate the cache before restoring it.
- Add a test for every new planner branch or mutation gate.

## Data and reason-code conventions

- `models.Message` mirrors Gmail-shaped JSON names (`threadId`, `isContact`) and uses `time.Time` for parsed dates.
- Stable reason codes are exported constants in `internal/models/models.go`; append new codes rather than renumbering or renaming existing values.
- Planner reasons use recognisable prefixes: `protect:`, `config_keep:`, `config_archive:`, `config_delete:`, plus `ignored_domain`, `delete_rule_refused_non_junk`, and `default_keep`.
- Store booleans as SQLite integers through `boolInt`; serialize labels as comma-separated values and headers as JSON at the current storage boundary.
- Keep aggregate/report construction close to the scan that supplies its fields; `storage.Aggregations()` is the single source for stats and sender safety rollups.

## Configuration DSL

- Rules use `key:value` predicates separated by spaces or commas.
- Supported keys are `has`, `subject`, `category`, `from`, `older_than`, and `larger_than` (`internal/engine/evaluator.go:81-124`).
- Durations accept only `Nd`; byte sizes accept `B`, `KB`, `MB`, or `GB`, case-insensitively.
- Empty rules never match. Unknown predicates return false.
- YAML field names are stable because users edit `config.yaml`; do not rename fields without a migration strategy.

## OAuth and filesystem conventions

- Persist OAuth tokens at mode `0600`; create parent directories with restrictive permissions where appropriate.
- Use `GCLEAN_TOKEN_PATH` for tests and non-default token locations.
- The loopback OAuth callback uses `localhost:8080`; keep the redirect URL and callback listener aligned.
- Validate fixture paths before opening them: `NewFakeClient` rejects symlinks and non-regular files.
- Use runtime address construction through `defang.MkEmail` for source strings that contain email addresses. The custom lint enforces this for non-test Go/JSON files.

## Comments and documentation

- Explain why a boundary or invariant exists, not only what a line does.
- Use exact file paths and PRD/safety references when documenting behavior that future changes could weaken.
- Keep user-facing status honest: the real read path and OAuth flow are implemented, but RealClient mutation remains stubbed.
