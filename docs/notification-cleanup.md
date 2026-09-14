# Repeatable notification cleanup

Use this when you want to move machine mail — GitHub, LinkedIn, Facebook,
YouTube, bots, `noreply@` — to Gmail Trash without a prior `gclean scan`.
People, banks, receipts, Google/Apple account mail, and Stripe
action-required messages stay out of the default sender list.

Nothing here permanently deletes. Gmail keeps Trash for 30 days.
`gclean undo` restores only the **last** gclean Trash batch.

## Prerequisites

```bash
go build -o gclean ./cmd/gclean
./gclean login    # if `token.json` is missing, expired, or revoked
```

`gclean` keeps one recovery batch at a time. A pending
`~/.config/gclean/undo-cache.json` blocks the next `--yes`. If that file is a
leftover fixture (IDs like `m01`) or you already rely on Gmail Trash for the
previous batch, move it aside using a unique backup name:

```bash
backup=$(mktemp ~/.config/gclean/undo-cache.json.bak-XXXXXXXX)
mv ~/.config/gclean/undo-cache.json "$backup"
```

Do not run another mutating `gclean` command while moving the cache.

## One sender (GitHub notifications)

```bash
./gclean trash-sender notifications@github.com
# Review the count and size, then run the printed command:
./gclean trash-sender notifications@github.com --preview-id <printed-id> --yes
./gclean undo   # restores this batch only
```

`--yes` repeats the Gmail listing and refuses to apply if the cohort changed
(new mail arrived). On a high-volume sender, run the `--yes` command
immediately after the preview. If it reports that the preview changed, preview
again and retry.

This path includes starred, important, recent, and otherwise protected mail
from that exact address.

## The notification sender list

The checked-in list is `scripts/notification-senders.txt`. It is the cohort
used for a live cleanup of machine notifications: GitHub status/`noreply`,
LinkedIn/Facebook/Instagram/YouTube notification addresses, and
dev-tool bots (Vercel, Sentry, Snyk, Heroku, Cronitor, Grafana, …).

Preview every sender (no Gmail writes):

```bash
./scripts/trash-notification-senders.sh
```

Trash every sender that still has mail outside Trash:

```bash
./scripts/trash-notification-senders.sh --yes
```

Trash one address from the list (or any other plain address):

```bash
./scripts/trash-notification-senders.sh --yes notifications@github.com
```

`--yes` allows only one instance of the wrapper per undo-cache path. Do not
run another mutating `gclean` or desktop operation at the same time.

With more than one sender, the wrapper rotates a pending undo cache out of the
way before each apply so the next sender is not blocked. Each rotated file has
a unique name and is kept next to the live cache. `gclean undo` then restores
**only the last sender**. Use Gmail Trash to recover earlier senders from the
same run.

## What this list does not trash

Keep these out of `scripts/notification-senders.txt` unless you add them on
purpose:

- Bank, card, investment, and cryptocurrency alerts
- Google account and Apple transactional mail
- Stripe, including account-closure / action-required mail
- Recruiting and scheduling services such as Greenhouse and Calendly
- Language-course and retail newsletters (EnglishClass101, TLDR, Uniqlo, …)
- Receipts and order confirmations (Grab, Foodpanda, …)

Edit the sender file to add or remove addresses, then re-run the script.
Input must stay one plain address per line. Query syntax and display names
are rejected by `trash-sender`.
