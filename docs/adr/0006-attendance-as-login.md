# 0006. Attendance is the login

**Status:** accepted (phase 1)

**Context.** HR wants reliable clock-in data without a separate badge system, and the bank wants access from the internal network only.

**Decision.** Logging in with Active Directory credentials from a site's IP ranges is the arrival clock-in; logging out is the departure. No request is served without an open work day (checked on every call, which also records the last activity). A single day can be open per employee whatever the number of devices. Days left open are closed by the worker at local midnight at the time of the last activity and flagged "not clocked out". Rules (hours, tolerance, ranges, time zone) are data per site, not code.

**Consequences.** A forgotten logout is visible and auditable but not blocking. Public holidays and leave are not modelled (an absent employee is anyone without a day record). The client IP is trusted from `X-Real-IP`, which only nginx can set because the api is not reachable from outside the `back` network.
