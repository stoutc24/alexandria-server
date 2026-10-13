package adminui

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// Both pages' Content-Security-Policy, pinned to the letter. The admin
// page may talk to this server and, for the Plex picker, to
// https://plex.tv — nothing broader; the invite page to nothing at all.
// Widening either is meant to be a deliberate edit here, never a side
// effect of another change.
func TestContentSecurityPolicyIsPinned(t *testing.T) {
	app := fiber.New()
	app.Get("/admin", Handler)
	app.Get("/invite", InviteHandler)

	for path, want := range map[string]string{
		"/admin": "default-src 'self'; style-src 'self' 'unsafe-inline'; " +
			"script-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
			"connect-src 'self' https://plex.tv; " +
			"frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"/invite": "default-src 'none'; style-src 'unsafe-inline'; " +
			"script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; " +
			"frame-ancestors 'none'",
	} {
		resp, err := app.Test(httptest.NewRequest("GET", path, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		if got := resp.Header.Values("Content-Security-Policy"); len(got) != 1 || got[0] != want {
			t.Errorf("GET %s Content-Security-Policy\n got %q\nwant %q", path, got, want)
		}
		if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("GET %s Referrer-Policy %q", path, got)
		}
	}

	// The admin page, framed by nobody — old browsers included.
	resp, err := app.Test(httptest.NewRequest("GET", "/admin", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("GET /admin X-Frame-Options %q, want DENY", got)
	}
}

// The Plex picker's pure helpers sit between two marker lines so that
// testdata/plex_picker_test.html can lift them out and test them in a
// browser. The markers must stay (once each, in order), and the block must
// stay pure: no page, network, storage or log access.
func TestPlexHelperBlockIsMarkedAndPure(t *testing.T) {
	const begin, end = "// ==== plex-pure: begin ====", "// ==== plex-pure: end ===="
	if strings.Count(indexHTML, begin) != 1 || strings.Count(indexHTML, end) != 1 {
		t.Fatalf("index.html needs exactly one %q and one %q", begin, end)
	}
	from, to := strings.Index(indexHTML, begin), strings.Index(indexHTML, end)
	if to < from {
		t.Fatal("the plex-pure end marker comes before its begin marker")
	}
	block := indexHTML[from:to]
	for _, banned := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.", "window.",
		"fetch(", "XMLHttpRequest", "localStorage", "sessionStorage", "cookie",
		"console.",
	} {
		if strings.Contains(block, banned) {
			t.Errorf("the plex-pure block uses %q; keep it pure", banned)
		}
	}
	if !strings.Contains(block, "var plexPure = (function () {") {
		t.Error("the plex-pure block no longer defines plexPure the way the browser test reads it")
	}
}
