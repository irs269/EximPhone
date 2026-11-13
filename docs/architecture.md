# Architecture

EximPhone runs entirely on Exim Bank servers: nothing depends on the Internet in production, and every business service (SIP, media, chat, presence, attendance) is our own code. Third-party software is limited to libraries (sipgo, pion, chi, pgx) and standard infrastructure (PostgreSQL, Redis, Nginx, an S3-compatible store).

## Services

```mermaid
flowchart LR
  subgraph clients
    web[Web app / Electron]
    phone[IP desk phone]
  end
  subgraph front
    nginx[nginx TLS 443]
  end
  subgraph back
    api[api REST + WebSocket]
    sip[sip registrar + call control]
    media[media RTP relay + recorder]
    turn[turn STUN/TURN]
    prov[provisioning]
    worker[worker jobs]
  end
  subgraph data
    pg[(PostgreSQL)]
    redis[(Redis)]
    s3[(Garage S3)]
    ldap[(Active Directory)]
  end
  web -->|HTTPS, WSS| nginx --> api
  nginx --> prov
  nginx -->|offer/answer| media
  web -. SRTP/DTLS UDP .-> media
  web -. relay .-> turn
  phone -->|SIP 5060/5061| sip
  phone -. RTP/SRTP UDP .-> media
  phone -->|HTTPS config| nginx
  api --- pg
  api --- redis
  api --- s3
  api -->|bind| ldap
  sip --- pg
  sip --- redis
  sip -->|internal HTTP| api
  sip -->|legs| media
  media --- s3
  media -->|recordings| api
  worker --- pg
  worker --- redis
  worker -->|nightly sync| ldap
  worker --- s3
```

Networks: `front` (nginx), `back` (services), `data` (database, Redis, object store; internal, never published). Only nginx publishes 443; `sip`, `media` and `turn` publish their signaling and UDP ranges because real-time media cannot go through an HTTP proxy.

## Attendance and sessions

```mermaid
sequenceDiagram
  participant U as Employee
  participant A as api
  participant L as Active Directory
  participant D as PostgreSQL
  U->>A: POST /auth/login (from the site network)
  A->>L: search then bind as the user
  A->>A: IP in the site ranges? schedule rules
  A->>D: open attendance day (late / on time)
  A-->>U: access token (15 min) + refresh cookie (httpOnly)
  U->>A: POST /auth/logout (reason if early)
  A->>D: close the day, duration, departure status
  Note over A,D: the worker closes forgotten days at local midnight
```

Login is the arrival clock-in; without an open day no request is accepted. Presence is kept in Redis and pushed to every client over WebSocket through Redis pub/sub, so any `api` instance can serve any user.

## A call between a desk phone and a PC

```mermaid
sequenceDiagram
  participant P as Desk phone
  participant S as sip
  participant A as api
  participant M as media
  participant C as PC (browser)
  P->>S: INVITE ext (digest auth)
  S->>A: create call (routing rules: DND, busy, away, offline)
  A-->>C: call_incoming (WebSocket)
  S->>M: allocate and activate the phone leg
  S-->>P: 180 Ringing
  C->>A: answer
  A->>M: start recording if the department rule says so
  A-->>S: pc_answered (Redis)
  S-->>P: 200 OK with the media address
  C->>M: WebRTC offer (token bound to the call)
  M-->>C: answer: G.711 because a phone is in the call
  P->>M: RTP/SRTP
  M->>C: relayed audio (and the other way round)
```

If the callee also has desk phones, `sip` forks an INVITE to each; the first device to answer wins and the others receive CANCEL (phones) or stop ringing (PC).

## Data protection summary

| Data | Protection |
|---|---|
| Traffic | TLS 1.2+ everywhere (nginx, SIP over TLS, DTLS-SRTP for browsers, SRTP for phones) |
| Chat messages | AES-256-GCM per message, key outside the database (`MESSAGE_KEY`) |
| Attachments, recordings | AES-256-GCM objects in the store, key bound to the object id (`MESSAGE_KEY`, `RECORDING_KEY`) |
| SIP passwords | sealed (`SIP_KEY`), shown once at creation |
| Passwords of employees | never stored: Active Directory is the only authority |
| Audit journal | append-only (triggers), readable only by the auditor role, reads are audited |
| Backups | encrypted with a passphrase before leaving the server |

See `docs/adr/` for the reasons behind the main decisions and `docs/deployment.md` for operation.
