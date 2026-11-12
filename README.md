# EximPhone

Internal communication and attendance platform for Exim Bank. Full spec: [docs/PROMPT_EximPhone.md](docs/PROMPT_EximPhone.md). Reference UI: [prototype/eximphone.html](prototype/eximphone.html).

## Status

Phase 0 (foundation) - Docker stack with Go service skeletons, Nginx TLS, PostgreSQL, Redis, LDAP mock.

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
