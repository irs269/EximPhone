# Attendance rules (for HR)

| Rule | Behaviour |
|---|---|
| Clock-in | Logging in with Active Directory credentials is the arrival clock-in. |
| Network | Clock-in is accepted only from the IP ranges of the employee's site (configurable per site). Elsewhere the login is refused. |
| Late | Arrival after start time + tolerance (default 08:00 + 15 min). Exactly 08:15 is still on time. |
| Clock-out | Logging out is the departure, after a confirmation screen (arrival, departure, duration). |
| Early departure | Before the end time (default 17:00) a reason is mandatory. |
| Not clocked out | The worker closes any day still open after local midnight, using the time of the last activity, status "not clocked". The manager id is recorded in the audit log for notification (delivery arrives with phase 3). |
| One day at a time | An employee has a single open day regardless of the number of devices. Logging in again after a same-day logout reopens that day (the first arrival time is kept). |
| Time zone | Stored in UTC; the day and the rules are evaluated in the site's time zone (default Africa/Dar_es_Salaam). |
| Audit | Logins (success, failure, refused network, disabled account), logouts and automatic closures are recorded in an append-only table. |

Known limits in phase 1: account deactivation is detected through `users.active` (the nightly Active Directory sync that sets it arrives with the directory phase), and the dev IP ranges in migration 000002 must be replaced by the real bank ranges before production.
