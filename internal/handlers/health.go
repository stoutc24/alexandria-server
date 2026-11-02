package handlers

import (
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

var startTime = time.Now()

type HealthResponse struct {
	// App identifies WHICH self-hosted thing answered: LAN discovery
	// sweeps a subnet probing :4000/health, and plenty of software
	// answers a /health probe with 200 — the sweep must never offer a
	// stranger's service as an Alexandria companion.
	App string `json:"app"`
	// Name labels this server in a discovery pick-list ("DESKTOP-PC")
	// so a household with two companions can tell them apart.
	Name     string `json:"name,omitempty"`
	Status   string `json:"status"`
	Uptime   string `json:"uptime"`
	Database string `json:"database"`
	Version  string `json:"version"`
	// No Go version: anyone can read this page, and the exact Go release
	// tells a stranger which of its published flaws to try (security
	// review, 2026-11-02). No app ever read it.

	// Capabilities tells the app which companion services this build
	// actually serves, so a new app against an old server degrades to
	// its on-device path instead of 404ing its way through a feature.
	//
	// This is a list of WORK the server can do. It is never a list of
	// what the caller is permitted to do: entitlement is decided on the
	// device against RevenueCat and nowhere else. Adding an entitlement
	// or tier field here would let anyone unlock paid features by
	// running their own server — see docs/COMPANION_DESIGN.md.
	Capabilities []string `json:"capabilities"`

	// PublicURL is ALEXANDRIA_PUBLIC_URL, when the operator set one: the
	// address this server is reached at from outside. A phone linked
	// through a LAN http:// address can offer to switch to it — over
	// https, its admin sign-in can stay signed in.
	PublicURL string `json:"publicUrl,omitempty"`
}

// capabilities is the advertised feature set. Add a string here only
// when the routes backing it are actually wired up in main.go.
var capabilities = []string{
	"catalog.sync",
	"catalog.items",
	"catalog.duplicates",
	"catalog.summary",
	"profiles",
	"profiles.devices",
	"stats.events",
	"stats.summary",
	"stats.rereads",
	"stats.smart-ingest",
	"link",
	"devices.provision",
	// PATCH /devices/me: a device changes the name the party shows for it.
	"devices.rename",
	// GET /devices/me: a device asks who it is — {deviceKey (its token
	// id), label, profileId, profileName} — whether or not anything is
	// playing. A link token only.
	"devices.me",
	"nowplaying",
	// The conductor protocol: device identity from the token, absolute
	// intents, millisecond anchors. Clients branch on these, never on a
	// 404 from a route an older server simply does not have.
	"nowplaying.leader",
	"nowplaying.intents",
	"nowplaying.ms",
	// A follower's join asks the leader for a settled anchor
	// (anchorRevision / anchorRefreshPending on the state; POST /follow
	// answers with the revision and takes refreshAnchor).
	"nowplaying.anchor-refresh",
	// DELETE ?reason=stop-all: the leader ends the party for every
	// device; the record lingers marked `ended` so followers stop.
	"nowplaying.stop-all",
	// GET /time: the party's clock, for NTP-style offset measurement;
	// reports may carry sampledAtServerMs for a latency-free anchor.
	"time",
	// startAtMs / startPositionMs / startItemId on the state: a start
	// announced for a server instant, so every device begins together.
	"nowplaying.scheduled-start",
	// POST /nowplaying/handover: the seat holder names the next leader.
	"nowplaying.handover",
	// ...and so may any device in the party (2026-10-30); the state then
	// carries handedOverFrom and handedOverByLabel for the old leader.
	"nowplaying.handover-any",
	// driftMs on POST /follow, listed per follower on the state.
	"nowplaying.drift",
	// Party volume: POST /nowplaying/volume (the party slider, or one
	// device's own level); on the state partyVolume, partyVolumeByLabel,
	// leaderVolume, yourDeviceVolume, followers[].volume, leaderFeatures
	// and followers[].features; `features` on POST /follow and the PUT.
	"nowplaying.volume",
	// Announcements: POST /nowplaying/announce-policy (the seat holder's
	// leader-only switch, announceLeaderOnly on the state), POST
	// /nowplaying/announcements (a 16 kHz mono PCM WAV clip), GET
	// /nowplaying/announcements/:id, POST /nowplaying/announcements/:id/
	// stop; state.announcement of kind "clip".
	"nowplaying.announce",
	// Live announcements: POST /nowplaying/live, POST and GET
	// /nowplaying/live/:id/audio (PCM chunks in; "PAL1" frames out), POST
	// /nowplaying/live/:id/end; state.announcement of kind "live".
	"nowplaying.live-announce",
	// The leader's play modes: its reports carry shuffle (a boolean) and
	// repeat ("off", "all", "one"), published on the state as reported
	// (absent = that leader can't do it); POST /nowplaying/intent takes
	// setShuffle {shuffle} and setRepeat {repeat}, carried out by the
	// leader like setPlaying (409 leader-cannot-shuffle / -repeat when it
	// can't).
	"nowplaying.shuffle-repeat",
	// Managing the server from a phone, all of it live together:
	// POST /admin/auth/device-token (a phone's admin sign-in, "Bearer
	// alxa_…", accepted by every requireAdmin route); GET and DELETE
	// /admin/admin-devices[/:id]; GET /admin/catalog/summary and
	// /admin/catalog/duplicates; POST /admin/backup (a fresh code, sign-in
	// secrets left out); GET /admin/backup answering 403 to a phone; and
	// POST /admin/links/approve answering 404 profile-not-found, and
	// asking a phone for a fresh code ("totpCode" or "recoveryCode")
	// with every approval, someone new included.
	"admin.device-token",
	// DELETE /admin/profiles/:id/devices signs out every device of one
	// person, and a profile route answers a token that is not (or no
	// longer) one of ours 401 with code "token-revoked" — and a database
	// it could not ask 503, never 401 (2026-10-30).
	"devices.sign-out",
	// Alexandria Accounts (2026-10-12): POST /accounts/login and
	// /accounts/invite/{check,accept}, GET /accounts/me and
	// /accounts/services (sealed to the device), PUT /accounts/device-key,
	// POST /accounts/password and /accounts/totp/{setup,enable,disable};
	// for the admin, /admin/profiles/:id/{account,invite,services}.
	// "accounts.services" is added at run time when a secrets key is set.
	"accounts",
}

// accountsServicesOn is whether Alexandria Accounts can keep service
// logins — set at startup from whether a secrets key is configured, and
// published as "accounts.services" so the app knows before it asks.
var accountsServicesOn bool

// SetAccountsServices records whether stored service logins are on.
func SetAccountsServices(on bool) { accountsServicesOn = on }

// configuredName is the operator's chosen label, set at startup. Empty
// falls back to the hostname — fine on a NAS, useless in Docker where
// the hostname is a per-recreate container id.
var configuredName string

// SetServerName pins the discovery label. Called once from main.
func SetServerName(name string) { configuredName = name }

// configuredPublicURL is ALEXANDRIA_PUBLIC_URL, validated by config.
var configuredPublicURL string

// SetPublicURL sets the address /health publishes. Called once from
// main.
func SetPublicURL(u string) { configuredPublicURL = u }

func Health(c *fiber.Ctx) error {
	dbStatus := "connected"
	if err := database.DB.Ping(); err != nil {
		dbStatus = "disconnected"
	}

	host := configuredName
	if host == "" {
		host, _ = os.Hostname()
	}
	caps := capabilities
	if accountsServicesOn {
		caps = append(append([]string{}, capabilities...), "accounts.services")
	}
	return c.JSON(HealthResponse{
		App:      "alexandria-companion",
		Name:     host,
		Status:   "healthy",
		Uptime:   time.Since(startTime).Round(time.Second).String(),
		Database: dbStatus,
		Version:  "1.0.0",

		Capabilities: caps,
		PublicURL:    configuredPublicURL,
	})
}
