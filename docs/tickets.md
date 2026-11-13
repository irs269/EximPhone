# Service tickets

An employee files a ticket for a **service** (a department). The **head of that service** approves or rejects it, then
**assigns it to a member of the service**, who carries it out.

```
pending --approve--> approved --assign--> assigned --start--> in_progress --resolve--> resolved
   |  \--reject--> rejected                    (re-assignment allowed until resolved)
   \--(requester) cancel--> cancelled          (also possible while approved)
```

## Who can do what

| Action | Who | Conditions |
|---|---|---|
| File a ticket | any employee | the target service must have at least one active head |
| Approve / reject | a head of the target service (or an `it_admin`) | status `pending`; **never on one's own ticket**; a reason is mandatory to reject |
| Assign / re-assign | a head of the service (or `it_admin`) | status `approved`, `assigned` or `in_progress`; the assignee must be an **active member of the same service** |
| Start / resolve | the assignee | status `assigned` (start), `assigned`/`in_progress` (resolve) |
| Cancel | the requester | status `pending` or `approved` |
| Comment | requester, assignee, heads of the service | any status |

A ticket is only visible to its requester, its assignee, the heads of its service and `it_admin`; anyone else gets `404`.
The API returns, for each ticket, a `can` object (`decide`, `assign`, `start`, `resolve`, `cancel`) computed for the caller, so the UI never guesses.
Several heads per service are supported: when the only head files a ticket, another head (or an `it_admin`) must approve it (separation of duties).

## Traceability and notifications

- Every step is stored in `ticket_events` (append-only, a database trigger refuses updates) and in the audit log
  (`ticket_created`, `ticket_approved`, `ticket_rejected`, `ticket_assigned`, `ticket_started`, `ticket_resolved`, `ticket_cancelled`, `department_heads_set`).
- The people concerned (requester, assignee, heads) receive a real-time `ticket` event over the WebSocket: the navigation badge and the lists refresh, and a toast announces the change.

## Designating the heads of service

- First installation: `INITIAL_DEPT_HEADS="IT=asaid;HR=mjuma;Finance=hbakari"` (`Department=user1,user2;Other=user3`), applied by the worker at startup (idempotent: it only adds).
- Afterwards, an `it_admin` manages them through the API (no screen yet):
  - `GET /api/admin/departments` lists the services with their heads;
  - `PUT /api/admin/departments/{id}/heads` with `{"user_ids": [..]}` replaces the heads of a service.

## API

| Method and path | Purpose |
|---|---|
| `GET /api/tickets?view=mine\|todo\|service` | list (default: everything the caller may see); `mine` = filed by me, `todo` = assigned to me, `service` = services I head |
| `GET /api/tickets/summary` | counters for the badge: `to_decide`, `to_assign`, `to_do`, `mine_open`, `waiting`, `is_head` |
| `GET /api/tickets/services` | services with the names of their heads (for the filing form) |
| `POST /api/tickets` | `{title, description, priority, department_id}` |
| `GET /api/tickets/{id}` | detail with history |
| `POST /api/tickets/{id}/decision` | `{approve, comment}` |
| `GET /api/tickets/{id}/candidates` | members a head can assign to |
| `POST /api/tickets/{id}/assign` | `{user_id, comment}` |
| `POST /api/tickets/{id}/start`, `/resolve` `{comment}`, `/cancel`, `/comments` `{comment}` | the other steps |

Tested end to end by `tests/e2e/tickets.mjs` (35 checks: workflow, visibility, separation of duties, races, notifications, administration).

## Not done yet

SLA timers and reminders, e-mail notifications, attachments on tickets, a screen to designate the heads, and escalation when a head is absent
(for now another head or an `it_admin` takes over).
