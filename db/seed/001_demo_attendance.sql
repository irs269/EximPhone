-- Demo data: 30 days of attendance for every active employee (weekdays only, today excluded)
-- and a few call records. Deterministic (hash based) and idempotent. Apply with: make seed
INSERT INTO attendance_days (user_id, work_date, clock_in, clock_out, arrival, departure, early_reason, duration_min, last_activity_at)
SELECT u.id, d.day,
       ((d.day + time '08:00') + make_interval(mins => cin.m - 20)) AT TIME ZONE s.timezone,
       ((d.day + time '17:00') + make_interval(mins => cout.m)) AT TIME ZONE s.timezone,
       CASE WHEN cin.m - 20 > 15 THEN 'late' ELSE 'on_time' END::arrival_status,
       CASE WHEN cout.kind = 'missed' THEN 'not_clocked' WHEN cout.kind = 'early' THEN 'early' ELSE 'normal' END::departure_status,
       CASE WHEN cout.kind = 'early' THEN 'Rendez-vous personnel' END,
       (540 + cout.m - (cin.m - 20))::int,
       ((d.day + time '17:00') + make_interval(mins => cout.m)) AT TIME ZONE s.timezone
FROM users u
JOIN sites s ON s.id = u.site_id
CROSS JOIN LATERAL (
  SELECT g::date AS day FROM generate_series(current_date - 30, current_date - 1, interval '1 day') g
  WHERE extract(isodow FROM g) < 6
) d
CROSS JOIN LATERAL (SELECT (abs(hashtext(u.id::text || d.day::text)) % 60) AS m) cin
CROSS JOIN LATERAL (
  SELECT CASE WHEN h % 23 = 0 THEN 'missed' WHEN h % 11 = 0 THEN 'early' ELSE 'normal' END AS kind,
         CASE WHEN h % 23 = 0 THEN 0 WHEN h % 11 = 0 THEN -75 ELSE (h % 45) END AS m
  FROM (SELECT abs(hashtext('out' || u.id::text || d.day::text)) AS h) x
) cout
WHERE u.active AND u.site_id IS NOT NULL
  AND (abs(hashtext('abs' || u.id::text || d.day::text)) % 17) <> 0
ON CONFLICT (user_id, work_date) DO NOTHING;

INSERT INTO calls (caller_id, callee_id, state, outcome, created_at, answered_at, ended_at, duration_sec)
SELECT a.id, b.id, 'ended', o.outcome,
       now() - make_interval(hours => o.h),
       CASE WHEN o.outcome = 'answered' THEN now() - make_interval(hours => o.h) END,
       now() - make_interval(hours => o.h),
       CASE WHEN o.outcome = 'answered' THEN o.secs ELSE 0 END
FROM (VALUES (1, 'answered', 26, 184), (2, 'missed', 30, 0), (3, 'answered', 50, 62), (4, 'busy', 70, 0)) AS o(n, outcome, h, secs)
JOIN LATERAL (SELECT id FROM users WHERE active ORDER BY id OFFSET o.n LIMIT 1) a ON TRUE
JOIN LATERAL (SELECT id FROM users WHERE active ORDER BY id OFFSET o.n + 3 LIMIT 1) b ON TRUE
WHERE NOT EXISTS (SELECT 1 FROM calls WHERE caller_id = a.id AND callee_id = b.id AND created_at < now() - interval '25 hours');
