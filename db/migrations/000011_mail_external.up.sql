-- External mail through the bank's existing mail server: outgoing via an SMTP relay, incoming via an IMAP mailbox.

-- A received message has no platform sender: it carries the external address instead.
ALTER TABLE mail_messages ALTER COLUMN sender_id DROP NOT NULL;
ALTER TABLE mail_messages
  ADD COLUMN source           TEXT NOT NULL DEFAULT 'internal' CHECK (source IN ('internal','inbound','system')),
  ADD COLUMN external_name    TEXT,
  ADD COLUMN external_address TEXT,
  ADD COLUMN header_id        TEXT;                  -- RFC 5322 Message-ID, used for de-duplication and threading
CREATE UNIQUE INDEX mail_messages_header_id_idx ON mail_messages(header_id) WHERE header_id IS NOT NULL;
ALTER TABLE mail_messages ADD CONSTRAINT mail_sender_or_external CHECK (sender_id IS NOT NULL OR external_address IS NOT NULL);

-- Attachments of received messages have no uploader.
ALTER TABLE mail_attachments ALTER COLUMN uploader_id DROP NOT NULL;

-- Recipients outside the platform (outgoing), or header-only recipients of a received message.
CREATE TABLE mail_external_recipients (
  message_id BIGINT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
  address    TEXT   NOT NULL,
  name       TEXT,
  kind       TEXT   NOT NULL CHECK (kind IN ('to','cc','bcc')),
  PRIMARY KEY (message_id, address)
);

-- Outgoing queue: one row per message that has external recipients; retried with a back-off, then reported to the sender.
CREATE TABLE mail_outbox (
  id              BIGSERIAL PRIMARY KEY,
  message_id      BIGINT NOT NULL UNIQUE REFERENCES mail_messages(id) ON DELETE CASCADE,
  status          TEXT   NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
  attempts        INT    NOT NULL DEFAULT 0,
  last_error      TEXT,
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at         TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mail_outbox_pending_idx ON mail_outbox(next_attempt_at) WHERE status = 'pending';
