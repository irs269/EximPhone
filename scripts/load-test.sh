#!/bin/sh
# Load test: 500 employees connected, chat traffic, 50 simultaneous calls with audio.
# Needs the dev stack (make dev) and the synthetic users: sh scripts/load-users.sh 500
# The per-IP request limits of nginx are raised for the run (a load generator is one IP).
# Usage: sh scripts/load-test.sh [users] [calls] [seconds]
set -eu
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
USERS="${1:-500}"; CALLS="${2:-50}"; SECONDS_PER_CALL="${3:-20}"
DC="docker compose -f docker-compose.yml -f docker-compose.dev.yml"

echo "raising the per-IP limits for the test"
NGINX_LOGIN_RATE=500r/s NGINX_API_RATE=2000r/s NGINX_LOGIN_BURST=600 NGINX_API_BURST=3000 $DC up -d --force-recreate --wait nginx > /dev/null

SAMPLES="$(mktemp)"
( sleep 12; while :; do
    docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' $(docker ps --format '{{.Names}}' | grep -E '^eximphone-(api|media|sip|postgres|redis|nginx)-1$') >> "$SAMPLES" 2>/dev/null
    echo "--" >> "$SAMPLES"; sleep 6
  done ) &
SAMPLER=$!

cd services
set +e
docker run --rm --network host -v gomod:/go/pkg/mod -v "$(pwd -W 2>/dev/null || pwd):/src" -w /src \
  -e LOAD_USERS="$USERS" -e LOAD_CALLS="$CALLS" -e LOAD_SECONDS="$SECONDS_PER_CALL" golang:1.23-alpine \
  go test -tags load -count=1 -v -timeout 15m -run TestLoad ./e2e/ 2>&1 | grep -E "^(=== RUN|--- |PASS|FAIL|ok)|load_test.go"
STATUS=$?
set -e
kill "$SAMPLER" 2>/dev/null || true
cd ..

echo "--- resource usage during the run (peak CPU per service)"
grep cpu= "$SAMPLES" | awk '{split($2,a,"="); gsub("%","",a[2]); if (a[2]+0 > m[$1]) {m[$1]=a[2]+0; u[$1]=$3" "$4" "$5}} END {for (k in m) printf "%-22s peak cpu %6.1f%%  mem %s\n", k, m[k], u[k]}' | sort
rm -f "$SAMPLES"
echo "restoring the default limits"
$DC up -d --force-recreate --wait nginx > /dev/null
exit "$STATUS"
