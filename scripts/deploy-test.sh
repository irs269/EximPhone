#!/bin/sh
# Test deployment on a fresh VPS (Linux, Docker + Compose v2, openssl): fresh random secrets, a mock
# directory with generated passwords, a self-signed certificate for the server IP, then the stack.
# Usage: sh scripts/deploy-test.sh <public-ip>
#   Test phase: login (clock-in) is NOT restricted by IP address (ENFORCE_SITE_NETWORK=false in docker-compose.test.yml),
#   so testers can connect from anywhere. Production keeps the rule: see docs/deployment.md.
# Refuses to overwrite an existing .env. Credentials of the test accounts go to credentials.txt (mode 600).
set -eu
cd "$(dirname "$0")/.."
IP="${1:?usage: deploy-test.sh <public-ip>}"
if [ -e .env ]; then echo ".env already exists: move it away first" >&2; exit 1; fi
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
command -v openssl >/dev/null || { echo "openssl is required" >&2; exit 1; }
hex() { openssl rand -hex "$1"; }
umask 077

# 1. directory: a random password per account
mkdir -p ldap-mock
echo "# username  password" > credentials.txt
while IFS= read -r line; do
  case "$line" in
    "uid: "*) user="${line#uid: }"; echo "$line" ;;
    "userPassword: "*) pw="$(openssl rand -base64 18 | tr -d '/+=\n')"; echo "userPassword: $pw"; echo "$user  $pw" >> credentials.txt ;;
    *) echo "$line" ;;
  esac
done < ldap-mock/employees.ldif > ldap-mock/employees.generated.ldif
chmod 644 ldap-mock/employees.generated.ldif

# 2. secrets
LDAP_PW="$(hex 16)"
cat > .env <<ENV
# Test deployment generated $(date -u +%Y-%m-%dT%H:%M:%SZ). Keep private (mode 600).
POSTGRES_USER=eximphone
POSTGRES_PASSWORD=$(hex 24)
POSTGRES_DB=eximphone
REDIS_PASSWORD=$(hex 24)
S3_ACCESS_KEY_ID=GK$(hex 12)
S3_SECRET_ACCESS_KEY=$(hex 32)
GARAGE_RPC_SECRET=$(hex 32)
LDAP_ADMIN_PASSWORD=$LDAP_PW
LDAP_BIND_PASSWORD=$LDAP_PW
JWT_SECRET=$(hex 32)
INTERNAL_TOKEN=$(hex 24)
TURN_SECRET=$(hex 24)
MESSAGE_KEY=$(hex 32)
RECORDING_KEY=$(hex 32)
SIP_KEY=$(hex 32)
BACKUP_PASSPHRASE=$(hex 24)
RECORDING_RETENTION_DAYS=365
LOG_LEVEL=info
PUBLIC_IP=$IP
SIP_DOMAIN=exim.local
LDAP_URL=ldap://ldap-mock:389
LDAP_BASE_DN=ou=people,dc=exim,dc=local
LDAP_USER_ATTR=uid
LDAP_BIND_DN=cn=admin,dc=exim,dc=local
INITIAL_IT_ADMINS=asaid
INITIAL_DEPT_HEADS="IT=asaid;HR=mjuma;Finance=hbakari;Operations=oabdallah;Communication=imoussa"
ENV
chmod 600 .env

# 3. self-signed certificate valid for the IP (browsers warn once: accept it)
mkdir -p nginx/certs
openssl req -x509 -nodes -newkey rsa:2048 -days 90 -keyout nginx/certs/server.key -out nginx/certs/server.crt \
  -subj "/CN=$IP" -addext "subjectAltName=IP:$IP" 2>/dev/null
chmod 644 nginx/certs/server.key nginx/certs/server.crt

# 4. start
docker compose -f docker-compose.yml -f docker-compose.test.yml up -d --build --wait
# 5. nothing to do for the client networks: the test stack does not apply the IP rule of the sites
echo
echo "Exim Connect is up: https://$IP  (accept the self-signed certificate)"
echo "Test accounts and passwords: credentials.txt  (asaid is the IT administrator)"
echo "Firewall: allow 443/tcp, 10000-10200/udp, 3478/udp+tcp, 49152-49200/udp (5060-5061 only for desk phones)."
