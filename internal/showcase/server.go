package showcase

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server hosts the three dialects over one catalog. Each dialect is an
// http.Handler the command binds to its own port (the servers' default
// ports, so the apps' placeholders already match): Audiobookshelf 13378,
// Jellyfin 8096, Plex 32400.
type Server struct {
	cat      *Catalog
	clipsDir string
	covers   sync.Map // cache key → []byte (JPEG)
	// Accepts any credentials; this is a stage set, not a server.
	Token string
	// Mutable "state" the apps write back: progress by catalog id, so a
	// screenshot session that plays something sees it move. In memory.
	mu       sync.Mutex
	progress map[string]float64
	// Plex transcode session id → clip and running time, so the playlist
	// and segment requests that follow a start.m3u8 know what to serve.
	sessions map[string]playSession
	log      *log.Logger
	// LossyMusic labels the Plex music tracks AAC instead of PCM (the bytes
	// served stay WAV). The phone app streams lossless tracks through
	// libmpv, which cannot open an audio device in the iOS simulator, so
	// they never start there; labelled lossy they go to AVPlayer, which
	// plays the same WAV. Off unless SHOWCASE_LOSSY_MUSIC=1 is set.
	LossyMusic bool
	// Lyrics gives every Plex music track a timed lyric stream (streamType
	// 4, LRC at /library/streams/:id): made-up lines, a few of them very
	// long, every four seconds — for the lyrics panels. Off unless
	// SHOWCASE_LYRICS=1 is set, so the store screenshots are unchanged.
	Lyrics bool
	// LyricsDown answers every lyric stream with Plex's 404 page while the
	// tracks still list their lyric streams — what the owner's Plex answered
	// on and off on 2026-10-30. Off unless SHOWCASE_LYRICS_DOWN=1 is set or
	// a probe POSTs /__showcase/lyrics?down=1 (read through lyricsDown).
	// GET /__showcase/lyrics counts the lyric requests, so a probe can see a
	// retry, or a saved copy that needed none.
	LyricsDown    bool
	lyricRequests int
	// LRCLib serves a stand-in for lrclib.net's /api/get and /api/search on
	// the Plex port (made-up timed lyrics for any song), counted in the
	// /__showcase/lyrics readout. Off unless SHOWCASE_LRCLIB=1 is set.
	LRCLib         bool
	lrclibRequests int
	// Books makes the Jellyfin dialect also serve the catalog's audiobooks
	// as a "books" view (AudioBook items with Chapters, one WAV each — the
	// Audiobookshelf dialect's books, in Jellyfin's shape), plus the
	// UserItems / UserPlayedItems write routes and a reports readout at
	// GET /__showcase/reports for a probe to check what was posted. Off
	// unless SHOWCASE_JELLYFIN_BOOKS=1 is set: the default output is
	// byte-identical (the mock feeds the store screenshots).
	Books bool
	// The playback reports the apps POST while Books is on, newest last.
	reports []reportRecord
	// RefusePlainParts answers a Plex song's file without `download=1`
	// with HTTP 500, as the owner's Plex did in June 2026 for a 24-bit
	// FLAC — for the phone's test that it falls back to the flag. Off
	// unless SHOWCASE_PLEX_REFUSE_PLAIN=1 is set or a probe POSTs
	// /__showcase/parts?refusePlain=1 (read through refusesPlain).
	RefusePlainParts bool
	// RefuseJellyfinDownload answers Jellyfin's /Items/{id}/Download with
	// 403, as Jellyfin does for a user without the download permission —
	// for the phone's test that a lossless Jellyfin song plays without it.
	// Off unless SHOWCASE_JELLYFIN_REFUSE_DOWNLOAD=1 is set or a probe
	// POSTs /__showcase/parts?refuseDownload=1 on the Jellyfin port.
	RefuseJellyfinDownload bool
	// ClipLengthVideo serves every film's and episode's HLS playlist at its
	// clip's own length instead of stretched to the running time, so an
	// episode ENDS within seconds — for the TV's probe that Play All runs a
	// whole show through (owner, 2026-10-28). Off unless
	// SHOWCASE_CLIP_LENGTH_VIDEO=1 is set.
	ClipLengthVideo bool
	// AllLeavesDelay holds a show's /allLeaves answer back, as a big show on
	// a real Plex does, so the TV's next-episode lookup lands after playback
	// has started — for the TV's probe of the player's tab row (owner,
	// 2026-10-29). Zero unless SHOWCASE_PLEX_ALLLEAVES_DELAY_MS is set.
	AllLeavesDelay time.Duration
	// Every Plex or Jellyfin song-file request this Server answered, newest
	// last, for a probe to check whether the app asked for a download (which
	// a real Plex lists as a "Media download" and a real Jellyfin logs as "is
	// downloading"): GET /__showcase/parts on that dialect's port, DELETE
	// clears. The command runs each dialect as its own Server.
	partRequests []partRequest
}

// partRequest is one recorded song-file request: a Plex part, or a
// Jellyfin song by route ("stream", "universal" or "Download").
type partRequest struct {
	Server   string `json:"server"`
	Part     int    `json:"part,omitempty"`
	Item     string `json:"item,omitempty"`
	Route    string `json:"route,omitempty"`
	Download bool   `json:"download"`
	Status   int    `json:"status"`
	At       string `json:"at"`
}

// recordPart keeps a song-file request for GET /__showcase/parts (bounded).
func (s *Server) recordPart(rec partRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.At = time.Now().UTC().Format(time.RFC3339Nano)
	s.partRequests = append(s.partRequests, rec)
	if len(s.partRequests) > 500 {
		s.partRequests = s.partRequests[len(s.partRequests)-500:]
	}
}

// lyricsDown reads LyricsDown (a probe may switch it mid-run).
func (s *Server) lyricsDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LyricsDown
}

// refusesPlain reads RefusePlainParts (a probe may switch it mid-run).
func (s *Server) refusesPlain() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RefusePlainParts
}

// refusesDownload reads RefuseJellyfinDownload (a probe may switch it).
func (s *Server) refusesDownload() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.RefuseJellyfinDownload
}

// recordedParts returns a copy of the song-file requests; clear empties them.
func (s *Server) recordedParts(clear bool) []partRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]partRequest(nil), s.partRequests...)
	if clear {
		s.partRequests = nil
	}
	if out == nil {
		out = []partRequest{}
	}
	return out
}

// reportRecord is one recorded POST (Books on): the route, and the fields
// a probe asserts on.
type reportRecord struct {
	Route         string `json:"route"`
	ItemID        string `json:"itemId"`
	PlaySessionID string `json:"playSessionId,omitempty"`
	PositionTicks int64  `json:"positionTicks"`
	IsPaused      bool   `json:"isPaused"`
	At            string `json:"at"`
}

// record keeps a POST's facts for GET /__showcase/reports (bounded).
func (s *Server) record(rec reportRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.At = time.Now().UTC().Format(time.RFC3339Nano)
	s.reports = append(s.reports, rec)
	if len(s.reports) > 500 {
		s.reports = s.reports[len(s.reports)-500:]
	}
}

// recorded returns a copy of the reports; clear empties them.
func (s *Server) recorded(clear bool) []reportRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]reportRecord(nil), s.reports...)
	if clear {
		s.reports = nil
	}
	if out == nil {
		out = []reportRecord{}
	}
	return out
}

// plexTrackCodec is the audioCodec the Plex dialect reports for music.
func (s *Server) plexTrackCodec() string {
	if s.LossyMusic {
		return "aac"
	}
	return "pcm"
}

func New(cat *Catalog, clipsDir string) *Server {
	return &Server{cat: cat, clipsDir: clipsDir, Token: "showcase-token", progress: map[string]float64{},
		log: log.New(os.Stdout, "", log.Ltime), LossyMusic: os.Getenv("SHOWCASE_LOSSY_MUSIC") == "1",
		Lyrics:                 os.Getenv("SHOWCASE_LYRICS") == "1",
		LyricsDown:             os.Getenv("SHOWCASE_LYRICS_DOWN") == "1",
		LRCLib:                 os.Getenv("SHOWCASE_LRCLIB") == "1",
		Books:                  os.Getenv("SHOWCASE_JELLYFIN_BOOKS") == "1",
		RefusePlainParts:       os.Getenv("SHOWCASE_PLEX_REFUSE_PLAIN") == "1",
		RefuseJellyfinDownload: os.Getenv("SHOWCASE_JELLYFIN_REFUSE_DOWNLOAD") == "1",
		ClipLengthVideo:        os.Getenv("SHOWCASE_CLIP_LENGTH_VIDEO") == "1",
		AllLeavesDelay:         envMillis("SHOWCASE_PLEX_ALLLEAVES_DELAY_MS")}
}

// envMillis reads a whole number of milliseconds from the environment; zero
// when it is unset or not a number.
func envMillis(name string) time.Duration {
	n, err := strconv.Atoi(os.Getenv(name))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}

// ---- JSON / HTTP helpers ---------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func ok(w http.ResponseWriter, v any) { writeJSON(w, http.StatusOK, v) }

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
}

func qInt(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func qStr(r *http.Request, name string) string { return r.URL.Query().Get(name) }

// pathParts splits a URL path into non-empty segments.
func pathParts(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// logging wraps a handler with one line per request.
func (s *Server) logging(name string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		h.ServeHTTP(rec, r)
		q := ""
		if r.URL.RawQuery != "" {
			q = "?" + truncate(r.URL.RawQuery, 80)
		}
		s.log.Printf("%-8s %s %s%s → %d (%s)", name, r.Method, r.URL.Path, q, rec.status, time.Since(started).Round(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---- progress state --------------------------------------------------------

func (s *Server) progressOf(id string, fallback float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.progress[id]; ok {
		return p
	}
	return fallback
}

func (s *Server) setProgress(id string, p float64) {
	s.mu.Lock()
	s.progress[id] = p
	s.mu.Unlock()
}

// ---- artwork ---------------------------------------------------------------

type artKind int

const (
	artPoster   artKind = iota // 2:3
	artSquare                  // 1:1
	artWide                    // 16:9, titled (episode stills)
	artBackdrop                // 16:9, no type (backdrops: the apps set the title over them)
)

func (s *Server) art(id string, kind artKind, width int) []byte {
	key := fmt.Sprintf("%s|%d|%d", id, kind, width)
	if v, ok := s.covers.Load(key); ok {
		return v.([]byte)
	}
	// People (cast, directors) get a portrait whatever shape was asked for:
	// the apps crop it to a circle.
	if name, isPerson := strings.CutPrefix(id, personPrefix); isPerson {
		size := 400
		if width > 0 && width < size {
			size = width
		}
		data, err := Portrait(name, size)
		if err != nil {
			return nil
		}
		s.covers.Store(key, data)
		return data
	}
	spec := s.coverSpec(id)
	switch kind {
	case artPoster:
		spec.Width, spec.Height = 600, 900
	case artSquare:
		spec.Width, spec.Height = 800, 800
	case artWide:
		spec.Width, spec.Height = 1280, 720
	case artBackdrop:
		spec.Width, spec.Height = 1280, 720
		spec.Bare = true
	}
	if width > 0 && width < spec.Width {
		spec.Height = spec.Height * width / spec.Width
		spec.Width = width
	}
	data, err := Cover(spec)
	if err != nil {
		return nil
	}
	s.covers.Store(key, data)
	return data
}

// coverSpec: what each kind of entity shows on its art.
func (s *Server) coverSpec(id string) CoverSpec {
	switch v := s.cat.Get(id).(type) {
	case *Book:
		kind := "book"
		if v.Ebook {
			kind = "ebook"
		}
		return CoverSpec{Title: v.Title, Subtitle: v.Author.Name, Kind: kind}
	case *Album:
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%s · %d", v.Artist.Name, v.Year), Kind: "album"}
	case *Artist:
		return CoverSpec{Title: v.Name, Subtitle: v.Genre, Kind: ""}
	case *Track:
		return CoverSpec{Title: v.Album.Title, Subtitle: v.Album.Artist.Name, Kind: "album"}
	case *Movie:
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%d · %s", v.Year, strings.Join(v.Genres, ", ")), Kind: "film"}
	case *Show:
		seasons := fmt.Sprintf("%d seasons", len(v.Seasons))
		if len(v.Seasons) == 1 {
			seasons = "1 season"
		}
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%d · %s", v.Year, seasons), Kind: "show"}
	case *Season:
		return CoverSpec{Title: v.Show.Title, Subtitle: fmt.Sprintf("Season %d", v.Index), Kind: "show"}
	case *Episode:
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%s · S%d E%d", v.Show.Title, v.Season.Index, v.Index), Kind: "show"}
	case *Collection:
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%d titles", len(v.Movies)+len(v.Shows)), Kind: ""}
	case *Playlist:
		return CoverSpec{Title: v.Title, Subtitle: "Playlist", Kind: ""}
	case *Author:
		return CoverSpec{Title: v.Name, Subtitle: "Author", Kind: ""}
	case *Series:
		return CoverSpec{Title: v.Name, Subtitle: fmt.Sprintf("%d books", len(v.Books)), Kind: "book"}
	}
	return CoverSpec{Title: "Alexandria", Subtitle: "Showcase", Kind: ""}
}

func serveJPEG(w http.ResponseWriter, data []byte) {
	if data == nil {
		http.Error(w, "no art", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ---- audio -----------------------------------------------------------------

// serveWAV answers a (possibly ranged) request for a synthetic WAV.
func serveWAV(w http.ResponseWriter, r *http.Request, seed uint32, durationSec float64) {
	total := WAVSize(durationSec)
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Accept-Ranges", "bytes")
	from, to := int64(0), total
	if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
		spec := strings.TrimPrefix(rng, "bytes=")
		if i := strings.Index(spec, ","); i >= 0 {
			spec = spec[:i]
		}
		parts := strings.SplitN(spec, "-", 2)
		if len(parts) == 2 {
			if parts[0] == "" {
				// suffix range: last N bytes
				if n, err := strconv.ParseInt(parts[1], 10, 64); err == nil && n > 0 {
					from = total - n
					if from < 0 {
						from = 0
					}
				}
			} else {
				if a, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
					from = a
				}
				if parts[1] != "" {
					if b, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						to = b + 1
					}
				}
			}
		}
		if from >= total {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if to > total {
			to = total
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to-1, total))
		w.Header().Set("Content-Length", strconv.FormatInt(to-from, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}
	_ = WriteWAVRange(w, seed, durationSec, from, to)
}

// ---- video -----------------------------------------------------------------

// clipPath resolves a file inside a clip directory; "" when absent.
func (s *Server) clipPath(clip, file string) string {
	if s.clipsDir == "" {
		return ""
	}
	p := filepath.Join(s.clipsDir, clip, file)
	if _, err := os.Stat(p); err != nil {
		// Fall back to the first clip that has the file.
		entries, _ := os.ReadDir(s.clipsDir)
		for _, e := range entries {
			if e.IsDir() {
				q := filepath.Join(s.clipsDir, e.Name(), file)
				if _, err := os.Stat(q); err == nil {
					return q
				}
			}
		}
		return ""
	}
	return p
}

// serveClipFile serves clip.mp4 / init.mp4 / segN.m4s / index.m3u8 with Range.
func (s *Server) serveClipFile(w http.ResponseWriter, r *http.Request, clip, file string) {
	p := s.clipPath(clip, file)
	if p == "" {
		http.Error(w, "no clip", http.StatusNotFound)
		return
	}
	switch {
	case strings.HasSuffix(file, ".m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	case strings.HasSuffix(file, ".m4s"), strings.HasSuffix(file, ".mp4"):
		w.Header().Set("Content-Type", "video/mp4")
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, p)
}

// serveRuntimePlaylist answers a clip's HLS media playlist stretched to an
// item's running time (stretchPlaylist). AVPlayer reads an item's duration
// from its playlist, so the player shows the film's or episode's real
// length instead of the clip's 45 s, and a resume point lands inside the
// stream. Nothing extra is stored: the same segment files repeat.
func (s *Server) serveRuntimePlaylist(w http.ResponseWriter, r *http.Request, clip string, seconds float64) {
	p := s.clipPath(clip, "index.m3u8")
	if p == "" {
		http.Error(w, "no clip", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, "no clip", http.StatusNotFound)
		return
	}
	text, stretched := stretchPlaylist(string(data), seconds)
	if !stretched || s.ClipLengthVideo {
		s.serveClipFile(w, r, clip, "index.m3u8")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, text)
}

// stretchPlaylist repeats a VOD media playlist's segments until it lasts at
// least `seconds`, ending on a segment boundary. Every pass after the first
// opens with EXT-X-DISCONTINUITY (the segments' timestamps start again at
// zero) and repeats the EXT-X-MAP. false when the playlist is already that
// long or cannot be read, so the caller serves the file as it is.
func stretchPlaylist(src string, seconds float64) (string, bool) {
	type segment struct {
		dur float64
		uri string
	}
	var segs []segment
	mapLine := ""
	pending := -1.0
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			mapLine = line
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.Index(v, ","); i >= 0 {
				v = v[:i]
			}
			d, err := strconv.ParseFloat(v, 64)
			if err != nil || d <= 0 {
				return "", false
			}
			pending = d
		case line != "" && !strings.HasPrefix(line, "#"):
			if pending < 0 {
				return "", false
			}
			segs = append(segs, segment{pending, line})
			pending = -1
		}
	}
	once, longest := 0.0, 0.0
	for _, sg := range segs {
		once += sg.dur
		longest = math.Max(longest, sg.dur)
	}
	if len(segs) == 0 || seconds <= once {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n", int(math.Ceil(longest)))
	total := 0.0
	for pass := 0; total < seconds; pass++ {
		if pass > 0 {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		if mapLine != "" {
			b.WriteString(mapLine + "\n")
		}
		for _, sg := range segs {
			if total >= seconds {
				break
			}
			fmt.Fprintf(&b, "#EXTINF:%.3f,\n%s\n", sg.dur, sg.uri)
			total += sg.dur
		}
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String(), true
}

// ClipIDs lists the clip directories available.
func ClipIDs(dir string) []string {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "clip.mp4")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

// clipSize is the progressive file's size in bytes (for Size fields).
func (s *Server) clipSize(clip string) int64 {
	p := s.clipPath(clip, "clip.mp4")
	if p == "" {
		return 0
	}
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// readBody drains a request body into a map (lenient).
func readBody(r *http.Request) map[string]any {
	defer r.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func ms(t time.Time) int64 { return t.UnixMilli() }
