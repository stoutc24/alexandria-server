#!/bin/bash
# A person's account on a media server, made from the admin page, end to
# end. TEST-ONLY. From the repository root:
#
#   internal/adminui/testdata/provision/run.sh
#
# Builds and starts the real server binary (a throwaway database, a fresh
# API token and secrets key) and a small Audiobookshelf (abs_stub.py),
# enrols the admin login, keeps an ABS Server Admin sign-in and makes a
# person, Emily, with curl. Headless Chrome then adds Emily's ABS service
# through the page with the "create this account" tick and one of ABS's two
# libraries unticked (her ABS account is made, seeing the other only),
# edits it with a new password (the page asks; her ABS password changes,
# her libraries do not), tries ABS's root account (refused), then Noah's
# with no library ticked (refused on the page) and with all of them
# (every library). Then "Change access" on Emily's login: both libraries and
# no downloads, then every library, then none ticked (refused on the page),
# then a save the stub answers by changing another right too (the page
# warns). Last, "Test accounts". The stub's state is checked at each step.
#
# Needs Go, curl, jq, openssl, python3 and Google Chrome (CHROME=... for
# another). Nothing leaves this machine. KEEP=1 keeps the work folder.
set -euo pipefail

root=$(cd "$(dirname "$0")/../../../.." && pwd)
here="$root/internal/adminui/testdata/provision"
chrome=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
port=${PORT:-4587}
proxy_port=${PROXY_PORT:-4588}
abs_port=${ABS_PORT:-4589}
work=$(mktemp -d "${TMPDIR:-/tmp}/alexandria-provision.XXXXXX")
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
password="provision-$(openssl rand -hex 8)"
abs_admin="abs-admin-$(openssl rand -hex 6)"
first="emily-first-$(openssl rand -hex 6)"
second="emily-second-$(openssl rand -hex 6)"
base="http://127.0.0.1:$port"
abs="http://127.0.0.1:$abs_port"

echo "starting the small Audiobookshelf on $abs"
PORT=$abs_port ABS_ADMIN_PASSWORD=$abs_admin python3 "$here/abs_stub.py" >"$work/abs.log" 2>&1 &
pids+=($!) && disown
up "$abs/__state" || { flunk "the ABS stub started"; exit 1; }

echo "starting the server on $base"
ALEXANDRIA_API_TOKEN=$api_token ALEXANDRIA_SECRETS_KEY=$secrets_key \
  DB_PATH="$work/alexandria.db" PORT=$port \
  "$work/server" >"$work/server.log" 2>&1 &
pids+=($!) && disown
up "$base/health" || { flunk "the server started"; tail -20 "$work/server.log"; exit 1; }

# Emily, before the admin login exists (the API token works until then).
emily=$(curl -fsS -H "Authorization: Bearer $api_token" -H "Content-Type: application/json" \
  -d '{"name":"Emily"}' "$base/api/v1/admin/profiles" | jq -er .profile.id)

enrol=$(curl -fsS -H "Authorization: Bearer $api_token" -H "Content-Type: application/json" \
  -d "$(jq -n --arg p "$password" '{password: $p}')" "$base/api/v1/admin/auth/enroll")
code_page=$(jq -er '.recoveryCodes[0]' <<<"$enrol")
code_curl=$(jq -er '.recoveryCodes[1]' <<<"$enrol")
jar="$work/curl.cookies"
curl -fsS -c "$jar" -H "Content-Type: application/json" -o /dev/null \
  -d "$(jq -n --arg p "$password" --arg c "$code_curl" '{password: $p, recoveryCode: $c}')" \
  "$base/api/v1/admin/auth/login"
admin() { curl -fsS -b "$jar" "$@"; }

# The admin's own ABS sign-in, kept by Server Admin.
admin -H "Content-Type: application/json" -o /dev/null \
  -d "$(jq -n --arg u "$abs" --arg p "$abs_admin" '{type: "abs", baseUrl: $u, username: "admin", password: $p}')" \
  "$base/api/v1/admin/service-admins"

jq -n --arg pw "$password" --arg rc "$code_page" --arg e "$emily" --arg u "$abs" \
  --arg h "127.0.0.1:$abs_port" --arg f "$first" --arg s "$second" \
  '{password: $pw, recovery: $rc, emilyId: $e, absUrl: $u, absHost: $h, firstPassword: $f, secondPassword: $s}' \
  >"$work/config.json"

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

if reached created; then
  curl -fsS "$abs/__state" >"$work/state1.json"
  check "ABS: emily made, active, a plain user, with the password typed in the page" \
    jq -e --arg p "$first" '(.users | map(select(.username == "emily")) | length == 1) and
      (.users[] | select(.username == "emily") | .type == "user" and .isActive == true) and
      .passwords.emily == $p' "$work/state1.json"
  check "ABS: emily reads and downloads, and sees only the library left ticked" \
    jq -e '.users[] | select(.username == "emily") |
      .permissions.download == true and .permissions.upload == false and .permissions.delete == false and
      .permissions.update == false and .permissions.accessAllLibraries == false and
      .librariesAccessible == ["lib-books"]' "$work/state1.json"
  admin "$base/api/v1/admin/profiles/$emily/services" >"$work/services.json"
  check "Alexandria keeps her ABS login (username emily)" \
    jq -e '.services | length == 1 and .[0].type == "abs" and .[0].username == "emily"' "$work/services.json"
  check "the server log names the account, never the password" \
    bash -c "grep -q 'provision: Audiobookshelf account \"emily\" created' '$work/server.log' &&
      ! grep -qF -- '$first' '$work/server.log'"
  go_on created
else
  flunk "the page reached the created checkpoint"
fi

if reached reset; then
  curl -fsS "$abs/__state" >"$work/state2.json"
  check "ABS: emily's password is the new one" \
    jq -e --arg p "$second" '.passwords.emily == $p' "$work/state2.json"
  check "ABS: a reset leaves her libraries as they were" \
    jq -e '.users[] | select(.username == "emily") | .librariesAccessible == ["lib-books"]' "$work/state2.json"
  go_on reset
else
  flunk "the page reached the reset checkpoint"
fi

if reached admin; then
  curl -fsS "$abs/__state" >"$work/state3.json"
  check "ABS: root untouched" jq -e '.passwords.root == null' "$work/state3.json"
  go_on admin
else
  flunk "the page reached the admin checkpoint"
fi

if reached none; then
  curl -fsS "$abs/__state" >"$work/state4.json"
  check "ABS: no account for noah with no library ticked" \
    jq -e '(.users | map(select(.username == "noah")) | length == 0) and .passwords.noah == null' "$work/state4.json"
  go_on none
else
  flunk "the page reached the none checkpoint"
fi

if reached all; then
  curl -fsS "$abs/__state" >"$work/state5.json"
  check "ABS: noah sees every library, the ones added later included" \
    jq -e '.users[] | select(.username == "noah") |
      .permissions.accessAllLibraries == true and .librariesAccessible == null' "$work/state5.json"
  go_on all
else
  flunk "the page reached the all checkpoint"
fi

if reached access; then
  curl -fsS "$abs/__state" >"$work/state-access.json"
  check "ABS: Change access gave emily both libraries, by name, and took downloads away" \
    jq -e '.users[] | select(.username == "emily") |
      .permissions.download == false and .permissions.accessAllLibraries == false and
      .librariesAccessible == ["lib-books", "lib-pods"]' "$work/state-access.json"
  check "ABS: nothing else about emily moved" \
    jq -e --arg p "$second" '.passwords.emily == $p and (.users[] | select(.username == "emily") |
      .permissions.upload == false and .permissions.update == false and .permissions.delete == false and
      .type == "user")' "$work/state-access.json"
  check "the server log says what she has, read back" \
    grep -q 'provision: Audiobookshelf access for "emily" on 127.0.0.1:'"$abs_port"' (Emily) set to 2 of 2 libraries, downloads off' "$work/server.log"
  go_on access
else
  flunk "the page reached the access checkpoint"
fi

if reached access-all; then
  curl -fsS "$abs/__state" >"$work/state-access-all.json"
  check "ABS: every library, the old list cleared" \
    jq -e '.users[] | select(.username == "emily") |
      .permissions.accessAllLibraries == true and .librariesAccessible == [] and .permissions.download == false' \
    "$work/state-access-all.json"
  go_on access-all
else
  flunk "the page reached the access-all checkpoint"
fi

if reached access-none; then
  curl -fsS "$abs/__state" >"$work/state-access-none.json"
  check "ABS: a pick of no library changed nothing" \
    jq -e '.users[] | select(.username == "emily") | .permissions.accessAllLibraries == true' "$work/state-access-none.json"
  # The next save also changes a right nobody asked about.
  curl -fsS -H "Content-Type: application/json" -d '{"on": true}' -o /dev/null "$abs/__sneak"
  go_on access-none
else
  flunk "the page reached the access-none checkpoint"
fi

if reached access-sneak; then
  curl -fsS -H "Content-Type: application/json" -d '{"on": false}' -o /dev/null "$abs/__sneak"
  curl -fsS "$abs/__state" >"$work/state-access-sneak.json"
  check "ABS: the asked-for change took (downloads back on)" \
    jq -e '.users[] | select(.username == "emily") | .permissions.download == true' "$work/state-access-sneak.json"
  check "the server log names what else changed" \
    grep -q 'provision: Audiobookshelf access for "emily" .* but these changed too: permissions.update' "$work/server.log"
  go_on access-sneak
else
  flunk "the page reached the access-sneak checkpoint"
fi

if reached selftest; then
  curl -fsS "$abs/__state" >"$work/state6.json"
  check "ABS: no test account left, the people and root untouched" \
    jq -e '(.users | map(select(.username | startswith("alexandria-selftest-"))) | length == 0) and
      (.users | map(.username) | sort == ["emily", "noah", "root"])' "$work/state6.json"
  check "the server log has the test's steps" \
    grep -q 'provision: test accounts on Audiobookshelf .*cleanup ok; create ok; sign-in ok; access ok; remove ok' "$work/server.log"
  go_on selftest
else
  flunk "the page reached the selftest checkpoint"
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
