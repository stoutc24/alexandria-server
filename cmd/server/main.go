package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/plexwatch"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

func main() {
	cfg := config.Load()

	if err := database.Connect(cfg.DBPath); err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations(); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Derived data heals itself: if the content-key algorithm changed
	// since this database's keys were written, recompute them now
	// rather than waiting for a device to re-sync the catalog.
	if err := repository.RekeyCatalogIfNeeded(); err != nil {
		log.Fatalf("Failed to rekey catalog: %v", err)
	}

	// Repositories are stateless, so main keeps its own for the startup
	// purge and the hourly housekeeping below; buildApp makes the ones
	// the routes use.
	linkRepo := repository.NewLinkRepository()
	adminAuthRepo := repository.NewAdminAuthRepository()
	if err := adminAuthRepo.PurgeExpiredSessions(); err != nil {
		log.Printf("could not purge expired admin sessions: %v", err)
	}
	purgeExpiredAdminDevices(adminAuthRepo)

	app := buildApp(cfg)

	// Picked Plex people's remote addresses follow the home's public IP
	// (internal/plexwatch): at start, then every 15 minutes, and only while
	// someone is picked from a Plex server. ALEXANDRIA_PLEX_ADDRESS_WATCH=off
	// turns it off.
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	// Its repository has no secrets key: it never reads or writes a login.
	if !plexwatch.Start(watchCtx, cfg.PlexAddressWatch,
		plexwatch.New(repository.NewAccountRepository(nil))) {
		log.Printf("plex address watch is off (ALEXANDRIA_PLEX_ADDRESS_WATCH)")
	}

	// Housekeeping. Expired link codes are the only table that grows
	// without anybody deleting from it, and a stale row is dead weight
	// in every pending-links query. Hourly is far more often than
	// necessary and costs a single indexed DELETE.
	housekeeping := time.NewTicker(time.Hour)
	defer housekeeping.Stop()
	go func() {
		for range housekeeping.C {
			if n, err := linkRepo.PurgeExpired(); err != nil {
				log.Printf("housekeeping: purge failed: %v", err)
			} else if n > 0 {
				log.Printf("housekeeping: removed %d expired link requests", n)
			}
			// Expired admin sessions and phones' admin sign-ins are also
			// deleted when presented; this clears the ones never seen
			// again, so the tables cannot grow without bound.
			if err := adminAuthRepo.PurgeExpiredSessions(); err != nil {
				log.Printf("housekeeping: admin session purge failed: %v", err)
			}
			purgeExpiredAdminDevices(adminAuthRepo)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down server...")
		if err := app.Shutdown(); err != nil {
			log.Printf("Error shutting down server: %v", err)
		}
	}()

	listen := net.JoinHostPort(cfg.ListenAddr, cfg.Port)
	if cfg.ListenAddr == "" {
		log.Printf("Alexandria Server starting on port %s", cfg.Port)
	} else {
		log.Printf("Alexandria Server starting on %s only (ALEXANDRIA_LISTEN_ADDR)", listen)
	}
	log.Printf("API available at http://localhost:%s/api/v1", cfg.Port)

	if err := app.Listen(listen); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// purgeExpiredAdminDevices deletes phones' lapsed admin sign-ins and
// logs each by label and id — never by token.
func purgeExpiredAdminDevices(auth *repository.AdminAuthRepository) {
	gone, err := auth.PurgeExpiredDeviceTokens(time.Now())
	if err != nil {
		log.Printf("housekeeping: admin device purge failed: %v", err)
		return
	}
	for _, d := range gone {
		log.Printf("admin device sign-in expired: %q (%s)", d.Label, d.ID)
	}
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}

	return c.Status(code).JSON(fiber.Map{
		"error": message,
	})
}
