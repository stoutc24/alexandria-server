package showcase

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mark Watched / Mark Unwatched — the smallest stand-in for what the apps'
// feature (2026-11-11) sends, and for what they read next:
//
//	Plex      GET    /:/scrobble?key=<ratingKey>             played
//	          GET    /:/unscrobble?key=<ratingKey>           unplayed
//	Jellyfin  POST   /UserPlayedItems/{id}?userId=           played (10.9+)
//	          DELETE /UserPlayedItems/{id}?userId=           unplayed
//	          POST / DELETE /Users/{uid}/PlayedItems/{id}    the same, legacy
//
// for a film, an episode, a season or a whole show. A season or a show
// marks every episode under it — what the apps rely on from the real
// servers, and so what this stand-in does. A mark is kept on the Server, in
// memory, and everything the dialect serves next reads it: the item's own
// viewCount / UserData, a season's and a show's counts, and Continue
// Watching / On Deck / Next Up (an episode marked watched puts the next one
// up; a film marked either way leaves Continue Watching).
//
// Until a mark is made, every answer is byte-identical to before (the mock
// feeds the store screenshots). The routes themselves answered before too
// (Plex: 200, empty; Jellyfin's legacy route: a fixed "Played": true), and
// still do for anything that is not a film, a show, a season or an episode.
//
//	GET    /__showcase/watched              the readout: every mark request
//	                                        (method, path, RAW query, the
//	                                        item, what was asked, the status
//	                                        answered) and what is marked now
//	POST   /__showcase/watched?refuse=1|0   the routes answer 500 and change
//	                                        nothing (a server that refuses)
//	POST   /__showcase/watched?legacy=1|0   Jellyfin: /UserPlayedItems
//	                                        answers 404, as a server before
//	                                        10.9 does, so the legacy route
//	                                        is the one that works
//	POST   /__showcase/watched?ms=N         the routes take N ms to answer
//	                                        (a slow server: what an app
//	                                        shows meanwhile can be seen)
//	DELETE /__showcase/watched              forgets the marks and requests

// watchedRequest is one recorded mark request.
type watchedRequest struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Query   string `json:"query"`
	Item    string `json:"item"` // catalog id; "" when the key named nothing known
	Kind    string `json:"kind,omitempty"`
	Watched bool   `json:"watched"`
	Status  int    `json:"status"`
}

// watchedState is the Server's stand-in; guarded by Server.mu.
type watchedState struct {
	// A film's or an episode's catalog id → marked watched (true) or
	// unwatched (false). An id that is absent reads as the catalog has it.
	marks    map[string]bool
	requests []watchedRequest
	refuse   bool
	legacy   bool
	delay    time.Duration
}

func (s *Server) watchedMark(id string) (watched, marked bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	watched, marked = s.watched.marks[id]
	return
}

func (s *Server) hasWatchedMarks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.watched.marks) > 0
}

// movieProgress is a film's progress (0 unstarted, 1 finished): the mark
// when there is one, else what it always was.
func (s *Server) movieProgress(m *Movie) float64 {
	if watched, marked := s.watchedMark(m.ID); marked {
		if watched {
			return 1
		}
		return 0
	}
	return s.progressOf(m.ID, m.Progress)
}

// episodeWatched: the mark when there is one, else the catalog's.
func (s *Server) episodeWatched(e *Episode) bool {
	if watched, marked := s.watchedMark(e.ID); marked {
		return watched
	}
	return e.Watched
}

// episodeProgress: a marked episode has no resume point either way.
func (s *Server) episodeProgress(e *Episode) float64 {
	if watched, marked := s.watchedMark(e.ID); marked {
		if watched {
			return 1
		}
		return 0
	}
	return s.progressOf(e.ID, e.Progress)
}

// seasonWatched counts a season's watched episodes.
func (s *Server) seasonWatched(se *Season) int {
	n := 0
	for _, e := range se.Episodes {
		if s.episodeWatched(e) {
			n++
		}
	}
	return n
}

// showCounts is Show.Counts with the marks.
func (s *Server) showCounts(sh *Show) (watched, total int) {
	for _, e := range sh.AllEpisodes() {
		total++
		if s.episodeWatched(e) {
			watched++
		}
	}
	return
}

// showNextUp is the first unwatched episode of a show somebody has watched
// some of; nil for a show nobody started and for one watched to the end.
func (s *Server) showNextUp(sh *Show) *Episode {
	var first *Episode
	started := false
	for _, e := range sh.AllEpisodes() {
		if s.episodeWatched(e) {
			started = true
		} else if first == nil {
			first = e
		}
	}
	if !started {
		return nil
	}
	return first
}

// continueWatching is Catalog.ContinueWatching — exactly that until a mark
// is made, and worked out from the marks after.
func (s *Server) continueWatching() (movies []*Movie, episodes []*Episode) {
	if !s.hasWatchedMarks() {
		return s.cat.ContinueWatching()
	}
	for _, m := range s.cat.Movies {
		if p := s.movieProgress(m); p > 0 && p < 1 {
			movies = append(movies, m)
		}
	}
	sort.SliceStable(movies, func(i, j int) bool { return movies[i].LastPlay.After(movies[j].LastPlay) })
	for _, sh := range s.cat.Shows {
		if e := s.showNextUp(sh); e != nil {
			if p := s.episodeProgress(e); p > 0 && p < 1 {
				episodes = append(episodes, e)
			}
		}
	}
	sort.SliceStable(episodes, func(i, j int) bool { return episodes[i].lastPlay(s.cat).After(episodes[j].lastPlay(s.cat)) })
	return
}

// nextUp is Catalog.NextUp the same way: the next unwatched episode per
// show the viewer is into, not yet started.
func (s *Server) nextUp() []*Episode {
	if !s.hasWatchedMarks() {
		return s.cat.NextUp()
	}
	var out []*Episode
	for _, sh := range s.cat.Shows {
		if e := s.showNextUp(sh); e != nil && s.episodeProgress(e) == 0 {
			out = append(out, e)
		}
	}
	return out
}

// markWatched marks a film, an episode, or every episode of a season or a
// show. It answers what kind of thing `id` is ("" = none of those, nothing
// marked).
func (s *Server) markWatched(id string, watched bool) string {
	var leaves []string
	kind := ""
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		kind, leaves = "movie", []string{v.ID}
	case *Episode:
		kind, leaves = "episode", []string{v.ID}
	case *Season:
		kind = "season"
		for _, e := range v.Episodes {
			leaves = append(leaves, e.ID)
		}
	case *Show:
		kind = "show"
		for _, e := range v.AllEpisodes() {
			leaves = append(leaves, e.ID)
		}
	default:
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watched.marks == nil {
		s.watched.marks = map[string]bool{}
	}
	for _, leaf := range leaves {
		s.watched.marks[leaf] = watched
	}
	return kind
}

// watchedKind says what kind of video `id` is without marking anything.
func (s *Server) watchedKind(id string) string {
	switch s.cat.Get(id).(type) {
	case *Movie:
		return "movie"
	case *Episode:
		return "episode"
	case *Season:
		return "season"
	case *Show:
		return "show"
	}
	return ""
}

func (s *Server) recordWatched(r *http.Request, id, kind string, watched bool, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watched.requests = append(s.watched.requests, watchedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		Item: id, Kind: kind, Watched: watched, Status: status})
	if len(s.watched.requests) > 500 {
		s.watched.requests = s.watched.requests[len(s.watched.requests)-500:]
	}
}

// watchedSwitches reads the probe's switches, after the answer's delay.
func (s *Server) watchedSwitches() (refuse, legacy bool) {
	s.mu.Lock()
	delay := s.watched.delay
	s.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watched.refuse, s.watched.legacy
}

// plexWatchedWrite: GET /:/scrobble | /:/unscrobble ?key=<ratingKey>. 200
// with an empty body, as before; now the mark is kept.
func (s *Server) plexWatchedWrite(w http.ResponseWriter, r *http.Request, watched bool) {
	id := s.cat.IDForPlexKey(qStr(r, "key"))
	if refuse, _ := s.watchedSwitches(); refuse {
		s.recordWatched(r, id, s.watchedKind(id), watched, http.StatusInternalServerError)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	s.recordWatched(r, id, s.markWatched(id, watched), watched, http.StatusOK)
	w.WriteHeader(http.StatusOK)
}

// jfWatchedWrite handles POST / DELETE on a played-items route for a film,
// an episode, a season or a series; false = not one of those (or another
// method), and the caller answers as it always did. `modern` is the 10.9+
// /UserPlayedItems route, which a "legacy" server does not have.
func (s *Server) jfWatchedWrite(w http.ResponseWriter, r *http.Request, itemID string, modern bool) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		return false
	}
	id := s.cat.IDForJF(itemID)
	kind := s.watchedKind(id)
	if kind == "" {
		return false
	}
	watched := r.Method == http.MethodPost
	refuse, legacy := s.watchedSwitches()
	switch {
	case modern && legacy:
		s.recordWatched(r, id, kind, watched, http.StatusNotFound)
		notFound(w)
	case refuse:
		s.recordWatched(r, id, kind, watched, http.StatusInternalServerError)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "refused"})
	default:
		s.markWatched(id, watched)
		s.recordWatched(r, id, kind, watched, http.StatusOK)
		plays := 0
		if watched {
			plays = 1
		}
		ok(w, map[string]any{"PlaybackPositionTicks": 0, "PlayCount": plays, "IsFavorite": false, "Played": watched, "Key": s.cat.JFID(id)})
	}
	return true
}

// jfWatchedRoute finds /UserPlayedItems/{id} anywhere in the path (the demo
// serves under a prefix) and handles it for a video; false = not that.
func (s *Server) jfWatchedRoute(w http.ResponseWriter, r *http.Request, p []string) bool {
	for i, seg := range p {
		if strings.EqualFold(seg, "UserPlayedItems") && i+1 < len(p) {
			return s.jfWatchedWrite(w, r, p[i+1], true)
		}
	}
	return false
}

// watchedControl: /__showcase/watched on either dialect's port.
func (s *Server) watchedControl(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	switch r.Method {
	case http.MethodPost:
		if v := qStr(r, "refuse"); v != "" {
			s.watched.refuse = v == "1"
		}
		if v := qStr(r, "legacy"); v != "" {
			s.watched.legacy = v == "1"
		}
		if n, err := strconv.Atoi(qStr(r, "ms")); err == nil && n >= 0 {
			s.watched.delay = time.Duration(n) * time.Millisecond
		}
	case http.MethodDelete:
		s.watched.marks = nil
		s.watched.requests = nil
	}
	requests := append([]watchedRequest{}, s.watched.requests...)
	marked := map[string]bool{}
	for id, watched := range s.watched.marks {
		marked[id] = watched
	}
	refuse, legacy, delay := s.watched.refuse, s.watched.legacy, s.watched.delay
	s.mu.Unlock()
	ok(w, map[string]any{"requests": requests, "marked": marked, "refuse": refuse, "legacy": legacy, "ms": delay.Milliseconds()})
}
