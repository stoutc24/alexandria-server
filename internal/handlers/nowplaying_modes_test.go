package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// The leader's play modes over the party routes (owner, 2026-10-31:
// Shuffle and Repeat on the Remote): the report carries them, the intent
// route takes setShuffle / setRepeat, and every refusal is a code a
// device can act on.

// partyApp is the party routes as RequireProfile leaves them for one
// device of profile p1.
func partyApp(store *nowplaying.Store, tokenID, label string) *fiber.App {
	app := fiber.New()
	h := NewNowPlayingHandler(store)
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("profileID", "p1")
		c.Locals("deviceTokenID", tokenID)
		c.Locals("deviceLabel", label)
		return c.Next()
	})
	app.Put("/", h.Report)
	app.Get("/", h.Get)
	app.Post("/intent", h.Intent)
	return app
}

func partyCall(t *testing.T, app *fiber.App, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// leaderReport is a phone leader's PUT; extra is spliced into the body
// (`,"shuffle":true` …).
func leaderReport(t *testing.T, app *fiber.App, extra string) map[string]any {
	t.Helper()
	code, out := partyCall(t, app, "PUT", "/",
		`{"source":"plex","itemId":"plex:a","title":"A","isPlaying":true,"positionMs":60000,"basedOnRevision":0`+extra+`}`)
	if code != fiber.StatusOK {
		t.Fatalf("leader report: %d %v", code, out)
	}
	state, _ := out["state"].(map[string]any)
	return state
}

func TestALeadersReportCarriesItsPlayModes(t *testing.T) {
	store := nowplaying.NewStore()
	phone := partyApp(store, "tok-phone", "Chris's iPhone")
	tv := partyApp(store, "tok-tv", "Living Room TV")

	st := leaderReport(t, phone, `,"shuffle":true,"repeat":"one"`)
	if st["shuffle"] != true || st["repeat"] != "one" {
		t.Fatalf("the report's answer: shuffle %v repeat %v", st["shuffle"], st["repeat"])
	}
	code, read := partyCall(t, tv, "GET", "/", "")
	if code != fiber.StatusOK || read["shuffle"] != true || read["repeat"] != "one" {
		t.Fatalf("a follower's read: %d shuffle %v repeat %v", code, read["shuffle"], read["repeat"])
	}

	// A leader that predates them: neither key is published.
	old := nowplaying.NewStore()
	st = leaderReport(t, partyApp(old, "tok-old", "Old iPad"), "")
	_, read = partyCall(t, partyApp(old, "tok-tv", "Living Room TV"), "GET", "/", "")
	for _, m := range []map[string]any{st, read} {
		for _, k := range []string{"shuffle", "repeat"} {
			if v, has := m[k]; has {
				t.Fatalf("a leader without the modes: %s = %v published", k, v)
			}
		}
	}

	// A repeat that is not one of the three: the heartbeat stands, the
	// repeat reads as absent.
	st = leaderReport(t, partyApp(old, "tok-old", "Old iPad"), `,"shuffle":false,"repeat":"sometimes"`)
	if _, has := st["repeat"]; has || st["shuffle"] != false {
		t.Fatalf("an unknown repeat: %v", st)
	}
}

func TestPlayModeIntentBodiesAreValidated(t *testing.T) {
	store := nowplaying.NewStore()
	leaderReport(t, partyApp(store, "tok-phone", "Chris's iPhone"), `,"shuffle":false,"repeat":"off"`)
	app := editApp(store)
	for name, body := range map[string]string{
		"setShuffle without shuffle": `{"type":"setShuffle"}`,
		"shuffle null":               `{"type":"setShuffle","shuffle":null}`,
		"shuffle a string":           `{"type":"setShuffle","shuffle":"true"}`,
		"shuffle a number":           `{"type":"setShuffle","shuffle":1}`,
		"setRepeat without repeat":   `{"type":"setRepeat"}`,
		"repeat null":                `{"type":"setRepeat","repeat":null}`,
		"repeat empty":               `{"type":"setRepeat","repeat":""}`,
		"repeat in capitals":         `{"type":"setRepeat","repeat":"ALL"}`,
		"repeat unknown":             `{"type":"setRepeat","repeat":"shuffle"}`,
		"repeat a boolean":           `{"type":"setRepeat","repeat":true}`,
		"type in capitals":           `{"type":"SetShuffle","shuffle":true}`,
	} {
		if code, out := postIntent(t, app, body); code != fiber.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", name, code, out)
		}
	}
	cur, _ := store.Get("p1")
	if cur.LastIntent != nil || cur.Shuffle == nil || *cur.Shuffle || cur.Repeat != nowplaying.RepeatOff {
		t.Fatalf("a refused body changed the party: lastIntent %+v shuffle %v repeat %q",
			cur.LastIntent, cur.Shuffle, cur.Repeat)
	}
}

func TestPlayModeIntentAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	leaderReport(t, partyApp(store, "tok-phone", "Chris's iPhone"), `,"shuffle":false,"repeat":"off"`)
	remote := editApp(store)

	code, out := postIntent(t, remote, `{"type":"setShuffle","shuffle":true}`)
	st, _ := out["state"].(map[string]any)
	last, _ := st["lastIntent"].(map[string]any)
	if code != fiber.StatusOK || out["ok"] != true || st["shuffle"] != true || st["repeat"] != "off" ||
		last["type"] != "setShuffle" || last["byLabel"] != "Living Room TV" ||
		st["lastIntentRevision"] != st["revision"] {
		t.Fatalf("setShuffle: %d %v", code, out)
	}
	code, out = postIntent(t, remote, `{"type":"setRepeat","repeat":"one"}`)
	st, _ = out["state"].(map[string]any)
	if code != fiber.StatusOK || st["repeat"] != "one" || st["shuffle"] != true {
		t.Fatalf("setRepeat: %d %v", code, out)
	}

	// A leader that predates the modes: a code per mode, and the state
	// that says so (no keys), so the Remote hides the buttons.
	old := nowplaying.NewStore()
	leaderReport(t, partyApp(old, "tok-old", "Old iPad"), "")
	for body, want := range map[string]string{
		`{"type":"setShuffle","shuffle":true}`: "leader-cannot-shuffle",
		`{"type":"setRepeat","repeat":"all"}`:  "leader-cannot-repeat",
	} {
		code, out := postIntent(t, editApp(old), body)
		st, _ := out["state"].(map[string]any)
		msg, _ := out["error"].(string)
		if code != fiber.StatusConflict || out["code"] != want || !strings.Contains(msg, "Old iPad") ||
			!strings.Contains(msg, "update Alexandria") || st == nil {
			t.Fatalf("%s to an old leader: %d %v", body, code, out)
		}
		if _, has := st["shuffle"]; has {
			t.Fatalf("the refusal's state publishes shuffle: %v", st)
		}
	}

	// The seat vacant: nobody to carry it out.
	vacant := nowplaying.NewStore()
	leaderReport(t, partyApp(vacant, "tok-phone", "Chris's iPhone"), `,"shuffle":false,"repeat":"off"`)
	vacant.Join("p1", "tok-tv", "Living Room TV", nowplaying.RoleFollower)
	vacant.EndBy("p1", "tok-phone", "Chris's iPhone", "stopped")
	code, out = postIntent(t, editApp(vacant), `{"type":"setRepeat","repeat":"all"}`)
	if code != fiber.StatusConflict || out["code"] != "leader-asleep" || out["error"] != "nobody is leading the party right now" {
		t.Fatalf("with the seat vacant: %d %v", code, out)
	}
	// The other intents keep their own rules: a vacant seat does not
	// refuse a setPlaying (here already satisfied), as before.
	code, out = postIntent(t, editApp(vacant), `{"type":"setPlaying","playing":true}`)
	if code != fiber.StatusOK {
		t.Fatalf("setPlaying with the seat vacant (unchanged behaviour): %d %v", code, out)
	}

	// Nothing playing.
	code, out = postIntent(t, editApp(nowplaying.NewStore()), `{"type":"setShuffle","shuffle":true}`)
	if code != fiber.StatusNotFound || out["code"] != "no-session" {
		t.Fatalf("no session: %d %v", code, out)
	}
}
