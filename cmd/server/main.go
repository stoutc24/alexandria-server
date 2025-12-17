package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/handlers"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
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

	app := fiber.New(fiber.Config{
		AppName:      "Alexandria Server v0.1.0",
		ErrorHandler: customErrorHandler,
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

	userRepo := repository.NewUserRepository()
	familyRepo := repository.NewFamilyRepository()

	authHandler := handlers.NewAuthHandler(cfg, userRepo)
	familyHandler := handlers.NewFamilyHandler(familyRepo)

	app.Get("/health", handlers.Health)

	api := app.Group("/api")
	v1 := api.Group("/v1")

	auth := v1.Group("/auth")
	auth.Post("/register", authHandler.Register)
	auth.Post("/login", authHandler.Login)
	auth.Post("/refresh", authHandler.Refresh)

	protected := v1.Group("", middleware.JWTProtected(cfg))

	protected.Get("/me", authHandler.Me)

	families := protected.Group("/families")
	families.Post("/", familyHandler.Create)
	families.Get("/", familyHandler.GetMyFamilies)
	families.Post("/join", familyHandler.Join)
	families.Get("/:id", familyHandler.GetFamily)
	families.Delete("/:id/leave", familyHandler.Leave)
	families.Delete("/:id/members/:userId", familyHandler.RemoveMember)
	families.Post("/:id/invite-code", familyHandler.RegenerateInviteCode)

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
