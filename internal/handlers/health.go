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
		Version:   "0.1.0",
	})
}
