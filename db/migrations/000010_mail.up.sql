-- Internal mail. A message is written by one sender, addressed to several recipients (to / cc / bcc) and
-- lives in each participant's own mailbox (folder, read and star state are per user).
-- Subject and body are sealed (AES-256-GCM, key MESSAGE_KEY) like chat messages; attachments are sealed in the object store.

CREATE TABLE mail_messages (
  id         BIGSERIAL PRIMARY KEY,
  thread_id  BIGINT NOT NULL,                       -- id of the first message of the conversation
  parent_id  BIGINT REFERENCES mail_messages(id),   -- the message this one answers or forwards
  sender_id  BIGINT NOT NULL REFERENCES users(id),
  subject    BYTEA NOT NULL,
  body       BYTEA NOT NULL,
  sent_at    TIMESTAMPTZ,                           -- NULL while it is still a draft
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mail_messages_thread_idx ON mail_messages(thread_id);

CREATE TABLE mail_recipients (
  message_id BIGINT NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
  user_id    BIGINT NOT NULL REFERENCES users(id),
  kind       TEXT   NOT NULL CHECK (kind IN ('to','cc','bcc')),
  PRIMARY KEY (message_id, user_id)
);
CREATE INDEX mail_recipients_user_idx ON mail_recipients(user_id);

-- One row per participant and role: a user who mails themselves has a 'sender' row (Sent) and a 'recipient' row (Inbox).
CREATE TABLE mail_boxes (
  message_id   BIGINT  NOT NULL REFERENCES mail_messages(id) ON DELETE CASCADE,
  user_id      BIGINT  NOT NULL REFERENCES users(id),
  role         TEXT    NOT NULL CHECK (role IN ('sender','recipient')),
  folder       TEXT    NOT NULL CHECK (folder IN ('inbox','sent','drafts','archive','trash')),
  trashed_from TEXT,
  is_read      BOOLEAN NOT NULL DEFAULT FALSE,
  starred      BOOLEAN NOT NULL DEFAULT FALSE,
  purged       BOOLEAN NOT NULL DEFAULT FALSE,      -- "deleted forever" by this user: hidden, but the record is kept
  PRIMARY KEY (message_id, user_id, role)
);
CREATE INDEX mail_boxes_user_idx ON mail_boxes(user_id, folder) WHERE NOT purged;

CREATE TABLE mail_attachments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  blob_id     UUID NOT NULL,                        -- object key and encryption context; shared by forwarded copies
  message_id  BIGINT REFERENCES mail_messages(id) ON DELETE CASCADE,   -- NULL until attached to a draft
  uploader_id BIGINT NOT NULL REFERENCES users(id),
  filename    TEXT   NOT NULL,
  mime        TEXT   NOT NULL,
  size        BIGINT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX mail_attachments_message_idx ON mail_attachments(message_id);
