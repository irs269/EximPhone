# Windows equivalent of the Makefile (no GNU Make needed). Usage: ./scripts/make.ps1 dev
param([Parameter(Mandatory)][ValidateSet('dev','up','down','logs','migrate','test','certs')][string]$Target)
Set-Location (Split-Path $PSScriptRoot -Parent)
$dev  = @('compose','-f','docker-compose.yml','-f','docker-compose.dev.yml')
$prod = @('compose','-f','docker-compose.yml','-f','docker-compose.prod.yml')

function Ensure-Env   { if (-not (Test-Path .env)) { Copy-Item .env.example .env } }
function Ensure-Certs {
  if (Test-Path nginx/certs/server.crt) { return }
  New-Item -ItemType Directory -Force nginx/certs | Out-Null
  docker run --rm -v "${PWD}/nginx/certs:/certs" alpine/openssl req -x509 -nodes -newkey rsa:2048 -days 365 `
    -keyout /certs/server.key -out /certs/server.crt -subj "/CN=eximphone.local" `
    -addext "subjectAltName=DNS:localhost,DNS:eximphone.local,IP:127.0.0.1"
}

switch ($Target) {
  'dev'     { Ensure-Env; Ensure-Certs; docker @dev up -d --build --wait }
  'up'      { Ensure-Env; docker @prod up -d --build --wait }
  'down'    { docker @dev down --remove-orphans }
  'logs'    { docker @dev logs -f --tail=100 }
  'migrate' { docker @dev run --rm migrate }
  'test'    { docker run --rm -v "${PWD}/services:/src" -w /src golang:1.23-alpine go test ./... }
  'certs'   { Ensure-Certs }
}
