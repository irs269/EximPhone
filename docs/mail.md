# Internal mail

Mail between the employees of the platform, hosted on the bank's own servers. It works like a mailbox (folders, drafts, conversations,
attachments, search) but stays inside the platform: **it does not send to or receive from the Internet yet** (see "Next: external mail").

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

## Next: external mail (not built)

Sending to and receiving from outside the platform needs the bank's existing mail server. The planned design keeps this module as is and adds a connector:

1. **Outgoing**: an SMTP relay on the existing server (`MAIL_SMTP_*` settings), used when a recipient is an external address.
2. **Incoming**: reading the mailboxes of the employees over IMAP (service account or per-user) and storing the messages in the same tables.
3. Mapping between directory users and their e-mail addresses (the directory already provides `mail`).

It needs from the bank: the server type and host, the ports and security (TLS), and an account allowed to relay. Other open items: rich text (HTML) composition, custom folders and labels, signatures, out-of-office replies, calendar invitations, shared mailboxes.
