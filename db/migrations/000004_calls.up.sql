-- Phase 4: call detail records (CDR). Call state lives here; live routing state lives in Redis.
CREATE TABLE calls (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  caller_id        BIGINT NOT NULL REFERENCES users(id),
  callee_id        BIGINT NOT NULL REFERENCES users(id),
  state            TEXT NOT NULL CHECK (state IN ('ringing', 'active', 'ended')),
  outcome          TEXT CHECK (outcome IN ('answered','missed','rejected','cancelled','busy','dnd','offline','transferred')),
  transferred_from UUID REFERENCES calls(id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  answered_at      TIMESTAMPTZ,
  ended_at         TIMESTAMPTZ,
  duration_sec     INT
);
CREATE INDEX calls_caller ON calls (caller_id, created_at DESC);
CREATE INDEX calls_callee ON calls (callee_id, created_at DESC);
