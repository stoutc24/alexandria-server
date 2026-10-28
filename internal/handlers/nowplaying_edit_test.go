package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// The intent route's queue edits (owner, 2026-10-28): every field bounded
// before the store sees it, and each refusal a code a device can act on.

func editApp(store *nowplaying.Store) *fiber.App {
	app := fiber.New()
	h := NewNowPlayingHandler(store)
	app.Use(func(c *fiber.Ctx) error {
		// What RequireProfile sets from a profile token.
		c.Locals("profileID", "p1")
		c.Locals("deviceTokenID", "tok-tv")
		c.Locals("deviceLabel", "Living Room TV")
		return c.Next()
	})
	app.Post("/intent", h.Intent)
	return app
}

func postIntent(t *testing.T, app *fiber.App, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/intent", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func editingSession(store *nowplaying.Store, edits bool) nowplaying.State {
	store.SetQueue("p1", []nowplaying.QueueItem{
		{Source: "plex", ItemID: "plex:a"}, {Source: "plex", ItemID: "plex:b"}})
	zero := int64(0)
	st, _ := store.Report("p1", nowplaying.State{Source: "plex", ItemID: "plex:a", IsPlaying: true},
		nowplaying.ReportOpts{DeviceID: "tok-phone", BasedOnRevision: &zero, EditsQueue: edits})
	return st
}

func TestQueueEditBodiesAreBounded(t *testing.T) {
	store := nowplaying.NewStore()
	editingSession(store, true)
	app := editApp(store)

	many := make([]string, nowplaying.MaxEditItems+1)
	for i := range many {
		many[i] = `{"source":"plex","itemId":"plex:x"}`
	}
	long := strings.Repeat("x", nowplaying.MaxQueueFieldLen+1)
	for name, body := range map[string]string{
		"no items":           `{"type":"addToQueue","items":[]}`,
		"too many items":     `{"type":"addToQueue","items":[` + strings.Join(many, ",") + `]}`,
		"no source":          `{"type":"addToQueue","items":[{"itemId":"plex:x"}]}`,
		"source not a word":  `{"type":"addToQueue","items":[{"source":"http://x","itemId":"plex:x"}]}`,
		"no itemId":          `{"type":"addToQueue","items":[{"source":"plex"}]}`,
		"title too long":     `{"type":"addToQueue","items":[{"source":"plex","itemId":"plex:x","title":"` + long + `"}]}`,
		"negative duration":  `{"type":"addToQueue","items":[{"source":"plex","itemId":"plex:x","durationSec":-1}]}`,
		"removal, no index":  `{"type":"removeFromQueue","itemId":"plex:b","ifQueueRevision":1}`,
		"removal, no guard":  `{"type":"removeFromQueue","index":1,"itemId":"plex:b"}`,
		"removal, no itemId": `{"type":"removeFromQueue","index":1,"ifQueueRevision":1}`,
	} {
		if code, _ := postIntent(t, app, body); code != fiber.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if cur, _ := store.Get("p1"); len(cur.QueueEdits) != 0 {
		t.Fatalf("a refused body left an edit: %+v", cur.QueueEdits)
	}
}

func TestQueueEditAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	st := editingSession(store, true)
	app := editApp(store)

	code, out := postIntent(t, app, `{"type":"addToQueue","next":true,"items":[{"source":"plex","itemId":"plex:x","title":"X"}]}`)
	if code != fiber.StatusOK || out["ok"] != true {
		t.Fatalf("add: %d %v", code, out)
	}
	state, _ := out["state"].(map[string]any)
	edits, _ := state["queueEdits"].([]any)
	if len(edits) != 1 {
		t.Fatalf("answer carries no edit: %v", state)
	}
	if e := edits[0].(map[string]any); e["byLabel"] != "Living Room TV" || e["next"] != true {
		t.Fatalf("edit = %v", e)
	}

	qrev := strconv.FormatInt(st.QueueRevision, 10)
	code, out = postIntent(t, app, `{"type":"removeFromQueue","index":0,"itemId":"plex:a","ifQueueRevision":`+qrev+`}`)
	if code != fiber.StatusConflict || out["code"] != "is-current" {
		t.Fatalf("removing the playing song: %d %v", code, out)
	}
	code, out = postIntent(t, app, `{"type":"removeFromQueue","index":1,"itemId":"plex:a","ifQueueRevision":`+qrev+`}`)
	if code != fiber.StatusConflict || out["code"] != "stale" {
		t.Fatalf("removing what is not there: %d %v", code, out)
	}
	code, _ = postIntent(t, app, `{"type":"removeFromQueue","index":1,"itemId":"plex:b","ifQueueRevision":`+qrev+`}`)
	if code != fiber.StatusOK {
		t.Fatalf("removal: %d", code)
	}

	// A leader that cannot edit: the code says to update it.
	old := nowplaying.NewStore()
	editingSession(old, false)
	code, out = postIntent(t, editApp(old), `{"type":"addToQueue","items":[{"source":"plex","itemId":"plex:x"}]}`)
	if code != fiber.StatusConflict || out["code"] != "leader-cannot-edit" ||
		!strings.Contains(out["error"].(string), "update Alexandria") {
		t.Fatalf("leader that cannot edit: %d %v", code, out)
	}

	// Nothing playing.
	code, out = postIntent(t, editApp(nowplaying.NewStore()), `{"type":"addToQueue","items":[{"source":"plex","itemId":"plex:x"}]}`)
	if code != fiber.StatusNotFound || out["code"] != "no-session" {
		t.Fatalf("no session: %d %v", code, out)
	}
}
