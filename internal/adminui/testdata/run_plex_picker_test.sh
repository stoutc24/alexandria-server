#!/bin/sh
# Runs plex_picker_test.html (the admin page's Plex picker helpers, read
# straight from ../index.html) in headless Chrome, prints every check, and
# exits non-zero unless all of them pass. TEST-ONLY. From the repository
# root:
#
#   internal/adminui/testdata/run_plex_picker_test.sh
#
# CHROME=/path/to/chrome to use another Chrome. Chrome gets a throwaway
# profile and resolves no host at all, so nothing leaves this machine.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
chrome=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
profile=$(mktemp -d "${TMPDIR:-/tmp}/plex-picker-test.XXXXXX")
trap 'rm -rf "${profile:?}"' EXIT

dom=$("$chrome" --headless=new --disable-gpu --no-first-run --no-default-browser-check \
  --user-data-dir="$profile" --allow-file-access-from-files \
  --host-resolver-rules="MAP * ~NOTFOUND" --no-proxy-server \
  --virtual-time-budget=10000 \
  --dump-dom "file://$here/plex_picker_test.html" 2>/dev/null)

printf '%s\n' "$dom" |
  awk '/<pre id="result">/ { on = 1 } on { print } /<\/pre>/ { if (on) exit }' |
  sed -e 's/<[^>]*>//g' -e 's/&lt;/</g' -e 's/&gt;/>/g' -e 's/&quot;/"/g' -e 's/&amp;/\&/g'

printf '%s\n' "$dom" | grep -q 'RESULT PASS'
