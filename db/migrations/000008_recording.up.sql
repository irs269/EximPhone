-- Phase 7: call recording (per department rule), encrypted attachments.
CREATE TABLE recording_rules (
  department_id INT PRIMARY KEY REFERENCES departments(id),
  enabled       BOOLEAN NOT NULL DEFAULT TRUE
);

ALTER TABLE calls ADD COLUMN recorded BOOLEAN NOT NULL DEFAULT FALSE;

-- One row per recorded direction of a call. The object in storage is AES-256-GCM sealed.
CREATE TABLE call_recordings (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  call_id         UUID NOT NULL REFERENCES calls(id),
  track           TEXT NOT NULL CHECK (track IN ('caller', 'callee')),
  codec           TEXT NOT NULL,
  s3_key          TEXT NOT NULL,
  bytes           BIGINT NOT NULL,
  duration_sec    INT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (call_id, track)
);
CREATE INDEX call_recordings_created ON call_recordings (created_at);

-- New attachments are stored encrypted; older ones (encrypted = false) stay readable.
ALTER TABLE attachments ADD COLUMN encrypted BOOLEAN NOT NULL DEFAULT FALSE;
