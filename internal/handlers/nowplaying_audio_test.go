package handlers

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
	app.Post("/announce-policy", h.SetAnnouncePolicy)
	app.Post("/announcements", h.PostClip)
	app.Get("/announcements/:id", h.GetClip)
	app.Post("/announcements/:id/stop", h.StopAnnouncement)
	app.Post("/live", h.StartLive)
	app.Post("/live/:id/audio", h.PostLiveAudio)
	app.Get("/live/:id/audio", h.GetLiveAudio)
	app.Post("/live/:id/end", h.EndLive)
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

// ---- announcements ------------------------------------------------------

// clipWAV is a canonical clip of n samples (16 kHz mono 16-bit PCM) whose
// samples carry seed, so two clips never match.
func clipWAV(n int, seed byte) []byte {
	le := binary.LittleEndian
	data := 2 * n
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	le.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16)
	le.PutUint16(b[20:], 1)
	le.PutUint16(b[22:], 1)
	le.PutUint32(b[24:], 16000)
	le.PutUint32(b[28:], 32000)
	le.PutUint16(b[32:], 2)
	le.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(data))
	for i := 44; i < len(b); i++ {
		b[i] = seed + byte(i)
	}
	return b
}

func postClip(t *testing.T, app *fiber.App, device string, wav []byte) audioReply {
	t.Helper()
	return call(t, app, "POST", "/announcements", device, "audio/wav", wav)
}

func TestAnnouncePolicyAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	if r := callJSON(t, app, "POST", "/announce-policy", "tok-phone", `{"leaderOnly":true}`); r.status != 404 || r.code() != "no-session" {
		t.Fatalf("no session: %d %s", r.status, r.raw)
	}
	audioSession(t, app)
	for _, body := range []string{``, `{}`, `{"leaderOnly":"yes"}`, `{"leaderOnly":1}`, `nope`} {
		if r := callJSON(t, app, "POST", "/announce-policy", "tok-phone", body); r.status != 400 || r.code() != "bad-request" {
			t.Errorf("%q: %d %s, want 400 bad-request", body, r.status, r.raw)
		}
	}
	r := callJSON(t, app, "POST", "/announce-policy", "tok-tv", `{"leaderOnly":true}`)
	if r.status != 409 || r.code() != "not-leader" || r.state() == nil {
		t.Fatalf("a follower: %d %s", r.status, r.raw)
	}
	r = callJSON(t, app, "POST", "/announce-policy", "tok-phone", `{"leaderOnly":true}`)
	if r.status != 200 || r.state()["announceLeaderOnly"] != true {
		t.Fatalf("the holder: %d %s", r.status, r.raw)
	}
	// While it stands, a member's clip is refused with the state.
	r = postClip(t, app, "tok-tv", clipWAV(16000, 1))
	if r.status != 409 || r.code() != "leader-only" || r.state()["announceLeaderOnly"] != true {
		t.Fatalf("a member's clip while leader-only: %d %s", r.status, r.raw)
	}
}

// The body is checked in the contract's order before the store is asked.
func TestClipUploadsAreCheckedInOrder(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	bad := clipWAV(16000, 1)
	bad[22] = 2 // stereo
	big := make([]byte, nowplaying.MaxClipBytes+1)
	for name, c := range map[string]struct {
		ctype  string
		body   []byte
		status int
		code   string
	}{
		"text/plain":                  {"text/plain", clipWAV(16000, 1), 415, "unsupported-type"},
		"no type":                     {"", clipWAV(16000, 1), 415, "unsupported-type"},
		"audio/mp4":                   {"audio/mp4", clipWAV(16000, 1), 415, "unsupported-type"},
		"a wav lookalike":             {"audio/wavx", clipWAV(16000, 1), 415, "unsupported-type"},
		"415 before empty":            {"text/plain", nil, 415, "unsupported-type"},
		"empty":                       {"audio/wav", nil, 400, "bad-audio"},
		"over 2 MiB":                  {"audio/wav", big, 413, "too-large"},
		"a bad header":                {"audio/wav", bad, 400, "bad-audio"},
		"299 ms":                      {"audio/wav", clipWAV(4784, 1), 400, "too-short"},
		"60.501 s":                    {"audio/wav", clipWAV(968_016, 1), 413, "too-large"},
		"body checks before session":  {"audio/x-wav", clipWAV(16, 1), 400, "too-short"},
		"a good clip with no session": {"Audio/WAV; codecs=1", clipWAV(16000, 1), 404, "no-session"},
		"audio/wave, no session":      {"audio/wave", clipWAV(16000, 1), 404, "no-session"},
	} {
		r := call(t, app, "POST", "/announcements", "tok-phone", c.ctype, c.body)
		if r.status != c.status || r.code() != c.code {
			t.Errorf("%s: %d %q, want %d %s", name, r.status, r.code(), c.status, c.code)
		}
	}
	// A gzip Content-Encoding is never decompressed: the bytes are taken
	// as they came, so a compressed clip is just a bad one.
	audioSession(t, app)
	r := call(t, app, "POST", "/announcements", "tok-phone", "audio/wav", []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0},
		"Content-Encoding", "gzip")
	if r.status != 400 || r.code() != "bad-audio" {
		t.Fatalf("a gzip body: %d %s", r.status, r.raw)
	}
}

func TestClipAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	audioSession(t, app)

	if r := postClip(t, app, "tok-stranger", clipWAV(16000, 1)); r.status != 409 || r.code() != "not-in-party" || r.state() == nil {
		t.Fatalf("a stranger: %d %s", r.status, r.raw)
	}

	wav := clipWAV(3*16000, 7)
	r := postClip(t, app, "tok-remote", wav)
	ann, _ := r.body["announcement"].(map[string]any)
	if r.status != 200 || r.body["ok"] != true || ann == nil {
		t.Fatalf("upload: %d %s", r.status, r.raw)
	}
	for k, want := range map[string]any{
		"kind": "clip", "byLabel": "tok-remote label", "byDeviceKey": "tok-remote", "sampleRate": float64(16000),
		"durationMs": float64(3000), "sizeBytes": float64(len(wav)),
	} {
		if ann[k] != want {
			t.Errorf("announcement %s = %v, want %v", k, ann[k], want)
		}
	}
	chime, _ := ann["chimeAtMs"].(float64)
	voice, _ := ann["voiceStartsAtMs"].(float64)
	ends, _ := ann["voiceEndsAtMs"].(float64)
	now, _ := r.state()["serverNowMs"].(float64)
	if id, _ := ann["id"].(string); len(id) != 32 || voice != chime+1100 || ends != voice+3000 ||
		chime < now+1300 || chime > now+1500 {
		t.Fatalf("instants: %v (server now %v)", ann, now)
	}
	if st, _ := r.state()["announcement"].(map[string]any); st == nil || st["id"] != ann["id"] {
		t.Fatalf("the state does not list it: %s", r.raw)
	}

	// Busy, with the state naming who.
	r = postClip(t, app, "tok-tv", clipWAV(16000, 2))
	busy, _ := r.state()["announcement"].(map[string]any)
	if r.status != 409 || r.code() != "busy" || busy == nil || busy["byLabel"] != "tok-remote label" {
		t.Fatalf("a second clip: %d %s", r.status, r.raw)
	}
}

// Every refusal the store can give an announcement start maps to the
// contract's status and code, with the state exactly where the contract
// marks one. (The store's own tests reach each outcome; this pins the
// wire.)
func TestStartRefusalsMapToTheContract(t *testing.T) {
	app := fiber.New()
	app.Get("/refuse/:n", func(c *fiber.Ctx) error {
		n, _ := strconv.Atoi(c.Params("n"))
		return startRefused(c, nowplaying.Outcome(n), nowplaying.State{ItemID: "a", Revision: 7})
	})
	for _, c := range []struct {
		outcome   nowplaying.Outcome
		status    int
		code      string
		withState bool
	}{
		{nowplaying.OutcomeNoSession, 404, "no-session", false},
		{nowplaying.OutcomeNotInParty, 409, "not-in-party", true},
		{nowplaying.OutcomeLeaderOnly, 409, "leader-only", true},
		{nowplaying.OutcomeAnnounceBusy, 409, "busy", true},
		{nowplaying.OutcomeRateLimited, 429, "rate-limited", false},
		{nowplaying.OutcomeServerBusy, 503, "server-busy", false},
	} {
		r := call(t, app, "GET", "/refuse/"+strconv.Itoa(int(c.outcome)), "", "", nil)
		_, hasState := r.body["state"]
		if r.status != c.status || r.code() != c.code || hasState != c.withState || r.body["error"] == nil {
			t.Errorf("outcome %d: %d %s, want %d %s (state %v)", c.outcome, r.status, r.raw, c.status, c.code, c.withState)
		}
	}
}

// Fiber reuses the request buffer (Immutable is off): the store must keep
// its own copy, or the next request's body overwrites a kept clip.
func TestClipBytesAreCopiedOutOfTheRequestBuffer(t *testing.T) {
	app := fiber.New()
	app.Post("/copy", func(c *fiber.Ctx) error {
		raw := c.Request().Body()
		cp := copyBody(c)
		if len(cp) != len(raw) || !bytes.Equal(cp, raw) || &cp[0] == &raw[0] {
			return c.SendStatus(500)
		}
		return c.SendStatus(200)
	})
	if r := call(t, app, "POST", "/copy", "", "audio/wav", clipWAV(100, 1)); r.status != 200 {
		t.Fatal("copyBody shares the request's backing array")
	}

	// Two uploads back to back, the first stopped: the second (refused,
	// busy) still passes through the same request buffer.
	store := nowplaying.NewStore()
	audio := audioApp(store)
	audioSession(t, audio)
	first, second := clipWAV(16000, 11), clipWAV(16000, 99)
	r := postClip(t, audio, "tok-remote", first)
	id, _ := r.body["announcement"].(map[string]any)["id"].(string)
	callJSON(t, audio, "POST", "/announcements/"+id+"/stop", "tok-remote", "")
	if r := postClip(t, audio, "tok-phone", second); r.status != 409 || r.code() != "busy" {
		t.Fatalf("second upload: %d %s", r.status, r.raw)
	}
	got := call(t, audio, "GET", "/announcements/"+id, "tok-tv", "", nil)
	if got.status != 200 || !bytes.Equal(got.raw, first) {
		t.Fatalf("the first clip after a second upload: %d, equal %v", got.status, bytes.Equal(got.raw, first))
	}
}

func TestGetClipAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	audioSession(t, app)
	wav := clipWAV(16000, 42)
	r := postClip(t, app, "tok-remote", wav)
	id := r.body["announcement"].(map[string]any)["id"].(string)

	got := call(t, app, "GET", "/announcements/"+id, "tok-tv", "", nil)
	if got.status != 200 || !bytes.Equal(got.raw, wav) {
		t.Fatalf("fetch: %d, %d bytes", got.status, len(got.raw))
	}
	if ct := got.header.Get("Content-Type"); ct != "audio/wav" {
		t.Errorf("Content-Type %q", ct)
	}
	if cc := got.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	if cl := got.header.Get("Content-Length"); cl != strconv.Itoa(len(wav)) {
		t.Errorf("Content-Length %q, want %d", cl, len(wav))
	}
	for name, c := range map[string]struct{ device, profile, id string }{
		"not in the party": {"tok-stranger", "p1", id},
		"another profile":  {"tok-sarah", "p2", id},
		"unknown":          {"tok-tv", "p1", strings.Repeat("0", 32)},
	} {
		if name == "another profile" {
			call(t, app, "PUT", "/nowplaying", "tok-sarah", "application/json",
				[]byte(`{"itemId":"q","title":"Q","isPlaying":true,"basedOnRevision":0}`), "X-Test-Profile", "p2")
		}
		r := call(t, app, "GET", "/announcements/"+c.id, c.device, "", nil, "X-Test-Profile", c.profile)
		if r.status != 404 || r.code() != "not-found" || len(r.raw) > 200 || bytes.Contains(r.raw, wav[44:80]) {
			t.Errorf("%s: %d %s, want 404 not-found", name, r.status, r.raw)
		}
	}
}

func TestStopAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	audioSession(t, app)
	r := postClip(t, app, "tok-tv", clipWAV(3*16000, 3))
	id := r.body["announcement"].(map[string]any)["id"].(string)
	if r := callJSON(t, app, "POST", "/announcements/"+id+"/stop", "tok-remote", ""); r.status != 409 ||
		r.code() != "not-allowed" || r.state() == nil {
		t.Fatalf("a bystander: %d %s", r.status, r.raw)
	}
	if r := callJSON(t, app, "POST", "/announcements/nope/stop", "tok-phone", ""); r.status != 404 || r.code() != "not-found" {
		t.Fatalf("unknown: %d %s", r.status, r.raw)
	}
	r = callJSON(t, app, "POST", "/announcements/"+id+"/stop", "tok-phone", `{"ignored":true}`)
	ann, _ := r.state()["announcement"].(map[string]any)
	if r.status != 200 || ann == nil || ann["stoppedAtMs"] == nil || ann["stoppedByLabel"] != "tok-phone label" {
		t.Fatalf("the holder's stop: %d %s", r.status, r.raw)
	}
	if r := callJSON(t, app, "POST", "/announcements/"+id+"/stop", "tok-tv", ""); r.status != 200 {
		t.Fatalf("a repeat stop: %d %s", r.status, r.raw)
	}
}

// ---- live ---------------------------------------------------------------

func liveChunk(n int, seed byte) []byte {
	b := make([]byte, 2*n)
	for i := range b {
		b[i] = seed + byte(i*3)
	}
	return b
}

func TestLiveAnswers(t *testing.T) {
	store := nowplaying.NewStore()
	app := audioApp(store)
	if r := call(t, app, "POST", "/live", "tok-remote", "", nil); r.status != 404 || r.code() != "no-session" {
		t.Fatalf("no session: %d %s", r.status, r.raw)
	}
	audioSession(t, app)
	if r := call(t, app, "POST", "/live", "tok-stranger", "", nil); r.status != 409 || r.code() != "not-in-party" {
		t.Fatalf("a stranger: %d %s", r.status, r.raw)
	}
	r := call(t, app, "POST", "/live", "tok-remote", "application/json", []byte(`{}`))
	ann, _ := r.body["announcement"].(map[string]any)
	if r.status != 200 || ann == nil || ann["kind"] != "live" || ann["live"] != true {
		t.Fatalf("start: %d %s", r.status, r.raw)
	}
	id := ann["id"].(string)
	chime, capture := ann["chimeAtMs"].(float64), ann["captureFromMs"].(float64)
	voice, maxEnds := ann["voiceStartsAtMs"].(float64), ann["maxEndsAtMs"].(float64)
	if capture != chime+1150 || voice != capture+1000 || maxEnds != voice+120000 {
		t.Fatalf("instants: %v", ann)
	}
	for _, k := range []string{"voiceEndsAtMs", "endSample", "ended", "durationMs"} {
		if _, has := ann[k]; has {
			t.Errorf("an open live announcement carries %s: %v", k, ann)
		}
	}
	if r := call(t, app, "POST", "/live", "tok-tv", "", nil); r.status != 409 || r.code() != "busy" {
		t.Fatalf("live during live: %d %s", r.status, r.raw)
	}

	audio := "/live/" + id + "/audio"
	for name, c := range map[string]struct {
		query  string
		body   []byte
		status int
		code   string
	}{
		"no from":                       {"", liveChunk(10, 1), 400, "bad-chunk"},
		"negative from":                 {"?from=-1", liveChunk(10, 1), 400, "bad-chunk"},
		"fractional from":               {"?from=1.5", liveChunk(10, 1), 400, "bad-chunk"},
		"words for from":                {"?from=abc", liveChunk(10, 1), 400, "bad-chunk"},
		"empty":                         {"?from=0", nil, 400, "bad-chunk"},
		"odd":                           {"?from=0", []byte{1, 2, 3}, 400, "bad-chunk"},
		"over 64 KiB":                   {"?from=0", make([]byte, nowplaying.MaxLiveChunkBytes+2), 413, "too-large"},
		"ahead (capture has not begun)": {"?from=0", liveChunk(10, 1), 409, "ahead"},
	} {
		r := call(t, app, "POST", audio+c.query, "tok-remote", "application/octet-stream", c.body)
		if r.status != c.status || r.code() != c.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.status, r.raw, c.status, c.code)
		}
	}
	if r := call(t, app, "POST", audio+"?from=0", "tok-tv", "application/octet-stream", liveChunk(10, 1)); r.status != 404 ||
		r.code() != "not-found" {
		t.Fatalf("a member who is not the talker: %d %s", r.status, r.raw)
	}

	// Capture may begin 2 s early by the slack: from 0 is taken ~150 ms in.
	time.Sleep(250 * time.Millisecond)
	voiceSamples := liveChunk(3200, 9)
	r = call(t, app, "POST", audio+"?from=0", "tok-remote", "application/octet-stream", voiceSamples)
	if r.status != 200 || r.body["total"] != float64(3200) {
		t.Fatalf("a chunk: %d %s", r.status, r.raw)
	}

	// A reader gets a PAL1 frame: the contract's golden header, then the
	// samples asked for.
	got := call(t, app, "GET", audio+"?from=1600", "tok-tv", "", nil)
	golden, _ := hex.DecodeString("50414c314006000000000000800c00000000000000000000")
	if got.status != 200 || len(got.raw) != 24+3200 || !bytes.Equal(got.raw[:24], golden) ||
		!bytes.Equal(got.raw[24:], voiceSamples[3200:]) {
		t.Fatalf("frame: %d, %d bytes, header % x", got.status, len(got.raw), got.raw[:min(24, len(got.raw))])
	}
	if got.header.Get("Content-Type") != "application/octet-stream" || got.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("frame headers: %v", got.header)
	}
	if r := call(t, app, "GET", audio+"?from=x", "tok-tv", "", nil); r.status != 400 || r.code() != "bad-chunk" {
		t.Fatalf("a bad from: %d %s", r.status, r.raw)
	}
	if r := call(t, app, "GET", audio+"?from=0", "tok-stranger", "", nil); r.status != 404 || r.code() != "not-found" {
		t.Fatalf("a stranger reading: %d %s", r.status, r.raw)
	}

	// The end: only the talker, once; its end on the answer and the state.
	if r := callJSON(t, app, "POST", "/live/"+id+"/end", "tok-tv", `{"endSample":10}`); r.status != 404 || r.code() != "not-found" {
		t.Fatalf("a member ending it: %d %s", r.status, r.raw)
	}
	r = callJSON(t, app, "POST", "/live/"+id+"/end", "tok-remote", `{"endSample":3200}`)
	ended, _ := r.body["announcement"].(map[string]any)
	if r.status != 200 || ended["ended"] != true || ended["endSample"] != float64(3200) || ended["live"] != nil ||
		ended["voiceEndsAtMs"] != voice+200 {
		t.Fatalf("end: %d %s", r.status, r.raw)
	}
	if st, _ := r.state()["announcement"].(map[string]any); st == nil || st["ended"] != true {
		t.Fatalf("the state after the end: %s", r.raw)
	}
	if r := call(t, app, "POST", "/live/"+id+"/end", "tok-remote", "", []byte("not json")); r.status != 200 {
		t.Fatalf("a repeat end with a body that does not parse: %d %s", r.status, r.raw)
	}
	r = call(t, app, "POST", audio+"?from=3200", "tok-remote", "application/octet-stream", liveChunk(10, 1))
	if _, hasState := r.body["state"]; r.status != 409 || r.code() != "live-ended" || r.body["endSample"] != float64(3200) || hasState {
		t.Fatalf("a chunk after the end: %d %s", r.status, r.raw)
	}
	got = call(t, app, "GET", audio+"?from=3200", "tok-tv", "", nil)
	if got.status != 200 || len(got.raw) != 24 || binary.LittleEndian.Uint32(got.raw[20:]) != 1 {
		t.Fatalf("a read past the end: %d % x", got.status, got.raw)
	}
	// A live id is no clip.
	if r := call(t, app, "GET", "/announcements/"+id, "tok-tv", "", nil); r.status != 404 || r.code() != "not-found" {
		t.Fatalf("a live id as a clip: %d %s", r.status, r.raw)
	}
}
