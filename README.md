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
There are no accounts and no passwords for your family.

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

- **It does not manage accounts.** No passwords for family members, no
  registration, no email. Your media servers already do that.
- **It does not hold your media-server credentials.** Those stay on each
  device, in its keychain, exactly as they do without this server.
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
  downloaded. See [legal/PRIVACY_POLICY.md](legal/PRIVACY_POLICY.md).

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
4. writes a `.env` with a new admin token and this machine's name;
5. pulls the image and starts it.

It prints the admin token once. The token is also kept in that folder's
`.env`.

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
      - TZ=${TZ:-UTC}

volumes:
  alexandria-data:
```

`.env` (make the token with `openssl rand -hex 32`):

```bash
ALEXANDRIA_API_TOKEN=<your token>
ALEXANDRIA_SERVER_NAME=Living Room NAS
```

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
  -e ALEXANDRIA_SERVER_NAME="Living Room NAS" \
  ghcr.io/stoutc24/alexandria-server:latest
```

Read the token back with
`docker inspect alexandria-server | grep ALEXANDRIA_API_TOKEN` and keep it
somewhere safe.

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
- the library index: distinct titles, duplicates and servers.

You can approve or revoke a device, remove a person entirely, download a
backup, and sign out any phone that is signed in to manage the server.

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

Then add this to `.env` and restart the server:

```bash
ALEXANDRIA_PUBLIC_URL=https://alexandria.example.com
```

The app is told about the address, so a phone linked at home can switch
to it. A phone stays signed in to **Manage server** only over
`https://`.

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
- **Device tokens, admin sessions and phones' admin sign-ins are stored
  only as hashes.** None of them can be replayed from a stolen database.
- **The admin login is different.** Its authenticator secret is stored as
  it is (the server needs it to check your codes), next to a slow hash of
  your password. A copy of the database lets someone generate your codes
  and guess at the password offline.
  - Guard backups and the data volume as you would the password.
  - Set the login up again if a copy leaks; that issues a new secret.
  - A backup taken from the phone leaves the admin login out.
- **These routes answer without a token**, by necessity:
  - `/health` reports the name, version and capabilities, never library
    contents.
  - `/admin` is the management page itself, inert until you sign in.
  - `/api/v1/link/request` and `/api/v1/link/claim` serve devices being
    linked, which have no token yet. Both are rate limited to 30 requests
    a minute.

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

## Support

stoutservers@gmail.com
