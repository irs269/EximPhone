-- Phase 5: SIP devices (desk phones, softphones), registrations, call delegation.
CREATE TABLE devices (
  id             SERIAL PRIMARY KEY,
  user_id        BIGINT NOT NULL REFERENCES users(id),
  type           TEXT NOT NULL CHECK (type IN ('desk_phone', 'softphone')),
  vendor         TEXT NOT NULL DEFAULT 'generic' CHECK (vendor IN ('yealink', 'grandstream', 'fanvil', 'generic')),
  model          TEXT,
  mac            TEXT UNIQUE CHECK (mac ~ '^[0-9a-f]{12}$'),     -- lower case, no separators
  sip_username   TEXT UNIQUE NOT NULL,                            -- digest auth name (unique per device)
  sip_secret_enc BYTEA NOT NULL,                                  -- AES-256-GCM sealed; never stored in clear
  active         BOOLEAN NOT NULL DEFAULT TRUE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX devices_user ON devices (user_id);

-- Live bindings are kept in Redis for speed; this table mirrors them for administration.
CREATE TABLE sip_registrations (
  device_id   INT PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
  source_addr TEXT NOT NULL,
  contact     TEXT NOT NULL,
  user_agent  TEXT,
  expires_at  TIMESTAMPTZ NOT NULL,
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- "Away" status forwards calls to this colleague.
ALTER TABLE users ADD COLUMN delegate_user_id BIGINT REFERENCES users(id);
