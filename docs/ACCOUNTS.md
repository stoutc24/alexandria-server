# Alexandria Accounts

**Status:** built October 2026 on the `accounts` branch (server + phone app).
**Audience:** admins setting it up, and whoever builds the next client (TV,
Samsung, Fire TV).

One username and password for everything a household runs. The admin sets
each person up once — their Audiobookshelf, Jellyfin, Plex, Kavita… logins —
and sends an invite. The person signs in to Alexandria on any device, and
every service the admin set up signs in with them. Signing in also links the
device to this server, so Party Play, shared stats and the watch work at once,
with no link code to read out and nobody to approve it.

Opting out costs nothing: a person who never signs in keeps today's
per-service sign-in, unchanged.

---

## The rule that still governs everything

> **The server may do WORK. It must never grant PERMISSION.**

Accounts carry logins and addresses, nothing else. No answer from this server
unlocks Premium; the app decides entitlement on the device against
RevenueCat, as before (see `COMPANION_DESIGN.md`). The free app's two
services are picked **in the admin's priority order**, and the person's own
30-day swap rule still applies to later changes, so an admin cannot rotate
services around the free tier.

---

## Turning it on

Accounts work without any setup: people can be invited, sign in, and link
their devices. **Storing service logins** needs a secrets key:

```bash
openssl rand -base64 32
```

Give it to the server as `ALEXANDRIA_SECRETS_KEY`, or put it in a file and set
`ALEXANDRIA_SECRETS_KEY_FILE` (better with Docker secrets). Restart.
`GET /health` then lists `accounts.services`.

- **Keep the key outside the database and its backups.** That is the point of
  it: a copied database file, or a backup, opens nothing without it.
- **Lose it** and the stored logins can't be read. The admin page flags each
  one ("enter it again") and nothing else breaks: devices keep their existing
  sign-ins, and edits never erase a login the server can't read.
- The server refuses to start with a key that is the wrong size, a
  placeholder, or the same as `ALEXANDRIA_API_TOKEN`.

---

## Setting a person up (the admin page)

People → **Sign-in & services**:

1. **Add their services**, in priority order (first = kept first on the free
   app). For each service, pick how they sign in:
   - **Username + password**: their own login on that service.
   - **API key** (Kavita).
   - **Address only**: they sign in themselves on the device.
   - **Plex: pick the person.** Nobody types an address or digs out a token.
     *Sign in with Plex* (your own Plex account, on Plex's own page), pick
     your server, then the person: anyone you share it with, Plex Home users
     and friends alike. Your browser (or, in the phone app, your phone) asks
     plex.tv for that person's key to your server and hands it to this
     server, sealed like a password. Their devices then open your server as
     them — their history, playlists and library limits — with no Plex
     sign-in of their own. Your own Plex sign-in stays in the browser tab
     and is dropped when you're done.

     **This server never contacts plex.tv**; only the admin's browser or
     phone does, at that moment.

   Advice per service:
   - **Jellyfin**: use their own Jellyfin login, never a dashboard API key
     (those are admin-wide).
   - **Audiobookshelf**: their own username and password. (ABS 2.26+ API
     keys made for one user would be better, but the apps don't sign in
     with them yet.)
2. **Invite to sign in.** The invite is shown **once**: a code to read out
   (`K7QF-2M9X`) and a link to text them. It works once and expires in 7 days.
   For a person who already has a sign-in, the same button makes a
   **new-password invite**. From the phone app (Manage server → a person),
   *Share invite* sends the link, the code and the address in one message.

   The link is this server's `/invite` page (`…/invite#invite=…`), because
   messaging apps make web links tappable and `alexandria://` ones not. The
   page hands the invite to the app. The invite rides after the `#`, which
   browsers never send, so it reaches no log.
3. Removing a service signs every one of that person's devices out of it at
   their next check. Downloads stay.

The admin can set a login but never read one back. No admin answer carries a
stored secret, and a saved login never moves: changing a service's address,
or what kind of service it is, needs the login typed again. Otherwise
whoever can edit a service (the admin page, or a phone signed in to manage
the server) could point it at a server they run and have the person's
devices sign in there with it.

> **Invite links carry the address you opened the admin page at.** Opened at
> `localhost` on the server itself, the link would point phones at their own
> loopback. The page warns when that happens. Open it at the server's network
> address, or set `ALEXANDRIA_PUBLIC_URL`.

---

## What the person does

- **From the invite:** open the link on the phone, or type the code and the
  server's address under *Have an Alexandria account? → I have an invite* on the
  first-run screen.
  - The app shows the server's name and address.
  - They choose a username (one is suggested) and a password (8+ characters).
- **On another device:** *Sign in with Alexandria* with the username and
  password. If two-step is on, the app asks for the code.
- **Two-step sign-in** is optional and recommended. The person turns it on in
  the app with any authenticator app.

The app then signs in to each service through **the same code a person typing
their own login runs**, and says plainly what happened to each one:
- signed in;
- already on this device;
- yours to sign in to;
- part of Premium;
- uses http (the app asks before using plain http, as its connect screens do);
- or failed, with the reason.

It checks for changes at launch and on resume (at most every 15 minutes,
never while something plays). The check costs one small request when nothing
changed.

---

## API

All under `/api/v1`. Refusals are `{ "error": "…", "reason": "…" }`, and
clients branch on `reason`.

### Anyone (rate limited)

Limits: 10 a minute per client, a shared allowance across every visitor
(60, refilling one per 3 s), and each sign-in's own lockout (10 wrong answers
→ 15 minutes).

| Route | Body | Answer |
|---|---|---|
| `POST /accounts/invite/check` | `{invite}`: the link's token or the typed code (any case, any separator) | `{profileName, purpose: "invite"\|"reset", expiresAt, serverName, suggestedUsername?, username?}` |
| `POST /accounts/invite/accept` | `{invite, username, password, device: {label, publicKey}}` (`username` ignored for a reset) | `201` + the sign-in answer |
| `POST /accounts/login` | `{username, password, code?, device: {label, publicKey}}` | `200` + the sign-in answer |

**The sign-in answer:**

```json
{"deviceToken": "alxp_…", "deviceId": "…", "profile": {"id": "…", "name": "Sam Rivera"},
 "username": "sam", "servicesAvailable": true}
```

`deviceToken` is an ordinary profile device token, used as
`Authorization: Bearer …` everywhere a linked device's token is.

**Reasons:**
- `bad-credentials`: a wrong password, or no such user. Both take the same
  time.
- `code-required`: the password was right and the two-step code is next.
- `bad-code`.
- `locked` (429, with `retryAfterSeconds`): ten wrong answers in a row
  pause a sign-in for 15 minutes. From 1.1.0: a username nobody has pauses
  by the same rule, with the same answer, so the pause does not say who
  exists; "in a row" lapses after 15 minutes without a wrong answer; the
  pause is read again after the password check, so answers sent together
  meet it; and a restart clears every pause and count.
- `busy`, `rate-limited`.
- `invite-invalid`, `username-taken`, `bad-username`.
  Usernames: `^[a-z0-9][a-z0-9._-]{2,31}$`, case-insensitive.
- `password-too-short`, `bad-device-key`.

From 1.1.0 a request with no credential the server knows may carry 16 KiB
at most (a larger one is refused with 413 before it is read), and a
compressed body is refused (415 `encoded-body`).

### A signed-in device (`Authorization: Bearer alxp_…`)

| Route | Answer |
|---|---|
| `GET /accounts/me` | `{profile, hasLogin, username?, totpEnabled?, adminAccess?, deviceKey?, servicesAvailable}` |
| `GET /accounts/services?since=<revision>` | `{revision, unchanged: true}` when nothing changed. Otherwise `{revision, box}`: the list, sealed to this device (below). `503 secrets-key-missing`, `409 device-key-required` |
| `POST /accounts/password` `{currentPassword, newPassword}` | Other devices stay signed in. The password is checked as the sign-in checks it: `429 locked` while the sign-in is paused, `429 busy`, and a wrong one (`401 bad-credentials`) counts toward the pause |
| `POST /accounts/totp/setup` | `{secret, uri}` for an authenticator app. It is on only after… |
| `POST /accounts/totp/enable` `{code, password}` | …a code from it is confirmed, with the account's password (from 1.0.0: a stolen device token alone cannot switch it on). `400 password-required`, `401 bad-credentials` (counts toward the lockout), `401 bad-code`. A code is never accepted twice. From 1.1.0 also `429 locked` and `429 busy`, as the sign-in |
| `POST /accounts/totp/disable` `{password}` | Also takes away any admin access, and signs out its Manage server sign-ins. `401 bad-credentials` counts toward the lockout; from 1.1.0 `429 locked` while the sign-in is paused (the password is not looked at) and `429 busy` |
| `POST /accounts/admin-sign-in` `{password, code, deviceLabel, persistent}` | Manage server with this account's **admin access** (below): `201 {token: "alxa_…", device, access}`, the admin device token `/admin/auth/device-token` gives, held to the access. `403 no-admin-access`, `403 two-step-required`, `409 not-enrolled`, `429 locked`, `401 code-required`, `401 bad-credentials` (counts as a wrong sign-in). Shares the admin sign-in's rate limit |

**Admin access** (owner, 2026-10-13). The admin login can give a person's
account one of `serveradmin` (the media servers' admin sign-ins this server
keeps: `/admin/service-admins…`), `manage` (everything else under `/admin`:
people, devices, invites, services, backups, the signed-in phones list) or
`full` (both), on the admin page's Sign-in tab, and only to a sign-in with
two-step on.
- The phone signs in with the account's own password and code
  (`POST /accounts/admin-sign-in`) and is then held to that access by
  `middleware.AdminScopeGate` (`403 access-denied` past it).
- Its step-ups (invites, link approvals, backups, opening the kept sign-ins)
  take a code from **the account's** authenticator, never the admin login's,
  and recovery codes stay the admin login's alone.
- It may sign itself out. It may not end anyone else's sign-in, nor change
  anyone's admin access, its own included.
- A change that only adds (to `full`) carries the account's signed-in phones
  over. Anything else signs them out, as do a new password, two-step off,
  and removing the sign-in or the person.

**Inside the box** (`services` in priority order):

```json
{"revision": "…", "services": [
  {"id": "…", "type": "abs", "name": "Audiobooks", "baseUrl": "https://abs.example",
   "loginKind": "password", "priority": 0, "username": "sam", "password": "…"},
  {"id": "…", "type": "plex", "name": "Plex",
   "baseUrl": "https://203-0-113-7.abc123.plex.direct:32400",
   "loginKind": "plexuser", "priority": 1, "token": "…",
   "plex": {"serverId": "5d77…2a3b", "serverName": "StoutOffice",
            "userId": 42, "userTitle": "Grandma",
            "connections": [
              {"uri": "http://192.168.1.9:32400", "local": true},
              {"uri": "https://203-0-113-7.abc123.plex.direct:32400"},
              {"uri": "https://relay-1.abc123.plex.direct:8443", "relay": true}]}}
]}
```

- **Types:** `abs`, `jellyfin`, `plex`, `kavita`, `komga`, `navidrome`,
  `booklore` (shown as Grimmory), `calibre`, `calibreweb`, `storyteller`.
- **Login kinds:** `password`, `token`, `apikey`, `none`, `plexuser`.
- **`plexuser`** (Plex only): `token` is the person's key to that one server
  (plex.tv's `shared_servers` access token for them). `plex` says which
  server (`serverId` = its machine identifier), who (`userTitle`), and every
  address plex.tv listed for it; the device picks one the way Plex's own apps
  do (local first on Wi-Fi). `baseUrl` is filled from them (remote https
  first) for display and for older apps.
- `unreadable: true` marks a login the server could not open (its key
  changed). The device keeps what it already has.

### The admin (`RequireAdmin`)

| Route | |
|---|---|
| `GET /admin/accounts/summary` | Everyone at once, for the admin page's People list: `{people: [{profileId, login: {username, totpEnabled, locked, adminAccess, lastLoginAt?} \| null, invite: {purpose, expiresAt} \| null, services: <count>}], secretsKeyConfigured}`, by name. The same reads as the route below, and no secret: no code, token, password or address |
| `PUT /admin/profiles/:id/admin-access` `{access}` | `none`, `serveradmin`, `manage` or `full` (above): `{adminAccess, signedOut}`. The admin login's alone (`403 access-denied` for an account's sign-in). `409 two-step-required`, `404 no-login`, `400 bad-access` |
| `GET /admin/profiles/:id/account` | Sign-in state, waiting invite, services (no secrets), `secretsKeyConfigured`, `serviceTypes` |
| `POST /admin/profiles/:id/invite` | `{invite: {token, code, purpose, expiresAt, profileName}, serverUrl, webLink, link, serverUrlWarning?}`. `webLink` (`…/invite#invite=…`) is the one to share; `link` (`alexandria://…`) is what the page opens. A phone's admin sign-in adds a fresh code (`totpCode` or `recoveryCode`), as link approvals do |
| `DELETE /admin/profiles/:id/invite` | Cancel the waiting invite |
| `DELETE /admin/profiles/:id/account` | Remove the sign-in. Devices stay linked; services stay set up |
| `GET` / `POST /admin/profiles/:id/services` | List / add `{type, name?, baseUrl, loginKind, username?, password?, token?, apiKey?, plex?}`. A `plexuser` needs `token` and `plex`, and no `baseUrl` |
| `PATCH /admin/profiles/:id/services/:sid` | Edit. A secret left out keeps its value; an empty one clears it. `plex` left out keeps the pick; a new `plex` (a person picked again) needs its `token`. An edit that moves the service (another address, or another `type`) keeps no saved secret: it must carry the login's own (`password`, `token` or `apiKey`), or name `loginKind: none`, else 400 `login-required` and nothing changes |
| `DELETE /admin/profiles/:id/services/:sid` | Devices sign out of it at their next check |
| `PUT /admin/profiles/:id/services/order` `{ids}` | Every id once, first to last |
| `GET /admin/provision` | What this server can do on each service with the Server Admin sign-ins it keeps: `{available, services: [{type, name, create, reset, email, libraries, note?, access?, serverAdmin, host?}]}`. `email` is `optional`, `required` or `username` (Komga signs in with one); `libraries`: the service has libraries to pick (all but Storyteller and Calibre-Web); `access`: what Change access can set there, `{libraries, download, laterLibraries, note?}` (absent on Calibre-Web). No secret |
| `GET /admin/profiles/:id/services/:sid/provision` | Change access (below): what the account that login's username names has on its service, read with the kept Server Admin sign-in: `{service, host, loginHost, username, can, access: {allLibraries, libraries, download}, libraries: [{id, name}]}`. `host` is where a change goes, `loginHost` the address the login itself uses. `404 no-account`, `409 admin-account`, `409 no-username` (the login has none), `409 no-service-admin`, `400 not-supported`, `404 service-not-found`, and the sign-in and service refusals below. An account's own admin sign-in needs `full` |
| `PUT /admin/profiles/:id/services/:sid/provision` `{allLibraries, libraries, download}` | Change that account's libraries and downloads, then read it back: the same answer as the `GET`, as the service now has it. `502 not-taken` (the service said yes but the account read back unchanged), `502 also-changed` (the change took, but the account's other rights read back different: the `error` names them), each answer carrying what the account has; `409 nothing-to-change` (Navidrome with one library), `400 no-libraries`, `409 libraries-gone`, and the refusals of the `GET` |
| `GET /admin/provision/:type/libraries` | That service's libraries, read with the kept Server Admin sign-in, for the forms to pick from: `{libraries: [{id, name}], service, host}` (none on a server with one). The same refusals as below for the sign-in and the service. An account's own admin sign-in needs `full` |
| `POST /admin/provision/:type/selftest` | "Test accounts" (below): with the kept Server Admin sign-in, delete any account an earlier test left, make a throwaway one, sign in as it, change its access, delete it: `{service, host, report: {service, ok, steps: [{name: cleanup\|create\|sign-in\|access\|remove, ok, detail?}], leftover?}}`. A failed step is in the report, not an HTTP error. `409 no-service-admin`, `409 service-admin-unreadable`, `400 not-supported`. An account's own admin sign-in needs `full` |
| `POST /admin/profiles/:id/provision` `{type, username, password, name?, email?, allowReset?, libraries?, totpCode?, recoveryCode?}` | Make the person's account on that service — or, with `allowReset`, set an existing account's password — signed in with the kept Server Admin sign-in (below): `{outcome: created\|reset, note?, service, host}`. From 1.0.0 a reset from an admin PHONE carries a fresh code, as an invite does: `403 step-up-required` without one, `401 step-up-failed` for a wrong or spent one (the phone stays signed in); phones share the sign-in rate limit here. The admin page and a new account need none. `libraries` is the ids a NEW account may see; left out (or null) is every library. `409 account-exists` (ask, then send `allowReset`), `409 admin-account`, `409 reset-unsupported`, `409 no-service-admin`, `409 service-admin-unreadable`, `409 libraries-gone` (none of the picked ones is there now), `400 email-required`, `400 email-username`, `400 no-libraries` (an empty pick), `400 not-supported`, `502 admin-sign-in-failed`, `502 service-unreachable`, `502 service-refused` (the service's own words). An account's own admin sign-in needs `full` |

### Making their accounts on your servers

Owner, 2026-10-14: setting up Emily's ABS and Grimmory should make those
accounts too. With a **Server Admin sign-in** kept for a service, the
admin page and an admin phone tick "Create this account on …" when they
add a person's username-and-password login; the server then signs in to
the service and makes the account, or — after asking — sets its password.
This is the one place the server connects out: to the household's own
servers, at the address saved with the sign-in, only when asked
(`internal/provision`, each service's calls read from its own source).

A new account can read, stream and download, and nothing more: no
uploads, edits, deletes or admin rights (owner, 2026-10-14: "by default we
should be doing read only, plus download for media"). The forms list the
service's libraries under the tick, all ticked; unticked ones are hidden
from the new account ("need to also pick libraries available"). All ticked
sends no list, which is every library, the ones added later included where
the service has that notion. Setting an existing account's password
changes its password and nothing else.

| Service | Create | Set an existing password | What a new account gets |
|---|---|---|---|
| Audiobookshelf | yes | yes | Active user: downloads; no update, delete or upload; every library, or the picked ones. ABS signs that account out elsewhere on a reset |
| Grimmory | yes | yes | Needs a name and an email; download, OPDS, KOReader and Kobo sync; every library, or the picked ones. Grimmory's website asks for a new password on the first sign-in there |
| Jellyfin | yes | yes | Jellyfin's own defaults for a new user (playback, downloads, nothing to manage or delete, hidden from the login screen); every library, or only the picked ones |
| Navidrome | yes | yes | Not an admin; every library, or the picked ones (downloads are one switch for the whole server there) |
| Komga | yes | yes | The username is an email address; file download and page streaming; every library, or the picked ones |
| Kavita | yes | yes | Invited and accepted in one go: Login, Download, Change Password, Bookmark; every library, or the picked ones. With mail set up Kavita also emails the invite |
| Stump | yes | yes | Download, change-password, KOReader sync; kept out of the libraries not picked. A reset keeps the account's permissions and limits |
| Storyteller | yes | no | Invited and accepted in one go: read, list and download; one collection. Storyteller can only email someone a reset link |
| Calibre-Web | yes | no | Its own form: downloads and the book viewer only, whatever its defaults for new users say (out of the box those give none); the server's own sidebar and language; one collection. Its edit form would reset an account's permissions |

Admin, owner and root accounts are never changed, and neither is the kept
sign-in's own account. When a service makes the account but will not take
the picked libraries (Jellyfin, Navidrome, Stump do that in a second step),
the answer's `note` says so, and what the account can see until it is
changed there.

**Change access.** A person's username-and-password login on a service
with a kept Server Admin sign-in has a "Change access" button (owner,
2026-10-14: "allow the admins to adjust permissions and library access
from the server"). It reads the account that username names and changes two
things: which libraries it sees, and whether it can download. Each
service's own record of the account goes back as it was read, with those
two changed, so its password, other rights and age limit stay. The account
is read just before and again afterwards with the same sign-in: "Saved"
means the service has it; a service that said yes but kept something
answers `not-taken`, and one whose other rights read back different
answers `also-changed`, naming them, so they can be put right in the
service. Both show what the account has.

| Service | Libraries | Downloads | "All libraries" |
|---|---|---|---|
| Audiobookshelf | yes | the download permission | includes ones added later |
| Grimmory | yes, at least one | `canDownload` | the ones there are now |
| Jellyfin | yes | content downloading | includes ones added later |
| Komga | yes | the `FILE_DOWNLOAD` role | includes ones added later |
| Kavita | yes | the `Download` role | the ones there are now |
| Navidrome | yes, at least one (none to pick with one library) | no: one switch for the whole server | the ones there are now |
| Stump | yes, by keeping it out of the others | `DOWNLOAD_FILE` | includes ones added later |
| Storyteller | no: one collection | `bookDownload` | — |
| Calibre-Web | no Change access: its edit form resets an account's permissions | | |

The change goes to the server the Server Admin sign-in names; when the
person's login uses another address for it, the panel says both.

**Test accounts.** Each kept sign-in on the Server Admin page has a "Test
accounts" button (owner, 2026-10-14: "so long as we also have a cleanup on
it so we don't create a bunch of junk accounts"). It deletes any account an
earlier test left, makes a throwaway account the same way a person's is
made, signs in as it with its own password, changes its access as Change
access does (downloads switched over and, with several libraries, only the
first) and reads it back, and deletes it again, even when a step before
failed. Its accounts, and the only ones it ever deletes, are
named `alexandria-selftest-` plus six random characters (on Komga that is
the email address, at `selftest.invalid`); admin, owner and root accounts
are never deleted. Each step shows on the page and in the server log
(`provision: test accounts on …`). Stump deletes for good (`hardDelete`);
deleting there needs the server owner's sign-in. A test account never
plays anything, so Audiobookshelf has no devices to clear when it goes.

---

## The sealed box

The companion often runs on plain http inside the house, so the logins
travel sealed to a key **the device made at sign-in**. It is an X25519 key
pair whose private half never leaves the device's keychain. A stolen device
token alone opens nothing.

```
eph          = a fresh X25519 key pair per box
shared       = X25519(eph.private, device.public)
key          = HKDF-SHA256(secret = shared,
                           salt   = eph.public ‖ device.public,
                           info   = "alexandria-accounts-v1", 32 bytes)
ciphertext   = AES-256-GCM(key, 12-byte random nonce, plaintext,
                           associated data = "alexandria-accounts-v1")
               with the 16-byte tag appended
box          = {"alg": "x25519-hkdf-sha256-aes256gcm-v1",
                "epk": eph.public, "nonce": …, "ct": …}   (standard base64)
```

`internal/vault/testdata/devicebox_vector.json` pins one box, made with fixed
randomness. **A new client must open it byte for byte before it ships.** The
phone app does (`test/account_box_test.dart`).

At rest, each stored login is AES-256-GCM under the secrets key. The
associated data binds it to its own row, so a login cannot be swapped onto
another person's service.

---

## What each party can see

- **The database or a backup, alone:** names, usernames, addresses, hashes;
  no service login opens without the secrets key. A waiting invite's link
  token is 256 random bits, stored as its hash; its short code (about 40
  bits) is stored, from 1.1.0, under a key made from `ALEXANDRIA_API_TOKEN`,
  which the database does not hold, so a copy alone cannot be searched for
  the code. A backup taken from a phone carries no waiting invite. (An
  invite made before 1.1.0 keeps working by its link; its typed code does
  not, so make a new one.)
- **The running server (its operator):** everything it stores. That is the
  trade the household chose ("1A"): fully automatic, at the price of trusting
  whoever runs the server. A zero-knowledge design was the alternative and
  was declined.
- **The network:** with http, the sign-in password itself crosses in the
  clear. Prefer https (`ALEXANDRIA_PUBLIC_URL` behind a reverse proxy) when
  anyone signs in from outside the house. Service logins are always sealed.

---

## Alexandria Link (2026-11-08)

Owner: "a user can send me the link code, I can auth them in and select
their profile and they just auth from there, with that profile."

A device linked by **code** gets its profile's services too, with no
username or password:

1. The app makes its X25519 key and sends the public half WITH the request:
   `POST /link/request {deviceLabel, publicKey}` (`400 bad-device-key`).
2. The admin approves the code with a profile, as before.
3. `POST /link/claim` mints the device's token and, in the same step, makes
   the request's key that device's: the answer carries `deviceKey: true`.
   A request without a key (an app from before Link) links as it always did
   — `deviceKey: false`, stats and Party Play only.
4. The device reads `GET /accounts/services` like a signed-in account's.

There is no route that sets a key by token alone. Until 2026-11-08
`PUT /accounts/device-key` did, once, for any token without one; on a
plain-http network a copied token could set its own key first and have the
person's services sealed to it. A device linked by code before Link gets its
services by linking again with a new code. `/health` lists
`link.device-key` on servers that do this.

Clients: phone, Mac, Windows, Apple TV, Samsung / Fire TV. On a TV the
services go into the TV profile that showed the code.

---

## Not built yet

- Signing in with SSO/OIDC (Alexandria's own, or per service).
- People adding their own services from the app. The owner said yes; it is
  not built.
- TV and Samsung / Fire TV sign-in by username and password (they link by
  code; see Alexandria Link).
- Server Premium: an admin buying Premium for the household through a relay
  of ours, never from this server.
- A QR code on the admin page.
- ABS sign-in by API key in the apps (the admin page steers to a password meanwhile).
