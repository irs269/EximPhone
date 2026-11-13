# EximPhone

Internal communication and attendance platform for Exim Bank. Full spec: [docs/PROMPT_EximPhone.md](docs/PROMPT_EximPhone.md). Reference UI: [prototype/eximphone.html](prototype/eximphone.html).

## Status

Phase 0 (foundation) done. Phase 1 (auth + attendance API) done: LDAP login = clock-in, network check, late/early rules, logout = clock-out, midnight auto-close by the worker. Phase 2 (frontend + brand) done: React app faithful to the prototype, design tokens, French UI with i18n catalogs, Electron shell. Login and Presence use the real API; Announcements are mock data until phase 6.

Phase 3 (presence + chat) done: realtime presence and messaging over WebSocket + Redis pub/sub, direct and group conversations, receipts (sent/delivered/read), typing indicator, attachments (PDF/PNG/JPEG, 20 MB, MIME-sniffed) in Garage, messages AES-256-GCM encrypted at rest, logical deletion only, nightly LDAP directory sync. Acceptance test: `make e2e` (22 checks; presence propagates in under 100 ms locally).

Try it (dev): `POST https://localhost/api/auth/login` with `{"username":"asaid","password":"Demo-Passw0rd!"}`. Rules: [docs/attendance-rules.md](docs/attendance-rules.md).

## Prerequisites

Docker with Compose v2. GNU Make on Linux/macOS; on Windows use `./scripts/make.ps1 <target>` instead.

## Quick start

```bash
make dev      # creates .env, dev certificate, builds and starts everything
make test     # Go tests (run in a container)
make down
```

Windows: `./scripts/make.ps1 dev`. Then open https://localhost (self-signed certificate) and `https://localhost/api/healthz`.

## Layout

`services/` (one Go module, shared `pkg/`), `db/migrations`, `nginx/`, `ldap-mock/` (20 demo employees, password `Demo-Passw0rd!`, dev only), `brand/`, `docs/`.

## Notes

- Networks: `front`, `back`, `data` (internal, no host access). Only Nginx publishes a port (443).
- Go services run as non-root on distroless and probe themselves with `/app -healthcheck`.
- Never reuse `.env.example` values in production.

## Frontend and desktop

- `frontend/`: React 18 + TypeScript + Vite. Brand tokens live in `src/theme/tokens.ts` (no hard-coded colors elsewhere). `npm run dev` (proxies `/api` to https://localhost), `npm test`, `npm run build`. The production build is baked into the nginx image (`frontend/Dockerfile`).
- `desktop/`: Electron shell that opens the server URL (`EXIM_URL`, or `%APPDATA%/EximPhone/config.json` `{"url": "..."}`; default https://localhost). `npm start` to try, `npm run dist` for the Windows installer. TLS uses the Windows trust store: install the bank CA there. The installer still needs a branded `icon.ico` (256x256).

Phase 4 (PC to PC calls) done: see [docs/calls.md](docs/calls.md). WebRTC audio relayed by `media`, in-house `turn`, signaling over the WebSocket, call journal, hold, mute, blind transfer, busy/DND handling. Verified with two real browsers through the Docker stack; the microphone could not be tested in the automation browser (listen-only fallback was used).

Phase 5 (desk phones) done: see [docs/provisioning.md](docs/provisioning.md). SIP registrar with SHA-256 and MD5 digest, calls between desk phones and PCs in both directions with forking, DND, busy, offline and away handling, G.711 bridge in `media`, auto-provisioning for Yealink, Grandstream and Fanvil, SIP over TLS listener. Verified with a SIP softphone and a WebRTC client (`make e2e-phones`). Not yet tested on physical phones; REFER and SRTP are open items.

Phase 6 (announcements, HR, RBAC, audit) done: see [docs/hr-admin.md](docs/hr-admin.md). Announcements with targeting and read receipts (urgent pop-up), real-time HR/manager dashboard, early-departure validation, justified corrections, monthly CSV/Excel export, role management, site rules, append-only audit viewer. Verified by `tests/e2e/hr.mjs` (36 checks). There is no admin screen yet for roles and sites (API only), and no public holiday calendar.

Service tickets (file for a service, head approves or rejects, then assigns to a member): see [docs/tickets.md](docs/tickets.md). English and French UI with a language switch; Help view with IT contacts and FAQ.
