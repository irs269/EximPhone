-- Phase 3: conversations, encrypted messages, receipts, attachments.

CREATE TABLE conversations (
  id          BIGSERIAL PRIMARY KEY,
  kind        TEXT NOT NULL CHECK (kind IN ('direct', 'group')),
  name        TEXT,
  direct_key  TEXT UNIQUE,                 -- "minId:maxId" so a direct chat exists once per pair
  created_by  BIGINT REFERENCES users(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((kind = 'group' AND name IS NOT NULL) OR (kind = 'direct' AND direct_key IS NOT NULL))
);

CREATE TABLE conversation_members (
  conversation_id BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  user_id         BIGINT NOT NULL REFERENCES users(id),
  last_read_id    BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_members_user ON conversation_members (user_id);

CREATE TABLE attachments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  conversation_id BIGINT NOT NULL REFERENCES conversations(id),
  uploader_id     BIGINT NOT NULL REFERENCES users(id),
  filename        TEXT NOT NULL,
  mime            TEXT NOT NULL,
  size            BIGINT NOT NULL,
  s3_key          TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- body is AES-256-GCM sealed (nonce||ciphertext); rows are never physically deleted.
CREATE TABLE messages (
  id              BIGSERIAL PRIMARY KEY,
  conversation_id BIGINT NOT NULL REFERENCES conversations(id),
  sender_id       BIGINT NOT NULL REFERENCES users(id),
  body            BYTEA NOT NULL,
  attachment_id   UUID REFERENCES attachments(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ,
  deleted_by      BIGINT REFERENCES users(id)
);
CREATE INDEX messages_conversation ON messages (conversation_id, id DESC);

CREATE TABLE message_receipts (
  message_id   BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
  user_id      BIGINT NOT NULL REFERENCES users(id),
  delivered_at TIMESTAMPTZ,
  read_at      TIMESTAMPTZ,
  PRIMARY KEY (message_id, user_id)
);
CREATE INDEX message_receipts_user_unread ON message_receipts (user_id) WHERE read_at IS NULL;

-- Messages are an archive: block physical deletion (logical deletion sets deleted_at).
CREATE FUNCTION messages_no_delete() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'messages are archived and cannot be physically deleted'; END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER messages_archive BEFORE DELETE ON messages FOR EACH ROW EXECUTE FUNCTION messages_no_delete();
