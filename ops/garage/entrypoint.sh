#!/bin/sh
# Starts Garage, then idempotently creates the layout, the application key and the buckets.
set -eu

garage server &
PID=$!
trap 'kill $PID 2>/dev/null' TERM INT

# Wait for the RPC endpoint.
i=0
until garage status >/dev/null 2>&1; do
  i=$((i + 1)); [ "$i" -gt 30 ] && { echo "garage did not start" >&2; exit 1; }
  sleep 1
done

# Single-node layout (first start only).
if garage status | grep -q "NO ROLE ASSIGNED"; then
  NODE=$(garage node id -q | cut -d@ -f1)
  garage layout assign -z dc1 -c 10G "$NODE"
  garage layout apply --version 1
fi

# Application key (imported so the api can be configured with fixed credentials).
if ! garage key info "$S3_ACCESS_KEY_ID" >/dev/null 2>&1; then
  garage key import --yes -n eximphone "$S3_ACCESS_KEY_ID" "$S3_SECRET_ACCESS_KEY"
fi

for b in attachments recordings exports; do
  garage bucket info "$b" >/dev/null 2>&1 || garage bucket create "$b"
  garage bucket allow --read --write --owner "$b" --key "$S3_ACCESS_KEY_ID"
done

echo "garage ready"
wait $PID
