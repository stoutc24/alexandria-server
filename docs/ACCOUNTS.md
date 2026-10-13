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
- `locked` (429, with `retryAfterSeconds`).
- `busy`, `rate-limited`.
- `invite-invalid`, `username-taken`, `bad-username`.
  Usernames: `^[a-z0-9][a-z0-9._-]{2,31}$`, case-insensitive.
- `password-too-short`, `bad-device-key`.

### A signed-in device (`Authorization: Bearer alxp_…`)

| Route | Answer |
|---|---|
| `GET /accounts/me` | `{profile, hasLogin, username?, totpEnabled?, deviceKey?, servicesAvailable}` |
| `GET /accounts/services?since=<revision>` | `{revision, unchanged: true}` when nothing changed. Otherwise `{revision, box}`: the list, sealed to this device (below). `503 secrets-key-missing`, `409 device-key-required` |
| `PUT /accounts/device-key` `{publicKey}` | A device linked by **code** gets a key, once (`409 device-key-exists` after) |
| `POST /accounts/password` `{currentPassword, newPassword}` | Other devices stay signed in |
| `POST /accounts/totp/setup` | `{secret, uri}` for an authenticator app. It is on only after… |
| `POST /accounts/totp/enable` `{code}` | …a code from it is confirmed. A code is never accepted twice |
| `POST /accounts/totp/disable` `{password}` | |

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
| `GET /admin/profiles/:id/account` | Sign-in state, waiting invite, services (no secrets), `secretsKeyConfigured`, `serviceTypes` |
| `POST /admin/profiles/:id/invite` | `{invite: {token, code, purpose, expiresAt, profileName}, serverUrl, webLink, link, serverUrlWarning?}`. `webLink` (`…/invite#invite=…`) is the one to share; `link` (`alexandria://…`) is what the page opens. A phone's admin sign-in adds a fresh code (`totpCode` or `recoveryCode`), as link approvals do |
| `DELETE /admin/profiles/:id/invite` | Cancel the waiting invite |
| `DELETE /admin/profiles/:id/account` | Remove the sign-in. Devices stay linked; services stay set up |
| `GET` / `POST /admin/profiles/:id/services` | List / add `{type, name?, baseUrl, loginKind, username?, password?, token?, apiKey?, plex?}`. A `plexuser` needs `token` and `plex`, and no `baseUrl` |
| `PATCH /admin/profiles/:id/services/:sid` | Edit. A secret left out keeps its value; an empty one clears it. `plex` left out keeps the pick; a new `plex` (a person picked again) needs its `token`. An edit that moves the service (another address, or another `type`) keeps no saved secret: it must carry the login's own (`password`, `token` or `apiKey`), or name `loginKind: none`, else 400 `login-required` and nothing changes |
| `DELETE /admin/profiles/:id/services/:sid` | Devices sign out of it at their next check |
| `PUT /admin/profiles/:id/services/order` `{ids}` | Every id once, first to last |

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
  no service login opens without the secrets key.
- **The running server (its operator):** everything it stores. That is the
  trade the household chose ("1A"): fully automatic, at the price of trusting
  whoever runs the server. A zero-knowledge design was the alternative and
  was declined.
- **The network:** with http, the sign-in password itself crosses in the
  clear. Prefer https (`ALEXANDRIA_PUBLIC_URL` behind a reverse proxy) when
  anyone signs in from outside the house. Service logins are always sealed.

---

## Not built yet

- Signing in with SSO/OIDC (Alexandria's own, or per service).
- People adding their own services from the app. The owner said yes; it is
  not built.
- Clients other than the phone (TV, Samsung, Fire TV).
- Server Premium: an admin buying Premium for the household through a relay
  of ours, never from this server.
- A QR code on the admin page.
- ABS sign-in by API key in the apps (the admin page steers to a password meanwhile).
