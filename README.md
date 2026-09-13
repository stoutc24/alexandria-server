# Alexandria Companion Server

A small, self-hosted helper for the [Alexandria](https://github.com/stoutc24/alexandria-app)
media app. It runs on hardware that is plugged in, so your phone does
less work on battery.

It is **optional**. Alexandria works fully without it.

---

## What it does

**One merged library index.** You point it at what your media servers
hold — Plex, Audiobookshelf, Jellyfin, Navidrome, Kavita, Komga and the
rest — and it collapses the duplicates. The same album on three servers
becomes one title with three copies, so the app can say "you own 4,000
things" instead of "you own 7,000 rows".

**Per-person stats.** Everyone in the house gets their own bucket. Your
listening does not land in anybody else's totals.

**Duplicate reporting.** It tells you which works sit on more than one
server. It only ever *reports* them — there is no route in this server
that deletes media, and there never will be.

**Trailers for the TV.** Jellyfin hands the app a YouTube link as a
film's trailer, and an Apple TV cannot play a YouTube page. The
companion turns that link into a real MP4 the TV plays directly. See
[Trailers](#trailers) below — it is the one feature with moving parts.

## What it does not do

- **It does not manage accounts.** No passwords, no registration, no
  email. Your media servers already do that.
- **It does not hold your media-server credentials.** Those stay on your
  device, in its keychain, exactly as they do without this server.
- **It does not decide what anyone is allowed to do.** Subscription
  status is checked on the device and nowhere else. Running this server
  does not unlock anything, and it never will — see
  [docs/COMPANION_DESIGN.md](docs/COMPANION_DESIGN.md) for why that rule
  is load-bearing.
- **It does not touch your media.** It keeps an index. Deleting a row
  here removes an index entry, never a file on any server.

---

## Requirements

- Docker (or Docker Compose)
- ~100 MB of disk for the index, plus whatever your library grows into
- Up to 4 GB more for cached trailers, if the TV uses them (adjustable —
  see [Trailers](#trailers))
- A machine that stays on — a NAS, a home server, a desktop

Runs on `linux/amd64` and `linux/arm64`.

---

## Install

### 1. Generate an admin token

```bash
openssl rand -hex 32
```

Keep it. The server refuses to start without one, so it cannot
accidentally come up open to your network.

### 2. Start it

```bash
docker run -d \
  --name alexandria-server \
  --restart unless-stopped \
  -e ALEXANDRIA_API_TOKEN=<the token you generated> \
  -p 4000:4000 \
  -v alexandria-data:/data \
  ghcr.io/stoutc24/alexandria-server:latest
```

Or with Compose — copy `.env.example` to `.env`, put your token in it,
then:

```bash
docker compose up -d
```

### 3. Check it came up

```bash
curl http://localhost:4000/health
```

You should see `"status": "healthy"` and a list of capabilities.

---

## Connecting a device

Nobody types a long token into a phone. Instead:

1. On the phone, open **Settings → Account → Companion Server**, enter
   the server address (`192.168.1.50:4000`), and tap **Get a link code**.
2. The app shows a six-character code, like `NMS82B`.
3. Open `http://your-server:4000/admin` in a browser, paste your admin
   token, and you will see that device waiting.
4. Type who it is and click **Approve**.
5. The phone connects on its own within a couple of seconds.

**The code is safe to read out loud.** On its own it does nothing:
approving it requires your admin token, and collecting the result
requires a secret that never leaves the phone. Codes expire after ten
minutes and work once.

Approving with a name that already exists adds the device to that
person, so a phone and a watch share one profile and one set of stats.
Each device holds its own token, so a lost phone can be revoked without
signing out the watch.

Watches do not need a code at all — the phone pairs them directly.

---

## The admin page

At `/admin`. It shows:

- Devices waiting to be approved
- Who is connected, their devices, and when each was last used
- How many sessions and hours each person has
- The library index: distinct titles, duplicates, servers

You can approve a device, revoke a device, remove a person entirely, and
download a backup.

**It does not show what anyone listened to.** The management API returns
counts and timestamps only, and the page can render nothing more.

### An honest note about that

You own this machine, so you can open the database with any SQLite
client and read whatever is in it. No API design changes that — the
software you run cannot keep secrets from you.

What is enforced is that the *product* never offers it: there is no
screen, and no endpoint, that hands a household admin someone's
listening history. Treat this as a trust boundary between people who
live together, not as cryptography. If that is not good enough for your
household, do not point the app at a companion server.

---

## Backups

The whole server is one SQLite file. Either:

- Click **Download backup** on the admin page, or
- `docker cp alexandria-server:/data/alexandria.db ./backup.db`

The index rebuilds itself from your media servers, so the only thing
genuinely worth backing up is people's stats.

---

## Trailers

Alexandria's TV app shows a trailer for a film or show. Jellyfin only
knows a YouTube URL for it, and an Apple TV cannot play a YouTube page —
so the TV asks the companion, which downloads the trailer, merges its
video and audio into one MP4 (YouTube serves them as separate streams
now), caches the file on the data volume and streams it back. The TV
plays it like any other video, seeking included.

**Three tools do the work**, all included in the Docker image:

| Tool | Job |
|---|---|
| `yt-dlp` | Talks to YouTube and picks an H.264 stream the TV can play |
| `ffmpeg` | Merges the separate video and audio into one MP4 |
| `deno` | A JavaScript runtime — YouTube extraction does not work without one |

On a bare-metal install put them on `PATH`, or point at them with
`YT_DLP_PATH`, `FFMPEG_PATH` and `DENO_PATH`. None is required: without
them the server starts normally, logs which one is missing and how to
fix it, and `/health` simply does not list `trailers.youtube` — the TV
then falls back to opening the trailer in the YouTube app.

**The cache.** Trailers are kept as MP4s in `TRAILER_CACHE_DIR`
(`/data/trailers` in Docker; otherwise a `trailers` directory beside the
database), up to `TRAILER_CACHE_MB` (default 4096). Past the cap the
least recently played trailers are deleted first. You can wipe the
directory at any time; it refills on demand.

**It can break, and it heals.** YouTube changes things and yt-dlp stops
working until it is updated — that is the nature of the tool, not a bug
here. The server therefore runs `yt-dlp -U` half a minute after start
and once a day after that, and every failure is reported to the TV as a
clean error, so the worst case is the YouTube-app fallback for a day.
If trailers stop working, `docker logs alexandria-server` shows
yt-dlp's complaint, and `docker compose pull && docker compose up -d`
gets a fresh image.

**What it will and will not fetch.** The endpoint accepts YouTube video
links only (`youtube.com/watch?v=`, `youtu.be/`, `/embed/`, `/shorts/`)
and only ever fetches the URL the app was given by your media server.
It is not a general downloader: it will not take a playlist, a channel
or a link to any other site, and the files it keeps are named by
YouTube's video id, nothing else.

The endpoints, for the curious — both need a token, like everything
else: `GET /api/v1/trailers/prepare?url=<youtube url>` answers `200`
with a `streamPath` once the MP4 is ready, `202` while it is still
downloading (poll again), `501` when the tools are missing and `502`
when the download failed; `GET /api/v1/trailers/stream/<id>` is the MP4
itself, with HTTP Range support.

---

## Upgrading

```bash
docker compose pull && docker compose up -d
```

Your data lives in the `alexandria-data` volume and survives.

---

## Security

- **Put it behind HTTPS if it leaves your LAN.** A reverse proxy
  (Caddy, nginx, Traefik) is the usual way. The admin token is sent as a
  bearer header — over plain HTTP on a hostile network, that is
  readable.
- The admin token is the keys to the whole thing. Treat it like a
  password.
- Device tokens are stored only as hashes, so a stolen database yields
  nothing that can be replayed.
- Three routes are reachable without a token, by necessity:
  - `/health` — reports capabilities, never library contents, so the app
    can find a companion before it has been given anything.
  - `/admin` — the management page itself. It is inert markup containing
    no secrets; it does nothing until you paste your token into it.
  - `/api/v1/link/request` and `/api/v1/link/claim` — a device being
    linked has no token yet, which is the problem they solve. Both are
    rate limited to 30 requests a minute, a link code is useless without
    your approval, and a claim secret is useless without the matching
    approved code.

  Everything else requires a token.

---

## Troubleshooting

**It exits immediately.** Check the logs: `docker logs alexandria-server`.
Almost always the token — unset, left as `change-me`, or under 16
characters.

**The app says "no Alexandria server answered".** Check the address and
port (4000, not 3000), that the container is running, and that your
phone is on the same network. `curl http://server:4000/health` from
another machine on the LAN is the fastest test.

**A link code says it expired.** They last ten minutes. Ask for a new
one.

**The library index is empty.** Nothing has synced yet. The index is
populated by the app, not by the server reaching out to your media
servers.

**Trailers do not play on the TV.** `curl http://server:4000/health`
and look for `trailers.youtube` under `capabilities`. If it is missing,
the logs say which tool was not found. If it is there and trailers still
fail, the logs show yt-dlp's error — usually YouTube changed something,
which the daily self-update fixes; or pull a fresh image.

---

## License

Proprietary. See [LICENSE](LICENSE). This is not open-source software:
it may not be redistributed, resold, or used to build a competing
product.

Third-party dependencies are listed in
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md); all are MIT or
BSD-3.

## Support

stoutservers@gmail.com
