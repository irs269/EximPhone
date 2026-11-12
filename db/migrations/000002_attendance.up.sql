-- Phase 1: identity, sites, schedules, attendance, audit.

CREATE TABLE sites (
  id         SERIAL PRIMARY KEY,
  code       TEXT UNIQUE NOT NULL,
  name       TEXT NOT NULL,
  ip_ranges  CIDR[] NOT NULL DEFAULT '{}',
  timezone   TEXT NOT NULL DEFAULT 'Africa/Dar_es_Salaam'
);

CREATE TABLE departments (
  id   SERIAL PRIMARY KEY,
  name TEXT UNIQUE NOT NULL
);

CREATE TABLE roles (name TEXT PRIMARY KEY);
INSERT INTO roles(name) VALUES ('employee'),('manager'),('hr'),('communication'),('it_admin'),('auditor');

CREATE TABLE users (
  id            BIGSERIAL PRIMARY KEY,
  username      CITEXT UNIQUE NOT NULL,        -- AD sAMAccountName / uid
  display_name  TEXT NOT NULL,
  email         CITEXT,
  extension     TEXT UNIQUE,
  department_id INT REFERENCES departments(id),
  site_id       INT REFERENCES sites(id),
  manager_id    BIGINT REFERENCES users(id),
  active        BOOLEAN NOT NULL DEFAULT TRUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX users_name_trgm ON users USING gin (display_name gin_trgm_ops);

CREATE TABLE user_roles (
  user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
  role    TEXT REFERENCES roles(name),
  PRIMARY KEY (user_id, role)
);

-- Work rules per site (all times are local site time).
CREATE TABLE work_schedules (
  id             SERIAL PRIMARY KEY,
  site_id        INT UNIQUE NOT NULL REFERENCES sites(id),
  start_time     TIME NOT NULL DEFAULT '08:00',
  tolerance_min  INT  NOT NULL DEFAULT 15,
  end_time       TIME NOT NULL DEFAULT '17:00'
);

CREATE TYPE arrival_status AS ENUM ('on_time', 'late');
CREATE TYPE departure_status AS ENUM ('normal', 'early', 'not_clocked');

CREATE TABLE attendance_days (
  id               BIGSERIAL PRIMARY KEY,
  user_id          BIGINT NOT NULL REFERENCES users(id),
  work_date        DATE NOT NULL,              -- local date at the site
  clock_in         TIMESTAMPTZ NOT NULL,       -- UTC
  clock_out        TIMESTAMPTZ,
  arrival          arrival_status NOT NULL,
  departure        departure_status,
  early_reason     TEXT,
  duration_min     INT,
  last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, work_date)
);
-- At most one open day per employee, whatever the number of devices.
CREATE UNIQUE INDEX one_open_day_per_user ON attendance_days (user_id) WHERE clock_out IS NULL;
CREATE INDEX attendance_work_date ON attendance_days (work_date);

CREATE TABLE attendance_corrections (
  id            BIGSERIAL PRIMARY KEY,
  day_id        BIGINT NOT NULL REFERENCES attendance_days(id),
  corrected_by  BIGINT NOT NULL REFERENCES users(id),
  justification TEXT NOT NULL,
  old_values    JSONB NOT NULL,
  new_values    JSONB NOT NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only audit log: UPDATE and DELETE are rejected by trigger.
CREATE TABLE audit_log (
  id         BIGSERIAL PRIMARY KEY,
  at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_id   BIGINT,
  actor_name TEXT,
  action     TEXT NOT NULL,
  ip         INET,
  details    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_log_at ON audit_log (at);

CREATE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN RAISE EXCEPTION 'audit_log is append-only'; END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER audit_log_no_change BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();

-- Reference data for development (3 sites matching the LDAP mock).
INSERT INTO sites(code, name, ip_ranges) VALUES
  ('HQ',            'Head Office',   ARRAY['10.0.0.0/16','172.16.0.0/12','192.168.0.0/16','127.0.0.0/8']::cidr[]),
  ('Agency-North',  'North Agency',  ARRAY['10.1.0.0/16']::cidr[]),
  ('Agency-South',  'South Agency',  ARRAY['10.2.0.0/16']::cidr[]);
INSERT INTO work_schedules(site_id) SELECT id FROM sites;
INSERT INTO departments(name) VALUES ('IT'),('HR'),('Finance'),('Operations'),('Communication');
