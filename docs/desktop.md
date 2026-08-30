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
5. Download the JSON file and save it as:
   - macOS/Linux: `~/.config/gclean/credentials.json`
   - Windows: set `GCLEAN_CREDENTIALS_PATH` to its absolute location
6. Run `gclean desktop`, select **Connect with Google**, and complete the
   browser flow. The refresh token is stored locally with restrictive file
   permissions at `~/.config/gclean/token.json` (override with
   `GCLEAN_TOKEN_PATH`).

The default grant is `gmail.modify`. It permits metadata reads and moving mail
to/from Trash without granting permanent-delete access. gclean never requests
or downloads message bodies.

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
- Every API call requires a cryptographically random process-local token.
- CSP, clickjacking, MIME-sniffing, and referrer protections are enabled.
- OAuth callbacks verify a random state value.
- Selection changes always re-run the planner; non-junk remains undeletable.
- Trash requires a typed phrase and a hash of the exact reviewed cohort. A
  changed preview returns a conflict instead of applying stale choices.
- Mutations are serialized, the undo cache is integrity checked, and partial
  Gmail failures reconcile back into local state.
- Restore acts only on the last gclean batch. Purge is disabled by default.

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
