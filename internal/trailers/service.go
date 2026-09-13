// Package trailers turns a YouTube trailer link into an MP4 an Apple TV
// can play.
//
// Why it exists: Jellyfin stores a film's trailer as a YouTube page URL,
// and AVPlayer cannot play a YouTube page. Nor can the server simply
// resolve a direct link and hand it over — YouTube now serves yt-dlp
// nothing but SEPARATE DASH video and audio streams; there is no muxed
// MP4 and no muxed HLS to point a player at. So the companion downloads
// both halves, has ffmpeg merge them into one MP4 (a stream copy, no
// re-encode), caches that file on disk and serves it with Range support.
//
// Three external tools, all discovered at startup:
//
//   - yt-dlp: the extractor. It is a cat-and-mouse tool — YouTube changes
//     something, it breaks, an update fixes it — so an updater runs
//     `yt-dlp -U` daily and every failure here is a clean, classified
//     error that the TV falls back from (it opens the YouTube app).
//   - ffmpeg: merges the two streams.
//   - deno: a JavaScript runtime. Extraction needs one now; without it
//     yt-dlp finds no usable formats at all and reports "Requested format
//     is not available".
//
// Missing any of them, the service stays constructed but Available() is
// false: the routes answer 501, /health does not advertise the
// capability, and the rest of the server is unaffected.
package trailers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// formatSelector picks H.264 video plus AAC audio, capped at 1080p.
	//
	// It must never pick VP9 or AV1: AVPlayer on tvOS will not play them,
	// and they are exactly what yt-dlp prefers when left to itself. The
	// fallbacks loosen the codec, then the container, so a video with an
	// odd format list still yields SOMETHING rather than nothing.
	// Verified against the owner's library: the best pair for a typical
	// trailer was 136+140 — 1280x720 avc1 + mp4a, ~14 MB for 173 s.
	formatSelector = "bv*[ext=mp4][vcodec^=avc1][height<=1080]+ba[ext=m4a]/bv*[ext=mp4][height<=1080]+ba/b"

	// maxFileSize refuses the odd "trailer" that is really a full film.
	maxFileSize = "400M"

	// DefaultDownloadTimeout bounds one yt-dlp run, slot wait included. A
	// trailer is a few tens of MB; anything still going after this is
	// stuck, not slow.
	DefaultDownloadTimeout = 150 * time.Second

	// updateTimeout bounds `yt-dlp -U`. It downloads a ~30 MB binary.
	updateTimeout = 5 * time.Minute

	// maxConcurrent bounds simultaneous downloads. Each one is a Python
	// process, a deno process and an ffmpeg run, and the container runs
	// with a memory limit sized for a Go server. Two is already generous;
	// a third caller waits for a slot (and its own wait budget decides
	// whether it gets a 202 meanwhile).
	maxConcurrent = 2

	// DefaultCacheMB is the on-disk cap when TRAILER_CACHE_MB is unset.
	DefaultCacheMB = 4096
)

// Reasons a preparation can fail. They are the contract with the TV,
// which branches on them (a missing tool is a configuration problem to
// surface; an unavailable video is YouTube's answer; a timeout or
// failure is "try the YouTube app instead").
const (
	ReasonToolMissing = "tool_missing"
	ReasonUnavailable = "unavailable"
	ReasonTimeout     = "timeout"
	ReasonFailed      = "failed"
)

// Error is a failed preparation with a Reason the TV can branch on.
type Error struct {
	Reason string
	Msg    string
	Err    error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// ErrInProgress is what Prepare returns when the caller's context ended
// while the download was still running. The download carries on in the
// background; the caller is expected to come back and ask again.
var ErrInProgress = errors.New("trailer download still in progress")

// Entry is a cached, playable trailer.
type Entry struct {
	ID          string `json:"id"`
	Title       string `json:"title,omitempty"`
	DurationSec int    `json:"durationSec,omitempty"`
	Bytes       int64  `json:"bytes"`
	// Path is where the MP4 sits on disk. Never sent to a client.
	Path string `json:"-"`
}

// sidecar is <id>.json next to <id>.mp4: the metadata from yt-dlp's
// --print-json, kept so a cache hit can still say what the file is.
type sidecar struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	DurationSec int       `json:"durationSec"`
	FetchedAt   time.Time `json:"fetchedAt"`
}

// Options configures a Service. Zero values mean "the default".
type Options struct {
	// Explicit tool paths (YT_DLP_PATH, FFMPEG_PATH, DENO_PATH). Empty
	// means look the tool up on PATH.
	YtDlpPath  string
	FfmpegPath string
	DenoPath   string

	// CacheDir holds <id>.mp4 and <id>.json. Created if missing.
	CacheDir string
	// CacheMB caps the directory; the least recently served trailers are
	// evicted first. Zero means DefaultCacheMB.
	CacheMB int64

	// DownloadTimeout bounds one download. Zero means
	// DefaultDownloadTimeout. Tests set it to milliseconds.
	DownloadTimeout time.Duration

	// Runner runs external programs. nil means os/exec; tests use a fake.
	Runner Runner
	// LookPath resolves a tool on PATH. nil means exec.LookPath; tests
	// pretend the tools exist.
	LookPath func(file string) (string, error)
	// Logf receives the startup and outcome lines. nil means log.Printf.
	Logf func(format string, args ...any)
}

// Service resolves, downloads, caches and hands out trailers.
type Service struct {
	ytdlp, ffmpeg, deno string
	// jsRuntime is the --js-runtimes value: "deno" when yt-dlp should find
	// it on PATH the same way we did, "deno:<path>" when the operator
	// pinned it with DENO_PATH.
	jsRuntime string

	cacheDir string
	capBytes int64
	cacheErr error
	timeout  time.Duration

	runner Runner
	logf   func(string, ...any)

	mu       sync.Mutex
	inflight map[string]*job
	slots    chan struct{}
	// toolLock is held shared by every download and exclusively by the
	// updater, so `yt-dlp -U` never swaps the binary out from under a
	// process being started, and never starts while one is running.
	toolLock sync.RWMutex
}

// job is one in-flight download that any number of callers may wait on.
type job struct {
	done  chan struct{}
	entry Entry
	err   error
}

// New discovers the tools, prepares the cache directory and reports —
// one line per missing tool, naming the variable that fixes it — what it
// found. It never fails: a server without the tools is a server without
// this one capability.
func New(opts Options) *Service {
	s := &Service{
		cacheDir: opts.CacheDir,
		capBytes: opts.CacheMB * 1024 * 1024,
		timeout:  opts.DownloadTimeout,
		runner:   opts.Runner,
		logf:     opts.Logf,
		inflight: make(map[string]*job),
		slots:    make(chan struct{}, maxConcurrent),
	}
	if opts.CacheMB <= 0 {
		s.capBytes = DefaultCacheMB * 1024 * 1024
	}
	if s.timeout <= 0 {
		s.timeout = DefaultDownloadTimeout
	}
	if s.runner == nil {
		s.runner = execRunner{}
	}
	if s.logf == nil {
		s.logf = log.Printf
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}

	s.ytdlp = findTool(lookPath, s.logf, "yt-dlp", opts.YtDlpPath, "YT_DLP_PATH")
	s.ffmpeg = findTool(lookPath, s.logf, "ffmpeg", opts.FfmpegPath, "FFMPEG_PATH")
	s.deno = findTool(lookPath, s.logf, "deno", opts.DenoPath, "DENO_PATH")
	s.jsRuntime = "deno"
	if opts.DenoPath != "" && s.deno != "" {
		s.jsRuntime = "deno:" + s.deno
	}

	if s.cacheDir == "" {
		s.cacheErr = errors.New("no cache directory configured")
	} else if err := os.MkdirAll(s.cacheDir, 0o755); err != nil {
		s.cacheErr = err
	}
	if s.cacheErr != nil {
		s.logf("trailers: cache directory %q unusable (%v) — set TRAILER_CACHE_DIR to a writable directory; YouTube trailers disabled", s.cacheDir, s.cacheErr)
	} else {
		// A crash mid-download leaves .tmp files that nothing else will
		// ever clean up, and a lowered cap should take effect now, not
		// after the next download.
		s.sweepTemp()
		s.prune("")
	}

	if s.Available() {
		s.logf("trailers: ready — yt-dlp=%s ffmpeg=%s deno=%s cache=%s (cap %d MB)",
			s.ytdlp, s.ffmpeg, s.deno, s.cacheDir, s.capBytes/(1024*1024))
	} else {
		s.logf("trailers: YouTube trailers disabled; /health will not advertise trailers.youtube")
	}
	return s
}

// findTool resolves one tool: the explicit path when the operator set
// one, else a PATH lookup. LookPath checks that the result is executable
// either way. Missing is logged, not fatal.
func findTool(lookPath func(string) (string, error), logf func(string, ...any), name, explicit, envVar string) string {
	if explicit != "" {
		p, err := lookPath(explicit)
		if err != nil {
			logf("trailers: %s not usable at %s=%q (%v)", name, envVar, explicit, err)
			return ""
		}
		return p
	}
	p, err := lookPath(name)
	if err != nil {
		logf("trailers: %s not found on PATH — install it, or set %s=/path/to/%s", name, envVar, name)
		return ""
	}
	return p
}

// Available reports whether every tool and the cache directory are in
// place. /health advertises "trailers.youtube" only while this is true.
func (s *Service) Available() bool {
	return s.ytdlp != "" && s.ffmpeg != "" && s.deno != "" && s.cacheErr == nil
}

// CacheDir is where the MP4s live.
func (s *Service) CacheDir() string { return s.cacheDir }

func (s *Service) finalPath(id string) string   { return filepath.Join(s.cacheDir, id+".mp4") }
func (s *Service) sidecarPath(id string) string { return filepath.Join(s.cacheDir, id+".json") }
func (s *Service) tempPath(id string) string    { return filepath.Join(s.cacheDir, id+".tmp.mp4") }

// outputTemplate is yt-dlp's -o value. The directory is escaped because
// -o is a TEMPLATE: a "%" in the cache path would otherwise be read as a
// field reference.
func (s *Service) outputTemplate(id string) string {
	dir := strings.ReplaceAll(s.cacheDir, "%", "%%")
	return filepath.Join(dir, id+".tmp.%(ext)s")
}

// Lookup returns the cached entry for id, if the file is on disk. It does
// not touch the file's mtime — see Touch.
func (s *Service) Lookup(id string) (Entry, bool) {
	if !ValidID(id) {
		return Entry{}, false
	}
	path := s.finalPath(id)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return Entry{}, false
	}
	e := Entry{ID: id, Bytes: fi.Size(), Path: path}
	if sc, err := s.readSidecar(id); err == nil {
		e.Title = sc.Title
		e.DurationSec = sc.DurationSec
	}
	return e, true
}

// Touch marks id as just used. Eviction is oldest-mtime-first, so a
// trailer that keeps being watched keeps being kept; without this the
// cache would be FIFO by download date, not LRU.
func (s *Service) Touch(id string) {
	if !ValidID(id) {
		return
	}
	now := time.Now()
	_ = os.Chtimes(s.finalPath(id), now, now)
}

// Prepare makes sure <id>.mp4 is on disk and returns it.
//
// A cached file returns at once. Otherwise the download starts in the
// background and Prepare waits for it — but only as long as ctx allows.
// If ctx ends first, Prepare returns ErrInProgress and the download
// CONTINUES: the caller polls, and its next call joins the same job.
// Downloads are single-flight per id, so a TV that retries, or two TVs
// wanting the same trailer, cost one yt-dlp run, not several.
func (s *Service) Prepare(ctx context.Context, id string) (Entry, error) {
	if !ValidID(id) {
		return Entry{}, &Error{Reason: ReasonFailed, Msg: "invalid video id"}
	}
	if !s.Available() {
		return Entry{}, &Error{Reason: ReasonToolMissing, Msg: s.missing()}
	}
	if e, ok := s.Lookup(id); ok {
		return e, nil
	}

	s.mu.Lock()
	j, running := s.inflight[id]
	if !running {
		j = &job{done: make(chan struct{})}
		s.inflight[id] = j
		go s.run(id, j)
	}
	s.mu.Unlock()

	select {
	case <-j.done:
		return j.entry, j.err
	case <-ctx.Done():
		// Both may be ready at once; prefer the answer over "not yet".
		select {
		case <-j.done:
			return j.entry, j.err
		default:
			return Entry{}, ErrInProgress
		}
	}
}

// missing names what Available() is missing, for the error message.
func (s *Service) missing() string {
	var parts []string
	if s.ytdlp == "" {
		parts = append(parts, "yt-dlp (YT_DLP_PATH)")
	}
	if s.ffmpeg == "" {
		parts = append(parts, "ffmpeg (FFMPEG_PATH)")
	}
	if s.deno == "" {
		parts = append(parts, "deno (DENO_PATH)")
	}
	if s.cacheErr != nil {
		parts = append(parts, "a writable cache directory (TRAILER_CACHE_DIR)")
	}
	return "trailer tools not installed on this companion: missing " + strings.Join(parts, ", ")
}

// run is the one goroutine per in-flight id. It owns the job: it fills
// in the result, drops the id from the in-flight table and only THEN
// closes done — so a waiter that wakes up sees a complete result, and a
// caller that arrives after the table entry is gone finds the file on
// disk (success) or starts a fresh attempt (failure).
func (s *Service) run(id string, j *job) {
	defer func() {
		if r := recover(); r != nil {
			j.entry, j.err = Entry{}, &Error{Reason: ReasonFailed, Msg: fmt.Sprintf("download panicked: %v", r)}
		}
		s.mu.Lock()
		delete(s.inflight, id)
		s.mu.Unlock()
		close(j.done)
	}()

	j.entry, j.err = s.download(id)
	if j.err != nil {
		s.logf("trailers: %s failed: %v", id, j.err)
		return
	}
	s.logf("trailers: cached %s %q (%d bytes, %d s)", id, j.entry.Title, j.entry.Bytes, j.entry.DurationSec)
}

// download runs yt-dlp for one id and promotes its output to <id>.mp4.
//
// The URL handed to yt-dlp is rebuilt from the validated id; the caller's
// string never reaches a command line. Output goes to <id>.tmp.mp4 and
// is renamed into place only once yt-dlp has exited cleanly AND the
// merged file exists, so a half-written trailer is never served, and a
// crash at any point leaves only .tmp files, which the next start sweeps.
func (s *Service) download(id string) (Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return Entry{}, &Error{Reason: ReasonTimeout, Msg: "waited too long for a download slot"}
	}
	defer func() { <-s.slots }()

	s.toolLock.RLock()
	defer s.toolLock.RUnlock()

	// A crash mid-merge can leave a truncated <id>.tmp.mp4 behind, and
	// --no-overwrites would make yt-dlp keep it. Start clean.
	s.removeTemp(id)

	args := []string{
		"--no-playlist",
		"--no-overwrites",
		// File mtime is the download time, never YouTube's upload date:
		// eviction is by mtime, and a freshly fetched old trailer must
		// not look like the stalest thing in the cache. (Current yt-dlp
		// defaults to this; older builds did not.)
		"--no-mtime",
		"--max-filesize", maxFileSize,
		"--js-runtimes", s.jsRuntime,
		"--ffmpeg-location", filepath.Dir(s.ffmpeg),
		// yt-dlp's own cache (player signatures, the JS challenge
		// components it fetches) goes on the data volume rather than
		// under $HOME, so it survives a container recreate.
		"--cache-dir", filepath.Join(s.cacheDir, ".yt-dlp"),
		"-f", formatSelector,
		"--merge-output-format", "mp4",
		"--print-json",
		"--no-progress",
		"-o", s.outputTemplate(id),
		"https://www.youtube.com/watch?v=" + id,
	}
	stdout, stderr, err := s.runner.Run(ctx, s.ytdlp, args...)
	if err != nil {
		s.removeTemp(id)
		return Entry{}, classify(ctx, err, stdout, stderr)
	}

	// Exit 0 is not enough: --max-filesize aborts the download and still
	// exits cleanly, with no merged file to show for it.
	tmp := s.tempPath(id)
	if fi, statErr := os.Stat(tmp); statErr != nil || fi.Size() == 0 {
		s.removeTemp(id)
		return Entry{}, &Error{Reason: ReasonFailed, Msg: "yt-dlp produced no file: " + lastLine(stderr, stdout)}
	}
	final := s.finalPath(id)
	if err := os.Rename(tmp, final); err != nil {
		s.removeTemp(id)
		return Entry{}, &Error{Reason: ReasonFailed, Msg: "could not move trailer into the cache", Err: err}
	}
	now := time.Now()
	_ = os.Chtimes(final, now, now)

	info := parseInfo(stdout)
	s.writeSidecar(id, info)
	s.removeTemp(id)
	s.prune(id)

	e, ok := s.Lookup(id)
	if !ok {
		return Entry{}, &Error{Reason: ReasonFailed, Msg: "trailer vanished after download"}
	}
	return e, nil
}

// ytInfo is the little we keep of yt-dlp's --print-json output.
type ytInfo struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Duration float64 `json:"duration"`
}

// parseInfo finds the info JSON in yt-dlp's stdout. --print-json makes
// yt-dlp quiet on stdout, but do not depend on that: take the first line
// that is a JSON object and ignore anything else.
func parseInfo(stdout []byte) ytInfo {
	for _, line := range bytes.Split(stdout, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var info ytInfo
		if json.Unmarshal(line, &info) == nil {
			return info
		}
	}
	return ytInfo{}
}

// unavailableMarkers are the phrases in yt-dlp's stderr that mean the
// VIDEO is the problem, not the tool. "Requested format is not
// available" is deliberately NOT matched by any of these: that one
// means yt-dlp itself is broken (usually the JS runtime), and should
// read as a failure to fix, not a video to give up on.
var unavailableMarkers = []string{
	"video unavailable",
	"private video",
	"has been removed",
	"this video is not available",
	"not available in your country",
	"account associated with this video has been terminated",
	"sign in to confirm your age",
	"age-restricted",
	"members-only",
	"join this channel",
	"premieres in",
	"this live event",
	"is not a valid url",
}

// classify maps a failed run to an Error the TV can branch on.
func classify(ctx context.Context, err error, stdout, stderr []byte) *Error {
	tail := lastLine(stderr, stdout)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &Error{Reason: ReasonTimeout, Msg: "yt-dlp did not finish in time", Err: err}
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return &Error{Reason: ReasonToolMissing, Msg: "yt-dlp could not be started", Err: err}
	}
	lower := strings.ToLower(string(stderr))
	for _, m := range unavailableMarkers {
		if strings.Contains(lower, m) {
			return &Error{Reason: ReasonUnavailable, Msg: "YouTube reports the video is unavailable: " + tail}
		}
	}
	return &Error{Reason: ReasonFailed, Msg: "yt-dlp failed: " + tail, Err: err}
}

// lastLine is the last non-empty line of the first non-empty output,
// trimmed of yt-dlp's "ERROR: " prefix and capped — it ends up in a JSON
// error body and a log line, not a dump.
func lastLine(outputs ...[]byte) string {
	for _, out := range outputs {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			l := strings.TrimSpace(lines[i])
			if l == "" {
				continue
			}
			l = strings.TrimPrefix(l, "ERROR: ")
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return "no output"
}

func (s *Service) readSidecar(id string) (sidecar, error) {
	var sc sidecar
	data, err := os.ReadFile(s.sidecarPath(id))
	if err != nil {
		return sc, err
	}
	return sc, json.Unmarshal(data, &sc)
}

// writeSidecar records the metadata next to the MP4. Written to a temp
// name and renamed, so a crash cannot leave a torn JSON file that reads
// as "no title" forever. Best effort: the MP4 is the product.
func (s *Service) writeSidecar(id string, info ytInfo) {
	sc := sidecar{
		ID:          id,
		Title:       info.Title,
		DurationSec: int(math.Round(info.Duration)),
		FetchedAt:   time.Now().UTC(),
	}
	data, err := json.Marshal(sc)
	if err != nil {
		return
	}
	tmp := s.sidecarPath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.sidecarPath(id))
}

// removeTemp deletes every <id>.tmp* file: the merged output, the
// per-format parts yt-dlp downloads first, and their .part files. Names
// are matched by prefix rather than glob so a cache path with glob
// characters in it cannot confuse the match.
func (s *Service) removeTemp(id string) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), id+".tmp") {
			_ = os.Remove(filepath.Join(s.cacheDir, e.Name()))
		}
	}
}

// sweepTemp removes every temp file in the cache, whatever id it was
// for. Final names are <id>.mp4 and <id>.json with an id that cannot
// contain a dot, so ".tmp" in a name means an abandoned download.
func (s *Service) sweepTemp() {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.Contains(e.Name(), ".tmp") {
			_ = os.Remove(filepath.Join(s.cacheDir, e.Name()))
		}
	}
}

// prune evicts trailers, least recently served first, until the cache is
// under its cap. keep is the id just downloaded: it is never evicted by
// the prune it triggered, or a cap smaller than one trailer would delete
// what the caller is about to be told is ready.
func (s *Service) prune(keep string) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return
	}
	type cached struct {
		id    string
		size  int64
		mtime time.Time
	}
	var files []cached
	var total int64
	for _, e := range entries {
		name := e.Name()
		id := strings.TrimSuffix(name, ".mp4")
		if id == name || !ValidID(id) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, cached{id: id, size: fi.Size(), mtime: fi.ModTime()})
		total += fi.Size()
	}
	if total <= s.capBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	for _, f := range files {
		if total <= s.capBytes {
			break
		}
		if f.id == keep {
			continue
		}
		if err := os.Remove(s.finalPath(f.id)); err != nil {
			continue
		}
		_ = os.Remove(s.sidecarPath(f.id))
		total -= f.size
		s.logf("trailers: evicted %s (%d MB) to stay under the %d MB cache cap",
			f.id, f.size/(1024*1024), s.capBytes/(1024*1024))
	}
}
