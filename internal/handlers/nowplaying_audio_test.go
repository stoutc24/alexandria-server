package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// Party audio over HTTP: every body bounded and checked before the store
// sees it, and each refusal a status and a code a device can act on.

// audioApp is the party routes behind a stand-in for RequireProfile that
// takes the device from X-Test-Device (its token id and label alike) and
// the profile from X-Test-Profile (default p1).
func audioApp(store *nowplaying.Store) *fiber.App {
	app := fiber.New()
	h := NewNowPlayingHandler(store)
	app.Use(func(c *fiber.Ctx) error {
		// Copies: c.Get points into fasthttp's reused buffer, and the
		// store keeps these strings (RequireProfile's come from the
		// database).
		profile := strings.Clone(c.Get("X-Test-Profile"))
		if profile == "" {
			profile = "p1"
		}
		device := strings.Clone(c.Get("X-Test-Device"))
		c.Locals("profileID", profile)
		c.Locals("deviceTokenID", device)
		c.Locals("deviceLabel", device+" label")
		return c.Next()
	})
	app.Put("/nowplaying", h.Report)
	app.Get("/nowplaying", h.Get)
	app.Post("/follow", h.Follow)
	app.Post("/volume", h.SetVolume)
	return app
}

type audioReply struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func (r audioReply) code() string { s, _ := r.body["code"].(string); return s }

func (r audioReply) state() map[string]any { m, _ := r.body["state"].(map[string]any); return m }

// call sends one request as device (a token id; "" for none), with a raw
// body and content type.
func call(t *testing.T, app *fiber.App, method, path, device, ctype string, body []byte, hdr ...string) audioReply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if device != "" {
		req.Header.Set("X-Test-Device", device)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := audioReply{status: resp.StatusCode, header: resp.Header, raw: raw}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func callJSON(t *testing.T, app *fiber.App, method, path, device, body string) audioReply {
	t.Helper()
	var b []byte
	if body != "" {
		b = []byte(body)
	}
	return call(t, app, method, path, device, "application/json", b)
}

// audioSession: tok-phone leads; tok-tv follows under uuid-tv; tok-remote
// is a Remote.
func audioSession(t *testing.T, app *fiber.App) {
	t.Helper()
	if r := callJSON(t, app, "PUT", "/nowplaying", "tok-phone",
		`{"source":"plex","itemId":"a","title":"A","isPlaying":true,"basedOnRevision":0}`); r.status != 200 {
		t.Fatalf("leader report: %d %s", r.status, r.raw)
	}
	if r := callJSON(t, app, "POST", "/follow", "tok-tv", `{"deviceKey":"uuid-tv","role":"follower"}`); r.status != 200 {
		t.Fatalf("follow: %d %s", r.status, r.raw)
	}
	if r := callJSON(t, app, "POST", "/follow", "tok-remote", `{"deviceKey":"tok-remote","role":"controller"}`); r.status != 200 {
		t.Fatalf("remote: %d %s", r.status, r.raw)
	}
}

func TestVolumeBodiesAreChecked(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	audioSession(t, app)
	long := strings.Repeat("k", nowplaying.MaxDeviceKeyLen+1)
	for name, c := range map[string]struct {
		body, code string
	}{
		"missing":             {`{}`, "bad-volume"},
		"null":                {`{"volume":null}`, "bad-volume"},
		"fraction":            {`{"volume":40.5}`, "bad-volume"},
		"exponent":            {`{"volume":4e1}`, "bad-volume"},
		"string":              {`{"volume":"40"}`, "bad-volume"},
		"bool":                {`{"volume":true}`, "bad-volume"},
		"negative":            {`{"volume":-1}`, "bad-volume"},
		"over 100":            {`{"volume":101}`, "bad-volume"},
		"huge":                {`{"volume":99999999999999999999}`, "bad-volume"},
		"device, no volume":   {`{"deviceKey":"uuid-tv"}`, "bad-volume"},
		"not JSON":            {`volume=40`, "bad-request"},
		"empty":               {``, "bad-request"},
		"an array":            {`[40]`, "bad-request"},
		"deviceKey too long":  {`{"deviceKey":"` + long + `","volume":40}`, "bad-request"},
		"deviceKey not a str": {`{"deviceKey":7,"volume":40}`, "bad-request"},
	} {
		r := callJSON(t, app, "POST", "/volume", "tok-remote", c.body)
		if r.status != fiber.StatusBadRequest || r.code() != c.code {
			t.Errorf("%s: %d %q, want 400 %s", name, r.status, r.code(), c.code)
		}
	}
	if st, _ := store.Get("p1"); st.PartyVolume != nil || st.VolumeFor("tok-tv") != nil {
		t.Fatalf("a refused body set a volume: %+v", st)
	}
}

func TestVolumeAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)

	// Nothing playing.
	if r := callJSON(t, app, "POST", "/volume", "tok-remote", `{"volume":40}`); r.status != 404 || r.code() != "no-session" {
		t.Fatalf("no session: %d %s", r.status, r.raw)
	}
	audioSession(t, app)

	// Not in the party: 409 with the state to catch up on.
	r := callJSON(t, app, "POST", "/volume", "tok-stranger", `{"volume":40}`)
	if r.status != 409 || r.code() != "not-in-party" || r.state() == nil {
		t.Fatalf("a stranger: %d %s", r.status, r.raw)
	}
	if _, has := r.state()["partyVolume"]; has {
		t.Fatalf("a refusal set the volume: %s", r.raw)
	}

	// The party slider, from the Remote. Content-Type does not matter.
	r = call(t, app, "POST", "/volume", "tok-remote", "", []byte(`{"volume":40}`))
	st := r.state()
	if r.status != 200 || r.body["ok"] != true || st["partyVolume"] != float64(40) ||
		st["partyVolumeByLabel"] != "tok-remote label" || st["serverNowMs"] == nil {
		t.Fatalf("set the party: %d %s", r.status, r.raw)
	}

	// One device: the TV's row, by its key.
	r = callJSON(t, app, "POST", "/volume", "tok-remote", `{"deviceKey":"uuid-tv","volume":25}`)
	if r.status != 200 {
		t.Fatalf("set the TV: %d %s", r.status, r.raw)
	}
	rows, _ := r.state()["followers"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["volume"] != float64(25) {
		t.Fatalf("the TV row: %s", r.raw)
	}
	if _, has := r.state()["yourDeviceVolume"]; has {
		t.Fatalf("the Remote was told the TV's level as its own: %s", r.raw)
	}
	// Each device is told its own level, per response.
	tv := callJSON(t, app, "GET", "/nowplaying", "tok-tv", "")
	phone := callJSON(t, app, "GET", "/nowplaying", "tok-phone", "")
	if tv.body["yourDeviceVolume"] != float64(25) || tv.body["partyVolume"] != float64(40) {
		t.Fatalf("the TV's read: %s", tv.raw)
	}
	if _, has := phone.body["yourDeviceVolume"]; has || phone.body["partyVolume"] != float64(40) {
		t.Fatalf("the phone's read: %s", phone.raw)
	}

	// 0 is real, and published.
	r = callJSON(t, app, "POST", "/volume", "tok-tv", `{"volume":0}`)
	if r.status != 200 || r.state()["partyVolume"] != float64(0) {
		t.Fatalf("0: %d %s", r.status, r.raw)
	}

	// Not a device in the party.
	r = callJSON(t, app, "POST", "/volume", "tok-remote", `{"deviceKey":"tok-remote","volume":25}`)
	if r.status != 409 || r.code() != "no-such-device" || r.state() == nil {
		t.Fatalf("a Remote as a device: %d %s", r.status, r.raw)
	}
	r = callJSON(t, app, "POST", "/volume", "tok-remote", `{"deviceKey":"","volume":25}`)
	if r.status != 409 || r.code() != "no-such-device" {
		t.Fatalf("an empty deviceKey: %d %s", r.status, r.raw)
	}
}

// A PUT body cannot set the read-time fields, and an invalid features
// list never fails a report or a follow.
func TestPartyAudioCannotComeFromAReportBody(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	audioSession(t, app)
	callJSON(t, app, "POST", "/volume", "tok-tv", `{"volume":40}`)
	r := callJSON(t, app, "PUT", "/nowplaying", "tok-phone", `{"source":"plex","itemId":"a","title":"A",
		"isPlaying":true,"basedOnRevision":0,"partyVolume":0,"partyVolumeByLabel":"evil","leaderVolume":1,
		"yourDeviceVolume":2,"leaderFeatures":["evil"],"deviceVolumes":{"tok-phone":3},
		"features":["volume","NOT VALID"]}`)
	st := r.state()
	if r.status != 200 || st["partyVolume"] != float64(40) || st["partyVolumeByLabel"] != "tok-tv label" {
		t.Fatalf("report: %d %s", r.status, r.raw)
	}
	for _, k := range []string{"leaderVolume", "yourDeviceVolume", "leaderFeatures", "deviceVolumes"} {
		if _, has := st[k]; has {
			t.Errorf("the body planted %s: %s", k, r.raw)
		}
	}
	// A valid list from an accepted report is the seat's.
	r = callJSON(t, app, "PUT", "/nowplaying", "tok-phone", `{"source":"plex","itemId":"a","title":"A",
		"isPlaying":true,"basedOnRevision":0,"features":["volume","announce","volume"]}`)
	if lf, _ := r.state()["leaderFeatures"].([]any); r.status != 200 || len(lf) != 2 {
		t.Fatalf("leaderFeatures: %d %s", r.status, r.raw)
	}
	// An invalid list on a follow is ignored, never refused.
	r = callJSON(t, app, "POST", "/follow", "tok-tv", `{"deviceKey":"uuid-tv","role":"follower","features":["x","y","z","a","b","c","d","e","f"]}`)
	if r.status != 200 {
		t.Fatalf("follow with an invalid list: %d %s", r.status, r.raw)
	}
	r = callJSON(t, app, "POST", "/follow", "tok-tv", `{"deviceKey":"uuid-tv","role":"follower","features":["volume"]}`)
	got := callJSON(t, app, "GET", "/nowplaying", "tok-phone", "")
	rows, _ := got.body["followers"].([]any)
	if r.status != 200 || len(rows) != 1 {
		t.Fatalf("follow: %d %s / %s", r.status, r.raw, got.raw)
	}
	if f, _ := rows[0].(map[string]any)["features"].([]any); len(f) != 1 || f[0] != "volume" {
		t.Fatalf("the TV row's features: %s", got.raw)
	}
}
