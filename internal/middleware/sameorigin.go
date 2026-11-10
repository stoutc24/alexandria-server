package middleware

import (
	"net/url"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// The admin session cookie is honoured only for a request the admin page
// itself made (security review, 2026-11-10).
//
// SameSite=Strict keeps the cookie off requests from other SITES, and a
// site is a scheme and a registrable domain: the port is not part of it.
// So a page served from another port of the same machine — a NAS runs a
// dozen web apps on one address — or from a sibling subdomain is the same
// site, and the browser sent the cookie along with whatever that page
// posted. It could not read an answer (the CORS policy here allows no
// credentials), but it did not need to: a form post approving its own link
// code into a person was enough to collect that person's saved logins.
//
// What tells the admin page from such a neighbour is the ORIGIN, which
// does include the port, and browsers state it on every request:
//
//   - Sec-Fetch-Site (every current browser): "same-origin" is the page's
//     own fetch, "none" is the person themselves (the address bar, a
//     bookmark). Anything else — "same-site", "cross-site" — is another
//     page, and the cookie counts for nothing.
//   - Without it (a browser from before 2023), Origin: its host must be
//     the one this request was sent to, or the configured public URL's.
//   - Neither header: not a page's request at all. A script holding the
//     cookie is not a forged request, and is let through as before.
//
// Bearer tokens are unaffected: a browser attaches no token by itself.

// sameOriginGuard reports whether a cookie-authenticated request may use
// the cookie. publicHost is ALEXANDRIA_PUBLIC_URL's host ("" when unset).
func sameOriginGuard(c *fiber.Ctx, publicHost string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Get("Sec-Fetch-Site"))) {
	case "same-origin", "none":
		return true
	case "":
		// An older browser, or no browser: Origin decides.
	default:
		return false
	}
	origin := strings.TrimSpace(c.Get(fiber.HeaderOrigin))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// "null" (a sandboxed frame, a file) or nonsense: not this page.
		return false
	}
	host := strings.ToLower(u.Host)
	if strings.EqualFold(host, strings.TrimSpace(string(c.Request().Host()))) {
		return true
	}
	return publicHost != "" && strings.EqualFold(host, publicHost)
}

// publicHostOf is the host[:port] of the configured public URL, "" when it
// is unset or unreadable.
func publicHostOf(publicURL string) string {
	u, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// refuseCrossOrigin answers a request that carried a good admin cookie from
// some other page. 403, not 401: the admin page must not read it as "signed
// out" — and it never sees it, since its own requests are same-origin.
func refuseCrossOrigin(c *fiber.Ctx) error {
	return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
		"error":  "this request did not come from the admin page",
		"reason": "cross-origin",
	})
}
