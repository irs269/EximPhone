#!/bin/sh
# Encrypted backup of the PostgreSQL database and the object store (attachments, recordings).
# Usage: sh scripts/backup.sh        (needs BACKUP_PASSPHRASE in .env or the environment, and openssl)
# Keep BACKUP_PASSPHRASE and the application keys (MESSAGE_KEY, RECORDING_KEY, SIP_KEY) OUTSIDE the backups.
set -eu
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
set -a; [ -f .env ] && . ./.env; set +a
: "${BACKUP_PASSPHRASE:?set BACKUP_PASSPHRASE in .env or the environment}"

COMPOSE="docker compose -f docker-compose.yml"
PROJECT="$(basename "$(pwd)" | tr 'A-Z' 'a-z')"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="backups/$STAMP"
mkdir -p "$OUT"
enc() { openssl enc -aes-256-cbc -pbkdf2 -iter 200000 -salt -pass env:BACKUP_PASSPHRASE -out "$1"; }

TABLES="users attendance_days messages calls announcements devices audit_log call_recordings"
counts() { for t in $TABLES; do printf '%s %s\n' "$t" "$($COMPOSE exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT count(*) FROM $t")"; done; }

counts > "$OUT/counts-before.txt"
$COMPOSE exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner | gzip -9 | enc "$OUT/db.sql.gz.enc"
counts > "$OUT/counts-after.txt"

# Object store: archive the data volume through a throw-away container.
docker run --rm -v "${PROJECT}_garagedata:/data:ro" alpine tar czf - -C /data . | enc "$OUT/garage.tar.gz.enc"

( cd "$OUT" && sha256sum db.sql.gz.enc garage.tar.gz.enc > SHA256SUMS )
echo "backup written to $OUT"
ls -l "$OUT"
