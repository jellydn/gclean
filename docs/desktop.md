# Desktop setup, security, and packaging

`gclean desktop` is a local desktop companion built into the normal Go binary.
It serves embedded HTML/CSS/JavaScript on a random IPv4 loopback port and opens
that page in the default browser. This architecture keeps the existing Go
engine, OAuth, SQLite, and Gmail adapters as the only backend, has no Node or
CGO runtime, and cross-compiles as one portable executable.

## One-time Gmail setup

1. Create or select a project in [Google Cloud Console](https://console.cloud.google.com/).
2. Enable **Gmail API**.
3. Configure the OAuth consent screen. While the app is in testing mode, add
   each Gmail account under **Test users**.
4. Create **Credentials → OAuth client ID → Desktop app**.
5. Download the JSON file. In **Settings → Account connection**, choose that
   file. gclean validates it as a Desktop OAuth client and stores it at the
   resolved credentials path with restrictive permissions.
6. Select **Connect / reconnect** and complete the
   browser flow. The refresh token is stored locally with restrictive file
   permissions at `~/.config/gclean/token.json` (override with
   `GCLEAN_TOKEN_PATH`).

The default grant is `gmail.modify`. It permits metadata reads and moving mail
to/from Trash without granting permanent-delete access. gclean never requests
or downloads message bodies. Login requests offline access so the desktop app
can refresh an expired access token without asking the user to reconnect.

## Settings

The Settings page is part of the same embedded interface and API—not a second
configuration system. It edits the existing YAML document used by every CLI
command:

- **Always keep:** contacts, replied conversations, Starred, Important, Sent,
  and the recent-mail protection window (0–3650 days).
- **Advanced rules:** one validated delete or archive DSL rule per line and one
  protected sender domain per line. Invalid keys, durations, sizes, and domains
  are rejected without replacing the previous file.
- **Reset defaults:** restores the repository's safe default protections and
  rules after an explicit confirmation.
- **OAuth:** shows only presence/status booleans, supports Desktop credential
  file selection, starts least-privilege connect/reconnect, and removes the
  local token on disconnect. Credential and token contents are never returned
  by the API or rendered in the UI.
- **Local files:** displays resolved paths and names active environment
  overrides. Paths are process startup settings; change the environment and
  restart gclean. The UI deliberately does not move databases or recovery
  files while the app is running.

Saving cleanup settings immediately refreshes the preview and never changes
Gmail. Replacing OAuth credentials disconnects the old local session and
requires re-authentication. Permanent deletion is not a persisted checkbox;
it remains behind the separate authorization and startup gates below.

### Configuration and startup controls

| Setting | Default | Desktop behavior |
| --- | --- | --- |
| `GCLEAN_CONFIG_PATH` | platform config directory + `gclean/config.yaml` | Settings writes this CLI-compatible YAML atomically; restart after changing the override |
| `GCLEAN_DB_PATH` | platform config directory + `gclean/gclean.db` | Read-only path diagnostic; use a separate path for each Gmail account |
| `GCLEAN_UNDO_CACHE` | platform config directory + `gclean/undo-cache.json` | Read-only recovery path; account-bound and integrity checked |
| `GCLEAN_SELECTION_PATH` | platform config directory + `gclean/tui-selection.json` | Read-only TUI interoperability diagnostic |
| `GCLEAN_CREDENTIALS_PATH` | platform config directory + `gclean/credentials.json` | Credential chooser securely replaces this file; restart after changing the override |
| `GCLEAN_TOKEN_PATH` | platform config directory + `gclean/token.json` | Connect/disconnect uses this path; restart after changing the override |
| `desktop --no-browser` | off | Startup-only headless option; the UI cannot change browser launch retroactively |
| `desktop --fixtures PATH` | off | Developer/test mode; Settings marks OAuth controls unavailable |
| `desktop --allow-purge` | off | Startup-only destructive capability gate; also requires full-access re-authentication |

CLI mutation flags such as `clean --yes` and `purge --yes` are intentionally not
desktop preferences. The GUI always uses its own preview, exact cohort hash,
typed Trash/permanent confirmation, and operation progress/error states.

### Optional permanent purge

Emptying Gmail Trash is global, irreversible, and may include messages that
gclean did not put there. It therefore requires two separate opt-ins:

```bash
gclean logout
gclean login --allow-permanent-delete  # requests mail.google.com
gclean desktop --allow-purge           # exposes the disabled-by-default control
```

The UI then requires typing `EMPTY TRASH PERMANENTLY`. Normal cleanup never
needs this scope or flag: it moves messages to Trash and supports restore.

## Safety and privacy architecture

```diagram
┌──────────────────┐   session token   ┌────────────────────┐
│ Default browser  │◀─────────────────▶│ 127.0.0.1 random   │
│ Embedded UI      │    same origin    │ Go HTTP handler    │
└──────────────────┘                   └─────────┬──────────┘
                                               │
                     ┌─────────────────────────┼────────────────────┐
                     ▼                         ▼                    ▼
              ┌────────────┐           ┌──────────────┐     ┌────────────┐
              │ Planner    │           │ Local SQLite │     │ Gmail API  │
              │ safeguards │           │ metadata only│     │ OAuth      │
              └────────────┘           └──────────────┘     └────────────┘
```

- Server binds only to `127.0.0.1` on an OS-assigned port.
- Requests with any other `Host` are rejected, and mutations require the exact
  same-origin `Origin`, preventing DNS-rebinding access to the session token.
- Every API call requires a cryptographically random process-local token.
- CSP, clickjacking, MIME-sniffing, and referrer protections are enabled.
- OAuth callbacks verify a random state value.
- Selection changes always re-run the planner; non-junk remains undeletable.
- Trash requires a typed phrase and a hash of the exact reviewed cohort. A
  changed preview returns a conflict instead of applying stale choices.
- Mutations are serialized, the undo cache is integrity checked, and partial
  Gmail failures reconcile back into local state. An OS-level lock extends
  serialization across CLI and desktop processes.
- The SQLite database and each undo batch are bound to the authenticated Gmail
  account. Account mismatches fail before Gmail is changed, and scans replace
  metadata transactionally rather than merging rows from different accounts.
- Restore acts only on the last gclean batch. Purge is disabled by default.

### Upgrade safety

Older databases and undo caches did not record a Gmail account identity. gclean
will not guess ownership:

- For an existing unowned database, choose a new `GCLEAN_DB_PATH` and rescan.
- Restore or remove a legacy undo cache with the previous gclean version before
  using this version for mutations.
- Existing OAuth tokens have no trusted scope profile and therefore cannot
  enable purge. Run `gclean login --allow-permanent-delete` again if permanent
  Empty Trash is genuinely required.

## Build and package

Requirements: Go 1.26.4 or newer. No frontend toolchain is required.

```bash
go build -trimpath -o gclean ./cmd/gclean
just check

# Build portable archives for macOS, Windows, and Linux:
./scripts/package-desktop.sh
```

Archives and `SHA256SUMS` are written to `dist/`. Set `VERSION` to control the
archive names. The packaging script creates unsigned portable binaries; it
does not publish anything.

### Platform notes

- **macOS:** first-party distribution should codesign and notarize the binary.
  An unsigned local build may require **Open Anyway** in Privacy & Security.
- **Windows:** an unsigned download may trigger SmartScreen. Signing with an
  Authenticode certificate is recommended before public distribution.
- **Linux:** browser launch uses `xdg-open` and otherwise prints the local URL.
- **Headless/remote hosts:** run `gclean desktop --no-browser` and access the UI
  only from that same machine. The server intentionally cannot bind publicly.

Native `.app`, `.msi`, and signed installer generation is release engineering;
the portable binary provides the complete desktop workflow on all platforms.
