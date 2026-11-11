# Alexandria Server — Companion Services Design

**Status:** design + first implementation, August 2026
**Audience:** whoever works on this next (including future Claude sessions)

---

## What this is for

The Alexandria app currently does all of its heavy thinking on the
phone. Three jobs in particular have outgrown it:

- **Indexing.** The media catalog sweep pulls up to 100,000 rows and
  processes them on the UI isolate. A pre-release review flagged this as
  a jank source on large libraries.
- **Cross-service dedupe.** The same album on Plex, Navidrome and
  Jellyfin is three rows. The app now collapses them
  (`CrossServiceDedupe`), but it re-derives that on every launch, on
  battery.
- **LLM coordination.** Several household members hitting one 16 GB GPU
  will thrash it. Nothing today queues requests or owns model lifecycle.

The server exists to do that work on hardware that is plugged in, so the
phone can ask a short question and get a small answer.

---

## The rule that governs everything here

> **The server may do WORK. It must never grant PERMISSION.**

Entitlement (premium) is verified **on the device, against RevenueCat**,
and nowhere else. The server has no opinion about who is allowed to do
what, and the app must never accept an entitlement claim from it.

### Why this rule, stated precisely

Self-hosting is the entire point of Alexandria — someone running their
own Alexandria Server is the intended user, not an adversary. The
threat model is not "a person self-hosts"; it is **"a forged
entitlement grants a paid feature."**

Those come apart cleanly once you notice that everything the server does
is *cost*, not *privilege*:

| The server provides | Which is |
|---|---|
| A faster, always-warm index | Labour the app already does, slower |
| Pre-collapsed dedupe results | Labour the app already does, slower |
| Stats rollups | Labour the app already does, slower |
| LLM queueing and model lifecycle | Coordination of a resource the user already owns |

None of that is a feature someone has to buy. A person who self-hosts
gets a faster app — which is exactly what they should get for running
the hardware. They do not get a paid feature, because the server is
never asked whether they may have one.

### The failure mode this prevents

If the app ever did this:

```dart
// NEVER
final ent = await companionClient.getEntitlement();
if (ent.premium) unlockEverything();
```

…then anyone could return `{"premium": true}` from their own server and
the paywall is gone in one line. Not because they self-host — because
the app delegated an answer it must compute itself.

So:

- ❌ The server never returns an entitlement, tier, or "allowed" flag.
- ❌ The app never asks it for one.
- ✅ The app checks `EntitlementService` (RevenueCat) exactly as it does
  today, and *then* decides whether to use a companion result.
- ✅ The server answers only factual questions: "what is in this
  library", "which of these are duplicates", "is a model loaded".

A gated feature therefore fails **closed on the device** whether the
companion is present or not.

---

## Licensing

The server is proprietary (see `LICENSE`) and its dependencies are
MIT/BSD-3 (see `THIRD_PARTY_LICENSES.md`) — permissive, attribution
only, no source-disclosure obligation.

**The one hard rule:** do not add a **GPL** or, especially, an **AGPL**
dependency. AGPL's network clause triggers on *serving users over a
network*, which is precisely what this is, and would compel publishing
the source. Check the licence before `go get`.

---

## Services

### 1. Catalog — BUILT

Holds the merged, deduplicated view of every library the household runs.

| Route | Does |
|---|---|
| `POST /api/v1/catalog/sync` | Replace one source's snapshot |
| `GET /api/v1/catalog/items` | Library, already collapsed |
| `GET /api/v1/catalog/duplicates` | Works held on 2+ servers |
| `GET /api/v1/catalog/summary` | Counts without shipping rows |

**Sync is a replace, not an append.** An item deleted on the media
server has to leave the index, or the index lies forever. The delete is
scoped to the one source, runs in one transaction, and touches only this
server's own cache rows — never media on a media server, and never
another source's rows.

The dedupe key mirrors the client's `CrossServiceDedupe`: normalised
`title|creator`, and **an item with no usable creator is never merged**.
That asymmetry is deliberate — a wrong merge hides media; a missed merge
only shows a duplicate. Unkeyable rows are stored as `NULL`, not `""`,
so SQL `GROUP BY` keeps them distinct instead of collapsing every
"Unknown" into one heap.

`GET /duplicates` reports works on more than one **source**. Two copies
on a single server are that server's business; the cross-service
duplicate is the one the app cannot see on its own. It reports them —
there is no route here that deletes one.

Implementation: `internal/catalog/dedupe.go` (pure, tested),
`internal/repository/catalog.go` (storage + SQL rollups),
`internal/handlers/catalog.go` (routes).

### 2. Stats (`/api/v1/stats`)

Rollups the app currently recomputes per launch: totals by type/source,
decades, top creators, series completion. Server-side means the phone
renders a report instead of building one.

### 3. LLM coordination (`/api/v1/llm`)

The valuable one. The server sits in front of Ollama and owns:

- **Status** — which model is resident, how much VRAM it holds.
- **Queueing** — one request at a time per GPU, so two people asking at
  once wait rather than thrash.
- **Lifecycle** — warm on demand, unload on request or idle. This is the
  "gaming mode" the app added, moved somewhere it can be enforced for
  the whole household rather than per-device.

**It proxies prompts; it does not author them.** The app builds the
briefing and the guardrail preamble (see `AI_GUARDRAILS.md` in the app
repo) and the server passes them through untouched. Two reasons: the
guardrails are tested in the app, and a server that rewrote prompts
would be a second, untested place where the model's instructions come
from.

### 4. Media review (`/api/v1/review`) — later

Health checks across libraries: items missing metadata, broken files,
series with gaps. Deliberately last; the three above pay for themselves
first.

---

## Profiles, not accounts

This server had a user/family/media-server account system. It was
removed in August 2026, and the reasoning is worth keeping:

For Alexandria to run household accounts properly, it would have to
reproduce what every media server it supports already does — user
management, per-service credential custody, token refresh for nine
different auth schemes. That is a different product. An earlier
iteration went further still and stored encrypted credentials for each
user's media servers (`internal/crypto`, removed in `f8c7dc3`), which
made the server a vault for other people's Plex and Jellyfin logins —
with an encryption key that defaulted to the JWT secret on the same box.
Both are gone and should stay gone.

What replaced it is much smaller. **A profile is a name and a token.**
No password, no email, no registration. The admin creates the profile
and hands the token over; the token *is* the identity. Its only job is
keeping one person's stats out of another person's totals.

### Ways in

| Token | Can | Cannot |
|---|---|---|
| **Admin** (env, `ALEXANDRIA_API_TOKEN`) | Sync the catalog, create/rotate/delete profiles, back up the database — until an admin login is set up. After that, only set the login up again (the break-glass path for a lost authenticator) | **Read anyone's stats** |
| **Admin login** (password + TOTP at `/admin`; a 12-hour browser session) | Everything the admin token could | **Read anyone's stats** |
| **Admin device** (`alxa_…`, one per phone) | The `/admin/*` routes — approve devices, manage people, the library index through `/admin/catalog/*`, sign phones out. With a fresh code every time: a backup with the sign-in secrets left out, and approving a device, for someone new or someone already here | **Read anyone's stats** or the party line on its own, use any profile or catalog route, set the login up, mint another token |
| **Device** (`alxp_…`, one per device) | Read the catalog, write and read its profile's stats, erase them | Sync the catalog, manage profiles |
| None | `/health` | Everything else |

No admin credential has a route that returns anyone's stats. Approving
a link is the one road from admin to a person's history, and it is
described under *What the admin can and cannot see*.

An admin device token is how a phone manages the server. It costs what
the web sign-in costs — the password and a fresh code, at
`POST /admin/auth/device-token`, inside the sign-in rate limit — and
`RequireAdmin` is the only guard that accepts it: `RequireProfile` and
`RequireAnyToken` never do. The two admin acts that do lead into
people's history, the backup and approving a link (every approval,
someone new included), each ask the phone for a fresh code as well, so
the token alone — a lost phone, say — is not a way into anyone's
history. The phone
decides whether it stays signed in: only over a certificate-validated
`https://` address. The server, which behind a TLS-terminating proxy
sees plain HTTP, honours that and records how HTTPS was established
(`tls`, `proxy`, `forwarded`, `cloudflare`, `config` from
`ALEXANDRIA_PUBLIC_URL`, or the phone's own word, `client`). A header
that says http is recorded as plain HTTP whatever was declared, and the
two declarations, `config` and `client`, are recorded only for a
sign-in that stays and whose headers name no scheme. A sign-in that
stays lapses after 180 days unused, every use renewing it; one that does
not lasts 12 hours. Signing out, revoking on the web page and setting
the login up again end it at once. Like every other token here it is
stored only as a hash.

The server refuses to boot when the admin token is unset, left at the
placeholder, or under 16 characters — an accidentally exposed companion
with no token is worse than one that failed to start. Comparison is
constant-time. Profile tokens, admin sessions and admin device tokens
are stored only as SHA-256 hashes, so none of them can be replayed from
a stolen database. (Plain SHA-256 is correct here: tokens are 32 bytes
from `crypto/rand`, and the slow hashes exist for low-entropy human
passwords.) The admin login is the exception: its TOTP secret is stored
as it is, because checking a code needs it, so a stolen database can
generate codes and attack the argon2id password hash offline. A backup
taken from a phone leaves the whole admin login out for that reason.

`/health` stays open so the app can discover whether a companion exists,
and what it can do, before it has been handed a token. It reveals
capabilities, never library contents. When the operator sets
`ALEXANDRIA_PUBLIC_URL`, it also publishes `publicUrl`, so a phone
linked over a LAN `http://` address can offer to move to the https one.
Only a plain https address is published — https, a host, no user name,
query or fragment, no percent-encoding, nothing outside printable ASCII:
the phone's own rule — rebuilt as `https://host[:port][/path]`. Anything
else is ignored at startup with a warning that does not repeat the value
(a password written into it would land in the log), so a typo, or an
address that reads as one place and goes to another
(`https://alexandria.example.com@evil.example`), never reaches a phone.

### Linking a device

Nobody types a long token. The flow runs in the Plex direction — the
code travels **toward** the admin:

1. The app asks the server for a code and shows it: `NMS82B`.
2. The person reads it to whoever runs the server.
3. The admin approves it, naming the person.
4. The app's next poll receives a token for that device.

**The spoken code is not a credential.** Approving it requires the admin
token; collecting the result requires a claim secret that only the
requesting app ever held. So the code can be read across a room, or over
the phone, and grants nothing by itself. Codes last 10 minutes, are
single use, and the alphabet omits `0 O 1 I L` because people misread
them aloud.

Approving by NAME reuses an existing person, so linking a second device
adds to that profile instead of creating "Sarah (2)". One person, one
stats bucket, however many devices.

The public link endpoints are rate limited (30/min). Everything else on
the server requires a token.

A failed approval — stale code, typo — must not leave an orphan person
behind, so `CheckPending` validates before any profile is created.

### One profile, many devices

Tokens belong to DEVICES, not people. Sarah's phone, watch and TV each
hold their own, all pointing at one profile and one set of stats.

That matters for revocation: a lost phone is unlinked on its own and her
watch keeps working. A single per-person token would have forced
"reset everything she owns" for one lost device.

A device can always ask who it is: `GET /api/v1/devices/me`
(capability `devices.me`) answers
`{"deviceKey", "label", "profileId", "profileName", "kids", "look",
"lookUpdatedAt"}`. `deviceKey` is
the token id — the id the admin's person list shows for that device,
the one `DELETE /admin/devices/:tokenId` revokes, and the party line's
`yourDeviceKey`. The party line names a device only while something is
playing; this route answers whenever the token is good, which is how a
phone managing the server marks itself in a person's device list and
warns before revoking its own link. It sits behind the profile guard
like everything a device does, so no admin credential gets an answer.
`kids` is the person's Kids mode (capability `profiles.kids`, switched by
the admin with `PUT /admin/profiles/:id/kids`); `look` and
`lookUpdatedAt` are their look (below). A read of the person that fails
answers 503, never a made-up `kids` or an empty `look` in its place.

### A person's look

A person's look (capability `profiles.look`) is how the apps look on
every device linked to them: a JSON object of up to six fields, each
one word from a fixed list. A field that is missing means the app's
own default.

| field | values |
|---|---|
| `color` (the profile's own color: the avatar circle) | `red`, `orange`, `amber`, `yellow`, `lime`, `green`, `mint`, `teal`, `cyan`, `sky`, `blue`, `indigo`, `violet`, `purple`, `magenta`, `pink`, `brown`, `gold`, `slate`, `gray` |
| `accent` (buttons, the focus highlight) | the same twenty, or `default` |
| `background` (the page background) | `midnight`, `black`, `charcoal`, `forest`, `plum`, `ocean` |
| `ambiance` (behind Now Playing) | `none`, `coverColors`, `fireplace`, `rain`, `snowfall`, `library`, `starfield` |
| `font` | `default`, `rounded`, `serif` |
| `textSize` | `normal`, `large`, `extraLarge` |

Each device reads it on `GET /api/v1/devices/me`, as `"look": {…}`
(only the fields that are set; `{}` when none is) and `"lookUpdatedAt"`
(unix milliseconds of the last change, `0` for never).

A device changes it with `PUT /api/v1/devices/me/look`:

```
PUT /api/v1/devices/me/look
Authorization: Bearer alxp_…
{"color": "teal", "ambiance": "rain", "font": ""}

200 {"look": {"color": "teal", "ambiance": "rain"}, "lookUpdatedAt": 1794419250081}
```

The fields sent are merged into the stored look, the fields left out
stay as they are, and `""` clears a field back to the default. The
answer is the whole look as it now stands. `lookUpdatedAt` moves only
when the look actually changed, and always forward. There is no person
in the path: the token says whose look it is, so a device changes its
own person's look and nobody else's. The body is refused, with nothing
written, when it is not a JSON object, has a key that is not one of the
six, or a value that is not a string on that field's list (`400`, with
`{"error": "…"}` naming the field and never repeating the value), or is
over 2 KB (`413`). A token that was signed out gets the same `401
token-revoked` as every other device route.

The admin's person list (`GET /admin/profiles`) shows each person's
`look` and `lookUpdatedAt`, read-only; no admin route changes a look.
The server only ever sends values from the lists above: a stored value
it does not know (written by a newer server) is left out of every
answer.

### What the admin can and cannot see

The admin runs the household, so they can see **who exists, when they
were last active, how many events they have, and how much time those
add up to.** That is enough to manage storage and membership.

They cannot call an API that returns a title, an author, or an event —
with ONE deliberate exception: `/admin/backup` streams the whole
database, stats rows included, because backups are the point of the
data being here. It is the single admin route that carries history, and
a leaked admin token therefore reaches everything a stolen disk would.
A phone's admin device token alone does not reach it: the phone's
backup (`POST`) asks for a fresh code every time, and its copy leaves
the admin login out; `GET` refuses the phone's token.
Every other admin route yields counts.
`RequireProfile` rejects the admin token outright, and the stats
repository offers no "read another profile" method for a future route to
reach for. The absence is the boundary — keep it absent.

There is one indirect road, and it is the admin's job itself: approving
a link. A device linked to someone IS that person's device — it reads
their stats and joins their party line — so whoever approves links can
see what someone's devices see by linking a device of their own to
them. It is never silent: the device appears under that person on the
admin page, where it can be revoked. From a phone every approval costs
a fresh code (`POST /admin/links/approve` answers `403
step-up-required` without one, and `401 step-up-failed` for a wrong,
spent or raced one, leaving the link waiting and creating nobody), spent
through the same atomic claim as a sign-in, and a phone's approvals
share the sign-in rate limit, so its codes cannot be ground there.
Someone new is no exception. A new profile has no history yet, but
approving by name reuses a person: a phone that could create "Mike"
without a code could link its own device to him before he is here, and
read his history from the day his devices are approved into that name.
So a phone's admin device token alone reaches no one's history by this
road either. The browser's session approves without a code, as it
always has: it was signed in with the password and a code, and lasts
12 hours.

### Now playing — the party line

A profile's devices can see what that profile is playing RIGHT NOW
(`PUT/GET/DELETE /api/v1/nowplaying`), so a TV can join a book or an
album mid-stream at the same spot.

Three properties carry the design:

- **Opt in, off by default.** Nothing is published until the person
  flips the switch on the companion screen.
- **Per profile, admin excluded.** The routes sit behind the profile
  guard; the admin token is 401'd on every method. Live listening is
  the most intimate data here — more so than history.
- **Memory only, never the database.** Presence expires 90 seconds
  after the last heartbeat and survives no restart. What someone is
  playing at this moment is never written anywhere.

The server stamps reports with ITS OWN clock, so a joiner extrapolates
`position + (serverNow − updatedAtMs)` without caring about either
device's clock.

#### The conductor (v2): one seat, absolute intents

Any device in the party may pause, seek or skip, and every device —
the one making the noise included — converges on the result. The
server is a conductor, not a relay: it holds ONE state per profile and
devices reconcile to it. Nothing dispatches a command at a device.

- **The seat.** The device actually playing owns the clock. Its
  identity is its token (`leaderTokenId`, `leaderLabel` from the link
  label) — never something a body claims. A second device reporting
  while the seat is held gets `409 seat-held`; an older client that
  sends no `basedOnRevision` is swallowed (`200 {ignored:true}`) so
  it cannot flap the state. A silent seat (30 s) is `leaderDormant`
  and unprotected; a follower may `claimSeat` on it, and `takeOver` is
  the explicit "play here instead" that works on a live one.
- **Intents** (`POST /nowplaying/intent`) are absolute and idempotent:
  `setPlaying(bool)`, `setPositionMs`, `setIndex`, each carrying the
  `basedOnRevision` the presser saw. Staleness is judged against the
  revision the current ITEM landed on, not the heartbeat counter, so a
  pause pressed during a heartbeat still lands and a second "next"
  computed from a state that predates the first is dropped — never
  re-applied as a double skip. `setIndex` may also carry
  `ifQueueRevision`, the queue revision the index was chosen from: a
  reorder bumps the session revision but not the item revision, so
  without it an index picked from the old list would land on whatever
  now sits in that slot (mismatch → `stale`; omitted → unguarded, for
  older devices). Refusals are honest codes with the
  state to catch up on: `stale`, `legacy-leader` (the playing device
  predates this), `leader-asleep` (a suspended phone cannot be resumed
  from here — iOS, not us), `no-queue`.
- **Play modes** (2026-10-31: Shuffle and Repeat on the Remote). The
  leader's reports carry `shuffle` (bool) and `repeat` (`off`, `all`,
  `one`), kept exactly as reported: a mode a report leaves out is one
  that leader cannot do, absent from the state, so a Remote offers only
  what the leader can carry out and nothing of another device's lingers
  (a hand-over or a vacant seat clears both until a leader reports).
  `setShuffle {shuffle}` and `setRepeat {repeat}` change the state at
  once and set `lastIntent` / `lastIntentRevision`, so the leader
  carries them out like a pause and its heartbeat from before the press
  is `409 stale` instead of undoing it. They never touch the position,
  the anchor or a scheduled start — the song does not move, and the
  leader's own report keeps or withdraws a next-track announcement, as
  after a queue edit. Refusals: `leader-cannot-shuffle` /
  `leader-cannot-repeat`, `leader-asleep` (nobody leads to carry it
  out), `legacy-leader`. Capability `nowplaying.shuffle-repeat`.
- **The leader converges like everyone else.** After an intent the
  leader's next report based on the old revision is `409 stale` with
  the state to apply; the first report that has seen it is the ack,
  and it re-anchors the clock from the leader's real position.
- **Milliseconds and rate.** `positionMs` and `rate` (1.5× audiobooks)
  feed the anchor `now − positionMs/rate`; it re-anchors on item
  change, on pause, on paused→playing (always — or followers run ahead
  by the length of the pause), after an ack, and on >2 s drift.
- **Revisions track the clock** (`max(prev+1, nowMs)`), so a restart
  can only move them forward; queue changes and presence CHANGES bump
  them, presence heartbeats do not (or a follower could never park).
- **Presence has roles.** `follower` counts toward the party;
  `viewer` is a screen merely showing the session — enough for the
  leader to listen for its intents, not enough to keep it in step.
- **Leaving.** `DELETE ?reason=stopped` from a leader with followers
  vacates the seat and keeps the music going; `stop-sharing` (or no
  reason) ends the session; from a mere member it is just leaving.

`/health` advertises `nowplaying.leader`, `nowplaying.intents` and
`nowplaying.ms`, so a client on an older server degrades to v1.

#### 0.3.0: the party clock

Converging on a state within a poll is not the same as starting the
same note at the same instant. 0.3.0 gives the party a clock and a way
to name a moment on it. Everything here is additive: a state without
the new fields reads exactly as it did before.

- **`GET /time`** answers `{serverNowMs}` and nothing else, so a device
  measures its offset NTP-style — a few round trips,
  `offset = serverNowMs − (sent + rtt/2)`, the smallest-rtt samples
  trusted most. Every state response is still stamped with
  `serverNowMs` at the instant it is built.
- **`sampledAtServerMs`** on a report: the server instant (the
  reporter's clock plus its offset) at which `positionMs` was measured.
  The anchor is then `sampledAt − positionMs/rate`, and the request's
  one-way latency never lands in it. Believed only within 5 s of
  arrival; absent, arrival time stands in as before.
- **A scheduled start** — `startAtMs`, `startPositionMs`, `startItemId`
  on the state. The leader announces that `startItemId` (empty = the
  current item) begins at `startPositionMs` when the server clock reads
  `startAtMs` (T). For the current item the server pins the anchor to T
  (less the start position) and holds the extrapolated position at
  `startPositionMs` until T, so every device that knows the clock
  starts the same note at T instead of starting when it can and
  settling. A press from the room (any intent) and a handover cancel
  it. A manual skip on the phone announces T = now + 1.5 s. A
  `startAtMs` of zero or below is no schedule.
- **The NEXT-track pre-announce** (protocol v2). While the current item
  A plays and the leader leads a live party with followers on a server
  that schedules starts, it announces the boundary ahead instead of
  holding at it. The contract:
  - **Announce.** When A has 3 s or less of WALL time left (remaining ÷
    rate), once per trajectory, the leader's report of A carries
    `startItemId` = the next item B, `startAtMs` = T and
    `startPositionMs` = 0, where T = the predicted boundary on the
    server clock + **max(time to audio + 40 ms, 700 ms)**: the leader's
    measured time to audio for B's kind (downloaded or streamed,
    100–2000 ms) plus a 40 ms margin, never less than the 700 ms every
    start at a track's end had before the pre-announce. So T sits
    700–2040 ms past the predicted boundary. The floor gives a follower
    that starts B from the leader's state (an older one) the notice it
    always had, and a latched phone that must still open B the time to
    do it; the cost is about 0.7 s of silence between tracks, where the
    bare formula gave a downloaded B as little as 0.14 s. The leader
    repeats the three fields on EVERY report of A while armed (an
    omission would withdraw), and forces a report at 1 Hz from the
    announcement until 1 s before the predicted boundary — normally the
    announcing report and one repeat. None is forced in the last
    second: an older follower that has already crossed to B on its own
    can read a report naming A as the party going back to A, so in the
    last second the odds of that stay what a pre-v2 leader always gave.
    A report carrying a new T for B replaces the old one.
  - **Withdraw = omission.** A report of A that omits the fields
    withdraws the announcement: the server carries nothing forward.
    The leader clears it at once on any pause (interruptions
    included), any seek within A (a scrub, ±30 s, previous-to-restart,
    a seek intent), a rate change, a repeat-mode change, a queue edit
    that changes the next item, a stop or leave, a handover, and a load
    of any index other than B. What tells the party is what follows
    each of them anyway: after a seek, a rate or repeat-mode change or
    a queue edit, a fresh report without the fields once it has landed
    (after a jump of more than 4 s that report waits until the
    leader's audio rolls, at most 4 s, so the anchor it moves is one
    the room can hear); after a pause, the paused report; after a stop
    or leave, its `DELETE`; after a handover, the handover itself;
    after a load of another index, that load's own scheduled report,
    which carries only its own current-item schedule. A new trajectory
    that again reaches 3 s announces afresh. A paused report never
    carries the fields. **Known residual:** a report of A without the
    fields keeps a latched follower's latch while A's anchor still puts
    A's end within 1.5 s of the armed boundary (see Followers), and
    after a long seek the report that moves the anchor waits for the
    leader's audio. So a long seek back on the leader in about the last
    second of a slowly streamed A, whose audio does not roll again
    before T, can briefly start B on latched followers; they reload A
    when that report reaches them. A seek asked from another device
    does not do this: the server re-anchors and drops the announcement
    as it applies the intent.
  - **The server's defence in depth.** A correct leader never needs
    these; they keep a stale announcement off the state when a leader
    is not correct. A next-item schedule (`startItemId` ≠ `itemId`) is
    zeroed:
    - on any report with `isPlaying` false;
    - when its T does not fit the trajectory the same report describes.
      With `durationSec` > 0, boundary = the sampled instant
      (`sampledAtServerMs`, else arrival) + (`durationSec`·1000 −
      `positionMs`) ÷ rate, and the schedule is dropped unless
      **−1000 ms ≤ T − boundary ≤ +4000 ms** (both ends included). A
      real T sits 700–2040 ms past the true boundary (the time to
      audio, 100–2000 ms, plus the margin, never under the 700 ms
      floor), and a whole-second `durationSec` puts the true boundary
      up to 1 s ÷ rate past the computed one; the early side absorbs
      clock and sampling error. Without a duration nothing is judged.
      A `durationSec` off from the file's real length by more than
      about a second can fail the check; the announcement is then
      dropped — fail-safe: followers take the ordinary item-change
      path, as before the pre-announce;
    - by any intent — and the server remembers the (item, T) an intent
      cleared and refuses a re-send of that same pair until T + 2 s (a
      different T for the item is a new announcement, judged on its
      own);
    - when the leader vacates the seat (`DELETE ?reason=stopped` with
      followers present; a start of the CURRENT item stays, as
      before), on a handover (which clears any schedule), and in the
      stop-all tombstone.

    A refused announcement is simply absent: the report itself applies
    as an ordinary one. A `startAtMs` of zero or below is no schedule,
    and its two siblings are dropped with it. The announcement bumps
    `revision` (every parked poll wakes within one round trip) but not
    `itemRevision` (pending intents stay valid); the current item's
    anchor and extrapolation are untouched by it. None of this needs a
    new capability: a server from before these checks already reads a
    report that omits the fields as the withdrawal (it stores what the
    report sends); it only lacks the checks.
  - **The boundary: C1, then the truthful pin.** Between A's end of
    file and the play of B the leader publishes no paused state. When
    its boundary consumes the announcement it publishes **C1**, B's
    ordinary scheduled start: `itemId` = B, `isPlaying`, `startItemId`
    = B, `startAtMs` = T, `startPositionMs` = 0 — a current-item
    schedule every follower understands, pre-v2 included. C1 goes out
    at the consume, as soon as the publisher is free (a report of A
    still in flight is waited for, briefly), and never once B's own
    first report has gone out: sent after that, it would pin the
    anchor back to T over a measured start. Whatever stops it simply
    skips it; B's first report carries T as well. A follower that
    starts B from the leader's state gets T less the boundary's run-up
    and the transit as notice: about 0.6 s for a downloaded B (the
    700 ms floor less about 0.1 s), about what the old 'advance'
    publish gave, and up to about 1.9 s for a streamed one. One that
    plays B the moment it sees it, without waiting for `startAtMs`,
    starts up to that notice early. The leader opens B paused and
    plays it so that its sound starts at T (at once, if it cannot make
    T). Its reports of B carry `startItemId` = B and `startAtMs` = T
    until its clock rolls PAST ITS START TRANSIENT: for the first
    moments after play() libmpv's clock reads about 0 and then dips by
    the route's output latency (read from mpv's source, not yet
    measured on a device), so the phone counts a reading as rolling
    only once the raw clock is max(150 ms, output latency + 50 ms) into
    B. Its first report past the transient is pushed at once and
    carries the measured audible zero Z = `sampledAtServerMs` − audible
    position ÷ rate, sent while `sampledAtServerMs` − Z < 1800 ms. An
    older Z is not sent, and B keeps the anchor T until an ordinary
    re-anchor moves it (on a route with about 1.75 s of output latency
    or more that is always the case). The pin is done only once the
    server accepts that report. Its clocks run from B's own start (its
    first rolling read; from the consume until then), so a leader that
    starts B late still gets its zero out: while outstanding it reports
    at 1 Hz for at most 3 s after that start, gives up after 3 s with
    no server time, and is never outstanding for more than 6 s. Within
    2 s of the pinned instant — judged at `sampledAtServerMs` when
    present, so a slow request does not forfeit it — the server anchors
    B AT `startAtMs`, whatever `positionMs` the report carries;
    `anchorRevision` moves to that report's revision (`itemRevision`
    moved once, with B's first report, normally C1), and the fields
    stay on the state for the readers. A later one, or a report without
    the fields, anchors as an ordinary report from its sampled
    position. A pending join refresh never overrides the pin. If B is
    itself 3 s long or less, B's outstanding pin goes out before B's
    own next-item announcement.
  - **Followers** (phone and TV) latch B@T, prepare B and start it at
    T. The latch owns the boundary only while each state agrees with
    it. A state naming A keeps it when it repeats the announcement (T
    within 50 ms; a different T for B re-arms at that T), or when it
    carries no fields and A's anchor still puts A's end within 1.5 s
    of the armed boundary (a restart or a scrub moves it further, and
    disarms). A state naming B keeps it too: with its own `startAtMs`
    within 250 ms of T it is C1 or the normal pin, without fields the
    ordinary follow-up; one further off means the leader started B on
    its own schedule — re-target to it, or correct once if B already
    started. Between a follower's own boundary and the leader's first
    report of B the state still names A (the server never promotes an
    item on its own): that state is NOT a track change. A paused or
    ended state disarms and is mirrored at once; any other item
    disarms. Check `ended` first: an ended party's tombstone carries
    no schedule, and nothing starts after it.
  - **Without the fields the pre-announce does nothing:** no
    announcement, latch, C1, pin or forced cadence — none of it runs
    with an older leader or on a server that does not schedule starts
    — and a next-item schedule never holds or moves the current item.
    The phone FOLLOWER's own landing is the deliberate exception: it
    never needs the fields, so it runs under any leader. With the party
    clock (`GET /time`) and a playing, anchored state:
    - **A self-scheduled join.** The join picks the instant it will
      start on first — now + its predicted queue fetch, resolve and
      open (learnt per phone; 1.5 s to begin with) + its engine lead —
      opens the track paused at the party's position for that instant,
      holds, and plays on it, re-aimed once if the preparation overran.
      Before, it started at the position the server had answered with,
      after about a second of fetching and opening, and stayed that far
      behind.
    - **A settle after every start** (a join, a timed or latched
      start, a skip, a resume, a correction): once the player is
      rolling it measures the drift against the party clock (the median
      of three readings) and, over 120 ms (1 s for audiobooks),
      relaunches — pause, seek to where the party will be, hold on the
      party clock, play. At most two relaunches per start.
    - **A 250 ms drift window between starts,** in place of the 2 s
      bar: each reading is against the party clock now, not the
      state's stamp; a median of three over 250 ms relaunches, at most
      once per 20 s and twice per track, never near the track's end or
      while a latched boundary is armed; past 3 s it is a leader scrub
      and seeks at once. Audiobooks keep a 3 s bar.
    - **A route-aware engine lead.** A start held for an instant plays
      one engine lead before it: 60 ms, plus the route's output latency
      on media_kit (libmpv's clock is the audible position), corrected
      by where held starts landed and capped at 1 s.

    Without the party clock (a server before 0.3.0 has no `GET /time`)
    a music join into a playing party still schedules itself, on the
    phone's OWN clock: the same plan, hold and single re-aim, with the
    party's position for an instant taken as the state's position as
    the server answered it, run forward at its rate from the moment the
    phone received that state. The fetch, resolve and open no longer
    leave it behind; only the response's one-way transit remains.
    Nothing can measure where that join landed without the clock, so
    no settle follows it and no drift window runs. The 2 s bar stays,
    but for a playing party it compares the player with the state's
    position run forward since it arrived, not with the state's stamp:
    against the stamp alone, the join's own state, applied straight
    after a join of more than 2 s, would read as the phone being that
    far ahead and seek it back. An audiobook join without the clock
    still starts at the answered position, as before.

    One more follower rule needs neither the fields nor the party
    clock: the phone's **seam guard**, modelled on the TV's. A playing
    state that names the track this phone's own advance left within
    the last 3 s, while the party's position in that track (on the
    party clock, or else the state's position run forward since it
    arrived) is in its last 3 s, is the leader's stale tail — a report
    from its last moments that landed after this phone crossed first —
    and is ignored instead of sending the phone back to that track. A
    paused state, or one from the top of the track (a Previous), is
    honoured.
- **Handover** (`POST /nowplaying/handover {deviceKey}`): the seat
  holder names a live follower the next leader. The position stays
  continuous (the anchor is kept), any schedule is cleared, the new
  leader is dropped from the followers list, and `handedOverAtMs` plus
  the bumped revision make the new leader read the state before it
  leads.
- **Drift** (`driftMs`, `driftMeasuredAtMs` on `POST /follow`): a
  follower's CLOCK drift as it last measured it — the party clock's
  target position (the leader's, extrapolated from the anchor) minus
  the follower's own PLAYER position, with no route or output latency
  in it. **Positive = the follower is BEHIND.** Ear-level (route)
  figures stay in the device's own status line. Listed per device on
  `followers[]` for the dashboard. Informational: the server never
  steers on it, and a heartbeat carrying one does not bump the
  revision.

`/health` advertises `time`, `nowplaying.scheduled-start`,
`nowplaying.handover` and `nowplaying.drift` for these; the
pre-announce rides `nowplaying.scheduled-start` (an older follower
sees a `startItemId` it ignores). There is no per-device room-delay
API: a room's output-chain delay is a knob on the device itself.

#### Party volume and announcements (the PA system)

- **Party volume** (`POST /nowplaying/volume`): the party slider
  (`partyVolume`, 0–100, absent = 100, 0 is real) and each device's own
  level on top of it, kept per device TOKEN — so an Apple TV keeps its
  level when it moves from following (under a UUID) to leading (under
  its token). The state lists `followers[].volume` and `leaderVolume`,
  and tells each requester its own `yourDeviceVolume`. Any device in the
  party may change them.
- **Features** (`features` on `POST /follow` and the PUT): what each
  device can do (`volume`, `announce`, `live-announce`), kept per token
  and listed per row and as `leaderFeatures`, so the Remote can say who
  will hear an announcement.
- **Announcements**: a recorded clip (`POST /nowplaying/announcements`,
  a 16 kHz mono 16-bit PCM WAV, 0.3–60.5 s) or live voice
  (`POST /nowplaying/live`, then PCM chunks in and "PAL1" frames out,
  at most 2 minutes). The SERVER picks the instants — when the chime
  starts and when voice sample 0 is heard — and every speaking device
  plays the same sample at the same moment on the party clock. One per
  party at a time, 6 a minute per profile, 32 MiB of clips and 16 live
  sessions across the server. The seat holder's leader-only switch
  (`POST /nowplaying/announce-policy`) reads true only while the device
  that set it holds the seat; the seat holder or the sender may stop one.
- **Never through the intent path, never from a body.** All of it is
  read-time, like queue edits: the PUT handler clears it, so an older
  leader cannot reset it and nothing a body says can plant it. A change
  bumps the revision and wakes the long-polls and touches no intent,
  anchor or schedule field — the leader's next heartbeat is not stale and
  a scheduled start stands. A live chunk never bumps at all.

**Voice is the most private thing the party line carries.** It is held
in the server's memory only — never the database, never disk, never a
log — and only the profile's own party can fetch it, with the Bearer
header (never a token in a URL). A clip is at most 60.5 seconds and a
live announcement at most 2 minutes; live samples are dropped 3 seconds
after they were due to play; everything is zeroed in place within 30
seconds of the announcement ending, and at once when the party ends or
expires. The access log skips every path that carries an announcement
id (an id is a key to someone's voice). Clients delete any temporary
file once it has played.

`/health` advertises `nowplaying.volume`, `nowplaying.announce` and
`nowplaying.live-announce`, each only alongside its routes; an app hides
every control whose capability is missing.

### The honest limit

**Whoever runs this server owns the file the data sits in, and can open
it with any SQLite client.** Nothing here changes that, and no amount of
API design can: the admin controls the process, so any key the server
holds to read the data, the admin holds too.

What is enforced is that *the API never becomes the tool for it* — the
product does not offer the household manager a screen showing what their
family listened to. This is a trust boundary between people who already
live together, not a cryptographic guarantee against the box's owner.

Two things that WOULD close that gap were considered and rejected:
hardware enclaves with remote attestation (real, but needs specific
server CPUs and a signed build pipeline — not something families run),
and client-side encryption where the phone holds the only key (real, but
the server could then compute nothing, which is the reason it exists).
Additive homomorphic encryption is a narrower third option — the server
could sum encrypted numbers it cannot read — but it does arithmetic
only, so no top-authors, no sorting, no grouping.

If that tradeoff ever stops being acceptable, the change is to stop
sending stats to the server at all, not to add ceremony that implies a
protection this does not provide.

## How the app talks to it

Optional, configured, and **always degradable**: every companion call
has a local fallback, because most users will never run this. The
pattern is the one the app already uses for AI — `fromSettings()`
returns null and the caller does it the old way.

```
CompanionClient.fromSettings()  → null when not configured
  → app uses its existing on-device path
```

No feature may become companion-only. If it would, it belongs in the
app.

## Implementation order

1. ✅ `/health` extension: advertises `capabilities`, so a new app can
   tell an old server from a new one and fall back rather than 404 its
   way through a feature.
2. ✅ Catalog store + dedupe + the four routes.
3. ✅ Profiles + per-person stats (`/admin/profiles/*`, `/stats/*`).
4. ✅ Device linking (`/link/*`, `/admin/links/*`) + per-device tokens.
5. `/llm/*` status, queue, unload.
6. Media review.

---

## Foreign keys: fixed

`database.Connect` used to open SQLite with `?_foreign_keys=on`, which is
**mattn/go-sqlite3** syntax. This project uses **modernc.org/sqlite**,
which ignored the unknown parameter silently — so for months every
`ON DELETE CASCADE` in the schema was inert.

It now uses `?_pragma=foreign_keys(1)`, verified against
modernc.org/sqlite v1.28.0:

| DSN | `PRAGMA foreign_keys` |
|---|---|
| `?_foreign_keys=on` (old) | `0` |
| `?_pragma=foreign_keys(1)` (now) | `1` |
| no parameters | `0` |

`TestForeignKeysAreEnforced` fails if that ever regresses. Even so,
`ProfileRepository.Delete` removes a profile's events **explicitly**
rather than trusting the cascade: this server spent months with those
constraints silently off, and "someone's data was supposed to be
deleted" is the wrong thing to be wrong about.
