# gclean Makefile — entry points for local-dev + CI.
#
# The lint-emails target pairs with the obfuscation-defense finding tracked
# in .plans/implement-notes.md (Cloudflare email-obfuscation silently
# rewriting literals). All email addresses in non-test source must be
# assembled at runtime via engine.MkEmail(local, domain).

.PHONY: lint-emails lint build test vet

# Run the email-literal lint. Exits 1 on any offender with file:line context.
lint-emails:
	@./scripts/lint-email-literals.sh

# Aggregate lint target: email literals plus existing pre-commit hooks
# (go-vet, go-build, golangci-lint). Wire to your local CI / pre-push.
lint: lint-emails
	@echo "→ go vet ./..."
	@go vet ./...
	@echo "→ go build ./..."
	@go build ./...
	@echo "→ golangci-lint run ./... (optional)"
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "  (golangci-lint not installed — skipping)"

build:
	@go build ./...

test:
	@go test ./...

vet:
	@go vet ./...
