package trailers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testID = "dQw4w9WgXcQ"

// fakeRunner stands in for yt-dlp. It records every call, optionally
// blocks until released (to hold a download "in flight"), and writes the
// merged file where yt-dlp would have, by reading the -o template it was
// given — so the service's own path handling is what is under test.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string

	started chan struct{} // receives once per call, as the call begins
	release chan struct{} // when non-nil, Run blocks until it is closed

	stdout    string
	stderr    string
	err       error
	writeFile bool // produce <id>.tmp.mp4
	partsOnly bool // produce only a per-format part, as a crash would
	fileBytes []byte
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	if tmpl := argAfter(args, "-o"); tmpl != "" {
		if f.writeFile {
			data := f.fileBytes
			if data == nil {
				data = []byte("not really an mp4")
			}
			_ = os.WriteFile(strings.Replace(tmpl, "%(ext)s", "mp4", 1), data, 0o644)
		}
		if f.partsOnly {
			_ = os.WriteFile(strings.Replace(tmpl, "%(ext)s", "f136.mp4.part", 1), []byte("partial"), 0o644)
		}
	}
	return []byte(f.stdout), []byte(f.stderr), f.err
}

func (f *fakeRunner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// fakeLookPath pretends every tool is installed. Like exec.LookPath, a
// name with a slash in it is taken as given; a bare name is "found".
func fakeLookPath(file string) (string, error) {
	if strings.Contains(file, "/") {
		return file, nil
	}
	return "/fake/bin/" + file, nil
}

func newTestService(t *testing.T, r Runner, tweak func(*Options)) *Service {
	t.Helper()
	opts := Options{
		CacheDir:        t.TempDir(),
		CacheMB:         64,
		DownloadTimeout: 5 * time.Second,
		Runner:          r,
		LookPath:        fakeLookPath,
		Logf:            t.Logf,
	}
	if tweak != nil {
		tweak(&opts)
	}
	return New(opts)
}

const infoJSON = `{"id":"dQw4w9WgXcQ","title":"Official Trailer","duration":173.4,"formats":[{"format_id":"136"}]}`

func TestPrepareDownloadsOnceAndCaches(t *testing.T) {
	r := &fakeRunner{writeFile: true, stdout: "WARNING: something harmless\n" + infoJSON + "\n"}
	s := newTestService(t, r, nil)

	e, err := s.Prepare(context.Background(), testID)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if e.ID != testID || e.Title != "Official Trailer" || e.DurationSec != 173 || e.Bytes == 0 {
		t.Fatalf("entry: %+v", e)
	}
	if _, err := os.Stat(filepath.Join(s.CacheDir(), testID+".mp4")); err != nil {
		t.Fatalf("final file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.CacheDir(), testID+".json")); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}

	// Second call: served from disk, yt-dlp not run again, metadata intact
	// because it came from the sidecar rather than a fresh --print-json.
	e2, err := s.Prepare(context.Background(), testID)
	if err != nil || e2.Title != "Official Trailer" || e2.DurationSec != 173 {
		t.Fatalf("cached prepare: %+v, %v", e2, err)
	}
	if r.count() != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1", r.count())
	}

	// The command line is the contract with yt-dlp: the canonical URL
	// built from the id, the H.264 selector, the mp4 merge, the runtime.
	call := r.calls[0]
	joined := strings.Join(call, " ")
	for _, want := range []string{
		"/fake/bin/yt-dlp",
		"--no-playlist", "--no-overwrites", "--no-mtime",
		"--max-filesize 400M",
		"--js-runtimes deno ",
		"--ffmpeg-location /fake/bin",
		"-f " + formatSelector,
		"--merge-output-format mp4",
		"--print-json", "--no-progress",
		"https://www.youtube.com/watch?v=" + testID,
	} {
		if !strings.Contains(joined+" ", want) {
			t.Errorf("command line missing %q:\n%s", want, joined)
		}
	}
}

// Two callers for the same id while it downloads must share one yt-dlp
// run: a TV that retries, or two TVs, cost one download.
func TestPrepareIsSingleFlight(t *testing.T) {
	r := &fakeRunner{writeFile: true, stdout: infoJSON, started: make(chan struct{}, 4), release: make(chan struct{})}
	s := newTestService(t, r, nil)

	type result struct {
		e   Entry
		err error
	}
	results := make(chan result, 2)
	go func() {
		e, err := s.Prepare(context.Background(), testID)
		results <- result{e, err}
	}()
	<-r.started // the first download is now in flight and registered

	go func() {
		e, err := s.Prepare(context.Background(), testID)
		results <- result{e, err}
	}()
	// Give the joiner a moment to reach the wait; it must not have
	// started a second run either way.
	time.Sleep(20 * time.Millisecond)
	if r.count() != 1 {
		t.Fatalf("second caller started its own download (%d runs)", r.count())
	}

	close(r.release)
	for i := 0; i < 2; i++ {
		res := <-results
		if res.err != nil || res.e.ID != testID || res.e.Bytes == 0 {
			t.Fatalf("caller %d: %+v, %v", i, res.e, res.err)
		}
	}
	if r.count() != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1", r.count())
	}
}

// A caller whose wait runs out gets ErrInProgress and the download keeps
// going; its next call finds the file.
func TestPrepareReturnsInProgressWhenTheCallerGivesUp(t *testing.T) {
	r := &fakeRunner{writeFile: true, stdout: infoJSON, release: make(chan struct{})}
	s := newTestService(t, r, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := s.Prepare(ctx, testID)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("got %v, want ErrInProgress", err)
	}

	close(r.release)
	e, err := s.Prepare(context.Background(), testID)
	if err != nil || e.Bytes == 0 {
		t.Fatalf("after release: %+v, %v", e, err)
	}
	if r.count() != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1", r.count())
	}
}

func TestPrepareRefusesWithoutTools(t *testing.T) {
	r := &fakeRunner{writeFile: true}
	s := newTestService(t, r, func(o *Options) {
		o.LookPath = func(file string) (string, error) {
			if file == "deno" {
				return "", exec.ErrNotFound
			}
			return "/fake/bin/" + file, nil
		}
	})
	if s.Available() {
		t.Fatal("available without deno")
	}
	_, err := s.Prepare(context.Background(), testID)
	var te *Error
	if !errors.As(err, &te) || te.Reason != ReasonToolMissing {
		t.Fatalf("got %v, want tool_missing", err)
	}
	if !strings.Contains(te.Msg, "DENO_PATH") {
		t.Errorf("message does not say what to set: %q", te.Msg)
	}
	if r.count() != 0 {
		t.Fatal("yt-dlp was run without deno")
	}
}

// An explicit DENO_PATH is passed through to yt-dlp as deno:<path>, so
// both sides use the same binary.
func TestExplicitDenoPathReachesYtDlp(t *testing.T) {
	r := &fakeRunner{writeFile: true, stdout: infoJSON}
	s := newTestService(t, r, func(o *Options) { o.DenoPath = "/opt/deno/bin/deno" })
	if _, err := s.Prepare(context.Background(), testID); err != nil {
		t.Fatal(err)
	}
	if got := argAfter(r.calls[0], "--js-runtimes"); got != "deno:/opt/deno/bin/deno" {
		t.Fatalf("--js-runtimes %q", got)
	}
}

func TestPrepareClassifiesFailures(t *testing.T) {
	cases := []struct {
		name   string
		runner *fakeRunner
		reason string
	}{
		{
			"video unavailable",
			&fakeRunner{err: errors.New("exit status 1"), stderr: "ERROR: [youtube] dQw4w9WgXcQ: Video unavailable. This video is private\n"},
			ReasonUnavailable,
		},
		{
			// The JS-runtime failure mode: a broken tool, not a bad video.
			"format not available",
			&fakeRunner{err: errors.New("exit status 1"), stderr: "WARNING: No supported JavaScript runtime could be found\nERROR: [youtube] dQw4w9WgXcQ: Requested format is not available.\n"},
			ReasonFailed,
		},
		{
			"binary gone",
			&fakeRunner{err: exec.ErrNotFound},
			ReasonToolMissing,
		},
		{
			// --max-filesize aborts and yt-dlp still exits 0 with no file.
			"exit 0 but no file",
			&fakeRunner{stdout: infoJSON, stderr: "[download] File is larger than max-filesize"},
			ReasonFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(t, tc.runner, nil)
			_, err := s.Prepare(context.Background(), testID)
			var te *Error
			if !errors.As(err, &te) {
				t.Fatalf("got %v (%T), want *Error", err, err)
			}
			if te.Reason != tc.reason {
				t.Fatalf("reason %q, want %q (%v)", te.Reason, tc.reason, err)
			}
			if _, ok := s.Lookup(testID); ok {
				t.Fatal("a failed download left a playable entry")
			}
		})
	}
}

func TestPrepareTimesOut(t *testing.T) {
	r := &fakeRunner{release: make(chan struct{})} // never released: blocks until ctx ends
	s := newTestService(t, r, func(o *Options) { o.DownloadTimeout = 40 * time.Millisecond })
	_, err := s.Prepare(context.Background(), testID)
	var te *Error
	if !errors.As(err, &te) || te.Reason != ReasonTimeout {
		t.Fatalf("got %v, want timeout", err)
	}
}

// Whatever yt-dlp left behind when it failed — per-format parts, a
// truncated merge — must go, or the next attempt's --no-overwrites keeps
// the garbage and the TV gets a broken file.
func TestFailureLeavesNoTempFiles(t *testing.T) {
	r := &fakeRunner{err: errors.New("exit status 1"), stderr: "ERROR: network", partsOnly: true}
	s := newTestService(t, r, nil)
	if _, err := s.Prepare(context.Background(), testID); err == nil {
		t.Fatal("expected failure")
	}
	entries, _ := os.ReadDir(s.CacheDir())
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file survived: %s", e.Name())
		}
	}
}

// A crash mid-download leaves temp files nothing else cleans up; the
// next start sweeps them.
func TestStartupSweepsTempFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, testID+".tmp.f136.mp4.part")
	keep := filepath.Join(dir, testID+".mp4")
	_ = os.WriteFile(stale, []byte("x"), 0o644)
	_ = os.WriteFile(keep, []byte("x"), 0o644)
	newTestService(t, &fakeRunner{}, func(o *Options) { o.CacheDir = dir })
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale temp file survived startup")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("startup sweep removed a finished trailer")
	}
}

// Eviction is by mtime, oldest first, until under the cap — and the
// trailer just downloaded is never the one evicted.
func TestPruneIsLRUByMtime(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"aaaaaaaaaaa", "bbbbbbbbbbb", "ccccccccccc", "ddddddddddd", "eeeeeeeeeee"}
	base := time.Now().Add(-time.Hour)
	for i, id := range ids {
		p := filepath.Join(dir, id+".mp4")
		if err := os.WriteFile(p, make([]byte, 1<<20), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{}`), 0o644)
		// aaaa is the oldest, eeee the newest.
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	// Constructing with a 3 MB cap prunes at startup: 5 MB → 3 MB.
	s := newTestService(t, &fakeRunner{}, func(o *Options) { o.CacheDir = dir; o.CacheMB = 3 })
	remaining := func() []string {
		var out []string
		for _, id := range ids {
			if _, ok := s.Lookup(id); ok {
				out = append(out, id)
			}
		}
		return out
	}
	if got := strings.Join(remaining(), ","); got != "ccccccccccc,ddddddddddd,eeeeeeeeeee" {
		t.Fatalf("after startup prune: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "aaaaaaaaaaa.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("evicted trailer's sidecar survived")
	}

	// Serving ccc makes it the most recently used; lowering the cap to
	// 1 MB must then keep ccc and evict ddd and eee.
	s.Touch("ccccccccccc")
	s.capBytes = 1 << 20
	s.prune("")
	if got := strings.Join(remaining(), ","); got != "ccccccccccc" {
		t.Fatalf("after touch + prune: %s", got)
	}

	// A cap smaller than the file just downloaded still keeps that file.
	s.capBytes = 1
	s.prune("ccccccccccc")
	if got := strings.Join(remaining(), ","); got != "ccccccccccc" {
		t.Fatalf("prune evicted the entry it was told to keep: %s", got)
	}
}

func TestLookupRejectsBadIDsWithoutTouchingDisk(t *testing.T) {
	s := newTestService(t, &fakeRunner{}, nil)
	for _, id := range []string{"..", "../alexandria", "", "x"} {
		if _, ok := s.Lookup(id); ok {
			t.Errorf("%q looked up", id)
		}
	}
}

func TestParseInfoSkipsNoise(t *testing.T) {
	got := parseInfo([]byte("[youtube] Extracting URL\n" + infoJSON + "\n[download] done\n"))
	if got.Title != "Official Trailer" || got.Duration != 173.4 {
		t.Fatalf("%+v", got)
	}
	if got := parseInfo([]byte("nothing here")); got.Title != "" {
		t.Fatalf("%+v", got)
	}
}
