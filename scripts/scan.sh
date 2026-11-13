#!/bin/sh
# Vulnerability scan with Trivy: every image of the stack plus the repository (dependencies, secrets, misconfigurations).
# Fails (exit 1) on any CRITICAL finding that has a fix; HIGH findings are reported but do not fail.
# Usage: sh scripts/scan.sh [image ...]      (default: all images built by docker compose)
set -eu
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
TRIVY="docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v trivy-cache:/root/.cache/ aquasec/trivy:latest"

if [ "$#" -gt 0 ]; then IMAGES="$*"; else
  IMAGES="$(docker compose -f docker-compose.yml -f docker-compose.dev.yml config --images | sort -u)"
fi

status=0
for img in $IMAGES; do
  echo "=== $img"
  $TRIVY image --quiet --scanners vuln --severity HIGH,CRITICAL --ignore-unfixed "$img" || true
  $TRIVY image --quiet --scanners vuln --severity CRITICAL --ignore-unfixed --exit-code 1 "$img" > /dev/null 2>&1 || { echo "CRITICAL findings in $img"; status=1; }
done

echo "=== repository (dependencies, secrets, configuration)"
docker run --rm -v "$(pwd -W 2>/dev/null || pwd):/repo:ro" -v trivy-cache:/root/.cache/ aquasec/trivy:latest \
  fs --quiet --scanners vuln,secret,misconfig --severity HIGH,CRITICAL --ignore-unfixed --skip-dirs node_modules,frontend/dist,desktop/dist,backups --skip-files .env /repo || true

[ "$status" -eq 0 ] && echo "SCAN PASSED (no fixable CRITICAL vulnerability)" || echo "SCAN FAILED"
exit "$status"
