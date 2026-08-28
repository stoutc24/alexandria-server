package handlers

import (
	"runtime"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

var startTime = time.Now()

type HealthResponse struct {
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
}

func Health(c *fiber.Ctx) error {
	dbStatus := "connected"
	if err := database.DB.Ping(); err != nil {
		dbStatus = "disconnected"
	}

	return c.JSON(HealthResponse{
		Status:    "healthy",
		Uptime:    time.Since(startTime).Round(time.Second).String(),
		Database:  dbStatus,
		GoVersion: runtime.Version(),
		Version:   "0.2.0",

		Capabilities: capabilities,
	})
}
