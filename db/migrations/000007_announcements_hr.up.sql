-- Phase 6: announcements with read receipts, early-departure validation, "direction" role.
INSERT INTO roles(name) VALUES ('direction') ON CONFLICT DO NOTHING;

ALTER TABLE attendance_days
  ADD COLUMN early_validation  TEXT CHECK (early_validation IN ('approved', 'rejected')),
  ADD COLUMN validated_by      BIGINT REFERENCES users(id),
  ADD COLUMN validated_at      TIMESTAMPTZ,
  ADD COLUMN validation_comment TEXT;

CREATE TABLE announcements (
  id          BIGSERIAL PRIMARY KEY,
  title       TEXT NOT NULL,
  body        TEXT NOT NULL,
  level       TEXT NOT NULL CHECK (level IN ('normal', 'urgent')),
  target_kind TEXT NOT NULL CHECK (target_kind IN ('all', 'department', 'site')),
  target_id   INT,
  author_id   BIGINT NOT NULL REFERENCES users(id),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK ((target_kind = 'all' AND target_id IS NULL) OR (target_kind <> 'all' AND target_id IS NOT NULL))
);
CREATE INDEX announcements_created ON announcements (created_at DESC);

CREATE TABLE announcement_acks (
  announcement_id BIGINT NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
  user_id         BIGINT NOT NULL REFERENCES users(id),
  acked_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (announcement_id, user_id)
);
