# EximPhone

Internal communication and attendance platform for Exim Bank. Full spec: [docs/PROMPT_EximPhone.md](docs/PROMPT_EximPhone.md). Reference UI: [prototype/eximphone.html](prototype/eximphone.html).

## Status

Phase 0 (foundation) done. Phase 1 (auth + attendance API) done: LDAP login = clock-in, network check, late/early rules, logout = clock-out, midnight auto-close by the worker. Phase 2 (frontend + brand) done: React app faithful to the prototype, design tokens, French UI with i18n catalogs, Electron shell. Login and Presence use the real API; Messages, Calls, Directory and Announcements are mock data until phases 3-6.

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
