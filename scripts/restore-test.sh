#!/bin/sh
# Proves that a backup can be restored: decrypts it, loads the dump into a scratch database and checks
# that the row counts match those recorded around the backup. Never touches the live database.
# Usage: sh scripts/restore-test.sh backups/<timestamp>
set -eu
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
DIR="${1:?usage: restore-test.sh backups/<timestamp>}"
set -a; [ -f .env ] && . ./.env; set +a
: "${BACKUP_PASSPHRASE:?set BACKUP_PASSPHRASE in .env or the environment}"

COMPOSE="docker compose -f docker-compose.yml"
SCRATCH="eximphone_restore_test"
dec() { openssl enc -d -aes-256-cbc -pbkdf2 -iter 200000 -pass env:BACKUP_PASSPHRASE -in "$1"; }
psql_admin() { $COMPOSE exec -T postgres psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 "$@"; }

echo "1/4 checking file integrity"
( cd "$DIR" && sha256sum -c SHA256SUMS )

echo "2/4 restoring the database into a scratch database"
psql_admin -q -c "DROP DATABASE IF EXISTS $SCRATCH" -c "CREATE DATABASE $SCRATCH"
dec "$DIR/db.sql.gz.enc" | gunzip | $COMPOSE exec -T postgres psql -U "$POSTGRES_USER" -d "$SCRATCH" -q -v ON_ERROR_STOP=1 > /dev/null

echo "3/4 comparing row counts (must lie between the counts taken before and after the dump)"
status=0
while read -r table before; do
  after="$(grep "^$table " "$DIR/counts-after.txt" | cut -d' ' -f2)"
  restored="$($COMPOSE exec -T postgres psql -U "$POSTGRES_USER" -d "$SCRATCH" -At -c "SELECT count(*) FROM $table" < /dev/null)"
  if [ "$restored" -ge "$before" ] && [ "$restored" -le "$after" ]; then verdict=ok; else verdict=MISMATCH; status=1; fi
  printf '  %-18s before=%-6s restored=%-6s after=%-6s %s\n' "$table" "$before" "$restored" "$after" "$verdict"
done < "$DIR/counts-before.txt"
psql_admin -q -c "DROP DATABASE $SCRATCH"

echo "4/4 checking the object store archive"
objects="$(dec "$DIR/garage.tar.gz.enc" | tar tzf - | wc -l | tr -d ' ')"
echo "  archive readable, $objects entries"
[ "$objects" -gt 0 ] || status=1

if [ "$status" -eq 0 ]; then echo "RESTORE TEST PASSED"; else echo "RESTORE TEST FAILED"; fi
exit "$status"
