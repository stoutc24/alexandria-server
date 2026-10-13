// Package adminui serves the household management page.
//
// One embedded HTML file, no build step. The previous admin was a
// SvelteKit app, which meant a Node stage in the Dockerfile and a
// fifteen-minute image build for a page that lists a handful of rows.
// The surface here is small enough that vanilla JS is the right size,
// and it keeps npm out of the supply chain for a server whose whole
// job is being trustworthy on someone's home network.
//
// The page itself holds no secrets and is served unauthenticated: it is
// inert markup until someone types the admin token into it. Every call
// it makes goes to /api/v1/admin/*, which requires that token.
package adminui

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed index.html
var indexHTML string

//go:embed invite.html
var inviteHTML string

// InviteHandler serves the page an Alexandria account invite link opens.
//
// Messaging apps make web links tappable, not alexandria:// ones, so the
// link an admin shares is this page (/invite#invite=…). The invite stays
// in the fragment — never sent here, never logged — and the page hands it
// to the app with this server's own address. Static, and safe to serve to
// anyone: without an invite it says so and does nothing.
func InviteHandler(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; "+
			"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Cache-Control", "no-store")
	return c.SendString(inviteHTML)
}

// Handler serves the admin page.
func Handler(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	// The page talks only to this origin and loads nothing external.
	c.Set("Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"script-src 'self' 'unsafe-inline'; img-src 'self' data:")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.SendString(indexHTML)
}
