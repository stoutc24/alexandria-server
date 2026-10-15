package main

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"

	"github.com/stoutsolutions/alexandria-server/internal/adminui"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/handlers"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

// buildApp wires every route onto a fresh Fiber app. It holds no
// process state of its own beyond the app (the rate limiters live in
// it), so main serves one and the HTTP tests build a new one per test
// against the real routes. The database must already be connected and
// migrated.
func buildApp(cfg *config.Config) *fiber.App {
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
		// Concurrency is a ceiling on open connections, not a
		// pre-allocation, so a high one costs nothing while idle — and
		// a low one is what let a few hundred slow sockets close the
		// door on the household (SECURITY_AUDIT_2026-09 #15).
		ReadBufferSize:    4096,
		Concurrency:       4096,
		ReduceMemoryUsage: true,

		// No request may sit half-sent forever. Read covers the whole
		// request incl. an 8 MB catalog chunk on a slow uplink; Write
		// must outlast the 15 s now-playing long poll plus the reply;
		// Idle is the keep-alive gap between requests. Without these,
		// fasthttp waited without limit on every one of them.
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  90 * time.Second,
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

	catalogHandler := handlers.NewCatalogHandler(catalogRepo)
	nowPlayingHandler := handlers.NewNowPlayingHandler(nowplaying.NewStore())
	adminHandler := handlers.NewAdminHandler(profileRepo, cfg.DBPath)
	statsHandler := handlers.NewStatsHandler(statsRepo)
	adminAuthHandler := handlers.NewAdminAuthHandler(adminAuthRepo, cfg.PublicURL)
	linkHandler := handlers.NewLinkHandler(linkRepo, profileRepo, adminAuthHandler.FreshCode)
	adminDevicesHandler := handlers.NewAdminDevicesHandler(adminAuthRepo)

	// Alexandria Accounts. The vault exists only when a secrets key is set
	// (config.SecretsKey); without one, people can still sign in and link
	// their devices, and stored service logins stay off.
	var secrets *vault.Vault
	if cfg.SecretsKey != nil {
		v, err := vault.New(cfg.SecretsKey)
		if err != nil {
			log.Fatalf("accounts: the secrets key is unusable: %v", err)
		}
		secrets = v
	}
	accountRepo := repository.NewAccountRepository(secrets)
	accountsHandler := handlers.NewAccountsHandler(accountRepo, profileRepo, adminAuthHandler.FreshCode)
	// Admin access through a person's own account: the sign-in reads their
	// login; a change to their access reaches their phones' sign-ins.
	adminAuthHandler.UseAccounts(accountRepo)
	accountsHandler.UseAdminDevices(adminAuthRepo)
	handlers.SetAccountsServices(secrets != nil)
	// Server Admin's sign-ins to the household's servers: the same vault.
	serviceAdminRepo := repository.NewServiceAdminRepository(secrets)
	serviceAdminsHandler := handlers.NewServiceAdminsHandler(serviceAdminRepo, adminAuthHandler.FreshCode)
	// A person's account on one of those servers, made (or its password
	// set) by this server with the kept sign-in.
	provisionHandler := handlers.NewProvisionHandler(serviceAdminRepo, profileRepo)

	// Unauthenticated on purpose: the app needs to know whether a
	// companion exists and what it can do before it has been given a
	// token. It reveals capabilities, never library contents.
	// Discovery label: pinned by ALEXANDRIA_SERVER_NAME, else hostname.
	handlers.SetServerName(cfg.ServerName)
	handlers.SetPublicURL(cfg.PublicURL)
	app.Get("/health", handlers.Health)

	// The management page. Inert markup until someone signs in through
	// it, so it needs no auth of its own — every call it makes goes to
	// /api/v1/admin/*, which does.
	app.Get("/admin", adminui.Handler)
	// What an Alexandria account invite link opens (adminui.InviteHandler).
	app.Get("/invite", adminui.InviteHandler)

	api := app.Group("/api")
	v1 := api.Group("/v1")

	// Three ways in, and the difference between them is the whole
	// access model. There are no accounts here: an admin token belongs
	// to whoever runs the box, and a profile token exists only to keep
	// one person's stats out of another person's totals. Neither is
	// ever an entitlement.
	requireAdmin := middleware.RequireAdmin(cfg, adminAuthRepo)
	requireProfile := middleware.RequireProfile(profileRepo)
	requireAnyToken := middleware.RequireAnyToken(cfg, profileRepo, adminAuthRepo)

	// Both rate limiters count each client on its own: the TCP peer, or
	// behind a proxy named in ALEXANDRIA_TRUSTED_PROXIES, the address it
	// forwarded for. Without that setting everyone behind a proxy is the
	// proxy, and one visitor could spend the whole household's budget.
	clientKey := middleware.ClientKey(cfg.TrustedProxies)

	// Household management: who exists, and how much space they use.
	// Nothing here returns anyone's history — see handlers/admin.go.
	// The admin login. Rate limited hard: this is the one place on the
	// server where guessing is the attack, and 10/min makes grinding a
	// 6-digit code hopeless while never inconveniencing a person who
	// mistypes it twice. Per client; the login's own allowance of wrong
	// guesses (adminauth.GuessLimit) holds for every client at once.
	authLimiter := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   time.Minute,
		KeyGenerator: clientKey,
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
	// A phone's admin sign-in: the password and a fresh code, exactly as
	// /login asks, answered with an admin device token instead of a
	// cookie. It MUST stay in this group and above the requireAdmin
	// group below: Fiber matches group middleware by path prefix in
	// registration order, so registered after it, requireAdmin would
	// answer every sign-in with a 401 before the handler ran — and here
	// it shares the 10-a-minute budget with /login.
	adminAuth.Post("/device-token", adminAuthHandler.IssueDeviceToken)
	// Enrolment is the ONE route that still trusts the environment token
	// outright. It is the break-glass path: whoever holds the .env can
	// always set up a new login, which is what recovers a companion
	// whose authenticator went into a lake.
	adminAuth.Post("/enroll",
		middleware.RequireAdminToken(cfg), adminAuthHandler.Enroll)

	// AdminScopeGate: a phone signed in with a person's account reaches only
	// what their admin access covers; the admin login's sign-ins, the
	// browser's included, pass straight through.
	admin := v1.Group("/admin", requireAdmin, middleware.AdminScopeGate())
	admin.Post("/profiles", adminHandler.CreateProfile)
	admin.Get("/profiles", adminHandler.ListProfiles)
	admin.Delete("/profiles/:id", adminHandler.DeleteProfile)
	admin.Delete("/devices/:tokenId", adminHandler.RevokeDevice)
	admin.Get("/backup", adminHandler.Backup)

	// Device linking, admin half: see who is waiting and say who they
	// are. Approving is what turns a spoken code into access, which is
	// why the code itself is harmless.
	admin.Get("/links", linkHandler.ListPendingLinks)
	// Every approval from a phone adds a fresh code (see
	// handlers.ApproveLink), so a phone's approvals go through the SAME
	// authLimiter instance as /admin/auth: its code guesses share the
	// one 10-a-minute budget. The browser's approvals carry no code and
	// stay unlimited.
	admin.Post("/links/approve",
		middleware.ForAdminDevices(authLimiter), linkHandler.ApproveLink)

	// Phones' admin sign-ins: the web page's "Signed-in phones" list and
	// its Revoke buttons, and a phone signing itself out (DELETE with its
	// own id). Not rate limited — the page polls the list.
	admin.Get("/admin-devices", adminDevicesHandler.List)
	admin.Delete("/admin-devices", adminDevicesHandler.RevokeAll)
	admin.Delete("/admin-devices/:id", adminDevicesHandler.Revoke)

	// The library index again, for a phone managing the server: its
	// admin token is accepted only behind requireAdmin, never by
	// requireAnyToken, so the phone reads the same handlers here.
	admin.Get("/catalog/summary", catalogHandler.GetSummary)
	admin.Get("/catalog/duplicates", catalogHandler.GetDuplicates)

	// A phone's backup: a fresh code on top of its sign-in, the copy
	// without the admin login in it (see handlers.BackupForDevice). The
	// SAME authLimiter instance as /admin/auth, so code guesses here and
	// at the sign-in share one 10-a-minute budget.
	admin.Post("/backup", authLimiter,
		adminAuthHandler.RequireFreshCode, adminHandler.BackupForDevice)

	// Alexandria Accounts, the admin's half: a person's sign-in, invite and
	// services. An invite lets someone into a person's profile, so a
	// phone's invites add a fresh code (handlers.AccountsHandler.
	// CreateInvite) and share the sign-in rate limit, as link approvals do.
	// The summary is everyone's sign-in at a glance, for the People list.
	admin.Get("/accounts/summary", accountsHandler.Summary)
	// Admin access per person (the admin login's alone; AdminScopeGate).
	admin.Put("/profiles/:id/admin-access", accountsHandler.SetAdminAccess)
	admin.Get("/profiles/:id/account", accountsHandler.Account)
	admin.Delete("/profiles/:id/account", accountsHandler.RemoveLogin)
	admin.Post("/profiles/:id/invite",
		middleware.ForAdminDevices(authLimiter), accountsHandler.CreateInvite)
	admin.Delete("/profiles/:id/invite", accountsHandler.CancelInvite)
	admin.Get("/profiles/:id/services", accountsHandler.ListServices)
	admin.Post("/profiles/:id/services", accountsHandler.AddService)
	admin.Put("/profiles/:id/services/order", accountsHandler.OrderServices)
	admin.Patch("/profiles/:id/services/:sid", accountsHandler.UpdateService)
	admin.Delete("/profiles/:id/services/:sid", accountsHandler.DeleteService)

	// Server Admin's sign-ins to the household's servers (handlers.
	// ServiceAdminsHandler): the page and admin phones list, save and
	// remove them. Only an admin phone reads the passwords back, with a
	// fresh code, through the SAME authLimiter instance as /admin/auth.
	admin.Get("/service-admins", serviceAdminsHandler.List)
	admin.Post("/service-admins", serviceAdminsHandler.Save)
	admin.Post("/service-admins/open", authLimiter, serviceAdminsHandler.Open)
	admin.Delete("/service-admins/:id", serviceAdminsHandler.Delete)

	// A person's account on one of the household's servers, made — or its
	// password set — with the kept Server Admin sign-in: the one thing this
	// server reaches out for (handlers.ProvisionHandler). The page and
	// admin phones call it before saving the person's login for that
	// service. "full" access only (middleware.AdminScopeGate).
	admin.Get("/provision", provisionHandler.Capabilities)
	admin.Get("/provision/:type/libraries", provisionHandler.Libraries)
	admin.Post("/profiles/:id/provision", provisionHandler.Provision)

	// Device linking, device half. Unauthenticated by necessity — the
	// device has no token yet, which is the problem being solved. Rate
	// limited so it cannot be used to churn codes or grind claim
	// secrets.
	linkLimiter := limiter.New(limiter.Config{
		Max:          30,
		Expiration:   time.Minute,
		KeyGenerator: clientKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many link attempts; wait a minute",
			})
		},
	})
	// And every visitor together, for new requests only: each one is a
	// row in the list of devices waiting, so a client with many addresses
	// (an IPv6 block behind a trusted proxy) could otherwise grow it
	// without limit. Four visitors' worth a minute: no one visitor can
	// reach it alone. Claims are polls and add nothing, so they are not
	// counted here.
	linkRequestsEveryone := limiter.New(limiter.Config{
		Max:          120,
		Expiration:   time.Minute,
		KeyGenerator: func(*fiber.Ctx) string { return "everyone" },
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "too many devices are asking to link right now; wait a minute",
			})
		},
	})
	link := v1.Group("/link", linkLimiter)
	link.Post("/request", linkRequestsEveryone, linkHandler.RequestLink)
	link.Post("/claim", linkHandler.Claim)

	// Alexandria Accounts, the person's half. Signing in IS the device
	// link — no admin approval — and answers with an ordinary profile
	// token. Unauthenticated by necessity, so rate limited: a limiter of
	// its own (a household member's typos must never spend the admin
	// sign-in's budget), on top of the handler's shared allowance and each
	// sign-in's lockout.
	accountLimiter := limiter.New(limiter.Config{
		Max:          10,
		Expiration:   time.Minute,
		KeyGenerator: clientKey,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":  "too many sign-in attempts; wait a minute",
				"reason": "rate-limited",
			})
		},
	})
	accounts := v1.Group("/accounts")
	accounts.Post("/login", accountLimiter, accountsHandler.Login)
	accounts.Post("/invite/check", accountLimiter, accountsHandler.CheckInvite)
	accounts.Post("/invite/accept", accountLimiter, accountsHandler.AcceptInvite)
	accounts.Get("/me", requireProfile, accountsHandler.Me)
	accounts.Get("/services", requireProfile, accountsHandler.Services)
	accounts.Put("/device-key", requireProfile, accountsHandler.SetDeviceKey)
	accounts.Post("/password", requireProfile, accountLimiter, accountsHandler.ChangePassword)
	accounts.Post("/totp/setup", requireProfile, accountsHandler.TOTPSetup)
	accounts.Post("/totp/enable", requireProfile, accountLimiter, accountsHandler.TOTPEnable)
	accounts.Post("/totp/disable", requireProfile, accountLimiter, accountsHandler.TOTPDisable)
	// Manage server with this account's admin access: its password and a
	// fresh code, behind the SAME authLimiter instance as /admin/auth, so
	// guesses here and at the admin sign-in share one budget.
	accounts.Post("/admin-sign-in", requireProfile, authLimiter,
		adminAuthHandler.IssueAccountDeviceToken)

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
	// A device renaming itself: the party name every other screen shows
	// for it. The label used to be fixed at link time.
	v1.Patch("/devices/me", requireProfile, linkHandler.RenameSelf)
	// A device asking who it is: its device key, label and person. It
	// answers whether or not anything is playing, which the party line's
	// yourDeviceKey does not. A link token only, like everything here.
	v1.Get("/devices/me", requireProfile, linkHandler.Self)

	// The party line: a profile's own devices report and read what is
	// playing RIGHT NOW, so a TV can join mid-stream. Behind
	// requireProfile — the admin token is rejected, same boundary as
	// stats. In memory only; nothing here ever touches the database.
	np := v1.Group("/nowplaying", requireProfile)
	np.Put("/", nowPlayingHandler.Report)
	np.Get("/", nowPlayingHandler.Get)
	np.Get("/queue", nowPlayingHandler.GetQueue)
	np.Post("/follow", nowPlayingHandler.Follow)
	np.Post("/intent", nowPlayingHandler.Intent)
	// The seat holder makes another device the leader.
	np.Post("/handover", nowPlayingHandler.HandOver)
	np.Delete("/", nowPlayingHandler.Clear)
	// The party's clock, for the devices' offset measurement.
	v1.Get("/time", requireProfile, handlers.Time)

	stats := v1.Group("/stats", requireProfile)
	stats.Post("/events", statsHandler.AppendEvents)
	stats.Get("/summary", statsHandler.GetSummary)
	stats.Delete("/", statsHandler.DeleteOwnStats)

	return app
}
