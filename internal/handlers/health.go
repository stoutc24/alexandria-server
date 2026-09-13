package handlers

import (
	"os"
	"runtime"
	"sync"
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
	Name      string `json:"name,omitempty"`
	Status    string `json:"status"`
	Uptime    string `json:"uptime"`
	Database  string `json:"database"`
	GoVersion string `json:"goVersion"`
	Version   string `json:"version"`

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
	"nowplaying",
	// The conductor protocol: device identity from the token, absolute
	// intents, millisecond anchors. Clients branch on these, never on a
	// 404 from a route an older server simply does not have.
	"nowplaying.leader",
	"nowplaying.intents",
	"nowplaying.ms",
}

// optionalCapabilities are advertised only while their probe says yes.
//
// Some services depend on things outside the binary — the trailer
// resolver needs yt-dlp, ffmpeg and deno on the box — and a capability
// the routes cannot actually deliver is worse than none: the app would
// try it, fail, and only then fall back. So these are asked on every
// /health call rather than listed once at compile time. The static list
// above stays exactly as it is; this is for the conditional ones only.
type optionalCapability struct {
	name      string
	available func() bool
}

var (
	optionalMu           sync.RWMutex
	optionalCapabilities []optionalCapability
)

// AdvertiseCapability registers a capability that is advertised only
// while available() returns true. Called from main at startup. The probe
// must be cheap: /health is what LAN discovery and the container health
// check poll.
func AdvertiseCapability(name string, available func() bool) {
	optionalMu.Lock()
	defer optionalMu.Unlock()
	optionalCapabilities = append(optionalCapabilities, optionalCapability{name, available})
}

// capabilityList is the static set plus whichever optional ones are
// available right now. A fresh slice each call, so nothing downstream
// can mutate the static list through it.
func capabilityList() []string {
	optionalMu.RLock()
	defer optionalMu.RUnlock()
	out := make([]string, 0, len(capabilities)+len(optionalCapabilities))
	out = append(out, capabilities...)
	for _, oc := range optionalCapabilities {
		if oc.available() {
			out = append(out, oc.name)
		}
	}
	return out
}

// configuredName is the operator's chosen label, set at startup. Empty
// falls back to the hostname — fine on a NAS, useless in Docker where
// the hostname is a per-recreate container id.
var configuredName string

// SetServerName pins the discovery label. Called once from main.
func SetServerName(name string) { configuredName = name }

func Health(c *fiber.Ctx) error {
	dbStatus := "connected"
	if err := database.DB.Ping(); err != nil {
		dbStatus = "disconnected"
	}

	host := configuredName
	if host == "" {
		host, _ = os.Hostname()
	}
	return c.JSON(HealthResponse{
		App:       "alexandria-companion",
		Name:      host,
		Status:    "healthy",
		Uptime:    time.Since(startTime).Round(time.Second).String(),
		Database:  dbStatus,
		GoVersion: runtime.Version(),
		Version:   "0.2.0",

		Capabilities: capabilityList(),
	})
}
