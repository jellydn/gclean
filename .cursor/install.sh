#!/usr/bin/env bash
# Idempotent Cloud Agent bootstrap for gclean.
#
# Runs after the repository is checked out. Primes the Go module + build caches
# and installs the two extra dev CLIs the documented `just check` gate expects
# (`just`, `golangci-lint`). Safe to run repeatedly: every install is guarded by
# a version check, so a warm snapshot short-circuits the downloads.
set -euo pipefail

JUST_VERSION="1.58.0"
GOLANGCI_LINT_VERSION="v2.13.1"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Choose a bin dir that is already on PATH. Prefer /usr/local/bin (via sudo when
# needed); fall back to the Go bin dir if we cannot write a system location.
bin_dir="/usr/local/bin"
sudo_cmd=""
if [ -w "$bin_dir" ]; then
  sudo_cmd=""
elif command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
  sudo_cmd="sudo"
else
  bin_dir="$(go env GOPATH)/bin"
  mkdir -p "$bin_dir"
  # The Go bin dir is not on PATH by default. Persist it for later shells
  # (idempotently): .bashrc for interactive bash, .profile for login shells
  # (sh/dash included). Export it for this run too.
  [ -f "$HOME/.bashrc" ] || [ -f "$HOME/.profile" ] || touch "$HOME/.bashrc"
  for rc_file in "$HOME/.bashrc" "$HOME/.profile"; do
    [ -f "$rc_file" ] || continue
    grep -Fqs "export PATH=\"$bin_dir" "$rc_file" 2>/dev/null || \
      printf '\n# gclean cloud-agent tools\nexport PATH="%s:$PATH"\n' "$bin_dir" >> "$rc_file"
  done
  export PATH="$bin_dir:$PATH"
fi

echo "==> Priming Go module cache"
go mod download

if ! command -v just >/dev/null 2>&1 \
  || [ "$(just --version 2>/dev/null | awk '{print $2}')" != "$JUST_VERSION" ]; then
  echo "==> Installing just $JUST_VERSION -> $bin_dir"
  # The official installer refuses to overwrite an existing binary, so remove
  # any stale one first.
  $sudo_cmd rm -f "$bin_dir/just"
  curl --proto '=https' --tlsv1.2 -sSf https://just.systems/install.sh \
    | $sudo_cmd bash -s -- --tag "$JUST_VERSION" --to "$bin_dir"
else
  echo "==> just already present: $(just --version)"
fi

if ! command -v golangci-lint >/dev/null 2>&1 \
  || [ "$(golangci-lint version 2>/dev/null | sed -n 's/.*version v\?\([^ ]*\).*/\1/p')" != "${GOLANGCI_LINT_VERSION#v}" ]; then
  echo "==> Installing golangci-lint $GOLANGCI_LINT_VERSION -> $bin_dir"
  tmp_bin="$(mktemp -d)"
  GOBIN="$tmp_bin" go install \
    "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
  $sudo_cmd install -m 0755 "$tmp_bin/golangci-lint" "$bin_dir/golangci-lint"
  rm -rf "$tmp_bin"
else
  echo "==> golangci-lint already present: $(golangci-lint --version)"
fi

echo "==> Warming build cache (go build ./...)"
go build ./...

echo "==> gclean environment ready"
