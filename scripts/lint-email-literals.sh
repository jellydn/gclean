#!/usr/bin/env bash
# scripts/lint-email-literals.sh
#
# Rejects raw `local@domain.tld` literals anywhere in non-test *.go and *.json
# files. Reason: Cloudflare's email-obfuscation pass silently rewrites any
# literal that matches the email regex into the placeholder "[email protected]"
# (no @), which then breaks domain extraction, map-key lookups, substring
# matching, and selection-slice equality downstream.
#
# The defense is to assemble strings at runtime via defang.MkEmail(local, domain)
# (defined in internal/defang/defang.go), which joins "@" at runtime so the
# obfuscator's regex never matches the literal.
#
# Allowances:
#   - *_test.go            : test code already constructs fixtures via runtime
#                            helpers (MkEmail, mkT) or local mk() equivalents.
#   - testdata/, vendor/   : fixtures and third-party code are out of scope.
#   - .git/.plans          : git metadata and agent handoff notes.
#   - Lines starting with  // : Go comment lines. (Docs/strings that quote an
#                            example email in prose are visually annoying but
#                            not load-bearing for runtime behavior.)
#
# Exit 0 on clean, exit 1 on any offender with file:line:snippet context.

set -euo pipefail

# Match a literal email anywhere in a line. Cloudflare's pass matches the same
# shape; broader TLD set (>=2 alpha) catches anything it would have rewritten.
REGEX='[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}'

# Run grep -r, narrow to source extensions, exclude tests/fixtures/vendor,
# then filter out Go-comment lines (didn't match "raw" intent; visible-text
# examples in prose are doc-style only).
OFFENDERS=$(
  grep -rnE "$REGEX" \
    --include='*.go' \
    --include='*.json' \
    --exclude='*_test.go' \
    --exclude-dir='testdata' \
    --exclude-dir='vendor' \
    --exclude-dir='.git' \
    --exclude-dir='.plans' \
    . 2>/dev/null \
    | grep -v -E '^[^:]+:[0-9]+:[[:space:]]*//'
) || true

if [ -n "$OFFENDERS" ]; then
  cat >&2 <<EOF
✗ lint-email-literals: raw email literals found.

  Cloudflare's source-pass email obfuscation silently rewrites any literal
  matching an email regex into the placeholder "[email protected]" (no "@")
  which breaks downstream domain extraction and equality checks.

  Wrap them at runtime with defang.MkEmail(local, domain) instead, e.g.:

      addr := defang.MkEmail("noreply", "example.com")

  Or, for fixture loaders and demo commands, build the local+domain and let
  the call site join them.

  Offenders (file:line:snippet):
EOF
  # Indent every offender line for readability.
  echo "$OFFENDERS" | sed 's/^/    /' >&2
  exit 1
fi

echo "✓ lint-email-literals: no raw email literals in non-test source."
exit 0
