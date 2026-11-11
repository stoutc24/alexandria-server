package showcase

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type scanReadout struct {
	Owner    bool           `json:"owner"`
	Ms       int64          `json:"ms"`
	Scanning []string       `json:"scanning"`
	Requests []scanRequest  `json:"requests"`
	All      map[string]int `json:"all"`
}

func scanSections(t *testing.T, srv *httptest.Server) map[string]struct {
	Refreshing bool  `json:"refreshing"`
	ScannedAt  int64 `json:"scannedAt"`
} {
	t.Helper()
	code, data := jfGet(t, srv, "/library/sections")
	if code != http.StatusOK {
		t.Fatalf("/library/sections answered %d", code)
	}
	var body struct {
		MediaContainer struct {
			Directory []struct {
				Key        string `json:"key"`
				Refreshing bool   `json:"refreshing"`
				ScannedAt  int64  `json:"scannedAt"`
			}
		}
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("/library/sections: %v", err)
	}
	out := map[string]struct {
		Refreshing bool  `json:"refreshing"`
		ScannedAt  int64 `json:"scannedAt"`
	}{}
	for _, d := range body.MediaContainer.Directory {
		out[d.Key] = struct {
			Refreshing bool  `json:"refreshing"`
			ScannedAt  int64 `json:"scannedAt"`
		}{d.Refreshing, d.ScannedAt}
	}
	return out
}

func scanReadoutOf(t *testing.T, srv *httptest.Server) scanReadout {
	t.Helper()
	_, data := jfGet(t, srv, "/__showcase/scan")
	var out scanReadout
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("scan readout: %v", err)
	}
	return out
}

// Scan Library Files: the owner's /:/prefs is 200, a refresh answers 200
// with an empty body, the section reads refreshing for the scan's length
// and then carries a newer scannedAt — and nothing changes until a scan
// is asked for.
func TestPlexScanLibraryFiles(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cat := Build(now, nil).Half(0)
	s := New(cat, "")
	srv := httptest.NewServer(s.PlexHandler())
	defer srv.Close()
	yesterday := cat.daysAgo(1).Unix()

	// Before any scan: exactly what the mock always said.
	for key, sec := range scanSections(t, srv) {
		if sec.Refreshing || sec.ScannedAt != yesterday {
			t.Errorf("section %s before a scan: refreshing=%v scannedAt=%d, want false / %d", key, sec.Refreshing, sec.ScannedAt, yesterday)
		}
	}
	if code, _ := jfGet(t, srv, "/:/prefs"); code != http.StatusOK {
		t.Errorf("/:/prefs for the owner answered %d, want 200", code)
	}

	if resp, err := http.Post(srv.URL+"/__showcase/scan?ms=300", "", nil); err == nil {
		resp.Body.Close()
	}
	code, body := jfGet(t, srv, "/library/sections/"+plexShows+"/refresh")
	if code != http.StatusOK || len(body) != 0 {
		t.Fatalf("refresh answered %d with %d bytes, want 200 and an empty body", code, len(body))
	}
	secs := scanSections(t, srv)
	if !secs[plexShows].Refreshing {
		t.Errorf("the scanned section does not read refreshing")
	}
	if secs[plexMovies].Refreshing || secs[plexMusic].Refreshing {
		t.Errorf("another section reads refreshing: %+v", secs)
	}
	if secs[plexShows].ScannedAt != yesterday {
		t.Errorf("scannedAt moved before the scan ended")
	}
	if got := scanReadoutOf(t, srv); len(got.Scanning) != 1 || got.Scanning[0] != plexShows || got.Ms != 300 {
		t.Errorf("readout while scanning: %+v", got)
	}

	time.Sleep(450 * time.Millisecond)
	secs = scanSections(t, srv)
	if secs[plexShows].Refreshing {
		t.Errorf("still refreshing after the scan's length")
	}
	if secs[plexShows].ScannedAt <= yesterday {
		t.Errorf("scannedAt did not move: %d", secs[plexShows].ScannedAt)
	}
	if secs[plexMovies].ScannedAt != yesterday {
		t.Errorf("a section that was not scanned moved its scannedAt")
	}

	// The readout: the request as it came (its raw query too), and /all reads.
	jfGet(t, srv, "/library/sections/"+plexMovies+"/refresh?force=1")
	jfGet(t, srv, "/library/sections/"+plexMovies+"/all?type=1")
	jfGet(t, srv, "/library/sections/"+plexMovies+"/all?type=1")
	got := scanReadoutOf(t, srv)
	want := []scanRequest{
		{Section: plexShows, Method: "GET", Query: "", Status: 200},
		{Section: plexMovies, Method: "GET", Query: "force=1", Status: 200},
	}
	if len(got.Requests) != len(want) || got.Requests[0] != want[0] || got.Requests[1] != want[1] {
		t.Errorf("requests %+v, want %+v", got.Requests, want)
	}
	if got.All[plexMovies] != 2 || !got.Owner {
		t.Errorf("readout %+v", got)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/__showcase/scan", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	if got := scanReadoutOf(t, srv); len(got.Requests) != 0 || len(got.All) != 0 {
		t.Errorf("readout not cleared: %+v", got)
	}
}

// Somebody who is not the owner: /:/prefs and the refresh answer 401 and
// nothing scans.
func TestPlexScanNotTheOwner(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(0)
	srv := httptest.NewServer(New(cat, "").PlexHandler())
	defer srv.Close()

	if resp, err := http.Post(srv.URL+"/__showcase/scan?owner=0", "", nil); err == nil {
		resp.Body.Close()
	}
	if code, _ := jfGet(t, srv, "/:/prefs"); code != http.StatusUnauthorized {
		t.Errorf("/:/prefs answered %d, want 401", code)
	}
	if code, _ := jfGet(t, srv, "/library/sections/"+plexMovies+"/refresh"); code != http.StatusUnauthorized {
		t.Errorf("refresh answered %d, want 401", code)
	}
	if secs := scanSections(t, srv); secs[plexMovies].Refreshing {
		t.Errorf("a refused scan reads refreshing")
	}
	got := scanReadoutOf(t, srv)
	if got.Owner || len(got.Scanning) != 0 || len(got.Requests) != 1 || got.Requests[0].Status != http.StatusUnauthorized {
		t.Errorf("readout %+v", got)
	}
	// The timeline routes beside /:/prefs are untouched.
	if code, _ := jfGet(t, srv, "/:/timeline"); code != http.StatusOK {
		t.Errorf("/:/timeline answered %d", code)
	}

	// And the owner again.
	if resp, err := http.Post(srv.URL+"/__showcase/scan?owner=1", "", nil); err == nil {
		resp.Body.Close()
	}
	if code, _ := jfGet(t, srv, "/:/prefs"); code != http.StatusOK {
		t.Errorf("/:/prefs answered %d for the owner again", code)
	}
}
