#!/bin/sh
# Desk phone integration test: a SIP softphone and a WebRTC client against the running dev stack.
# Runs in a Go container on the host network so the published SIP/RTP ports are reachable.
set -eu
cd "$(dirname "$0")/../services"
MSYS_NO_PATHCONV=1 docker run --rm --network host -v gomod:/go/pkg/mod -v "$(pwd -W 2>/dev/null || pwd):/src" -w /src golang:1.23-alpine \
  go test -tags e2e -count=1 -v -timeout 120s ./e2e/
