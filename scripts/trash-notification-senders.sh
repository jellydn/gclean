#!/usr/bin/env bash
# Preview or Trash machine-notification senders via `gclean trash-sender`.
#
#   scripts/trash-notification-senders.sh                 # preview all
#   scripts/trash-notification-senders.sh --yes           # trash all
#   scripts/trash-notification-senders.sh --yes ADDR      # trash one address
#
# See docs/notification-cleanup.md.

set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/trash-notification-senders.sh [--yes] [address]

Preview (default) or Trash exact-sender notification mail.
With no address, uses scripts/notification-senders.txt.
EOF
}

yes=0
sender_arg=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --yes) yes=1; shift ;;
    -h|--help) usage; exit 0 ;;
    --)
      shift
      if [[ $# -gt 0 ]]; then sender_arg=$1; shift; fi
      break
      ;;
    -*) echo "unknown flag: $1" >&2; usage >&2; exit 2 ;;
    *) sender_arg=$1; shift; break ;;
  esac
done
if [[ $# -gt 0 ]]; then
  echo "unexpected extra arguments: $*" >&2
  exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
senders_file=${GCLEAN_NOTIFICATION_SENDERS:-"$root/scripts/notification-senders.txt"}
if [[ -n ${GCLEAN_BIN:-} ]]; then
  gclean=$GCLEAN_BIN
elif [[ -x $root/gclean ]]; then
  gclean=$root/gclean
elif command -v gclean >/dev/null 2>&1; then
  gclean=$(command -v gclean)
else
  echo "gclean not found. Build it with: go build -o gclean ./cmd/gclean" >&2
  exit 1
fi

cache=${GCLEAN_UNDO_CACHE:-"$HOME/.config/gclean/undo-cache.json"}
run_lock=${cache}.notification-cleanup.lock
run_lock_held=0

release_run_lock() {
  if [[ $run_lock_held -eq 1 ]]; then
    rmdir "$run_lock" 2>/dev/null || true
  fi
}

if [[ $yes -eq 1 ]]; then
  mkdir -p "$(dirname "$cache")"
  if ! mkdir "$run_lock" 2>/dev/null; then
    echo "another notification cleanup is already running for $cache" >&2
    exit 1
  fi
  run_lock_held=1
  trap release_run_lock EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
fi

load_senders() {
  if [[ -n $sender_arg ]]; then
    printf '%s\n' "$sender_arg"
    return
  fi
  if [[ ! -f $senders_file ]]; then
    echo "sender list not found: $senders_file" >&2
    exit 1
  fi
  grep -vE '^[[:space:]]*(#|$)' "$senders_file"
}

rotate_undo_if_needed() {
  if [[ $yes -ne 1 || ! -s $cache ]]; then
    return 0
  fi
  local backup
  backup=$(mktemp "${cache}.bak-XXXXXXXX")
  if ! mv "$cache" "$backup"; then
    rm -f "$backup"
    return 1
  fi
  echo "Moved pending undo cache aside to $backup (gclean undo will track only the next batch)."
}

preview_id_from() {
  # Match the copy-paste command printed by `gclean trash-sender`.
  sed -n 's/.*--preview-id \([^[:space:]]*\) --yes.*/\1/p' | tail -1
}

trash_one() {
  local sender=$1
  local out preview_id
  local retries=3
  local attempt=1

  while (( attempt <= retries )); do
    if ! out=$("$gclean" trash-sender "$sender" 2>&1); then
      printf '%s\n' "$out" >&2
      if printf '%s\n' "$out" | grep -qiE 'invalid_grant|cannot fetch token|credentials.json not found'; then
        echo "Auth failed. Run: $gclean login" >&2
      fi
      return 1
    fi
    printf '%s\n' "$out"
    if printf '%s\n' "$out" | grep -q 'Found 0 messages'; then
      return 0
    fi
    if [[ $yes -ne 1 ]]; then
      return 0
    fi
    preview_id=$(printf '%s\n' "$out" | preview_id_from)
    if [[ -z $preview_id ]]; then
      echo "could not parse --preview-id for $sender" >&2
      return 1
    fi
    rotate_undo_if_needed
    if out=$("$gclean" trash-sender "$sender" --preview-id "$preview_id" --yes 2>&1); then
      printf '%s\n' "$out"
      return 0
    fi
    printf '%s\n' "$out" >&2
    if printf '%s\n' "$out" | grep -qi 'preview changed\|was not supplied'; then
      echo "Preview changed for $sender; retrying ($attempt/$retries)."
      attempt=$((attempt + 1))
      continue
    fi
    if printf '%s\n' "$out" | grep -qi 'one recovery batch at a time\|existing recovery record'; then
      echo "Pending undo cache blocked $sender. Move $cache aside or run: $gclean undo" >&2
      return 1
    fi
    return 1
  done
  echo "preview kept changing for $sender after $retries attempts" >&2
  return 1
}

failed=0
while IFS= read -r sender; do
  [[ -z $sender ]] && continue
  echo "==> $sender"
  if ! trash_one "$sender"; then
    failed=1
  fi
done < <(load_senders)

exit "$failed"
