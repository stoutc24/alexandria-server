#!/bin/bash
# Server Admin sign-ins on the admin page, end to end. TEST-ONLY. From the
# repository root:
#
#   internal/adminui/testdata/serviceadmins/run.sh
#
# Builds and starts the real server binary (a throwaway database, a fresh
# API token and secrets key), enrols the admin login and puts the flow
# harness proxy (../flow) in front of the admin page with this folder's
# driver.js. Headless Chrome saves a Grimmory sign-in through the page's
# form; curl then checks it is kept sealed and listed without its password,
# a phone signs in, fetches it with a fresh code into a box only its key
# opens; the page removes it.
#
# Needs Go, curl, jq, openssl, sqlite3 and Google Chrome (CHROME=... for
# another). Nothing leaves this machine. KEEP=1 keeps the work folder (it
# is kept anyway when something fails).
set -euo pipefail

root=$(cd "$(dirname "$0")/../../../.." && pwd)
here="$root/internal/adminui/testdata/serviceadmins"
chrome=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
port=${PORT:-4577}
proxy_port=${PROXY_PORT:-4578}
work=$(mktemp -d "${TMPDIR:-/tmp}/alexandria-svcadmins.XXXXXX")
pids=()
failed=0

cleanup() {
  local p
  for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
  pkill -f -- "--user-data-dir=$work/chrome" 2>/dev/null || true
  if [ "${KEEP:-0}" = 1 ] || [ "$failed" != 0 ]; then
    echo "work folder kept: $work"
  else
    rm -rf "${work:?}"
  fi
}
trap cleanup EXIT

pass() { printf 'PASS %s\n' "$1"; }
flunk() { printf 'FAIL %s\n' "$1"; failed=1; }
check() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then pass "$name"; else flunk "$name"; fi
}
up() {
  local _
  for _ in $(seq 1 100); do
    curl -fsS "$1" >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}
reached() {
  local _
  for _ in $(seq 1 1200); do
    [ -e "$work/checkpoint-$1" ] && return 0
    [ -e "$work/report.json" ] && return 1
    sleep 0.1
  done
  return 1
}
go_on() { : >"$work/continue-$1"; }

echo "building the server and the harness"
(cd "$root" && go build -o "$work/server" ./cmd/server &&
  go build -o "$work/flow" ./internal/adminui/testdata/flow)

api_token=$(openssl rand -hex 24)
secrets_key=$(openssl rand -base64 32)
password="svc-admin-$(openssl rand -hex 8)"
grimmory_password="grimmory-pass-$(openssl rand -hex 8)"
hostile='<img src=x onerror=alert(1)>'
base="http://127.0.0.1:$port"

echo "starting the server on $base"
ALEXANDRIA_API_TOKEN=$api_token ALEXANDRIA_SECRETS_KEY=$secrets_key \
  DB_PATH="$work/alexandria.db" PORT=$port \
  "$work/server" >"$work/server.log" 2>&1 &
pids+=($!) && disown
up "$base/health" || { flunk "the server started"; tail -20 "$work/server.log"; exit 1; }

enrol=$(curl -fsS -H "Authorization: Bearer $api_token" -H "Content-Type: application/json" \
  -d "$(jq -n --arg p "$password" '{password: $p}')" "$base/api/v1/admin/auth/enroll")
code_page=$(jq -er '.recoveryCodes[0]' <<<"$enrol")
code_curl=$(jq -er '.recoveryCodes[1]' <<<"$enrol")
code_phone=$(jq -er '.recoveryCodes[2]' <<<"$enrol")
code_open=$(jq -er '.recoveryCodes[3]' <<<"$enrol")
jar="$work/curl.cookies"
curl -fsS -c "$jar" -H "Content-Type: application/json" -o /dev/null \
  -d "$(jq -n --arg p "$password" --arg c "$code_curl" '{password: $p, recoveryCode: $c}')" \
  "$base/api/v1/admin/auth/login"
admin() { curl -fsS -b "$jar" "$@"; }

jq -n --arg pw "$password" --arg rc "$code_page" --arg h "$hostile" --arg gp "$grimmory_password" \
  '{password: $pw, recovery: $rc, hostile: $h, grimmoryPassword: $gp}' >"$work/config.json"

"$work/flow" proxy -listen "127.0.0.1:$proxy_port" -upstream "$base" \
  -driver "$here/driver.js" -config "$work/config.json" -dir "$work" >"$work/proxy.log" 2>&1 &
pids+=($!) && disown
up "http://127.0.0.1:$proxy_port/health" || { flunk "the proxy started"; exit 1; }

echo "driving the admin page in headless Chrome"
"$chrome" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$work/chrome" --no-proxy-server \
  --host-resolver-rules="MAP * ~NOTFOUND, EXCLUDE 127.0.0.1" \
  --disable-background-networking --disable-component-update --disable-sync \
  --disable-extensions \
  "http://127.0.0.1:$proxy_port/admin" >"$work/chrome.log" 2>&1 &
pids+=($!) && disown

if reached saved; then
  admin "$base/api/v1/admin/service-admins" >"$work/list.json"
  check "listed: one Grimmory sign-in, the address tidied, the username as typed" \
    jq -e --arg h "$hostile" '.available and (.admins | length == 1) and
      .admins[0].type == "booklore" and .admins[0].baseUrl == "http://grimmory.lan:6060" and
      .admins[0].username == $h' "$work/list.json"
  check "listed without the password" bash -c "! grep -qF -- '$grimmory_password' '$work/list.json'"
  check "kept sealed: the database never holds the password" \
    bash -c "! sqlite3 '$work/alexandria.db' 'SELECT secret FROM service_admin_logins' | grep -qF -- '$grimmory_password'"

  phone=$(curl -fsS -H "Content-Type: application/json" \
    -d "$(jq -n --arg p "$password" --arg c "$code_phone" \
      '{password: $p, recoveryCode: $c, deviceLabel: "Test phone", persistent: true}')" \
    "$base/api/v1/admin/auth/device-token" | jq -er .token)
  pub=$("$work/flow" devicekey -out "$work/phone.key")
  curl -fsS -H "Authorization: Bearer $phone" -H "Content-Type: application/json" \
    -d "$(jq -n --arg k "$pub" --arg c "$code_open" '{publicKey: $k, recoveryCode: $c}')" \
    "$base/api/v1/admin/service-admins/open" >"$work/open.json"
  check "the open answer is a box, the password not in it" \
    bash -c "jq -e '.box.ct' '$work/open.json' && ! grep -qF -- '$grimmory_password' '$work/open.json'"
  "$work/flow" openbox -key "$work/phone.key" <"$work/open.json" >"$work/opened.json" || true
  check "the phone's key opens it: the password as typed in the page" \
    jq -e --arg p "$grimmory_password" '.admins[0].type == "booklore" and .admins[0].password == $p' \
    "$work/opened.json"
  go_on saved
else
  flunk "the page reached the saved checkpoint"
fi

if reached removed; then
  admin "$base/api/v1/admin/service-admins" >"$work/after.json"
  check "removed from the page: nothing listed" jq -e '.admins | length == 0' "$work/after.json"
  go_on removed
else
  flunk "the page reached the removed checkpoint"
fi

for _ in $(seq 1 600); do [ -e "$work/report.json" ] && break; sleep 0.1; done
if [ -e "$work/report.json" ]; then
  jq -r '.checks[] | (if .ok then "PASS page: " else "FAIL page: " end) + .name +
    (if .ok then "" else " :: " + .detail end)' "$work/report.json"
  jq -e '(.checks | length) > 0 and all(.checks[]; .ok)' "$work/report.json" >/dev/null || failed=1
else
  flunk "the page reported"
fi

if [ "$failed" = 0 ]; then echo "ALL PASSED"; else echo "SOMETHING FAILED"; fi
exit "$failed"
