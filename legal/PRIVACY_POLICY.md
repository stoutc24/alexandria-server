# Privacy Policy — Alexandria Companion Server

**Last updated:** October 2026

## The short version

This is software you run on your own machine. It has no connection to
Stout Solutions, sends nothing to us, and we never see any of it.

There is no "we collect" section below because there is nothing for us
to collect.

---

## What the software stores

Everything lives in one SQLite file on your machine (`/data/alexandria.db`
in the Docker image).

**A library index.** Titles, authors, series, genres, years and durations
for the items on the media servers you point it at, plus which server
each came from. This mirrors what your own servers already hold. It
contains no file paths and no credentials.

**Profiles.** A display name for each person in your household, and a
SHA-256 hash of each device's access token. The tokens themselves are
never stored, so the database holds nothing that could be replayed if it
leaked.

**Activity events.** For each person: what they listened to or read,
for how long, and when. This is the data that makes per-person stats
possible.

**Pending link requests.** A short code and an expiry, deleted after use
or expiry.

**The administrator's login.** A slow hash of the admin password, the
authenticator secret used to check its codes, and hashes of the one-time
recovery codes, of browser sessions and of phones signed in to manage the
server.

**Alexandria accounts, only if you invite people.** For each person the
administrator invites: a username, a slow hash (argon2id) of their
password, their two-step secret if they turn it on (sealed, below), and a
public key for each of their signed-in devices. And the media-server
logins the administrator sets up for them — usernames, passwords, tokens,
API keys, or a Plex person's key to the administrator's own server —
sealed with AES-256-GCM under a secrets key (`ALEXANDRIA_SECRETS_KEY`)
that is not stored in the database and is kept out of its backups. A
login is opened only to send it, sealed again, to that person's own
device. Without accounts, none of this exists.

**Party Play, in memory only.** While a party runs, the server holds what
is playing (title, author, position and the queue) and which devices are
in it. This is never written to the database, and it is gone when the
party ends or the server restarts.

## What it never stores

- **Credentials for your media servers, unless you use Alexandria
  accounts.** Without accounts, Plex, Audiobookshelf, Jellyfin and the rest
  are authenticated by the app on each device, using its keychain, and
  those credentials never reach this server. With accounts, only the logins
  the administrator sets up are kept, sealed as described above.
- **Media files.** It keeps an index, not content.
- **Email addresses or payment information.** Passwords only for the
  administrator and for people invited to an Alexandria account, each
  stored only as a slow hash. Nobody can register themselves.
- **Analytics or telemetry of any kind.**

## What leaves your machine

**Nothing, by default.** The server makes no outbound connections. It
does not check for updates, report usage, or contact any third party —
including us.

The only traffic is between the server and the Alexandria apps on your
own network that you have linked to it.

When the administrator picks a Plex person for an Alexandria account,
their own browser or phone asks plex.tv, with their own Plex sign-in, for
that person's key to their server. The server takes no part in that and
never contacts plex.tv.

---

## Who can see what

**Whoever runs the server** can see, through the admin page: who is
connected, their device names, when each was last used, how many
sessions each person has, and how many hours those add up to.

**They cannot see, through the app or the API, what anyone listened to.**
No endpoint returns another person's events, titles, or authors.

**With Alexandria accounts,** they also see each invited person's username
and which services are set up for them, and whether a login is saved —
never the login itself. No screen or API reads a saved login back.

**But be clear about the limit:** the person who runs the machine owns
the database file and can open it with any SQLite client. Software
cannot keep secrets from the person operating it. What is enforced is
that the product never *offers* that view — there is no screen and no
API for it. Treat this as a trust boundary between people who already
live together, not as a cryptographic guarantee.

If that is not acceptable in your household, do not link the app to a
companion server. Everything in Alexandria works without one.

## Deleting data

- **Any person** can erase their own history from the app, without
  asking the administrator.
- **The administrator** can remove a person entirely, which deletes
  their stats and unlinks every device they have.
- **Revoking one device** signs that device out and leaves everything
  else intact.
- **Deleting the container and its volume** removes everything.

We cannot delete your data on request, because we never have it.

## Children

The software collects no personal information from anyone and is not
directed at children. What appears in it comes entirely from the media
servers its operator configures.

## Security

- The admin token is required to start the server; it refuses to run
  without one, so it cannot come up unauthenticated by accident.
- Device tokens, admin sessions and phones' admin sign-ins are stored only
  as hashes.
- The authenticator secret of the admin login is stored as it is (the
  server needs it to check codes). Guard backups and the data volume as
  you would the admin password.
- Saved service logins (Alexandria accounts) are sealed with the secrets
  key, which is not in the database or its backups: a stolen database
  alone does not open them. Whoever holds both the database and the key —
  the server's operator — can, the same trust boundary as above.
- Run it behind HTTPS if it is reachable from outside your network. Over
  plain HTTP on a hostile network, tokens and codes in transit are
  readable.

## Changes

Material changes will update the date above and appear in the repository
history.

## Contact

stoutservers@gmail.com
