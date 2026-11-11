package showcase

import (
	"net/http"
	"strconv"
	"time"
)

// Plex "Scan Library Files" — the smallest stand-in for what the apps'
// admin feature reads (2026-11-10), after the owner's server:
//
//	GET /:/prefs                          200 for the owner (as this mock
//	                                      always answered), 401 otherwise
//	GET /library/sections/{key}/refresh   200, empty body; the section then
//	                                      reads "refreshing": true for the
//	                                      scan's length, and its scannedAt
//	                                      is the moment the scan ended
//
// Until a scan is asked for, /library/sections is byte-identical to before
// (the mock feeds the store screenshots).
//
//	GET    /__showcase/scan            the readout: owner, the scan's length,
//	                                   the sections scanning, every refresh
//	                                   request (section, method, RAW query —
//	                                   so a probe can see there was no
//	                                   force=1), and how often each section's
//	                                   /all was read
//	POST   /__showcase/scan?owner=0|1  whoever asks is, or is not, the owner
//	POST   /__showcase/scan?ms=N       how long a scan takes
//	DELETE /__showcase/scan            forgets the requests and the counts

// plexScanDefault is a scan's length unless SHOWCASE_PLEX_SCAN_MS or a
// probe says otherwise.
const plexScanDefault = 2 * time.Second

// scanRequest is one recorded refresh request.
type scanRequest struct {
	Section string `json:"section"`
	Method  string `json:"method"`
	Query   string `json:"query"`
	Status  int    `json:"status"`
}

// plexScanState is the Server's scan stand-in; guarded by Server.mu.
type plexScanState struct {
	notOwner bool
	length   time.Duration
	ends     map[string]time.Time // section → when its scan ends
	scanned  map[string]int64     // section → scannedAt, once a scan has ended
	requests []scanRequest
	allReads map[string]int
}

// settle moves every scan whose time is up to "scanned". Call with mu held.
func (st *plexScanState) settle(now time.Time) {
	for key, end := range st.ends {
		if !now.Before(end) {
			if st.scanned == nil {
				st.scanned = map[string]int64{}
			}
			st.scanned[key] = end.Unix()
			delete(st.ends, key)
		}
	}
}

func (s *Server) plexScanLength() time.Duration {
	if s.scan.length > 0 {
		return s.scan.length
	}
	if d := envMillis("SHOWCASE_PLEX_SCAN_MS"); d > 0 {
		return d
	}
	return plexScanDefault
}

// plexScanSection is what /library/sections says about one section's scan:
// whether it is running, and its scannedAt (def until a scan has ended).
func (s *Server) plexScanSection(key string, def int64) (refreshing bool, scannedAt int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scan.settle(time.Now())
	_, refreshing = s.scan.ends[key]
	if at, done := s.scan.scanned[key]; done {
		return refreshing, at
	}
	return refreshing, def
}

// plexIsOwner reads the owner switch (a probe may flip it mid-run).
func (s *Server) plexIsOwner() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.scan.notOwner
}

// plexScanStart answers /library/sections/{key}/refresh.
func (s *Server) plexScanStart(w http.ResponseWriter, r *http.Request, key string) {
	s.mu.Lock()
	status := http.StatusOK
	if s.scan.notOwner {
		status = http.StatusUnauthorized
	} else {
		if s.scan.ends == nil {
			s.scan.ends = map[string]time.Time{}
		}
		s.scan.ends[key] = time.Now().Add(s.plexScanLength())
	}
	s.scan.requests = append(s.scan.requests, scanRequest{Section: key, Method: r.Method, Query: r.URL.RawQuery, Status: status})
	s.mu.Unlock()
	// An empty body, as the real server answers.
	w.WriteHeader(status)
}

// plexCountAll notes one read of a section's /all (a library reload).
func (s *Server) plexCountAll(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scan.allReads == nil {
		s.scan.allReads = map[string]int{}
	}
	s.scan.allReads[key]++
}

// plexScanControl answers /__showcase/scan.
func (s *Server) plexScanControl(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	switch r.Method {
	case http.MethodPost:
		if v := qStr(r, "owner"); v != "" {
			s.scan.notOwner = v == "0"
		}
		if n, err := strconv.Atoi(qStr(r, "ms")); err == nil && n > 0 {
			s.scan.length = time.Duration(n) * time.Millisecond
		}
	case http.MethodDelete:
		s.scan.requests = nil
		s.scan.allReads = nil
	}
	s.scan.settle(time.Now())
	scanning := []string{}
	for key := range s.scan.ends {
		scanning = append(scanning, key)
	}
	requests := append([]scanRequest{}, s.scan.requests...)
	reads := map[string]int{}
	for key, n := range s.scan.allReads {
		reads[key] = n
	}
	out := map[string]any{"owner": !s.scan.notOwner, "ms": s.plexScanLength().Milliseconds(),
		"scanning": scanning, "requests": requests, "all": reads}
	s.mu.Unlock()
	ok(w, out)
}
