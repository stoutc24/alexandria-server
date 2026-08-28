# Privacy Policy — Alexandria Companion Server

**Last updated:** August 2026

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

## What it never stores

- **Credentials for your media servers.** Plex, Audiobookshelf, Jellyfin
  and the rest are authenticated by the app on your device, using its
  keychain. Those credentials never reach this server.
- **Media files.** It keeps an index, not content.
- **Email addresses, passwords, or payment information.** There are no
  accounts here.
- **Analytics or telemetry of any kind.**

## What leaves your machine

**Nothing, by default.** The server makes no outbound connections. It
does not check for updates, report usage, or contact any third party —
including us.

The only traffic is between the server and the Alexandria apps on your
own network that you have linked to it.

---

## Who can see what

**Whoever runs the server** can see, through the admin page: who is
connected, their device names, when each was last used, how many
sessions each person has, and how many hours those add up to.

**They cannot see, through the app or the API, what anyone listened to.**
No endpoint returns another person's events, titles, or authors.

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
- Device tokens are stored only as hashes.
- Run it behind HTTPS if it is reachable from outside your network. Over
  plain HTTP on a hostile network, the bearer token is readable.

## Changes

Material changes will update the date above and appear in the repository
history.

## Contact

stoutservers@gmail.com
