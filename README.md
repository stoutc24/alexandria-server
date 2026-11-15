# Alexandria Companion Server

A small, self-hosted helper for the
[Alexandria](https://stoutc24.github.io/AlexandriaUpdatesRoadmap/) media
app. It runs on a machine that stays on — a NAS, a home server, a
Raspberry Pi, a desktop — and gives everyone in your household one shared
place for the things a household shares.

It is **optional**. Alexandria works fully without it.

**Version 1.1.0** · Docker image for `linux/amd64` and `linux/arm64`

New to it? Start at [Install](#install). How each feature works in the
apps is in the [help articles](docs/kb/README.md); what changed in each
version is in [CHANGELOG.md](CHANGELOG.md).

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

**Kids mode.** Mark a person as a kid and every device linked to them
shows a simple Home of big pictures, with no Search, Settings or
downloads, until you turn it off. It changes the screens only; what a
child can open is still up to their media-server user. See the
[help article](docs/kb/kids-mode.md).

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
- **It does not phone home.** It sends nothing to the developer and needs
  no internet access once its image is downloaded. It connects only to
  your own servers, and only to create or reset an account you asked for:
  with the Server Admin sign-in you saved for a service, it can list that
  server's libraries for you to pick from, and make a person's account
  there (or set its password), when you set up their services. (Picking a Plex person for an account is done by your own
  browser or phone talking to plex.tv, not by this server.) One exception,
  only while someone is picked from a Plex server: every 15 minutes it asks
  plex.tv for its own public internet address, and when that changed, asks
  your Plex server's new address who it is, to keep the person's saved Plex
  addresses current (no sign-in or key is sent with either). Set
  `ALEXANDRIA_PLEX_ADDRESS_WATCH=off` to stop it. See
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
- **A current Alexandria app**: iPhone, iPad, Android, Mac, Windows or
  Apple TV. Signing a device in to someone's services by code
  ([Alexandria Link](docs/kb/alexandria-link.md)) needs Alexandria 3.2.1
  or later.

---

## Install

On **Windows**, use [Option 2](#option-2-docker-compose) with Docker
Desktop; [Windows](#windows) below has the two commands that differ.

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
that folder's `.env`, which only your user can read. Copy the secrets key
somewhere safe, apart from your backups: without it, the service logins
accounts keep cannot be opened. It then waits for the server to answer and
prints the address of its admin page.

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

This is the short version. The repo's
[`docker-compose.yml`](docker-compose.yml) adds a health check (so
`docker compose up -d --wait` waits until the server is healthy), memory
limits and the Plex address-watch switch; download that file instead if
you want them. `:latest` follows every release; to choose when you update,
pin a release such as `ghcr.io/stoutc24/alexandria-server:1.1.0`.

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

#### Windows

Install [Docker Desktop](https://docs.docker.com/desktop/install/windows-install/),
make a folder with the two files above, and run `docker compose up -d`
there in PowerShell. Windows has no `openssl`, so make the two values in
PowerShell instead. Run this once for the token:

```powershell
$b = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
($b | ForEach-Object { $_.ToString('x2') }) -join ''
```

and this once for the secrets key (a different value):

```powershell
$b = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
[Convert]::ToBase64String($b)
```

Paste each into `.env` after its `=`, with no quotes. Save `.env` as plain
text (Notepad: **Save as type › All files**, so it is not named
`.env.txt`). Or write it from PowerShell, which also keeps it plain text
(Windows PowerShell's `>` writes UTF-16, which Docker Compose can misread):

```powershell
@"
ALEXANDRIA_API_TOKEN=<your token>
ALEXANDRIA_SECRETS_KEY=<your secrets key>
ALEXANDRIA_SERVER_NAME=Living Room NAS
"@ | Set-Content -Path .env -Encoding ascii
```

Allow Docker Desktop through Windows Firewall on private
networks when Windows asks, or the apps cannot reach port 4000.

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
(in PowerShell: `docker inspect alexandria-server | Select-String 'ALEXANDRIA_(API_TOKEN|SECRETS_KEY)'`)
and keep both somewhere safe. Re-create the container with the **same**
secrets key, or the service logins it keeps can no longer be opened.

### Check it is running

```bash
curl http://localhost:4000/health
```

You should see `"status": "healthy"`, the version and a list of
capabilities. In Windows PowerShell, use `curl.exe` (plain `curl` is a
different command there) or `Invoke-RestMethod http://localhost:4000/health`.

### Set up the admin login

Open `http://<server address>:4000/admin` in a browser. The first time,
the page asks for the **admin token**. It then sets up your admin login:

- a password;
- an entry in your authenticator app;
- ten single-use recovery codes (keep them).

After that you sign in with your password and the current code from your
authenticator. The token stays your way back in: if you lose the
authenticator, it can always set the login up again. After about ten wrong
tries in a minute, sign-in pauses for a minute.

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
| `ALEXANDRIA_PLEX_ADDRESS_WATCH` | on | Keeps the Plex addresses saved for people you picked from your Plex current when your home's internet address changes. Only while someone is picked from a Plex server, every 15 minutes, the server asks `https://plex.tv/:/ip` (Plex's own "what is my address" page, no sign-in) for its public IPv4 address. When it changed, it asks your Plex server's new `…plex.direct` address for `/identity` (no key sent) and moves the saved address only when your server answers as itself, or, when your router can't reach its own public address from inside, when your server answers on its home address and the old address was this server's own. Local and relay addresses are never changed. `off` (or `false`, `0`, `no`) stops it; the admin's app still refreshes the addresses from plex.tv when it opens. |
| `ALEXANDRIA_LISTEN_ADDR` | empty | **Leave unset in Docker**, where the `ports:` line decides what reaches the server. Running the server program directly, this is the one address it listens on: `127.0.0.1` keeps it to this machine (behind a reverse proxy on the same machine), `192.168.1.50` to one network. Empty listens on every address. Anything but one IP address stops the server from starting. |
| `TZ` | `UTC` | Timezone for the logs. |
| `PORT` | `4000` | In the installer's `.env`, the port on this machine. Keep 4000: the apps search your network on it. |
| `DB_PATH` | `/data/alexandria.db` | Where the database lives inside the container. Leave it unless you know why. |

All data lives in the `/data` volume: one SQLite file.

---

## Linking devices

Nobody types a long token into a phone. Each device asks for a code, and
you approve it.

**iPhone, iPad, Android, Mac and Windows**

1. Open **Settings › Companion Server**.
2. Tap **Find my server on this network**, or type its address
   (`192.168.1.50:4000`).
3. Tap **Get a link code**. The app shows a six-character code, like
   `NMS82B`.
4. Approve it, either:
   - on the admin page, where the device is waiting: type who it is and
     click **Approve**; or
   - from a phone that is already linked, under **Manage server** on its
     Companion Server screen: the device is under **Waiting to connect**.
     Each approval from a phone asks for a fresh code from your
     authenticator.

The device connects on its own within a couple of seconds.

**Apple TV**: open **Settings › Companion Server › Link Companion Server**
(or **Link with a Code** on a new TV's first screen). Press **Find My
Server** (or enter the address), then approve the code the same way. From
a phone you can also start from the person: **Manage server ›** the
person **› Link a TV**, and pick or type the code the TV shows.

**A TV takes no invite.** An invite (a person's **Invite to sign in**) is
for a phone, tablet or computer, where the person chooses a password. A
TV always shows a code of its own, and approving that code for a person
signs the TV in to the services you set up for them. More in
[Alexandria Link](docs/kb/alexandria-link.md).

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
  sign-in, a waiting invite, and the servers set up for them, in order;
- **Server Admin sign-ins**: your own admin sign-in to each server you
  run, so Server Admin on your phones can manage all of them, including
  ones you do not play from. Save one per service (Audiobookshelf,
  Jellyfin, Kavita, Komga, Navidrome, Grimmory, Storyteller, Stump,
  Calibre-Web); a phone signed in to **Manage server** fetches them once,
  with a fresh authenticator code. Passwords are never shown again. Plex
  is not kept: its admin sign-in is your whole Plex account, so each
  phone signs in with Plex itself.

You can approve or revoke a device, remove a person entirely, download a
backup, and sign out any phone that is signed in to manage the server.
Each person also has a **Kids mode** button
([what it does](docs/kb/kids-mode.md)).
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

The whole server is one SQLite database. Either:

- click **Download backup** on the admin page (a complete copy, taken
  safely while the server runs); or
- stop the server and copy its data folder:

  ```bash
  docker stop alexandria-server
  docker cp alexandria-server:/data ./alexandria-data-backup
  docker start alexandria-server
  ```

Do not copy `alexandria.db` out of a **running** server: the newest
changes sit in a second file beside it until the server tidies up, so
the copy can be missing them.

A backup taken from a phone (**Manage server › Back up**) asks for a fresh
authenticator code and leaves the admin login out of the file, and the
Server Admin sign-ins with it. Restoring one means setting the login up
again with your admin token, and saving those sign-ins again.

The library index rebuilds itself from the app. What is really worth
backing up is people, their devices and their stats.

### Restoring a backup

With the server stopped, put the backup file in place of the database
and start it again. The second command also clears the two side files a
stopped server can leave, which belong to the old database:

```bash
docker stop alexandria-server
docker run --rm --volumes-from alexandria-server -v "${PWD}:/backup" alpine sh -c 'rm -f /data/alexandria.db-wal /data/alexandria.db-shm && cp /backup/backup.db /data/alexandria.db && chown 1000:1000 /data/alexandria.db'
docker start alexandria-server
```

Run it in the folder that holds `backup.db` (the file **Download backup**
gave you, renamed). The middle command is one line, and works as written
in a Linux or macOS terminal and in Windows PowerShell. To move to another machine, install the server there
with the **same** `ALEXANDRIA_SECRETS_KEY`, then restore the backup the
same way. Linked devices keep working if the new machine answers at the
old address; otherwise link them again.

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

### Changing the compose file

Keep your own changes out of `docker-compose.yml`: put them in a file
named `docker-compose.override.yml` next to it, which Docker Compose
reads by itself and no update touches. For example, to keep the data in
a folder instead of a Docker volume:

```yaml
services:
  alexandria-server:
    volumes:
      - ./data:/data
```

If you did edit `docker-compose.yml`, the update script notices, **keeps
your file** and saves the current one beside it as
`docker-compose.yml.new`. It never swaps an edited file for you: a
different `volumes:` line would start the server on an empty database.

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
    each backup from a phone, each device a phone approves, each invite
    from a phone, and setting someone's media-server password from a
    phone. Making a new media-server account from a phone needs no code.
  - A phone given part of the admin access (for example "manage") reaches
    only that part, however a request's address is written.
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
  - One visitor alone can never use that up. A visitor is one IPv4
    address, or one IPv6 /56 (the block a home or a rented server is
    commonly given), so a block of addresses is still one visitor. If several keep
    it used up at once, your own sign-in waits too until they stop or the
    server restarts. Browsers and phones already signed in keep working.
  - Each signed-in phone has its own allowance for the codes its backups
    and approvals carry: 10 wrong, then one a minute. Nobody else can use
    it up. Ten wrong codes in a row sign that phone out of Manage server,
    so a stolen phone cannot keep guessing; signing in again takes the
    password.
- **The browser's admin sign-in works only from the admin page itself.**
  A page on another address, another port of the same machine, a sibling
  subdomain, or the same name over the other of http and https cannot use
  it, even though the browser sends the cookie along. That covers signing
  out too. (Browsers say which page a request came from; the server
  checks.)
- **Large requests need a credential.** A request with no token or admin
  sign-in the server knows may send 16 KB at most; the 8 MB limit the
  library index needs is for linked devices and signed-in admins. Room
  for a request is decided before any of it is read.
- **Password checks run two at a time.** Each takes about 19 MB while it
  runs, so a burst of sign-ins waits in line instead of using up the
  machine's memory.
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
- **Server Admin sign-ins are sealed the same way**, each bound to its
  service. The admin page saves and lists them but never shows a
  password. Only a phone signed in to Manage server reads them back, and
  only with a fresh authenticator code (sharing the sign-in rate limit),
  sealed to a key it made for that one request. Each phone then signs in
  to each server itself. A phone's backup leaves them out.
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
- **Compressed request bodies are refused** (415), however the request
  labels them. A small compressed request could otherwise unpack to
  gigabytes. No Alexandria app sends one.
- **Making or resetting an account on your media servers** follows a
  redirect only on the same server, never to another address and never
  from https down to http, so the saved admin password goes nowhere else.
- **Turning on two-step for an Alexandria account takes its password**, as
  turning it off always did. Both, and changing the password, count wrong
  tries exactly as the sign-in does: ten in a row pause that person's
  sign-in for 15 minutes. "In a row" means within 15 minutes of each
  other, wrong answers sent at the same moment are counted before any is
  answered, and a restart clears the pauses, as it clears every other
  count of wrong guesses.
- **A username that does not exist answers like one that does**, pause
  included, so trying names does not reveal who has a sign-in.
- **An invite's short code is stored under a key made from the admin
  token**, which is not in the database: a copy of the database alone
  does not give a waiting invite away, and a backup taken from a phone
  carries no waiting invite at all. Changing the admin token therefore
  ends the typed codes of invites still waiting (their links keep
  working); make a new invite.
- **Signing a device out ends everything it had.** Revoking a person's
  device also ends the Manage server sign-in made on it, and **Sign out
  all devices** also cancels a link you approved that the device had not
  collected yet.
- **Each build is checked before it is published:** go vet, every test and
  govulncheck (known Go vulnerabilities the code can reach), with the same
  Go the image ships.
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

**There is nowhere on the TV to type an invite code.** There is not meant
to be: an invite is for a phone, tablet or computer. On the TV, open
**Link Companion Server**; it shows a code of its own. Approve that code
on the admin page, or on a phone under **Manage server › Waiting to
connect** (or the person's **Link a TV**).

**Authenticator codes are always wrong.** The codes depend on the time.
Check the clock on the machine that runs the server (and on your phone):
more than about a minute out, and every code is refused. On a NAS or a
Raspberry Pi, turn on automatic time (NTP).

**The admin page signs in, then says "this request did not come from the
admin page".** Your reverse proxy changes the address the server sees.
Make the proxy pass the `Host` header on, and `X-Forwarded-Proto` if it
serves https (the nginx example above does both), or set
`ALEXANDRIA_PUBLIC_URL` to the `https://` address you open.

**A phone was signed out of Manage server.** Ten wrong authenticator
codes in a row sign that phone out. Sign in again with your password and
a fresh code.

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

- **How things work in the apps:** the [help articles](docs/kb/README.md).
- **Bugs and feature requests:** [open an issue](../../issues/new/choose).
- **Questions and setup help:**
  [r/AlexandriaMediaLib2](https://www.reddit.com/r/AlexandriaMediaLib2/).
- **Email:** stoutservers@gmail.com
- **Security problems:** privately, as described in
  [SECURITY.md](SECURITY.md). Never in a public issue.

This is proprietary software, so code contributions are not accepted and
pull requests are closed automatically. See
[CONTRIBUTING.md](CONTRIBUTING.md).
