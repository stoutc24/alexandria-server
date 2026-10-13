# Alexandria Companion Server

A small, self-hosted helper for the
[Alexandria](https://stoutc24.github.io/AlexandriaUpdatesRoadmap/) media
app. It runs on a machine that stays on — a NAS, a home server, a
Raspberry Pi, a desktop — and gives everyone in your household one shared
place for the things a household shares.

It is **optional**. Alexandria works fully without it.

**Version 0.3.0** · Docker image for `linux/amd64` and `linux/arm64`

---

## What it does

**People and devices.** Everyone in the house gets a profile. Each phone
and Apple TV links to a person with a one-time code that you approve.
Nobody needs an account or a password for that.

**One sign-in, if you want it (Alexandria accounts).** Optional. Invite a
person to a username and password on this server, and set their media
servers up for them once — their own logins, sealed with a key that stays
out of the database and its backups; Plex people picked from your Plex,
nothing typed. Every phone they sign in on is then set up for them, and a
service you remove signs out everywhere. See
[docs/ACCOUNTS.md](docs/ACCOUNTS.md).

**Party Play.** Play music on a phone and every linked Apple TV, phone
and watch in the house can join and play along in sync. The server keeps
the shared clock and schedules each start, so the next song begins on
every device together. You can also hand the lead to another device.

**Listening statistics.** Each person's listening and reading history
stays in their own bucket. It never lands in anybody else's totals.

**One household library index.** The app sends the server what your media
servers hold, and the server merges it into one index. The same album on
three servers becomes one title with three copies. It reports which works
sit on more than one server — and only ever *reports* them.

Indexed: Audiobookshelf, Plex, Jellyfin, Navidrome, Grimmory (BookLore),
Calibre, Calibre-Web, Storyteller, Komga and Stump. Kavita and generic
OPDS catalogues are not indexed.

**Management from a browser or your phone.** You can approve devices,
manage people, take backups and review duplicates on the admin page at
`/admin`. A phone that is already linked can do the same from **Manage
server**.

## What it does not do

- **It has no sign-up and no email.** Nobody can register themselves. A
  person gets a sign-in only when you invite them (Alexandria accounts,
  optional), and nothing here asks for an email address.
- **It holds media-server logins only if you use Alexandria accounts.**
  Without them, logins stay on each device, in its keychain, exactly as
  they do without this server. With them, the logins you set up for
  people are sealed with `ALEXANDRIA_SECRETS_KEY`, which never sits in the
  database, and are opened only to travel, sealed again, to that person's
  own devices. No screen or API ever reads one back.
- **It does not decide what anyone is allowed to do.** Subscription status
  is checked on the device and nowhere else. Running this server does not
  unlock anything, and it never will. See
  [docs/COMPANION_DESIGN.md](docs/COMPANION_DESIGN.md) for why that rule
  is load-bearing.
- **It does not touch your media.** It keeps an index. Deleting a row here
  removes an index entry, never a file on any server. No route in this
  server deletes media.
- **It does not phone home.** It makes no outbound connections. It sends
  nothing to the developer and needs no internet access once its image is
  downloaded. (Picking a Plex person for an account is done by your own
  browser or phone talking to plex.tv, not by this server.) See
  [legal/PRIVACY_POLICY.md](legal/PRIVACY_POLICY.md).

---

## What you need

- **Docker**: Docker Engine on Linux, or Docker Desktop on macOS or
  Windows. Docker Compose v2 is recommended.
- **A 64-bit machine that stays on**: `linux/amd64` or `linux/arm64`.
  That covers most NAS boxes, a Raspberry Pi 4 or 5 on a 64-bit OS, and
  any PC. It idles at around 20 MB of memory.
- **Port 4000** on that machine, reachable from your network. The apps
  look for the server on port 4000 when they search your network.
- **Alexandria 3.1 or later** on iPhone, iPad or Android, and Alexandria
  on Apple TV.

---

## Install

### Option 1: the installer (Linux and macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/stoutc24/alexandria-server/main/scripts/install.sh | bash
```

The installer:

1. checks for Docker and Docker Compose;
2. creates `~/alexandria-server` (set `INSTALL_DIR` to choose another
   folder);
3. downloads `docker-compose.yml`;
4. writes a `.env` with a new admin token, a new secrets key for
   Alexandria accounts, and this machine's name;
5. pulls the image and starts it.

It prints the admin token and the secrets key once. Both are also kept in
that folder's `.env`. Copy the secrets key somewhere safe, apart from your
backups: without it, the service logins accounts keep cannot be opened.

### Option 2: Docker Compose

Make a folder with these two files.

`docker-compose.yml`:

```yaml
services:
  alexandria-server:
    image: ghcr.io/stoutc24/alexandria-server:latest
    container_name: alexandria-server
    restart: unless-stopped
    ports:
      - "4000:4000"
    volumes:
      - alexandria-data:/data
    environment:
      - ALEXANDRIA_API_TOKEN=${ALEXANDRIA_API_TOKEN:?set ALEXANDRIA_API_TOKEN in .env}
      - ALEXANDRIA_SERVER_NAME=${ALEXANDRIA_SERVER_NAME:-Alexandria Companion}
      - ALEXANDRIA_PUBLIC_URL=${ALEXANDRIA_PUBLIC_URL:-}
      - ALEXANDRIA_TRUSTED_PROXIES=${ALEXANDRIA_TRUSTED_PROXIES:-}
      - ALEXANDRIA_SECRETS_KEY=${ALEXANDRIA_SECRETS_KEY:-}
      - TZ=${TZ:-UTC}

volumes:
  alexandria-data:
```

`.env` (make the token with `openssl rand -hex 32`, and the secrets key —
a different value — with `openssl rand -base64 32`):

```bash
ALEXANDRIA_API_TOKEN=<your token>
ALEXANDRIA_SECRETS_KEY=<your secrets key>
ALEXANDRIA_SERVER_NAME=Living Room NAS
```

Keep a copy of the secrets key apart from your backups. A key that is set
but malformed stops the server from starting, on purpose. Left out,
Alexandria accounts still sign people in, but keep none of their service
logins.

Then:

```bash
docker compose up -d
```

### Option 3: a single `docker run`

```bash
docker run -d \
  --name alexandria-server \
  --restart unless-stopped \
  -p 4000:4000 \
  -v alexandria-data:/data \
  -e ALEXANDRIA_API_TOKEN="$(openssl rand -hex 32)" \
  -e ALEXANDRIA_SECRETS_KEY="$(openssl rand -base64 32)" \
  -e ALEXANDRIA_SERVER_NAME="Living Room NAS" \
  ghcr.io/stoutc24/alexandria-server:latest
```

Read the token and the secrets key back with
`docker inspect alexandria-server | grep -E 'ALEXANDRIA_(API_TOKEN|SECRETS_KEY)'`
and keep both somewhere safe. Re-create the container with the **same**
secrets key, or the service logins it keeps can no longer be opened.

### Check it is running

```bash
curl http://localhost:4000/health
```

You should see `"status": "healthy"`, the version and a list of
capabilities.

### Set up the admin login

Open `http://<server address>:4000/admin` in a browser. The first time,
the page asks for the **admin token**. It then sets up your admin login:

- a password;
- an entry in your authenticator app;
- ten single-use recovery codes (keep them).

After that you sign in with your password and the current code from your
authenticator. The token stays your way back in: if you lose the
authenticator, it can always set the login up again.

---

## Settings

All settings are environment variables, set in `.env` or with `-e`.

| Variable | Default | What it does |
|---|---|---|
| `ALEXANDRIA_API_TOKEN` | **required** | The admin token. The server refuses to start without one, with the placeholder `change-me`, or with fewer than 16 characters. Make one with `openssl rand -hex 32`. |
| `ALEXANDRIA_SERVER_NAME` | the machine's hostname | The name devices see when they find the server. Set it under Docker: inside a container the hostname is the container id, so without a name the server looks like a new machine after every recreate. |
| `ALEXANDRIA_PUBLIC_URL` | empty | The `https://` address the server is reached at through a reverse proxy. See [Reaching it from outside your home](#reaching-it-from-outside-your-home). |
| `ALEXANDRIA_TRUSTED_PROXIES` | empty | The address your reverse proxy connects from, if you use one (one address or range, or several, comma-separated). The sign-in and linking limits then count each visitor separately instead of everyone as the proxy. See [Reaching it from outside your home](#reaching-it-from-outside-your-home). |
| `ALEXANDRIA_SECRETS_KEY` | empty | Lets [Alexandria accounts](docs/ACCOUNTS.md) store each person's service logins, sealed, so their devices sign in by themselves. Make one with `openssl rand -base64 32` and keep it out of your backups. Without it, people can still sign in and link devices. `ALEXANDRIA_SECRETS_KEY_FILE` reads it from a file instead. |
| `TZ` | `UTC` | Timezone for the logs. |
| `PORT` | `4000` | In the installer's `.env`, the port on this machine. Keep 4000: the apps search your network on it. |
| `DB_PATH` | `/data/alexandria.db` | Where the database lives inside the container. Leave it unless you know why. |

All data lives in the `/data` volume: one SQLite file.

---

## Linking devices

Nobody types a long token into a phone. Each device asks for a code, and
you approve it.

**iPhone, iPad and Android**

1. Open **Settings › Companion Server**.
2. Tap **Find my server on this network**, or type its address
   (`192.168.1.50:4000`).
3. Tap **Get a link code**. The app shows a six-character code, like
   `NMS82B`.
4. Approve it, either:
   - on the admin page, where the device is waiting: type who it is and
     click **Approve**; or
   - from a phone that is already linked, under **Manage server** on its
     Companion Server screen. Each approval from a phone asks for a fresh
     code from your authenticator.

The device connects on its own within a couple of seconds.

**Apple TV**: open **Settings › Companion Server › Link Companion Server**.
Press **Find My Server** (or enter the address), then approve the code the
same way.

**Apple Watch and Wear OS**: no code. A linked phone links its watch on
its own, and the phone's Companion Server screen shows the watch's status.
If a try failed, a **Link watch** button appears there. The watch apps
need Alexandria Premium.

**The code is safe to read out loud.** On its own it does nothing:
approving it needs your admin login, and collecting the result needs a
secret that never leaves the device. Codes expire after ten minutes and
work once.

Approving a device under a name that already exists adds it to that
person, so someone's phone and iPad share one profile and one set of
stats. Each device holds its own token, so a lost phone can be revoked
without signing out anything else.

---

## Party Play

1. Link every device you want in the party to the same server.
2. On the phone that will lead, play something and turn on **Share what
   I'm playing** on its Companion Server screen.
3. Other linked devices show what is playing and can join it.

The server's clock keeps everyone together. The next song is announced
ahead of time and starts on every device at the same moment. The phone's
party dashboard shows each device and how far it has drifted. **Make
leader** hands the lead to another device.

---

## The admin page

At `/admin`. It shows:

- devices waiting to be approved;
- who is connected, their devices, and when each was last used;
- how many sessions and hours each person has;
- the library index: distinct titles, duplicates and servers;
- each person's **Sign-in & services** (Alexandria accounts): their
  sign-in, a waiting invite, and the servers set up for them, in order.

You can approve or revoke a device, remove a person entirely, download a
backup, and sign out any phone that is signed in to manage the server.
For a person's account you can invite them, set up, reorder and remove
their services, and pick their Plex from your Plex (your browser signs in
to Plex for that, on Plex's own page).

**It does not show what anyone listened to.** The management API returns
counts and timestamps only, and the page can render nothing more.

### An honest note about that

You own this machine, so you can open the database with any SQLite client
and read whatever is in it. No API design changes that: software you run
cannot keep secrets from you.

What is enforced is that the *product* never offers it. No screen and no
endpoint hands a household admin someone's listening history. Treat this
as a trust boundary between people who live together, not as
cryptography. If that is not good enough for your household, do not point
the app at a companion server.

---

## Reaching it from outside your home

Inside your home network nothing more is needed. To reach the server from
outside, put it behind an **HTTPS reverse proxy**, then set
`ALEXANDRIA_PUBLIC_URL` to that address. Over plain HTTP on someone else's
network, the tokens and codes in each request can be read.

Caddy, which handles the certificate itself:

```
alexandria.example.com {
    reverse_proxy 192.168.1.50:4000
}
```

nginx:

```nginx
server {
    listen 443 ssl;
    server_name alexandria.example.com;
    # ssl_certificate / ssl_certificate_key for this name

    location / {
        proxy_pass http://192.168.1.50:4000;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Party Play holds a request open for up to 15 seconds while it waits for
news, which fits every proxy's default timeout.

Then add these to `.env` and restart the server:

```bash
ALEXANDRIA_PUBLIC_URL=https://alexandria.example.com
ALEXANDRIA_TRUSTED_PROXIES=192.168.1.10
```

The app is told about the address, so a phone linked at home can switch
to it. A phone stays signed in to **Manage server** only over
`https://`.

`ALEXANDRIA_TRUSTED_PROXIES` is the address your proxy connects to the
server from: here, the machine Caddy or nginx runs on. With it, the
limits on signing in and linking count each visitor separately. Without
it, everyone who comes through the proxy counts as the proxy, so one
stranger could use up everyone's tries.

- **Not sure of the address?** Leave it out, open the admin page through
  the proxy once, then run `docker logs alexandria-server`. The server
  names the address that forwarded the request.
- **Name only your proxy.** A range that also covers other machines lets
  them claim to be anyone. Some Docker setups (Docker Desktop, for
  example) can show every connection as coming from one internal
  address. If the logged address is also what your other devices
  connect from, leave the setting out.
- **Your proxy must set or append `X-Forwarded-For`.** Caddy does by
  default, and the nginx example above appends it. A proxy that passes
  on whatever the client sent lets a client claim to be anyone.

`ALEXANDRIA_PUBLIC_URL` must be a plain https address: no user name,
query or fragment. Anything else is ignored with a warning in the log
(`docker logs alexandria-server`), and the app is not told about it.

---

## Backups

The whole server is one SQLite file. Either:

- click **Download backup** on the admin page; or
- run `docker cp alexandria-server:/data/alexandria.db ./backup.db`.

A backup taken from a phone (**Manage server › Back up**) asks for a fresh
authenticator code and leaves the admin login out of the file. Restoring
one means setting the login up again with your admin token.

The library index rebuilds itself from the app. What is really worth
backing up is people, their devices and their stats.

**The secrets key is not in any backup**, on purpose: a backup alone
cannot open the service logins Alexandria accounts keep. Keep the key
(`ALEXANDRIA_SECRETS_KEY` in your `.env`) somewhere safe of its own, such
as a password manager. Restored on a server with a different key, people,
devices and passwords come back, but two things the key sealed do not:
each saved service login shows as unreadable until you enter it again,
and anyone with two-step sign-in turned on cannot pass it until you
remove their sign-in and invite them again.

---

## Updating

If you used the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/stoutc24/alexandria-server/main/scripts/update.sh | bash
```

With Compose, in the folder that holds your `docker-compose.yml`:

```bash
docker compose pull && docker compose up -d
```

With `docker run`, pull the image, then remove and re-create the
container with the same `-v alexandria-data:/data` and settings.

Your data lives in the volume and survives updates.

**Set up before Alexandria accounts?** The installer's update adds a
secrets key to your `.env` and prints it once (copy it somewhere safe).
With Compose, add the `ALEXANDRIA_SECRETS_KEY` line to the
`environment:` list in `docker-compose.yml` and a key to `.env`
(`openssl rand -base64 32`). With `docker run`, add
`-e ALEXANDRIA_SECRETS_KEY=…` when you re-create the container.

## Uninstalling

```bash
docker compose down    # removes the container and keeps your data
```

To delete the data too, remove the volume: `docker compose down -v`, or
`docker volume rm` with the name `docker volume ls` shows. **This erases
every person, device and statistic.** Take a backup first.

---

## Security

- **The admin token is the master key.** Once the admin login is set up
  the token no longer opens the admin page, but it can always set the
  login up again. That is how you recover a lost authenticator. Treat it
  like a password.
- **Use HTTPS when the server leaves your home network.** See
  [Reaching it from outside your home](#reaching-it-from-outside-your-home).
- **A phone stays signed in to manage the server** (approve devices,
  manage people, back up) only when it reaches the server through an
  `https://` address. Over plain http it signs in for that session only,
  twelve hours at most.
  - Signing in always takes your password and a fresh code, and so does
    each backup from a phone and each device a phone approves.
  - A phone that stays signed in is signed out after 180 days unused.
  - You can sign out any phone, or all of them, on the admin page.
    Setting the login up again signs out every browser and phone.
  - The admin page notes how each phone's HTTPS was established (for
    example "HTTPS via proxy"). That note repeats what the request's
    headers said, and a client can write those itself, so read it as a
    hint, not proof. It grants nothing.
- **Guessing at the admin login is limited three ways.**
  - Each visitor gets 10 sign-in attempts a minute.
  - All visitors together get at most 20 wrong passwords or codes a
    minute, after a first 60. More addresses buy no more guesses.
  - One visitor alone can never use that up. If several keep it used up
    at once, your own sign-in waits too until they stop or the server
    restarts. Browsers and phones already signed in keep working.
  - Each signed-in phone has its own allowance for the codes its backups
    and approvals carry: 10 wrong, then one a minute. Nobody else can use
    it up, and a stolen phone cannot grind codes.
- **Device tokens, admin sessions and phones' admin sign-ins are stored
  only as hashes.** None of them can be replayed from a stolen database.
- **The admin login is different.** Its authenticator secret is stored as
  it is (the server needs it to check your codes), next to a slow hash of
  your password. A copy of the database lets someone generate your codes
  and guess at the password offline.
  - Guard backups and the data volume as you would the password.
  - Set the login up again if a copy leaks; that issues a new secret.
  - A backup taken from the phone leaves the admin login out.
- **Alexandria accounts' saved logins are sealed.** Each is encrypted
  (AES-256-GCM) with `ALEXANDRIA_SECRETS_KEY`, which never sits in the
  database, and bound to its own row. A login is opened only to send it,
  sealed again, to a key the person's own device made at sign-in: a
  stolen device token alone opens nothing. No admin screen or API returns
  one. People's own passwords are stored only as slow hashes (argon2id).
  See [docs/ACCOUNTS.md](docs/ACCOUNTS.md) for who can see what.
- **These routes answer without a token**, by necessity:
  - `/health` reports the name, version and capabilities, never library
    contents.
  - `/admin` is the management page itself, inert until you sign in.
  - `/api/v1/link/request` and `/api/v1/link/claim` serve devices being
    linked, which have no token yet. Both are rate limited to 30 requests
    a minute for each visitor.
  - `/invite` is the page an account invite link opens; it only hands the
    invite to the app.
  - `/api/v1/accounts/login`, `/invite/check` and `/invite/accept` serve
    people signing in to an Alexandria account. Each visitor gets 10 a
    minute; all visitors together get 20 wrong answers a minute after a
    first 60 (an allowance of their own, so a typo never spends the admin
    login's); and 10 wrong answers in a row lock that person's sign-in for
    15 minutes.

  Everything else requires a token.
- **The container runs as a non-root user** (uid 1000), and its memory use
  is capped (`GOMEMLIMIT=96MiB`).

---

## Troubleshooting

**The container exits straight away.** Run `docker logs alexandria-server`.
It is almost always the token: unset, left as `change-me`, or under 16
characters.

**"Find my server" finds nothing.** The apps search the device's own
network (its /24 subnet) on port 4000. Check that:

- the phone or TV is on the same network as the server, not a guest
  network;
- the server is on host port 4000;
- no firewall blocks that port.

You can always type the address instead. The fastest check is
`curl http://<server address>:4000/health` from another machine.

**A link code says it expired.** Codes last ten minutes. Ask for a new
one.

**Signing in says "too many wrong passwords or codes on this server".**
More wrong guesses have come in lately, from several addresses together,
than the admin login takes. Wait the seconds it names, then sign in once,
carefully. If it keeps happening, someone is guessing at the login:
`docker logs alexandria-server` shows the attempts, and
`docker restart alexandria-server` clears the count.

**A phone says "Too many sign-in attempts" while backing up or approving
a device.** That phone has made more than 10 tries in a minute, or sent
10 wrong codes lately. Wait a minute, then try once with a fresh code.
Signing in on the admin page is not affected.

**The library index is empty.** The app fills it, not the server. On a
linked phone, open the Companion Server screen and press **Refresh**
under **Household library**.

**Party Play devices drift apart.** Every device must be linked to the
same server. On Apple TV, **Settings › Companion Server › Party Play
diagnostics** shows how far each start landed from the party clock.

---

## Privacy, terms and license

- [Privacy policy](legal/PRIVACY_POLICY.md): what the server stores, and
  confirmation that nothing leaves your machine.
- [Terms of service](legal/TERMS_OF_SERVICE.md) · [EULA](legal/EULA.md)
- **License: proprietary.** See [LICENSE](LICENSE). You may run it for
  personal or internal use. It may not be redistributed, resold, or used
  to build a competing product.
- Third-party dependencies are listed in
  [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md); all are MIT or
  BSD-3.

## Support and feedback

- **Bugs and feature requests:** [open an issue](../../issues/new/choose).
- **Questions and setup help:**
  [r/AlexandriaMediaLib2](https://www.reddit.com/r/AlexandriaMediaLib2/).
- **Email:** stoutservers@gmail.com
- **Security problems:** privately, as described in
  [SECURITY.md](SECURITY.md). Never in a public issue.

This is proprietary software, so code contributions are not accepted and
pull requests are closed automatically. See
[CONTRIBUTING.md](CONTRIBUTING.md).
