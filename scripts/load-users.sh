#!/bin/sh
# Adds N synthetic employees (load0001..loadNNNN, password Demo-Passw0rd!) to the DEV directory (ldap-mock)
# and runs a directory synchronisation so they exist in the database. Development stack only.
# Usage: sh scripts/load-users.sh [count]     (default 500)
set -eu
cd "$(dirname "$0")/.."
export MSYS_NO_PATHCONV=1
set -a; [ -f .env ] && . ./.env; set +a
N="${1:-500}"
DC="docker compose -f docker-compose.yml -f docker-compose.dev.yml"

depts="IT HR Finance Operations Communication"
i=1
{
  while [ "$i" -le "$N" ]; do
    id="$(printf 'load%04d' "$i")"
    dept="$(echo "$depts" | cut -d' ' -f$(( (i % 5) + 1 )))"
    printf 'dn: uid=%s,ou=people,dc=exim,dc=local\nobjectClass: inetOrgPerson\nuid: %s\ncn: Load User %04d\ngivenName: Load\nsn: User%04d\nmail: %s@exim.local\ntelephoneNumber: %d\ndepartmentNumber: %s\nphysicalDeliveryOfficeName: HQ\nuserPassword: Demo-Passw0rd!\n\n' "$id" "$id" "$i" "$i" "$id" $((3000 + i)) "$dept"
    i=$((i + 1))
  done
} > .load-users.ldif

$DC cp .load-users.ldif ldap-mock:/tmp/load-users.ldif
rm -f .load-users.ldif
$DC exec -T ldap-mock ldapadd -c -x -H ldap://localhost -D cn=admin,dc=exim,dc=local -w "$LDAP_ADMIN_PASSWORD" -f /tmp/load-users.ldif > /dev/null || true
echo "directory now holds $($DC exec -T ldap-mock ldapsearch -x -H ldap://localhost -D cn=admin,dc=exim,dc=local -w "$LDAP_ADMIN_PASSWORD" -b ou=people,dc=exim,dc=local -s one dn | grep -c '^dn:') people"

# The worker synchronises the directory when it starts.
$DC restart worker > /dev/null
sleep 8
$DC exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At -c "SELECT count(*) || ' active users in the database' FROM users WHERE active"
