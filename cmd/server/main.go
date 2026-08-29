package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/stoutsolutions/alexandria-server/internal/adminui"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/handlers"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
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

	app := fiber.New(fiber.Config{
		AppName:      "Alexandria Server v0.2.0",
		ErrorHandler: customErrorHandler,

		// Catalogue chunks are the largest thing this accepts. 8 MB is
		// double what a full chunk needs, so a slightly chatty client
		// still fits, while a runaway request is still refused rather
		// than buffered.
		BodyLimit: 8 * 1024 * 1024,

		// Trim the idle footprint: fewer, smaller buffers held per
		// connection between requests on a box that is mostly quiet.
		ReadBufferSize:    4096,
		Concurrency:       256,
		ReduceMemoryUsage: true,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "${time} | ${status} | ${latency} | ${method} ${path}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowHeaders: "Origin, Content-Type, Accept, Authorization",
		AllowMethods: "GET, POST, PUT, DELETE, PATCH, OPTIONS",
	}))

	catalogRepo := repository.NewCatalogRepository()
	profileRepo := repository.NewProfileRepository()
	statsRepo := repository.NewStatsRepository()
	linkRepo := repository.NewLinkRepository()
	adminAuthRepo := repository.NewAdminAuthRepository()
	if err := adminAuthRepo.PurgeExpiredSessions(); err != nil {
		log.Printf("could not purge expired admin sessions: %v", err)
	}

	catalogHandler := handlers.NewCatalogHandler(catalogRepo)
	nowPlayingHandler := handlers.NewNowPlayingHandler(nowplaying.NewStore())
	adminHandler := handlers.NewAdminHandler(profileRepo, cfg.DBPath)
	statsHandler := handlers.NewStatsHandler(statsRepo)
	linkHandler := handlers.NewLinkHandler(linkRepo, profileRepo)
	adminAuthHandler := handlers.NewAdminAuthHandler(adminAuthRepo)

	// Unauthenticated on purpose: the app needs to know whether a
	// companion exists and what it can do before it has been given a
	// token. It reveals capabilities, never library contents.
	app.Get("/health", handlers.Health)

	// The management page. Inert markup until someone signs in through
	// it, so it needs no auth of its own — every call it makes goes to
	// /api/v1/admin/*, which does.
	app.Get("/admin", adminui.Handler)

	api := app.Group("/api")
	v1 := api.Group("/v1")

	// Three ways in, and the difference between them is the whole
	// access model. There are no accounts here: an admin token belongs
	// to whoever runs the box, and a profile token exists only to keep
	// one person's stats out of another person's totals. Neither is
	// ever an entitlement.
	requireAdmin := middleware.RequireAdmin(cfg, adminAuthRepo)
	requireProfile := middleware.RequireProfile(profileRepo)
	requireAnyToken := middleware.RequireAnyToken(cfg, profileRepo)

	// Household management: who exists, and how much space they use.
	// Nothing here returns anyone's history — see handlers/admin.go.
	// The admin login. Rate limited hard: this is the one place on the
	// server where guessing is the attack, and 10/min makes grinding a
	// 6-digit code hopeless while never inconveniencing a person who
	// mistypes it twice.
	authLimiter := limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many sign-in attempts; wait a minute",
			})
		},
	})
	adminAuth := v1.Group("/admin/auth", authLimiter)
	adminAuth.Get("/status", adminAuthHandler.Status)
	adminAuth.Post("/login", adminAuthHandler.Login)
	adminAuth.Post("/logout", adminAuthHandler.Logout)
	// Enrolment is the ONE route that still trusts the environment token
	// outright. It is the break-glass path: whoever holds the .env can
	// always set up a new login, which is what recovers a companion
	// whose authenticator went into a lake.
	adminAuth.Post("/enroll",
		middleware.RequireAdminToken(cfg), adminAuthHandler.Enroll)

	admin := v1.Group("/admin", requireAdmin)
	admin.Post("/profiles", adminHandler.CreateProfile)
	admin.Get("/profiles", adminHandler.ListProfiles)
	admin.Delete("/profiles/:id", adminHandler.DeleteProfile)
	admin.Delete("/devices/:tokenId", adminHandler.RevokeDevice)
	admin.Get("/backup", adminHandler.Backup)

	// Device linking, admin half: see who is waiting and say who they
	// are. Approving is what turns a spoken code into access, which is
	// why the code itself is harmless.
	admin.Get("/links", linkHandler.ListPendingLinks)
	admin.Post("/links/approve", linkHandler.ApproveLink)

	// Device linking, device half. Unauthenticated by necessity — the
	// device has no token yet, which is the problem being solved. Rate
	// limited so it cannot be used to churn codes or grind claim
	// secrets.
	linkLimiter := limiter.New(limiter.Config{
		Max:        30,
		Expiration: time.Minute,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many link attempts; wait a minute",
			})
		},
	})
	link := v1.Group("/link", linkLimiter)
	link.Post("/request", linkHandler.RequestLink)
	link.Post("/claim", linkHandler.Claim)

	// The library index describes the household's SERVERS, not any
	// person, so anyone with a valid token may read it — and write it.
	//
	// Sync is NOT admin-only, deliberately. The index is populated by
	// the app, from what it can already see on the servers it is signed
	// in to; gating it behind the admin token would leave it empty
	// unless somebody ran curl by hand. The worst a household member can
	// do is refresh it with their own view, which degrades an index, not
	// security — and anyone can refresh it back.
	//
	// The trade-off to know about: sync REPLACES a source wholesale, so
	// if two people see different subsets of the same server, the last
	// to sync wins. Fine when everyone shares the same libraries, which
	// is the case this is built for.
	catalogGroup := v1.Group("/catalog")
	catalogGroup.Post("/sync", requireAnyToken, catalogHandler.SyncSource)
	catalogGroup.Get("/items", requireAnyToken, catalogHandler.GetItems)
	catalogGroup.Get("/duplicates", requireAnyToken, catalogHandler.GetDuplicates)
	catalogGroup.Get("/summary", requireAnyToken, catalogHandler.GetSummary)

	// A person's own numbers. requireProfile REJECTS the admin token on
	// purpose: managing the household must not be a way to read what
	// someone listened to.
	// A device pairing another device it already owns: the phone hands a
	// token to the watch, no code typed on a tiny screen.
	v1.Post("/devices", requireProfile, linkHandler.ProvisionDevice)

	// The party line: a profile's own devices report and read what is
	// playing RIGHT NOW, so a TV can join mid-stream. Behind
	// requireProfile — the admin token is rejected, same boundary as
	// stats. In memory only; nothing here ever touches the database.
	np := v1.Group("/nowplaying", requireProfile)
	np.Put("/", nowPlayingHandler.Report)
	np.Get("/", nowPlayingHandler.Get)
	np.Delete("/", nowPlayingHandler.Clear)

	stats := v1.Group("/stats", requireProfile)
	stats.Post("/events", statsHandler.AppendEvents)
	stats.Get("/summary", statsHandler.GetSummary)
	stats.Delete("/", statsHandler.DeleteOwnStats)

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

	log.Printf("Alexandria Server starting on port %s", cfg.Port)
	log.Printf("API available at http://localhost:%s/api/v1", cfg.Port)

	if err := app.Listen(":" + cfg.Port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
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
