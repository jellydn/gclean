# STACK

Technologies, frameworks, and runtime configuration that the project builds on.

## Language & Toolchain

- **Go** `1.26.4` (`go.mod:3`) — single language for the entire project, including CLI, persistence, and TUI.
- Build: `go build ./...` (entry point produced from `cmd/gclean/main.go`).
- Tests: `go test ./...`. Pure-Go SQLite (`modernc.org/sqlite`) so no CGO toolchain is required.
- Pre-commit gate: `prek` (`.pre-commit-config.yaml`) runs `go vet`, `go build`, `golangci-lint`, and the custom `scripts/lint-email-literals.sh` over staged Go files plus standard formatting/YAML hooks.

## Direct Dependencies

| Module                               | Version   | Where it shows up                                                    |
| ------------------------------------ | --------- | -------------------------------------------------------------------- |
| `github.com/spf13/cobra`             | `v1.10.2` | `internal/cli/cli.go:35` — the entire command tree (17 subcommands). |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | `internal/tui/app.go` — TUI Model implementation (`gclean tui`).     |
| `github.com/charmbracelet/lipgloss`  | `v1.1.0`  | `internal/tui/app.go` — TUI rendering styles.                        |
| `gopkg.in/yaml.v3`                   | `v3.0.1`  | `internal/config/yaml.go` — YAML config parsing.                     |
| `modernc.org/sqlite`                 | `v1.53.0` | `internal/storage/sqlite.go:14` — pure-Go SQLite driver (CGO-free).  |
| `log/slog`                           | stdlib    | `cmd/gclean/main.go:7` — text-handler logger to stderr.              |

`Viper` is deliberately **not** used (`internal/config/config.go:4-9` comments explain why).

## Indirect Dependencies (high-traffic ones)

- `github.com/charmbracelet/x/{ansi,cellbuf,term}`, `github.com/charmbracelet/colorprofile` — bubbletea/lipgloss transitive deps for terminal escape sequences.
- `modernc.org/{libc,mathutil,memory}` — sqlite driver stack.
- `golang.org/x/{sys,text}` — sqlite + unicode support.
- `github.com/google/uuid` — pulled in by sqlite/bubbletea.
- `github.com/dustin/go-humanize` — size formatting; currently transitively included but not directly imported in `gclean` code (gclean ships its own `humanBytes()` at `internal/cli/cli.go:152`).

Dev tooling:

- `golangci-lint` (optional — `justfile` and `Makefile` skip silently when missing).
- `pre-commit` / `prek` — local-only pre-commit hooks.
- `just` (`justfile`) and `make` (`Makefile`) — both provide `check`/`lint`/`build`/`test`/`e2e` recipes.

## Configuration Surfaces

| Surface               | Loader                                                                                 | Path                                                                                                                                                                                                                                             |
| --------------------- | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **YAML rules**        | `config.Load()` (`internal/config/config.go:65`)                                       | `GCLEAN_CONFIG_PATH`, else `$XDG_CONFIG_HOME/gclean/config.yaml`, else `~/.config/gclean/config.yaml`. Auto-creates `defaultConfig` on first run; auto-create is also absorbed by `gclean dev`'s watch loop (see `internal/cli/dev.go:124-139`). |
| **SQLite DB**         | `storage.Open()` (`internal/storage/sqlite.go:46`)                                     | `GCLEAN_DB_PATH`, else `~/.config/gclean/gclean.db`.                                                                                                                                                                                             |
| **OAuth credentials** | `cli.credentialsPath()` (`internal/cli/cli.go:90`)                                     | `GCLEAN_CREDENTIALS_PATH`, else `~/.config/gclean/credentials.json`.                                                                                                                                                                             |
| **Undo cache**        | `cli.defaultCache()` (`internal/cli/pipeline.go`)                                      | `GCLEAN_UNDO_CACHE`, else `~/.config/gclean/undo-cache.json`. Cache IO itself is `storage.SaveUndoCache`/`LoadUndoCache` (`internal/storage/undocache.go`).                                                                                      |
| **Dev-mode fixtures** | `--fixtures PATH` flag on `gclean dev`                                                 | `testdata/fixtures/messages.json` (40-message corpus, relative to CWD).                                                                                                                                                                          |
| **Fixture corpus**    | `--fixtures PATH` flag on `scan`/`clean`/`dry-run`/`undo` (`internal/cli/pipeline.go`) | Bundled `testdata/fixtures/messages.json` (corrupted by Cloudflare obfuscation; integration tests synthesize their own).                                                                                                                         |

## Environment Variables That Gate Behavior

| Var                       | Purpose                                                                                                            |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `GCLEAN_DB_PATH`          | Override SQLite path; commonly set to a tempdir for sandboxed runs (`just e2e`).                                   |
| `GCLEAN_CREDENTIALS_PATH` | Where to find `credentials.json` for OAuth. Until present, `--fixtures` is the only path that drives the pipeline. |
| `GCLEAN_CONFIG_PATH`      | Override config file location; honored by `gclean dev` watch loop (`internal/cli/dev.go:124`).                     |
| `GCLEAN_UNDO_CACHE`       | Override undo-cache path.                                                                                          |
| `XDG_CONFIG_HOME`         | Standard; config loader falls back to it before `~/.config`.                                                       |

## Build / Run / Test Entry Points

```
bin/source : cmd/gclean/main.go
build      : go build ./...                        (or `just build`, `make build`)
vet        : go vet ./...                          (or `just vet`, `make vet`)
test       : go test ./...                         (or `just test`, `make test`)
check      : just check                            = vet + build + lint + test
e2e (dev)  : just e2e fixtures=testdata/...        = scan → stats → dry-run → clean → undo end-to-end
one-shot   : go run ./cmd/gclean dev --watch=false --fixtures testdata/fixtures/messages.json
watch      : go run ./cmd/gclean dev               (Ctrl+C to exit)
```

## Schema / Migrations

SQLite schema is **inline** in `internal/storage/sqlite.go:22` — `CREATE TABLE IF NOT EXISTS messages …` plus four indexes (`idx_messages_sender`, `_date`, `_junk`, `_verdict`). No migration table, no version column. Greenfield scaffold (see `CONCERNS.md #9`).

## File-Watching Implementation Choice

`gclean dev` watch mode uses **polling** (2s default interval, configurable via `--interval`) — see `internal/cli/dev.go:25-26` for the rationale. **Not fsnotify** — would be a better fit for sub-second feedback but adds a dep + moving part for a dev-only tool. Polling is sufficient below human iteration speed.
