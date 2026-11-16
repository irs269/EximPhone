# Internal mail

Mail between the employees of the platform, hosted on the bank's own servers. It works like a mailbox (folders, drafts, conversations,
attachments, search) but stays inside the platform: it can also **send to and receive from external addresses** through the bank's existing mail server (see "External mail"; optional).

## Features

- **Compose**: recipients in **To, Cc and Bcc** (autocomplete over the directory, up to 50), subject, text body, **attachments** (up to 10 files, 20 MB each, 25 MB per message).
- **Drafts**: saved by hand or automatically 8 seconds after the last change; re-opened from the Drafts folder; discarded for good while unsent.
- **Conversations**: replying or forwarding keeps the message in the same conversation; the list shows one line per conversation with the number of messages.
- **Reply, reply all, forward** (quoted text prefilled; forwarding brings the attachments along).
- **Folders** per user: Inbox, Starred, Sent, Drafts, Archive, Trash. Read/unread, star, archive, delete, restore, **delete forever**.
- **Search** in subject, body, sender and recipients (over the 1,000 most recent messages of the folder).
- **Real time**: the Mail badge counts unread inbox messages and a notification announces each new message.
- French and English interface.

## Rules

- **Bcc is private**: recipients never see the Bcc list; only the sender does.
- **Each person owns their copy**: archiving, deleting or reading never affects another participant. "Delete forever" hides the message **for that user only**; the record is kept (logical deletion, like chat).
- Mailing yourself puts the message in both Sent and Inbox.
- A message is only visible to its participants (anyone else gets `404`).
- Attachments: executables and scripts (`.exe .bat .cmd .com .scr .msi .dll .js .vbs .ps1 .jar .lnk .sh .apk .hta .reg`) are refused; the content type is sniffed server side; downloads are always forced as attachments with `nosniff`.
- Messages are plain text (no HTML), so there is no script or tracking-pixel risk when reading.

## Security and traceability

- Subject and body are encrypted at rest (AES-256-GCM, key `MESSAGE_KEY`, bound to the message id); attachments are encrypted in the object store (the same mechanism as chat attachments). Back up `MESSAGE_KEY` separately from the database.
- Audit log: `mail_sent` (message id and number of recipients, never the content) and `mail_purged`.

## API (all under `/api`, authenticated)

| Method and path | Purpose |
|---|---|
| `GET /mail/folders` | counters: `unread_inbox`, `drafts`, `starred` |
| `GET /mail/messages?folder=&q=&page=` | conversations of a folder (`inbox starred sent drafts archive trash`), 30 per page, `more` tells if there is a next page |
| `GET /mail/threads/{id}` | a whole conversation, decrypted, with recipients and attachments |
| `POST /mail/drafts` | create or update a draft `{id?, to, cc, bcc, subject, body, parent_id?, forward_of?, attachment_ids}` |
| `DELETE /mail/drafts/{id}` | discard an unsent draft |
| `POST /mail/drafts/{id}/send` | send a draft |
| `POST /mail/send` | save and send in one call (same body as drafts) |
| `POST /mail/batch` | `{ids, action}` with `read unread star unstar archive trash restore purge`, applied to the caller's own copies |
| `POST /mail/attachments`, `GET /mail/attachments/{id}` | upload (multipart `file`) and download |

Tested end to end by `tests/e2e/mail.mjs` (54 checks: delivery, Bcc privacy, conversations, folders, drafts, attachments, forwarding, search, access control, pagination).

## External mail (through the bank's mail server)

Optional. Without the settings below, the platform stays internal and refuses external addresses. With them:

- **Outgoing**: type any e-mail address in To, Cc or Bcc. Messages with external recipients are queued (`mail_outbox`) and handed to an **SMTP relay**.
  The message is a standard MIME message (UTF-8, attachments, `Message-ID`, `In-Reply-To`); Bcc recipients are in the SMTP envelope only.
  Retries: 1 min, 5 min, 30 min, 2 h, 6 h; a permanent refusal (5xx) or the last failure tells the sender with a "Delivery failure" message in the inbox.
  An address that belongs to a platform user is delivered **internally** (no relay).
- **Incoming**: an **IMAP mailbox** is polled (every 30 s by default). Each unread message is filed in the inbox of the platform users it is addressed to (matched by their directory e-mail
  in To, Cc, `Delivered-To`, `X-Original-To`...), with its attachments; HTML-only mail is converted to plain text (no script, no tracking pixel); duplicates (same `Message-ID`) are ignored;
  messages for nobody on the platform are skipped; replies are threaded with `In-Reply-To` and `References`. Executables and scripts are dropped from attachments.
- External senders are shown with their address and an "External" badge; replying to one goes through the relay.

### Settings (environment of the `api` service)

| Variable | Meaning | Default |
|---|---|---|
| `MAIL_SMTP_HOST`, `MAIL_SMTP_PORT` | SMTP relay (enables outgoing) | port by security |
| `MAIL_SMTP_SECURITY` | `starttls`, `tls` or `none` | `starttls` (587) |
| `MAIL_SMTP_USER`, `MAIL_SMTP_PASSWORD` | relay account (PLAIN auth, only over TLS) | none |
| `MAIL_FROM_MODE` | `user`: From = the user's directory e-mail; `fixed`: From = `MAIL_FROM_ADDRESS`, Reply-To = the user | `user` |
| `MAIL_FROM_ADDRESS` | address used in `fixed` mode (or when a user has no e-mail) | none |
| `MAIL_DOMAIN` | domain used in generated `Message-ID` | `exim.local` |
| `MAIL_CA_FILE` | PEM file of the bank's internal CA, to verify the servers' certificates | system roots |
| `MAIL_IMAP_HOST`, `MAIL_IMAP_PORT` | IMAP server (enables incoming) | port by security |
| `MAIL_IMAP_SECURITY` | `tls` (993), `starttls` (143) or `none` | `tls` |
| `MAIL_IMAP_USER`, `MAIL_IMAP_PASSWORD`, `MAIL_IMAP_FOLDER` | the polled mailbox | folder `INBOX` |
| `MAIL_POLL_SECONDS` | polling interval | 30 |

### What the mail server must provide

1. **An SMTP relay** that accepts the platform: its address, port, TLS mode and an account allowed to send. In `user` mode that account must be allowed to send as each employee
   (Exchange "Send As", or a relay by source IP); otherwise use `fixed` mode.
2. **One mailbox for incoming mail**, filled by a rule on the mail server: *forward or Bcc* the mail of the employees who use Exim Connect to that mailbox (a transport rule or alias).
   The platform files each message by the recipients named in the headers. Only one account and password are stored on the platform.
3. The directory (`mail` attribute) must hold each employee's address: the platform uses it to match incoming mail and to build the From of outgoing mail.
4. SPF/DKIM/DMARC: the relay signs and sends as usual; if `From` addresses differ from the relay's domain, allow the platform server in the domain's SPF.

### Testing

The dev stack includes **GreenMail** (SMTP 3025, IMAP 3143 on localhost, plain) wired to the api by `docker-compose.dev.yml`.
`tests/e2e/mail-external.mjs` (22 checks) relays real messages through it and injects incoming ones: MIME, Bcc privacy, UTF-8, attachments, duplicates, HTML, unknown recipients, threading.

### Not built

Per-user IMAP mailboxes (the design uses one service mailbox), rich text composition, custom folders and labels, signatures, out-of-office replies, calendar invitations, shared mailboxes, spam filtering (left to the mail server).
