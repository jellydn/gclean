#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fake="$tmp/gclean"
log="$tmp/calls.log"
cache="$tmp/undo-cache.json"

cat >"$fake" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$GCLEAN_TEST_LOG"
if [[ " $* " == *" --yes "* ]]; then
  printf '{"records":[{"id":"new"}]}\n' >"$GCLEAN_UNDO_CACHE"
  echo "Moved 1 message to Trash."
else
  echo "Found 1 messages from $2 (1 KB estimated)."
  echo "Nothing changed. Run gclean trash-sender '$2' --preview-id preview-1 --yes"
fi
EOF
chmod +x "$fake"

export GCLEAN_BIN="$fake"
export GCLEAN_TEST_LOG="$log"
export GCLEAN_UNDO_CACHE="$cache"

bash "$root/scripts/trash-notification-senders.sh" test@example.com >/dev/null
if grep -q -- '--yes' "$log"; then
  echo "preview mode invoked an apply" >&2
  exit 1
fi

: >"$log"
printf '{"records":[{"id":"old-1"}]}\n' >"$cache"
bash "$root/scripts/trash-notification-senders.sh" --yes test@example.com >/dev/null
printf '{"records":[{"id":"old-2"}]}\n' >"$cache"
bash "$root/scripts/trash-notification-senders.sh" --yes test@example.com >/dev/null

backup_count=$(find "$tmp" -name 'undo-cache.json.bak-*' -type f | wc -l | tr -d ' ')
if [[ $backup_count -ne 2 ]]; then
  echo "expected two unique undo backups, found $backup_count" >&2
  exit 1
fi

mkdir "${cache}.notification-cleanup.lock"
if bash "$root/scripts/trash-notification-senders.sh" --yes test@example.com >/dev/null 2>&1; then
  echo "concurrent apply was not rejected" >&2
  exit 1
fi

echo "notification cleanup wrapper tests passed"
