DC_DEV  = docker compose -f docker-compose.yml -f docker-compose.dev.yml
DC_PROD = docker compose -f docker-compose.yml -f docker-compose.prod.yml

.PHONY: dev up down logs migrate seed test e2e certs backup

.env:
	cp .env.example .env

dev: .env certs ## Start the full dev stack and wait until healthy
	$(DC_DEV) up -d --build --wait

up: .env ## Start the production stack
	$(DC_PROD) up -d --build --wait

down:
	$(DC_DEV) down --remove-orphans

logs:
	$(DC_DEV) logs -f --tail=100

migrate:
	$(DC_DEV) run --rm migrate

seed: ## Apply db/seed/*.sql (LDAP demo users are loaded automatically by ldap-mock)
	@for f in db/seed/*.sql; do [ -f "$$f" ] && $(DC_DEV) exec -T postgres sh -c 'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"' < "$$f"; done; true

test:
	docker run --rm -v "$(CURDIR)/services:/src" -w /src golang:1.23-alpine go test ./...

e2e: ## Chat, presence and call signaling acceptance tests (needs make dev; local self-signed cert only)
	NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/realtime.mjs https://127.0.0.1
	NODE_TLS_REJECT_UNAUTHORIZED=0 node tests/e2e/calls.mjs https://127.0.0.1

certs: ## Self-signed dev certificate (nginx/certs)
	@if [ ! -f nginx/certs/server.crt ]; then mkdir -p nginx/certs && \
	docker run --rm -v "$(CURDIR)/nginx/certs:/certs" alpine/openssl req -x509 -nodes -newkey rsa:2048 -days 365 \
	  -keyout /certs/server.key -out /certs/server.crt -subj "/CN=eximphone.local" \
	  -addext "subjectAltName=DNS:localhost,DNS:eximphone.local,IP:127.0.0.1"; fi

backup:
	@mkdir -p backups
	$(DC_DEV) exec -T postgres sh -c 'pg_dump -U "$$POSTGRES_USER" "$$POSTGRES_DB"' | gzip > backups/pg-$$(date +%Y%m%d-%H%M%S).sql.gz
