# Deployment and operations

## Requirements

- One Linux server (Docker 24+ with Compose v2) for up to a few hundred users; 4 vCPU, 8 GB RAM and 100 GB of disk are a reasonable start. Media is the only CPU and bandwidth sensitive part (about 100 kbit/s per direction per G.711 call, relayed twice).
- A DNS name (for example `eximphone.exim.local`) and a **certificate from the bank's internal CA** for it. PCs and phones must trust that CA. Never run production with the self-signed development certificate.
- Network access from the server to Active Directory (LDAPS 636) and NTP.
- Time zone: timestamps are stored in UTC; sites define their own time zone (default `Africa/Dar_es_Salaam`).

## Firewall

| From | To | Port | Purpose |
|---|---|---|---|
| employee PCs and phones | server | 443/tcp | web app, API, WebSocket, provisioning |
| phones | server | 5060/udp+tcp, 5061/tcp | SIP, SIP over TLS (use 5061 in production) |
| PCs and phones | server | 10000-10200/udp | RTP (10000-10099 WebRTC, 10100-10200 SIP phones) |
| PCs | server | 3478/udp+tcp, 49152-49200/udp | TURN (only needed when UDP to media is blocked) |
| server | Active Directory | 636/tcp | directory (LDAPS) |

Nothing else is published: PostgreSQL, Redis and the object store live on an internal Docker network. **Why an explicit UDP range and not `network_mode: host`:** RTP needs real source addresses and many ports; Docker NAT on a handful of ports breaks it, a published range keeps the other services isolated on their networks. If you prefer host networking for `media`, set `MEDIA_PUBLIC_IP` to the server address, keep the same ranges and reach `media` from the other services through the host address (`MEDIA_URL`). Widen the SIP half of the range for more than about 50 simultaneous phone calls (one UDP port per phone leg).

## First installation

1. Copy `.env.example` to `.env` and **replace every value** (it contains development secrets). Generate keys with `openssl rand -hex 32` for `MESSAGE_KEY`, `RECORDING_KEY`, `SIP_KEY`, `GARAGE_RPC_SECRET`, `S3_SECRET_ACCESS_KEY` (64 hex), a key id `GK` + 24 hex for `S3_ACCESS_KEY_ID`, and long random values for `JWT_SECRET`, `INTERNAL_TOKEN`, `TURN_SECRET` and the database, Redis and LDAP passwords. Set `PUBLIC_IP` to the address clients use to reach the server.
2. Point `LDAP_URL` (use `ldaps://`), `LDAP_BASE_DN`, `LDAP_USER_ATTR=sAMAccountName`, `LDAP_BIND_DN` and its password at Active Directory (a read-only service account). Review the attribute mapping in `services/pkg/directory/ldap.go` (display name, mail, department, office) against your schema before the first synchronisation.
3. Put the certificate and key in the `certs` volume (`certs/server.crt`, `certs/server.key`), set `SIP_TLS_CERT` and `SIP_TLS_KEY` for the `sip` service to the same files.
4. Replace the development IP ranges of the three sites (migration 000002) with the real ones, through `PUT /api/admin/sites/{id}`.
5. Start: `docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build --wait`. The first synchronisation runs when the worker starts; the first `it_admin` accounts come from `INITIAL_IT_ADMINS` (remove that variable afterwards).
6. Grant roles (`PUT /api/admin/users/{id}/roles`), set managers, add phones (docs/provisioning.md), enable recording for the departments that need it (`PUT /api/admin/recording-rules`).

## Hardening switches

| Variable | Effect |
|---|---|
| `SIP_REQUIRE_TLS=true` | the registrar refuses REGISTER and INVITE that do not arrive over TLS |
| `SIP_REQUIRE_SRTP=true` | phones must offer SRTP (SDES, AES_CM_128_HMAC_SHA1_80); plain RTP offers get `488` |
| `RECORDING_RETENTION_DAYS` | recordings older than this are deleted by the worker (default 365) |

Turn both SIP switches on once all phones are provisioned for TLS and SRTP.

## Backups and restore

`make backup` (or `sh scripts/backup.sh`) writes `backups/<timestamp>/` containing an encrypted PostgreSQL dump and an encrypted archive of the object store volume. Encryption uses `BACKUP_PASSPHRASE` (AES-256, PBKDF2): keep it, and the application keys (`MESSAGE_KEY`, `RECORDING_KEY`, `SIP_KEY`), **outside** the server and outside the backup files. Without the keys, restored messages, recordings and phone secrets are unreadable.

`sh scripts/restore-test.sh backups/<timestamp>` restores the latest dump into a scratch database and compares row counts with the live one; run it after every backup change and at least monthly. A real restore: stop the stack, restore the dump into an empty `eximphone` database and the archive into the `garagedata` volume, start the stack.

Copy `backups/` to another machine daily (the retention of attendance data and the audit journal is a legal matter of the bank).

## Key management and rotation

Keys live in `.env` (file mode 600, root only) or Docker secrets. Rotating `JWT_SECRET` logs everyone out (acceptable). Rotating `INTERNAL_TOKEN` and `TURN_SECRET` needs a restart. Rotating `MESSAGE_KEY`, `RECORDING_KEY` or `SIP_KEY` needs a re-encryption of existing data, which is not implemented: plan it before the first rotation, or keep old keys readable. The target for production is to hold these keys in the bank's KMS or HSM (single `crypto.Box` type to replace).

## Monitoring

Every service exposes `/healthz` and `/metrics` (Prometheus text format: `eximphone_up`, `eximphone_uptime_seconds`) on its internal port 8080; container health checks are defined in the compose file. Logs are JSON on stdout (never passwords, tokens or message contents); ship them with the Docker logging driver of your choice. Watch: failed logins and `login_denied_network` in the audit journal, `recording_failed`, disk usage of the PostgreSQL and object store volumes.

## Upgrades

`git pull`, then `docker compose ... up -d --build --wait`. Migrations run automatically (one-shot `migrate` service) before the api starts and are forward-only in normal operation; take a backup first.

## Vulnerability scan

`sh scripts/scan.sh` runs Trivy on all images and the repository (also part of the CI). Rebuild images regularly to pick up base image fixes.

## Load test (measured)

`sh scripts/load-users.sh 500` then `sh scripts/load-test.sh 500 50 20` (dev stack, single Docker Desktop host, 7 GB RAM).
The script raises the per-IP nginx limits (`NGINX_*_RATE`, `NGINX_*_BURST`) because the generator is a single IP; the defaults stay in force otherwise.

| Metric | Result | Threshold |
|---|---|---|
| Logins (clock-in) | 500/500, p95 123 ms | < 1 % failures |
| WebSocket sessions | 500/500 connected | all |
| Chat messages | 500 sent, 500 delivered, 0 lost, p95 77 ms | no loss |
| Simultaneous calls with audio | 50/50 for 20 s, setup p95 3.4 s, RTP loss avg 0.09 % (max 0.20 %) | setup < 5 s, loss < 1 % |
| Peak CPU | media 295 %, api 71 %, nginx 54 %, postgres 13 % | — |
| Peak memory | media 169 MiB, api 71 MiB | — |

Media is the dominant consumer (WebRTC legs plus relay). Size the VPS for it: about 4 vCPU for 50 concurrent calls.

## Image scan (Trivy)

`sh scripts/scan.sh` scans our images and the base images. Findings in the upstream `postgres:16-alpine` image
(Go standard library inside its `gosu` helper) are not fixable by us: track upstream tags and rebuild when a patched image is published.
