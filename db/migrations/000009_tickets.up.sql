-- Service tickets: an employee files a ticket for a service (department); a head of that service approves or
-- rejects it, then assigns it to a member of the service, who carries it out. Every step is kept in ticket_events.

-- Heads of service (several per department are allowed: one can decide when another is the requester).
CREATE TABLE department_heads (
  department_id INT    NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
  user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (department_id, user_id)
);

CREATE TABLE tickets (
  id               BIGSERIAL PRIMARY KEY,
  title            TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
  description      TEXT NOT NULL CHECK (char_length(description) BETWEEN 1 AND 5000),
  priority         TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','urgent')),
  status           TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','approved','rejected','assigned','in_progress','resolved','cancelled')),
  requester_id     BIGINT NOT NULL REFERENCES users(id),
  department_id    INT    NOT NULL REFERENCES departments(id),
  decided_by       BIGINT REFERENCES users(id),
  decided_at       TIMESTAMPTZ,
  decision_comment TEXT,
  assignee_id      BIGINT REFERENCES users(id),
  assigned_by      BIGINT REFERENCES users(id),
  assigned_at      TIMESTAMPTZ,
  resolution       TEXT,
  resolved_at      TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX tickets_requester_idx  ON tickets(requester_id, created_at DESC);
CREATE INDEX tickets_assignee_idx   ON tickets(assignee_id) WHERE assignee_id IS NOT NULL;
CREATE INDEX tickets_department_idx ON tickets(department_id, status);

CREATE TABLE ticket_events (
  id         BIGSERIAL PRIMARY KEY,
  ticket_id  BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor_id   BIGINT NOT NULL REFERENCES users(id),
  kind       TEXT NOT NULL,      -- created, approved, rejected, assigned, started, resolved, cancelled, comment
  comment    TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ticket_events_ticket_idx ON ticket_events(ticket_id, id);

-- history is append-only, like the audit log
CREATE FUNCTION ticket_events_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'ticket_events is append-only'; END $$;
CREATE TRIGGER ticket_events_no_update BEFORE UPDATE ON ticket_events FOR EACH ROW EXECUTE FUNCTION ticket_events_immutable();
