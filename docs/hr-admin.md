# Announcements, HR dashboard, RBAC and audit (phase 6)

## Roles (RBAC)

Every logged-in user is an `employee`. Other roles are granted by an `it_admin` and replace the user's previous set: `manager`, `hr`, `communication`, `direction`, `it_admin`, `auditor`.

| Role | Can |
|---|---|
| employee | clock in/out, chat, calls, read and acknowledge announcements |
| manager | see the **team** (users whose `manager_id` is the manager) in the dashboard, validate their early departures |
| hr | see **everyone**, validate early departures, correct clock-ins (justified), export the monthly payroll file, see announcement read receipts |
| communication, direction | publish announcements (normal or urgent, to all / a department / a site) and follow who has read them |
| it_admin | manage roles, managers, sites (IP ranges, working hours, tolerance, time zone), phones |
| auditor | read the audit journal (and nothing else extra) |

Rules enforced by the api: an admin cannot remove their own `it_admin` role; a manager cannot validate their own early departure; role changes take effect on the next request (roles are read per request, not stored in the token).

The first `it_admin` accounts are bootstrapped by the worker from `INITIAL_IT_ADMINS` (comma separated usernames, set it only for the initial installation). Managers are set with `PUT /api/admin/users/{id}/manager` until the directory sync provides the manager attribute.

## Announcements

- `POST /api/announcements` (communication or direction): title (200), body (5000), level `normal|urgent`, target `{kind: all|department|site, id}`.
- An **urgent** announcement is pushed over the WebSocket to every targeted connected employee and shown as a pop-up until acknowledged (also after a reconnection).
- `POST /api/announcements/{id}/ack` records the acknowledgement (idempotent; refused for announcements not addressed to you).
- `GET /api/announcements/{id}/acks` returns the follow-up table: who has read (with time) and who has not. Publishers, direction and HR only.

## HR and manager dashboard

- `GET /api/hr/overview?date=` : present, late and absent per site and department, and one row per employee. A day without attendance record counts as absent, so weekends and public holidays show everyone as absent (the screen warns on weekends; a holiday calendar is not implemented).
- `GET /api/hr/attendance` : history with filters (`from`, `to` up to one year, `user_id`, `site`, `department`, `status=late|early|not_clocked`).
- `GET /api/hr/early-departures` and `POST /api/hr/attendance/{id}/validate` : early departure queue and decision (`approved` or `rejected`, optional comment).
- `POST /api/hr/attendance/{id}/correct` (HR): new arrival and/or departure plus a justification of at least 5 characters. Status and duration are recalculated with the site rules, the previous values are kept in `attendance_corrections`, and the early-departure validation is reset.
- `GET /api/hr/export?month=YYYY-MM&format=csv|xlsx` (HR): CSV is UTF-8 with BOM and `;` separators (French Excel), the Excel file has a "Detail" sheet and a per-employee "Synthese" sheet (days present, late, early, not clocked, total hours). Text cells starting with `=`, `+`, `-` or `@` are neutralised against formula injection. Times are in the employee's site time zone.

## Audit journal

`audit_log` is append-only (database triggers reject UPDATE, DELETE and TRUNCATE). `GET /api/audit` is limited to the `auditor` role, filterable by action, actor and dates, and every consultation is itself recorded (`audit_viewed`). Recorded actions include: `login`, `login_failed`, `login_denied_network`, `logout`, `auto_close_not_clocked`, `roles_changed`, `manager_changed`, `site_updated`, `attendance_corrected`, `early_departure_validated`, `hr_export`, `announcement_published`, `message_deleted`, `device_created`, `device_disabled`, `audit_viewed`. Call recording access will be added with recording in phase 7.

## Demo data

`make seed` loads 30 weekdays of attendance for the 20 demo employees (about 25% late arrivals, some early departures and missing clock-outs) and a few call records. It is deterministic and safe to run again. Demo chat messages are not seeded because they are encrypted at rest with the deployment key.

## Tests

`node tests/e2e/hr.mjs https://127.0.0.1` (36 checks: roles, scopes, validation, corrections, exports, announcements, audit) against the running stack; `make e2e` runs it with the chat and call suites.
