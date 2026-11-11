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
// What tells the admin page from such a neighbour is the ORIGIN — scheme,
// host and port together — and browsers state it:
//
//   - Sec-Fetch-Site: "same-origin" is the page's own fetch, "none" is the
//     person themselves (the address bar, a bookmark). Anything else —
//     "same-site", "cross-site" — is another page, and the cookie counts
//     for nothing. Browsers send this header only to https addresses (and
//     localhost), so a server opened at http://192.168.1.50:4000 never
//     sees it, from any browser.
//   - Without it, Origin, which browsers put on every request that can
//     change something: it must be the origin this request was sent to
//     (the scheme the server sees, and the Host header), or the configured
//     public URL's. The scheme counts (checker, 2026-11-10): an app on
//     https://name is not the admin page at http://name.
//   - Neither header: not a page's request that can change anything. A
//     script holding the cookie is not a forged request, and is let
//     through as before.
//
// Bearer tokens are unaffected: a browser attaches no token by itself.

// SameOriginCookie reports whether a request carrying the admin cookie may
// use it. publicURL is ALEXANDRIA_PUBLIC_URL ("" when unset).
func SameOriginCookie(c *fiber.Ctx, publicURL string) bool {
	return sameOriginGuard(c, originOf(publicURL))
}

// sameOriginGuard is SameOriginCookie with the public URL's origin already
// worked out ("" when unset).
func sameOriginGuard(c *fiber.Ctx, publicOrigin string) bool {
	switch strings.ToLower(strings.TrimSpace(c.Get("Sec-Fetch-Site"))) {
	case "same-origin", "none":
		return true
	case "":
		// Plain http, or no browser: Origin decides.
	default:
		return false
	}
	raw := strings.TrimSpace(c.Get(fiber.HeaderOrigin))
	if raw == "" {
		return true
	}
	origin := originOf(raw)
	if origin == "" {
		// "null" (a sandboxed frame, a file) or nonsense: not this page.
		return false
	}
	own := strings.ToLower(c.Protocol() + "://" + strings.TrimSpace(string(c.Request().Host())))
	return origin == own || (publicOrigin != "" && origin == publicOrigin)
}

// originOf is scheme://host[:port] of a URL or an Origin header, lowered,
// "" when it has no scheme or host (or is not http/https).
func originOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(u.Host)
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
