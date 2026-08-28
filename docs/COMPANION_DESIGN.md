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

### Three ways in

| Token | Can | Cannot |
|---|---|---|
| **Admin** (env, `ALEXANDRIA_API_TOKEN`) | Sync the catalog, create/rotate/delete profiles, back up the database | **Read anyone's stats** |
| **Device** (`alxp_…`, one per device) | Read the catalog, write and read its profile's stats, erase them | Sync the catalog, manage profiles |
| None | `/health` | Everything else |

The server refuses to boot when the admin token is unset, left at the
placeholder, or under 16 characters — an accidentally exposed companion
with no token is worse than one that failed to start. Comparison is
constant-time. Profile tokens are stored only as SHA-256 hashes, so a
stolen database yields nothing replayable. (Plain SHA-256 is correct
here: tokens are 32 bytes from `crypto/rand`, and the slow hashes exist
for low-entropy human passwords.)

`/health` stays open so the app can discover whether a companion exists,
and what it can do, before it has been handed a token. It reveals
capabilities, never library contents.

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

### What the admin can and cannot see

The admin runs the household, so they can see **who exists, when they
were last active, how many events they have, and how much time those
add up to.** That is enough to manage storage and membership.

They cannot call an API that returns a title, an author, or an event —
with ONE deliberate exception: `/admin/backup` streams the whole
database, stats rows included, because backups are the point of the
data being here. It is the single admin route that carries history, and
a leaked admin token therefore reaches everything a stolen disk would.
Every other admin route yields counts.
`RequireProfile` rejects the admin token outright, and the stats
repository offers no "read another profile" method for a future route to
reach for. The absence is the boundary — keep it absent.

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
