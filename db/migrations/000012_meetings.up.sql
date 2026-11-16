-- Video meetings (up to 12 people, camera, microphone and screen sharing) and one-to-one video calls.
-- The media itself goes through the media service (SFU); this only records who is invited and who took part.

CREATE TABLE meetings (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  title      TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  host_id    BIGINT NOT NULL REFERENCES users(id),
  kind       TEXT NOT NULL DEFAULT 'meeting' CHECK (kind IN ('meeting','video_call')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at   TIMESTAMPTZ
);
CREATE INDEX meetings_host_idx ON meetings(host_id, created_at DESC);
CREATE INDEX meetings_open_idx ON meetings(created_at DESC) WHERE ended_at IS NULL;

CREATE TABLE meeting_participants (
  meeting_id UUID   NOT NULL REFERENCES meetings(id) ON DELETE CASCADE,
  user_id    BIGINT NOT NULL REFERENCES users(id),
  status     TEXT   NOT NULL DEFAULT 'invited' CHECK (status IN ('invited','joined','declined','left')),
  invited_by BIGINT REFERENCES users(id),
  invited_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  joined_at  TIMESTAMPTZ,
  left_at    TIMESTAMPTZ,
  PRIMARY KEY (meeting_id, user_id)
);
CREATE INDEX meeting_participants_user_idx ON meeting_participants(user_id, invited_at DESC);
