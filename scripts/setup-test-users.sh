#!/bin/sh
# Test phase: sets up 6 ready-to-use accounts on the test server (idempotent, safe to run again):
#   mjuma     Mariam Juma     HR              role "hr"      sees everybody, validates, corrects, exports
#   oabdallah Omar Abdallah   Operations       role "manager" sees his team, validates its early departures
#   snassor   Salma Nassor    Operations       employee of Omar's team
#   imwinyi   Issa Mwinyi     Operations       employee of Omar's team
#   imoussa   Idrissa Moussa  Communication    role "communication"  publishes announcements, follows who read them
#   hkhamis   Hamad Khamis    Finance          role "direction"      publishes announcements, follows who read them
# Needs credentials.txt (written by deploy-test.sh) with the IT administrator "asaid", curl and python3.
# Usage: sh scripts/setup-test-users.sh
# Optional: CREDENTIALS=<file> and API_BASE=https://host:port/api to target another installation.
set -eu
cd "$(dirname "$0")/.."
CREDENTIALS="${CREDENTIALS:-credentials.txt}"
[ -f "$CREDENTIALS" ] || { echo "$CREDENTIALS not found: run this on the server where deploy-test.sh was run" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }
if [ -z "${API_BASE:-}" ]; then
  PORT="$(grep -E '^HTTPS_PORT=' .env 2>/dev/null | cut -d= -f2 | tr -d '"' || true)"
  API_BASE="https://localhost:${PORT:-443}/api"
fi

pw() { awk -v u="$1" '$1 == u { print $2 }' "$CREDENTIALS"; }

# login <user> -> prints the JSON answer; the first login of an account creates it from the directory.
# nginx limits the rate of logins: a refused attempt is simply retried a few seconds later.
login() {
  p="$(pw "$1")"
  [ -n "$p" ] || { echo "no password for $1 in $CREDENTIALS" >&2; exit 1; }
  n=0
  while [ "$n" -lt 12 ]; do
    out="$(curl -sk -X POST "$API_BASE/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$1\",\"password\":\"$p\"}" || true)"
    case "$out" in *access_token*) sleep 1; printf '%s' "$out"; return 0 ;; esac
    n=$((n + 1)); sleep 5
  done
  echo "sign-in of $1 failed: $(printf '%s' "$out" | cut -c1-160)" >&2
  exit 1
}
field() { python3 -c 'import sys, json; d = json.load(sys.stdin); print(eval(sys.argv[1]))' "$1"; }
# id_of <user>: signs in (which creates the account on its first login) and prints the user id
id_of() { J="$(login "$1")" || exit 1; printf '%s' "$J" | field 'd["user"]["id"]'; }

echo "Signing in the administrator and the 6 accounts (this also creates the accounts)..."
AJ="$(login asaid)" || exit 1
ADMIN="$(printf '%s' "$AJ" | field 'd["access_token"]')"
ID_HR="$(id_of mjuma)" || exit 1
ID_MGR="$(id_of oabdallah)" || exit 1
ID_E1="$(id_of snassor)" || exit 1
ID_E2="$(id_of imwinyi)" || exit 1
ID_COM="$(id_of imoussa)" || exit 1
ID_DIR="$(id_of hkhamis)" || exit 1

put() { # put <path> <json> <label>
  code="$(curl -sk -o /dev/null -w '%{http_code}' -X PUT "$API_BASE$1" -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' -d "$2")"
  case "$code" in 200|204) echo "  ok   $3" ;; *) echo "  FAIL $3 (HTTP $code)" >&2; exit 1 ;; esac
}
put "/admin/users/$ID_HR/roles" '{"roles":["hr"]}' "mjuma = HR"
put "/admin/users/$ID_MGR/roles" '{"roles":["manager"]}' "oabdallah = manager"
put "/admin/users/$ID_COM/roles" '{"roles":["communication"]}' "imoussa = communication"
put "/admin/users/$ID_DIR/roles" '{"roles":["direction"]}' "hkhamis = direction"
put "/admin/users/$ID_E1/manager" "{\"manager_id\":$ID_MGR}" "snassor reports to oabdallah"
put "/admin/users/$ID_E2/manager" "{\"manager_id\":$ID_MGR}" "imwinyi reports to oabdallah"

echo
echo "Accounts ready (sign in at the address of the test server):"
printf '  %-10s %-16s %-31s %s\n' "username" "name" "role" "password"
printf '  %-10s %-16s %-31s %s\n' mjuma "Mariam Juma" "HR" "$(pw mjuma)"
printf '  %-10s %-16s %-31s %s\n' oabdallah "Omar Abdallah" "Manager (Operations)" "$(pw oabdallah)"
printf '  %-10s %-16s %-31s %s\n' snassor "Salma Nassor" "Employee of Omar's team" "$(pw snassor)"
printf '  %-10s %-16s %-31s %s\n' imwinyi "Issa Mwinyi" "Employee of Omar's team" "$(pw imwinyi)"
printf '  %-10s %-16s %-31s %s\n' imoussa "Idrissa Moussa" "Communication (announcements)" "$(pw imoussa)"
printf '  %-10s %-16s %-31s %s\n' hkhamis "Hamad Khamis" "Direction (announcements)" "$(pw hkhamis)"
echo
echo "HR and the manager see the \"Team\" screen; Communication and Direction see the \"New announcement\" button (Announcements page)."
echo "Delete credentials.txt when the trial ends."
