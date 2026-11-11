# Changes

What changed in each version of the Alexandria Companion Server, newest
first. To update, see [Updating](README.md#updating); your data is kept.

## 1.1.0

**New**

- **Alexandria Link.** Approving a device's link code for a person now also
  signs that device in to the services you set up for them. The device
  sends its own key with the code request, so the logins it receives can be
  opened by that device alone. See the
  [help article](docs/kb/alexandria-link.md). Needs Alexandria 3.2.1 or
  later on the device.
- **Kids mode.** A **Kids mode** button on each person (the admin page,
  and Manage server in the app). Every device linked to that person shows
  the simple Kids screens until an admin turns it off; a device that
  cannot reach the server keeps the last answer. It changes screens only
  and filters no titles. See the [help article](docs/kb/kids-mode.md).
  Needs Alexandria 3.2.1 (build 1261) or later, Apple TV 1.1.2 (205).
- **A person's look follows them.** Their profile color, accent color,
  page background, Now Playing background, font and text size are kept
  on the server, so every device linked to them looks the same. A device
  can change only its own person's look, and only to the choices the
  apps offer: twenty profile and accent colors (red, orange, amber,
  yellow, lime, green, mint, teal, cyan, sky, blue, indigo, violet,
  purple, magenta, pink, brown, gold, slate, gray). The people list the admin page and Manage server read now
  includes each person's look, read-only (nothing on the admin page shows
  it yet). Needs app versions that support it.
- `ALEXANDRIA_LISTEN_ADDR`: when the server program runs outside Docker,
  the one address it listens on.
- The installer waits for the server to answer and prints the address of
  its admin page.

**Security** (found by a review before public release; none was known to
have been used)

- A compressed request is refused however it is labelled. One labelled on
  two lines could make the server unpack far more than it was sent.
- The browser's admin sign-in works only from the admin page itself, not
  from a page on another port or subdomain of the same machine.
- A request with no credential may send 16 KB at most, and password checks
  run two at a time, so neither can be used to exhaust the machine's
  memory.
- An IPv6 visitor is counted by its /56 block, so one home cannot use up
  everyone's sign-in attempts.
- Turning two-step on or off, and changing a password, count wrong tries
  as the sign-in does. A username that does not exist answers like one
  that does. Wrong answers sent at the same moment no longer all get
  through before the pause; "ten in a row" now means within 15 minutes.
- An invite's short code is stored under a key that is not in the
  database, and a backup taken from a phone carries no waiting invite.
- Revoking a device also ends the Manage server sign-in made on it; "Sign
  out all devices" also cancels a link approved but not yet collected.
- Ten wrong authenticator codes in a row sign a phone out of Manage
  server.
- The installer and updater keep every file they write private, and
  never replace a `docker-compose.yml` you edited (the current one is
  saved beside it; keep your own changes in `docker-compose.override.yml`).

**When you update**

- An invite made before 1.1.0 keeps working by its link. Its typed code
  does not: make a new invite if someone needs the code.
- Changing `ALEXANDRIA_API_TOKEN` now ends the typed codes of invites
  still waiting (their links keep working).
- `docker-compose.yml` no longer has a `build:` section. If you build from
  a checkout, add `-f docker-compose.build.yml`.
- A phone signed in to **Manage server** with a person's account (not the
  admin login) is signed out once by this update, and signs in again.
- A restart now clears paused sign-ins.

## 1.0.0

The first numbered release: household profiles and device linking, Party
Play, listening statistics, the household library index, the admin page
and Manage server on a phone, and Alexandria accounts.
