# 0005. Real-time events over WebSocket and Redis pub/sub

**Status:** accepted (phase 3)

**Context.** Presence, chat, call signaling and announcements must reach clients in under a second, and the api must scale beyond one instance.

**Decision.** Clients keep one WebSocket to the api (first frame authenticates; tokens never appear in URLs). Every event is published to a Redis channel; each api instance delivers it to its local sockets. Live state (presence, who is in a call, desk phone registrations) lives in Redis; durable state lives in PostgreSQL. The `sip` service receives commands from the api on a second channel.

**Alternatives.** Server-sent events (one direction only); a message broker such as NATS (one more component to run for the same result at this scale).

**Consequences.** Redis is a dependency for real-time features: if it is down, chat and calls stop while attendance and history keep working. Events are not persisted: after a reconnection the client reloads state from the REST endpoints.
