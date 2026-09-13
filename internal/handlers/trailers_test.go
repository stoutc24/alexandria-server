package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/trailers"
)

const testID = "dQw4w9WgXcQ"
const testURL = "https://www.youtube.com/watch?v=" + testID

// fakeTrailers is the service the handler sees: canned answers, and a
// record of what it was asked, including the deadline it was given.
type fakeTrailers struct {
	mu        sync.Mutex
	available bool
	entry     trailers.Entry
	err       error
	// waitForCtx makes Prepare block until the handler's deadline, then
	// report in-progress — the shape of a slow download.
	waitForCtx bool

	prepared []string
	deadline time.Time
	touched  []string
}

func (f *fakeTrailers) Available() bool { return f.available }

func (f *fakeTrailers) Prepare(ctx context.Context, id string) (trailers.Entry, error) {
	f.mu.Lock()
	f.prepared = append(f.prepared, id)
	f.deadline, _ = ctx.Deadline()
	f.mu.Unlock()
	if f.waitForCtx {
		<-ctx.Done()
		return trailers.Entry{}, trailers.ErrInProgress
	}
	return f.entry, f.err
}

func (f *fakeTrailers) Lookup(id string) (trailers.Entry, bool) {
	if f.entry.ID == id && f.entry.Path != "" {
		return f.entry, true
	}
	return trailers.Entry{}, false
}

func (f *fakeTrailers) Touch(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.touched = append(f.touched, id)
}

func newTrailerApp(svc TrailerService) *fiber.App {
	app := fiber.New()
	h := NewTrailerHandler(svc)
	tr := app.Group("/api/v1/trailers")
	tr.Get("/prepare", h.Prepare)
	tr.Get("/stream/:id", h.Stream)
	return app
}

func get(t *testing.T, app *fiber.App, path string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func decode(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return m
}

func TestPrepareReady(t *testing.T) {
	svc := &fakeTrailers{available: true, entry: trailers.Entry{
		ID: testID, Title: "Official Trailer", DurationSec: 173, Bytes: 14105147, Path: "/x/y.mp4",
	}}
	app := newTrailerApp(svc)

	resp, body := get(t, app, "/api/v1/trailers/prepare?url="+testURL, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	m := decode(t, body)
	if m["id"] != testID || m["ready"] != true ||
		m["streamPath"] != "/api/v1/trailers/stream/"+testID ||
		m["title"] != "Official Trailer" || m["durationSec"] != float64(173) || m["bytes"] != float64(14105147) {
		t.Fatalf("body: %s", body)
	}
	if len(svc.prepared) != 1 || svc.prepared[0] != testID {
		t.Fatalf("service asked for %v", svc.prepared)
	}
	// The default wait is 45 s.
	if d := time.Until(svc.deadline); d < 40*time.Second || d > 46*time.Second {
		t.Fatalf("default deadline %v from now, want ~45 s", d)
	}
}

// The handler must not hold a request past `wait`: a slow download
// turns into a 202 the TV polls on, and the download keeps going.
func TestPrepareAcceptedWhileDownloading(t *testing.T) {
	svc := &fakeTrailers{available: true, waitForCtx: true}
	app := newTrailerApp(svc)

	start := time.Now()
	resp, body := get(t, app, "/api/v1/trailers/prepare?url="+testURL+"&wait=1", nil)
	held := time.Since(start)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if held < 900*time.Millisecond || held > 3*time.Second {
		t.Fatalf("request held %v, want about 1 s", held)
	}
	m := decode(t, body)
	if m["id"] != testID || m["ready"] != false || m["retryAfterSec"] != float64(5) {
		t.Fatalf("body: %s", body)
	}
	if resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("Retry-After %q", resp.Header.Get("Retry-After"))
	}
}

// wait is clamped: nothing above 90 s, nothing below 0.
func TestPrepareWaitIsClamped(t *testing.T) {
	svc := &fakeTrailers{available: true, entry: trailers.Entry{ID: testID}}
	app := newTrailerApp(svc)

	get(t, app, "/api/v1/trailers/prepare?url="+testURL+"&wait=100000", nil)
	if d := time.Until(svc.deadline); d < 85*time.Second || d > 91*time.Second {
		t.Fatalf("wait=100000 gave a deadline %v out, want ~90 s", d)
	}
	get(t, app, "/api/v1/trailers/prepare?url="+testURL+"&wait=-5", nil)
	if d := time.Until(svc.deadline); d > time.Second {
		t.Fatalf("wait=-5 gave a deadline %v out, want ~0", d)
	}
	get(t, app, "/api/v1/trailers/prepare?url="+testURL+"&wait=garbage", nil)
	if d := time.Until(svc.deadline); d < 40*time.Second || d > 46*time.Second {
		t.Fatalf("wait=garbage gave %v, want the 45 s default", d)
	}
}

func TestPrepareBadURL(t *testing.T) {
	svc := &fakeTrailers{available: true}
	app := newTrailerApp(svc)
	for _, q := range []string{
		"",
		"?url=",
		"?url=https://vimeo.com/123",
		"?url=https://www.youtube.com/watch?v=../../etc/passwd",
		"?url=https://www.youtube.com/playlist?list=abc",
	} {
		resp, body := get(t, app, "/api/v1/trailers/prepare"+q, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d: %s", q, resp.StatusCode, body)
		}
		if m := decode(t, body); m["error"] == "" {
			t.Errorf("%q: no error message", q)
		}
	}
	if len(svc.prepared) != 0 {
		t.Fatalf("a bad URL reached the service: %v", svc.prepared)
	}
}

func TestPrepareNotImplementedWithoutTools(t *testing.T) {
	svc := &fakeTrailers{available: false}
	app := newTrailerApp(svc)
	resp, body := get(t, app, "/api/v1/trailers/prepare?url="+testURL, nil)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	m := decode(t, body)
	if m["reason"] != trailers.ReasonToolMissing || m["error"] == "" {
		t.Fatalf("body: %s", body)
	}
	if len(svc.prepared) != 0 {
		t.Fatal("service was asked to prepare without tools")
	}
}

func TestPrepareFailuresCarryTheirReason(t *testing.T) {
	cases := []struct {
		reason string
		status int
	}{
		{trailers.ReasonUnavailable, http.StatusBadGateway},
		{trailers.ReasonTimeout, http.StatusBadGateway},
		{trailers.ReasonFailed, http.StatusBadGateway},
		// The tools vanished between the Available() check and the run.
		{trailers.ReasonToolMissing, http.StatusNotImplemented},
	}
	for _, tc := range cases {
		svc := &fakeTrailers{available: true, err: &trailers.Error{Reason: tc.reason, Msg: "because " + tc.reason}}
		app := newTrailerApp(svc)
		resp, body := get(t, app, "/api/v1/trailers/prepare?url="+testURL, nil)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.reason, resp.StatusCode, tc.status, body)
		}
		m := decode(t, body)
		if m["reason"] != tc.reason || m["error"] != "because "+tc.reason {
			t.Errorf("%s: body %s", tc.reason, body)
		}
	}

	// An error that is not a *trailers.Error still gets a reason.
	svc := &fakeTrailers{available: true, err: errors.New("disk on fire")}
	resp, body := get(t, newTrailerApp(svc), "/api/v1/trailers/prepare?url="+testURL, nil)
	if resp.StatusCode != http.StatusBadGateway || decode(t, body)["reason"] != trailers.ReasonFailed {
		t.Fatalf("plain error: %d %s", resp.StatusCode, body)
	}
}

// writeTrailerFile makes a fake MP4 whose bytes are their own offsets, so
// a range response can be checked byte for byte.
func writeTrailerFile(t *testing.T, size int) string {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	p := filepath.Join(t.TempDir(), testID+".mp4")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStreamServesWholeFile(t *testing.T) {
	const size = 100_000
	path := writeTrailerFile(t, size)
	svc := &fakeTrailers{available: true, entry: trailers.Entry{ID: testID, Bytes: size, Path: path}}
	app := newTrailerApp(svc)

	resp, body := get(t, app, "/api/v1/trailers/stream/"+testID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if len(body) != size || body[size-1] != byte((size-1)%251) {
		t.Fatalf("body %d bytes, want %d", len(body), size)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type %q", ct)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("Accept-Ranges %q — AVPlayer will not seek without it", ar)
	}
	if resp.ContentLength != size {
		t.Fatalf("Content-Length %d", resp.ContentLength)
	}
	// Serving is use, for the LRU.
	if len(svc.touched) != 1 || svc.touched[0] != testID {
		t.Fatalf("touched %v", svc.touched)
	}
}

func TestStreamHonoursRange(t *testing.T) {
	const size = 100_000
	path := writeTrailerFile(t, size)
	svc := &fakeTrailers{available: true, entry: trailers.Entry{ID: testID, Bytes: size, Path: path}}
	app := newTrailerApp(svc)

	// A middle slice, as a seek would ask for.
	resp, body := get(t, app, "/api/v1/trailers/stream/"+testID, map[string]string{"Range": "bytes=1000-1999"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status %d, want 206", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 1000-1999/100000" {
		t.Fatalf("Content-Range %q", cr)
	}
	if len(body) != 1000 || body[0] != byte(1000%251) || body[999] != byte(1999%251) {
		t.Fatalf("wrong bytes: len=%d first=%d last=%d", len(body), body[0], body[len(body)-1])
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/mp4" {
		t.Fatalf("Content-Type %q on 206", ct)
	}

	// Open-ended, as the initial probe does.
	resp, body = get(t, app, "/api/v1/trailers/stream/"+testID, map[string]string{"Range": "bytes=99000-"})
	if resp.StatusCode != http.StatusPartialContent || len(body) != 1000 {
		t.Fatalf("open-ended range: %d, %d bytes", resp.StatusCode, len(body))
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes 99000-99999/100000" {
		t.Fatalf("Content-Range %q", cr)
	}

	// Past the end: 416, and the header says how big the file is.
	resp, _ = get(t, app, "/api/v1/trailers/stream/"+testID, map[string]string{"Range": "bytes=500000-"})
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status %d, want 416", resp.StatusCode)
	}
	if cr := resp.Header.Get("Content-Range"); cr != "bytes */100000" {
		t.Fatalf("416 Content-Range %q", cr)
	}
	resp, _ = get(t, app, "/api/v1/trailers/stream/"+testID, map[string]string{"Range": "bytes=2000-1000"})
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("inverted range: status %d, want 416", resp.StatusCode)
	}
}

func TestStreamHead(t *testing.T) {
	const size = 4096
	path := writeTrailerFile(t, size)
	svc := &fakeTrailers{available: true, entry: trailers.Entry{ID: testID, Bytes: size, Path: path}}
	app := newTrailerApp(svc)

	req := httptest.NewRequest(http.MethodHead, "/api/v1/trailers/stream/"+testID, nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Fatalf("HEAD: %d, %d body bytes", resp.StatusCode, len(body))
	}
	if resp.Header.Get("Accept-Ranges") != "bytes" || resp.ContentLength != size {
		t.Fatalf("HEAD headers: Accept-Ranges=%q Content-Length=%d", resp.Header.Get("Accept-Ranges"), resp.ContentLength)
	}
}

func TestStreamRejectsBadIDsAndMisses(t *testing.T) {
	path := writeTrailerFile(t, 10)
	svc := &fakeTrailers{available: true, entry: trailers.Entry{ID: testID, Bytes: 10, Path: path}}
	app := newTrailerApp(svc)

	for _, id := range []string{
		"..",
		"..%2F..%2Fetc%2Fpasswd",
		"%2E%2E",
		"short",
		"dQw4w9WgXcQ.mp4",
		"dQw4w9WgXcQ%00",
	} {
		resp, body := get(t, app, "/api/v1/trailers/stream/"+id, nil)
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("%q: status %d: %s", id, resp.StatusCode, body)
		}
		if resp.Header.Get("Content-Type") == "video/mp4" {
			t.Errorf("%q: served a video", id)
		}
	}
	if len(svc.touched) != 0 {
		t.Fatalf("a rejected id was touched: %v", svc.touched)
	}

	// Well-formed but not cached.
	resp, body := get(t, app, "/api/v1/trailers/stream/aaaaaaaaaaa", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("uncached: status %d: %s", resp.StatusCode, body)
	}
}

// /health advertises trailers.youtube only while the service says it can
// deliver, and the static capabilities are untouched either way.
func TestHealthAdvertisesTrailersOnlyWhenAvailable(t *testing.T) {
	if err := database.Connect(filepath.Join(t.TempDir(), "health.db")); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer database.Close()

	optionalMu.Lock()
	saved := optionalCapabilities
	optionalCapabilities = nil
	optionalMu.Unlock()
	defer func() {
		optionalMu.Lock()
		optionalCapabilities = saved
		optionalMu.Unlock()
	}()

	svc := &fakeTrailers{available: false}
	AdvertiseCapability("trailers.youtube", svc.Available)

	app := fiber.New()
	app.Get("/health", Health)

	caps := func() map[string]bool {
		resp, body := get(t, app, "/health", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("health: %d %s", resp.StatusCode, body)
		}
		var h HealthResponse
		if err := json.Unmarshal(body, &h); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, c := range h.Capabilities {
			out[c] = true
		}
		return out
	}

	before := caps()
	if before["trailers.youtube"] {
		t.Fatal("advertised trailers.youtube with no tools")
	}
	for _, static := range []string{"catalog.sync", "nowplaying.ms", "link"} {
		if !before[static] {
			t.Fatalf("static capability %q missing", static)
		}
	}

	svc.available = true
	after := caps()
	if !after["trailers.youtube"] {
		t.Fatal("tools present but trailers.youtube not advertised")
	}
	if len(after) != len(before)+1 {
		t.Fatalf("capability count went %d → %d, want +1", len(before), len(after))
	}
}
