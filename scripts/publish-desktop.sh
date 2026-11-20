#!/bin/sh
# Publishes a desktop installer that was built elsewhere (for example by scripts/build-desktop.ps1 on a Windows PC) so that the
# web app offers it. Usage on the server:  sh scripts/publish-desktop.sh /path/to/Exim-Connect-Setup.exe [version]
# It copies the file into ./downloads and writes manifest.json (size and SHA-256). The nginx container reads that folder read-only.
set -eu
cd "$(dirname "$0")/.."
SRC="${1:?usage: publish-desktop.sh <installer.exe> [version]}"
VERSION="${2:-0.1.0}"
[ -f "$SRC" ] || { echo "file not found: $SRC" >&2; exit 1; }
mkdir -p downloads
cp "$SRC" downloads/Exim-Connect-Setup.exe
SIZE="$(wc -c < downloads/Exim-Connect-Setup.exe | tr -d ' ')"
HASH="$(sha256sum downloads/Exim-Connect-Setup.exe | cut -d' ' -f1)"
cat > downloads/manifest.json <<EOF
{"file":"Exim-Connect-Setup.exe","version":"$VERSION","size":$SIZE,"sha256":"$HASH","published_at":"$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF
chmod 644 downloads/Exim-Connect-Setup.exe downloads/manifest.json
echo "published downloads/Exim-Connect-Setup.exe ($SIZE bytes, sha256 $HASH)"
