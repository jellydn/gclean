gclean_bin := "cmd/gclean"
default_recipe := "check"

# ── Aggregates ──────────────────────────────────────────────────────────────

# Full default gate: vet + build + lint + test
check: vet build lint test

# Quick feedback: vet + build + test (no lint scripts)
check-quick: vet build test

# ── Go toolchain ────────────────────────────────────────────────────────────

build:
    go build ./...

vet:
    go vet ./...

test args="":
    go test ./... {{args}}

test-pkg pkg="internal/engine/":
    go test ./{{pkg}}

test-integration:
    go test -run TestScanCommand_DevFixturePipeline ./internal/cli/

# ── Linting ─────────────────────────────────────────────────────────────────

lint-emails:
    ./scripts/lint-email-literals.sh

lint: lint-emails
    go vet ./...
    go build ./...
    if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; else echo "  (golangci-lint not installed — skipping)"; fi

# ── E2E dev flow (uses --fixtures) ──────────────────────────────────────────

e2e fixtures="testdata/fixtures/messages.json":
    export GCLEAN_DB_PATH="$(mktemp -d)/gclean.db"
    go run {{gclean_bin}} scan  --fixtures {{fixtures}}
    go run {{gclean_bin}} stats
    go run {{gclean_bin}} dry-run
    go run {{gclean_bin}} clean --yes --fixtures {{fixtures}}
    go run {{gclean_bin}} undo  --fixtures {{fixtures}}

# ── Cleanup ─────────────────────────────────────────────────────────────────

clean:
    go clean ./...
