package showcase

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	// Plex transcode session id → clip, so the segment requests that follow
	// a start.m3u8 know which clip to serve.
	sessions map[string]string
	log      *log.Logger
}

func New(cat *Catalog, clipsDir string) *Server {
	return &Server{cat: cat, clipsDir: clipsDir, Token: "showcase-token", progress: map[string]float64{},
		log: log.New(os.Stdout, "", log.Ltime)}
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
	artPoster artKind = iota // 2:3
	artSquare                // 1:1
	artWide                  // 16:9
)

func (s *Server) art(id string, kind artKind, width int) []byte {
	key := fmt.Sprintf("%s|%d|%d", id, kind, width)
	if v, ok := s.covers.Load(key); ok {
		return v.([]byte)
	}
	spec := s.coverSpec(id)
	switch kind {
	case artPoster:
		spec.Width, spec.Height = 600, 900
	case artSquare:
		spec.Width, spec.Height = 800, 800
	case artWide:
		spec.Width, spec.Height = 1280, 720
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
		return CoverSpec{Title: v.Title, Subtitle: fmt.Sprintf("%d · %d seasons", v.Year, len(v.Seasons)), Kind: "show"}
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
