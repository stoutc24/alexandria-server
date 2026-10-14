#!/bin/bash
# The admin page's Plex picker, end to end. TEST-ONLY. From the repository
# root:
#
#   internal/adminui/testdata/flow/run.sh
#
# Builds and starts the real server binary (a throwaway database, a fresh
# API token and secrets key), sets up two people with curl and enrols the
# admin login. The harness proxy (main.go) serves the admin page with
# driver.js ahead of the page's own script and passes every other request
# to the server untouched. Headless Chrome drives the page: plex.tv is
# stubbed IN THE PAGE, nothing else is. At each checkpoint curl reads the
# real admin API; at the end a device signs in and opens what the server
# hands it.
#
# Needs Go, curl, jq, openssl and Google Chrome (CHROME=... for another).
# Nothing leaves this machine: Chrome resolves no host but 127.0.0.1, and
# the server makes no outbound connections. KEEP=1 keeps the work folder
# (it is kept anyway when something fails). NO_STORAGE=1 runs it all with
# the page's storage throwing, as with site data blocked.
set -euo pipefail

root=$(cd "$(dirname "$0")/../../../.." && pwd)
here="$root/internal/adminui/testdata/flow"
chrome=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
port=${PORT:-4567}
proxy_port=${PROXY_PORT:-4568}
work=$(mktemp -d "${TMPDIR:-/tmp}/alexandria-flow.XXXXXX")
pids=()
failed=0

cleanup() {
  local rc=$?
  for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done
  sleep 1
  for p in ${pids[@]+"${pids[@]}"}; do kill -9 "$p" 2>/dev/null || true; done
  pkill -f -- "--user-data-dir=$work/chrome" 2>/dev/null || true
  if [ "${KEEP:-}" = 1 ] || [ "$failed" != 0 ] || [ "$rc" != 0 ]; then
    echo "work folder kept: $work"
  else
    rm -rf "${work:?}"
  fi
}
trap cleanup EXIT

pass() { printf 'PASS %s\n' "$1"; }
flunk() { printf 'FAIL %s\n' "$1"; failed=1; }
# check <name> <command...>: the command's exit status decides.
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

echo "building the server and the harness"
(cd "$root" && go build -o "$work/server" ./cmd/server &&
  go build -o "$work/flow" ./internal/adminui/testdata/flow)

api_token=$(openssl rand -hex 24)
secrets_key=$(openssl rand -base64 32)
password="flow-admin-$(openssl rand -hex 8)"
admin_plex_token="ADMIN-PLEX-TOKEN-$(openssl rand -hex 10)"
abs_password="abs-pass-$(openssl rand -hex 6)"
old_token="OLD-PLEX-TOKEN-$(openssl rand -hex 8)"
key_grandma="PERSON-KEY-grandma-$(openssl rand -hex 8)"
key_bob="PERSON-KEY-bob-$(openssl rand -hex 8)"
key_carol="PERSON-KEY-carol-$(openssl rand -hex 8)"
key_hostile="PERSON-KEY-hostile-$(openssl rand -hex 8)"
secrets="$admin_plex_token $key_grandma $key_bob $key_carol $key_hostile $abs_password $old_token"

base="http://127.0.0.1:$port"
echo "starting the server on $base"
ALEXANDRIA_API_TOKEN=$api_token ALEXANDRIA_SECRETS_KEY=$secrets_key \
  DB_PATH="$work/alexandria.db" PORT=$port \
  "$work/server" >"$work/server.log" 2>&1 &
pids+=($!) && disown
up "$base/health" || { flunk "the server started"; tail -20 "$work/server.log"; exit 1; }

# Before enrolment the API token is the admin: two people, one of them with
# Plex set up the old way (an address and a typed token).
auth=(-H "Authorization: Bearer $api_token" -H "Content-Type: application/json")
grandma=$(curl -fsS "${auth[@]}" -d '{"name":"Grandma Test"}' "$base/api/v1/admin/profiles" |
  jq -er .profile.id)
old=$(curl -fsS "${auth[@]}" -d '{"name":"Old Plex Person"}' "$base/api/v1/admin/profiles" |
  jq -er .profile.id)
curl -fsS "${auth[@]}" "$base/api/v1/admin/profiles/$old/services" -o /dev/null \
  -d "$(jq -n --arg t "$old_token" '{type: "plex", name: "Plex (old)",
        baseUrl: "https://plex.example.com:32400", loginKind: "token", token: $t}')"
# Grandma's devices, linked as an app links one (request, approve, claim):
# her phone in use now, an old iPad last seen 41 days ago, a watch never
# used since. A TV is left waiting for the page to approve (step P).
link_device() {
  local req code claim
  req=$(curl -fsS -H "Content-Type: application/json" "$base/api/v1/link/request" \
    -d "$(jq -n --arg l "$1" '{deviceLabel: $l}')")
  code=$(jq -er .code <<<"$req")
  claim=$(jq -er .claimSecret <<<"$req")
  curl -fsS "${auth[@]}" "$base/api/v1/admin/links/approve" -o /dev/null \
    -d "$(jq -n --arg c "$code" --arg p "$grandma" '{code: $c, profileId: $p}')"
  curl -fsS -H "Content-Type: application/json" "$base/api/v1/link/claim" \
    -d "$(jq -n --arg s "$claim" '{claimSecret: $s}')" | jq -er .token
}
phone_tok=$(link_device "Grandma's iPhone")
ipad_tok=$(link_device "Old iPad")
link_device "Grandma's Apple Watch" >/dev/null
for tok in "$phone_tok" "$ipad_tok"; do
  curl -fsS -H "Authorization: Bearer $tok" "$base/api/v1/devices/me" -o /dev/null
done
"$work/flow" lastseen -db "$work/alexandria.db" -label "Old iPad" -days 41
# Ada: signed in with two-step on, for admin access (step Q).
ada=$(curl -fsS "${auth[@]}" -d '{"name":"Ada Admin"}' "$base/api/v1/admin/profiles" |
  jq -er .profile.id)
ada_invite=$(curl -fsS "${auth[@]}" -X POST "$base/api/v1/admin/profiles/$ada/invite" |
  jq -er .invite.token)
ada_pub=$("$work/flow" devicekey -out "$work/ada.key")
ada_tok=$(curl -fsS -H "Content-Type: application/json" "$base/api/v1/accounts/invite/accept" \
  -d "$(jq -n --arg i "$ada_invite" --arg k "$ada_pub" '{invite: $i, username: "ada",
        password: "ada-password-1", device: {label: "Ada phone", publicKey: $k}}')" |
  jq -er .deviceToken)
ada_secret=$(curl -fsS -X POST -H "Authorization: Bearer $ada_tok" \
  "$base/api/v1/accounts/totp/setup" | jq -er .secret)
curl -fsS -H "Authorization: Bearer $ada_tok" -H "Content-Type: application/json" -o /dev/null \
  "$base/api/v1/accounts/totp/enable" \
  -d "$(jq -n --arg c "$("$work/flow" totp -secret "$ada_secret")" '{code: $c}')"
tv=$(curl -fsS -H "Content-Type: application/json" "$base/api/v1/link/request" \
  -d '{"deviceLabel": "Playroom TV"}')
tv_code=$(jq -er .code <<<"$tv")
tv_claim=$(jq -er .claimSecret <<<"$tv")
# The admin login, as the page's set-up screen makes it. The page signs in
# with one recovery code, curl with another.
enrol=$(curl -fsS "${auth[@]}" "$base/api/v1/admin/auth/enroll" \
  -d "$(jq -n --arg p "$password" '{password: $p}')")
code_page=$(jq -er '.recoveryCodes[0]' <<<"$enrol")
code_curl=$(jq -er '.recoveryCodes[1]' <<<"$enrol")
jar="$work/curl.cookies"
curl -fsS -c "$jar" -H "Content-Type: application/json" -o /dev/null \
  -d "$(jq -n --arg p "$password" --arg c "$code_curl" '{password: $p, recoveryCode: $c}')" \
  "$base/api/v1/admin/auth/login"
admin() { curl -fsS -b "$jar" "$@"; }

jq -n --arg pw "$password" --arg rc "$code_page" --arg at "$admin_plex_token" \
  --arg g "$grandma" --arg o "$old" --arg abs "$abs_password" \
  --arg kg "$key_grandma" --arg kb "$key_bob" --arg kc "$key_carol" --arg kh "$key_hostile" \
  --arg ns "${NO_STORAGE:-}" --arg tc "$tv_code" --arg ts "$tv_claim" --arg ada "$ada" \
  '{password: $pw, recovery: $rc, adminToken: $at, grandmaId: $g, oldId: $o, absPassword: $abs,
    keys: {grandma: $kg, bob: $kb, carol: $kc, hostile: $kh}, noStorage: ($ns == "1"),
    tvCode: $tc, tvClaim: $ts, adaId: $ada}' \
  >"$work/config.json"
if [ "${NO_STORAGE:-}" = 1 ]; then echo "storage blocked in the page for this run (NO_STORAGE=1)"; fi

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

# reached <checkpoint>: wait for the driver to get there (2 minutes at most).
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

server_id=5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b
remote="https://203-0-113-7.abc123.plex.direct:32400"
hostile='<img src=x onerror=alert(1)>'
stout_conns='[
  {"uri": "https://192-168-1-9.abc123.plex.direct:32400", "local": true},
  {"uri": "https://203-0-113-7.abc123.plex.direct:32400"},
  {"uri": "https://2001-db8--9.abc123.plex.direct:32400"},
  {"uri": "https://198-51-100-20.def456.plex.direct:8443", "relay": true},
  {"uri": "http://192.168.1.9:32400", "local": true}]'
pick() {
  jq -cn --arg id "$server_id" --argjson c "$stout_conns" --argjson u "$1" --arg t "$2" \
    '{serverId: $id, serverName: "StoutOffice", userId: $u, userTitle: $t, connections: $c}'
}
# no_secret <file>: none of this run's secrets appear in it.
no_secret() {
  local s
  for s in $secrets; do
    if grep -qF -- "$s" "$1"; then return 1; fi
  done
  return 0
}
# the_plex <file>: the one Plex service in an admin answer.
the_plex() { jq -ce '[.services[] | select(.type == "plex")] | select(length == 1) | .[0]' "$1"; }

# check_admin <label> <profile> <name> <want-plex>: the admin's two views of
# a person (the services list and the account route the page reads) show
# the picked Plex service as expected, and no secret.
check_admin() {
  local label=$1 profile=$2 name=$3 want=$4 route svc
  for route in services account; do
    admin "$base/api/v1/admin/profiles/$profile/$route" >"$work/$label-$route.json"
    svc=$(the_plex "$work/$label-$route.json" || echo '{}')
    check "$label: curl GET /admin/profiles/:id/$route: saved as plexuser, named \"$name\"" \
      jq -e --arg n "$name" '.loginKind == "plexuser" and .name == $n and .hasSecret == true' <<<"$svc"
    check "$label: curl GET /admin/profiles/:id/$route: the plex object as picked" \
      jq -e --argjson want "$want" '.plex == $want' <<<"$svc"
    check "$label: curl GET /admin/profiles/:id/$route: the address the server chose ($remote)" \
      jq -e --arg r "$remote" '.baseUrl == $r' <<<"$svc"
    check "$label: curl GET /admin/profiles/:id/$route: no key, token or password in the answer" \
      no_secret "$work/$label-$route.json"
  done
}

if reached people; then
  admin "$base/api/v1/admin/profiles" >"$work/people.json"
  check "people: curl GET /admin/profiles: the TV approved on the page is Grandma's, the old iPad gone" \
    jq -e --arg g "$grandma" --arg a "Grandma's Apple Watch" --arg i "Grandma's iPhone" \
    '[.profiles[] | select(.id == $g) | .devices[].label] | sort == ([$a, $i, "Playroom TV"] | sort)' \
    "$work/people.json"
  check "people: curl GET /admin/profiles: the person added and removed on the page is gone" \
    jq -e '[.profiles[] | select(.name == "Flow Visitor")] | length == 0' "$work/people.json"
  go_on people
else
  flunk "the page reached the people checkpoint"
fi
if reached access; then
  admin "$base/api/v1/admin/accounts/summary" >"$work/access.json"
  check "access: curl GET /admin/accounts/summary: Ada's account has Server Admin access" \
    jq -e --arg a "$ada" '[.people[] | select(.profileId == $a) | .login.adminAccess] == ["serveradmin"]' \
    "$work/access.json"
  go_on access
else
  flunk "the page reached the access checkpoint"
fi
if reached added; then
  check_admin "after the add" "$grandma" "Plex" "$(pick 42 Grandma)"
  check "after the add: the Audiobookshelf service is still there, password not shown" \
    jq -e '[.services[] | select(.type == "abs" and .username == "grandma" and .hasSecret)] | length == 1' \
    "$work/after the add-services.json"
  go_on added
else
  flunk "the page reached the add checkpoint"
fi
if reached renamed; then
  check_admin "after the rename" "$grandma" "Grandma's Plex" "$(pick 42 Grandma)"
  go_on renamed
else
  flunk "the page reached the rename checkpoint"
fi
if reached repicked; then
  check_admin "after picking again" "$grandma" "Grandma's Plex" "$(pick 1002 bobsmith)"
  go_on repicked
else
  flunk "the page reached the pick-again checkpoint"
fi
if reached converted; then
  check_admin "after converting the old service" "$old" "Plex (old)" "$(pick 43 "$hostile")"
  go_on converted
else
  flunk "the page reached the convert checkpoint"
fi

for _ in $(seq 1 600); do [ -e "$work/report.json" ] && break; sleep 0.1; done
if [ -e "$work/report.json" ]; then
  jq -r '.checks[] | (if .ok then "PASS " else "FAIL " end) + "page: " + .name +
    (if .ok then "" else " :: " + .detail end)' "$work/report.json"
  jq -e '(.checks | length) > 0 and all(.checks[]; .ok)' "$work/report.json" >/dev/null || failed=1
  echo "page: $(jq '[.checks[] | select(.ok)] | length' "$work/report.json")/$(jq '.checks | length' \
    "$work/report.json") checks passed; $(jq .plexCalls "$work/report.json") plex.tv calls (stubbed)," \
    "$(jq .serverCalls "$work/report.json") calls to this server"
else
  flunk "the page sent its report"
fi

# device_box <profile> <username>: a device signs in from a fresh invite
# and opens its sealed services; prints what the box holds.
device_box() {
  local profile=$1 user=$2 pub inv tok
  pub=$("$work/flow" devicekey -out "$work/$user.key")
  inv=$(admin -X POST "$base/api/v1/admin/profiles/$profile/invite" | jq -er .invite.token)
  tok=$(curl -fsS -H "Content-Type: application/json" "$base/api/v1/accounts/invite/accept" \
    -d "$(jq -n --arg i "$inv" --arg u "$user" --arg k "$pub" \
          '{invite: $i, username: $u, password: "device-password-1", device: {label: $u, publicKey: $k}}')" |
    jq -er .deviceToken)
  curl -fsS -H "Authorization: Bearer $tok" "$base/api/v1/accounts/services" >"$work/$user-answer.json"
  "$work/flow" openbox -key "$work/$user.key" <"$work/$user-answer.json"
}

device_box "$grandma" grandma-device >"$work/grandma-box.json" || true
check "device: Grandma's device gets the picked person's key (bob's, after picking again)" \
  jq -e --arg k "$key_bob" '[.services[] | select(.type == "plex")][0] |
    .loginKind == "plexuser" and .token == $k' "$work/grandma-box.json"
check "device: ...with the server, the person and every address, local and relay marked" \
  jq -e --argjson want "$(pick 1002 bobsmith)" '[.services[] | select(.type == "plex")][0].plex == $want' \
  "$work/grandma-box.json"
check "device: ...and the remote https address as baseUrl" \
  jq -e --arg r "$remote" '[.services[] | select(.type == "plex")][0].baseUrl == $r' "$work/grandma-box.json"
check "device: Audiobookshelf still signs in with its password" \
  jq -e --arg p "$abs_password" '[.services[] | select(.type == "abs")][0].password == $p' \
  "$work/grandma-box.json"
check "device: no secret crosses in the clear (the answer is the sealed box)" \
  no_secret "$work/grandma-device-answer.json"

device_box "$old" old-device >"$work/old-box.json" || true
check "device: the converted service now carries the picked person's key, the old token gone" \
  jq -e --arg k "$key_hostile" --arg o "$old_token" '[.services[] | select(.type == "plex")][0] |
    .loginKind == "plexuser" and .token == $k and (tostring | contains($o) | not)' "$work/old-box.json"
check "device: ...and the hostile name, as plain data" \
  jq -e --argjson want "$(pick 43 "$hostile")" '[.services[] | select(.type == "plex")][0].plex == $want' \
  "$work/old-box.json"

check "the server's log holds no secret" no_secret "$work/server.log"
check "the proxy's log holds no secret" no_secret "$work/proxy.log"
check "Chrome's log holds no secret" no_secret "$work/chrome.log"

if [ "$failed" = 0 ]; then echo "RESULT PASS"; else echo "RESULT FAIL"; exit 1; fi
