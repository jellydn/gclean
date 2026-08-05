# STACK

Current technology stack and local development surfaces for `gclean`.

## Runtime and language

- **Go 1.26.4** — declared in `go.mod:1-3`; all application, CLI, persistence, OAuth, and TUI code is Go.
- **CLI executable** — `cmd/gclean/main.go` configures `log/slog`, constructs the Cobra root command, and executes it with a context.
- **CGO-free SQLite** — `modernc.org/sqlite` is used by `internal/storage/sqlite.go`, avoiding a native SQLite toolchain.
- **No message bodies** — `models.Message` deliberately stores metadata, headers, labels, and a snippet, but not full bodies (`internal/models/models.go:9-22`).

## Direct dependencies

| Dependency | Version | Usage |
| --- | --- | --- |
| `github.com/spf13/cobra` | `v1.10.2` | Root command and subcommands in `internal/cli/` |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` | Interactive sender-selection TUI in `internal/tui/app.go` |
| `github.com/charmbracelet/lipgloss` | `v1.1.0` | TUI styling and terminal rendering |
| `gopkg.in/yaml.v3` | `v3.0.1` | YAML config parsing through `internal/config/yaml.go` |
| `modernc.org/sqlite` | `v1.53.0` | Pure-Go SQLite driver in `internal/storage/sqlite.go` |
| `google.golang.org/api` | `v0.287.1` indirect in `go.mod` | Gmail API service used by `internal/gmailclient/real.go` and `oauth.go` |
| `golang.org/x/oauth2` | indirect in `go.mod` | OAuth config, token exchange, and refresh token source |

The standard library supplies `database/sql`, `encoding/json`, `net/http`, `net/mail`, `log/slog`, `text/tabwriter`, signal handling, and filesystem access.

## Developer commands

`justfile` is the preferred command runner:

```text
just check          # vet + build + lint + test
just check-quick    # vet + build + test
just test-pkg pkg="internal/engine/"
just test-integration
just e2e
```

Equivalent Make targets are in `Makefile`: `lint-emails`, `lint`, `build`, `test`, and `vet`. `golangci-lint` is optional in the aggregate lint recipe and is skipped when unavailable.

## Validation and repository hooks

- `go vet ./...`
- `go build ./...`
- `go test ./...`
- `scripts/lint-email-literals.sh` — rejects raw email literals in non-test Go/JSON source.
- `.pre-commit-config.yaml` runs standard whitespace/YAML/large-file hooks plus Go vet, build, golangci-lint, and the email-literal script.
- `.github/workflows/lint-emails.yml` runs the email-literal check on pushes to `main` and pull requests.

## Configuration and runtime paths

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `GCLEAN_DB_PATH` | `~/.config/gclean/gclean.db` | SQLite metadata database |
| `GCLEAN_CONFIG_PATH` | `$XDG_CONFIG_HOME/gclean/config.yaml` or `~/.config/gclean/config.yaml` | YAML rules |
| `GCLEAN_CREDENTIALS_PATH` | `~/.config/gclean/credentials.json` | Google OAuth client credentials |
| `GCLEAN_TOKEN_PATH` | `~/.config/gclean/token.json` | Persisted OAuth token (`internal/gmailclient/oauth.go`) |
| `GCLEAN_UNDO_CACHE` | `~/.config/gclean/undo-cache.json` | Pre-trash records for undo |

`config.Load()` creates the default YAML configuration on first use (`internal/config/config.go:65-90`). SQLite schema creation happens during `storage.Open()` (`internal/storage/sqlite.go:22-55`).

## Build status and implementation boundary

The local fixture pipeline is end-to-end: fake Gmail input → classification → SQLite → planning → reporting. Real Gmail authentication is implemented through `gclean login`, and `RealClient.ListMessages` fetches metadata through the Gmail API (`internal/gmailclient/real.go:23-95`). The mutating RealClient methods still return `ErrNotImplemented` (`internal/gmailclient/real.go:98-106`), so production Trash/restore/purge is not complete.

## Design choices

- YAML rather than Viper: `internal/config/config.go:5-7` explicitly avoids Viper's larger dependency graph.
- Polling rather than filesystem notifications: `internal/cli/dev.go:18-25` uses a configurable two-second mtime poll for the development watcher.
- Runtime email assembly through `defang.MkEmail`: `internal/defang/defang.go` protects source strings from the repository's email-obfuscation failure mode.
