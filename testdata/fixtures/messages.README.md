# `testdata/fixtures/messages.json`

40-message fixture corpus used by `TestScanCommand_DevFixturePipeline`
(reads it as the `--fixtures` input for `gclean scan`) and the
development end-to-end recipe (`just e2e`).

## Why this file exists

The fixture gives `gclean scan` real-shaped input without OAuth. The
FakeClient reads it through `NewFakeClient(path)` and the same JSON
tags also accept live Gmail API responses (one struct, two sources).

## Sender diversity

Each `sender.email` value pairs with its `sender.name` to exercise
the classifier's vendor table (`internal/engine/classifier.go`). The
corpus has 40 distinct sender addresses spanning:

- **Vendor notifications**: GitHub (3×), Stripe (2×), AWS Billing,
  Azure, GitLab, Jira, Slack, GitGuardian
- **Social networks**: LinkedIn (2×), Reddit, X, Facebook, Twitter
- **Newsletters / mailing lists**: Marketing Newsletter (2×),
  Pragmatic Engineer, golangweekly
- **Spam-bucket markers**: Phishing Test (Precedence:bulk), Spam
  (Precedence:junk + SPAM label), Pharma Spam (SPAM label), Survey
  Bot (Precedence:bulk)
- **Personal / contacts**: Daughter, Wife, Manager, Colleague,
  Customer, Old Colleague (all `isContact:true`, expected to land in
  the Keep cohort under default config since
  `KeepConfig.Contacts=true`)
- **Self-sent**: Me (SENT label, expected to land in Keep via
  `KeepConfig.SentByUser=true`)
- **Bank-style statement**: Bank of Example (NOT covered by
  `keep ignore: bank.com` — domain is `bank.example.com`, intentional
  for fixture diversity)

## Why `internal/cli/cli_test.go` includes `TestMessagesJSON_HasNoPlaceholder`

Cloudflare's email-obfuscation source-pass silently rewrites any
literal `local@domain.tld` in source files into the placeholder
`[email protected]` (with the `@` stripped). A previous version of
this file was hit by that pass; every `sender.email` collapsed to
the same string, so `SendersByVolume` reported only one row. The
regression test reads the fixture as bytes and fails loudly if the
placeholder appears anywhere.

If you regenerate this fixture (e.g. via a `make fixture` script):
**construct every address at runtime by joining `local + "@" + domain`**
in your generator's source — never commit a literal `local@domain` if
the generator's output passes through any pipeline that touches
Cloudflare's email-obfuscation pass. The `engine.MkEmail` style join
used in `internal/engine/testutil.go` is the in-Go reference.

## What the JSON must contain for tests to be meaningful

- 40 messages (id m01..m40, no gaps)
- ≥30 distinct `sender.email` values (current count: ~30)
- ≥3 messages with `isContact:true` (5 currently: m23, m24, m25,
  m28, m29, m39)
- ≥3 messages with `labels` containing STARRED / IMPORTANT / SENT /
  REPLIED (the protector's keep paths)
- ≥1 message with `size > 10MB` for the `Attachments >10MB` stats
  line (m38 currently has size=15728640)

The diversity assertion in `TestMessagesJSON_HasNoPlaceholder`
verifies the distinct-email count.
