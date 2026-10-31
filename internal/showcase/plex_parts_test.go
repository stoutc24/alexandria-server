package showcase

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The Plex song-file switches: by default a song plays with or without
// Plex's download flag and every request is listed at /__showcase/parts;
// with RefusePlainParts the plain address answers 500 (the owner's Plex in
// June 2026) while the flagged one still plays.
func TestPlexPartsRefusePlainAndReadout(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	if len(cat.Tracks) == 0 {
		t.Fatal("no tracks in the catalog")
	}
	part := cat.plexKey[cat.Tracks[0].ID]*10 + 1
	plain := fmt.Sprintf("/library/parts/%d/file.wav", part)
	flagged := plain + "?download=1"

	readout := func(t *testing.T, srv *httptest.Server) []partRequest {
		t.Helper()
		_, data := jfGet(t, srv, "/__showcase/parts")
		var out struct {
			Parts []partRequest `json:"parts"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("parts readout: %v", err)
		}
		return out.Parts
	}

	for _, refuse := range []bool{false, true} {
		s := New(cat, "")
		s.RefusePlainParts = refuse
		srv := httptest.NewServer(s.PlexHandler())

		code, _ := jfGet(t, srv, plain)
		wantPlain := http.StatusOK
		if refuse {
			wantPlain = http.StatusInternalServerError
		}
		if code != wantPlain {
			t.Errorf("refuse=%v: plain address answered %d, want %d", refuse, code, wantPlain)
		}
		if code, _ := jfGet(t, srv, flagged); code != http.StatusOK {
			t.Errorf("refuse=%v: flagged address answered %d, want 200", refuse, code)
		}

		got := readout(t, srv)
		if len(got) != 2 || got[0].Part != part || got[0].Download || got[0].Status != wantPlain ||
			!got[1].Download || got[1].Status != http.StatusOK {
			t.Errorf("refuse=%v: readout %+v", refuse, got)
		}
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/__showcase/parts", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
		if got := readout(t, srv); len(got) != 0 {
			t.Errorf("refuse=%v: readout not cleared: %+v", refuse, got)
		}

		// Switched mid-run: the other way round.
		flip := "1"
		if refuse {
			flip = "0"
		}
		if resp, err := http.Post(srv.URL+"/__showcase/parts?refusePlain="+flip, "", nil); err == nil {
			resp.Body.Close()
		}
		code, _ = jfGet(t, srv, plain)
		if (code == http.StatusOK) != refuse {
			t.Errorf("refuse=%v, switched: plain address answered %d", refuse, code)
		}
		srv.Close()
	}
}

// Jellyfin's side of the log, in every mode: a song's stream and its
// Download are listed; with the refusal on, Download answers 403 (a user
// without the download permission) while the stream still plays.
func TestJellyfinSongLogAndDownloadRefusal(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	s := New(cat, "")
	s.Books = false
	srv := httptest.NewServer(s.JellyfinHandler())
	defer srv.Close()
	jf := cat.JFID(cat.Tracks[0].ID)

	type log struct {
		Parts []partRequest `json:"parts"`
	}
	read := func(method, path string) log {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out log
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return out
	}

	for _, refuse := range []bool{false, true} {
		flag := "0"
		if refuse {
			flag = "1"
		}
		read(http.MethodPost, "/__showcase/parts?refuseDownload="+flag)
		if code, _ := jfGet(t, srv, "/Audio/"+jf+"/stream?static=true"); code != http.StatusOK {
			t.Errorf("refuse=%v: stream answered %d", refuse, code)
		}
		wantDownload := http.StatusOK
		if refuse {
			wantDownload = http.StatusForbidden
		}
		if code, _ := jfGet(t, srv, "/Items/"+jf+"/Download"); code != wantDownload {
			t.Errorf("refuse=%v: Download answered %d, want %d", refuse, code, wantDownload)
		}
		got := read(http.MethodGet, "/__showcase/parts").Parts
		if len(got) != 2 || got[0].Route != "stream" || got[0].Download ||
			got[1].Route != "Download" || !got[1].Download || got[1].Status != wantDownload {
			t.Errorf("refuse=%v: log %+v", refuse, got)
		}
	}
}

// Lyrics down: the track still lists its lyric stream, the stream answers
// Plex's 404, and /__showcase/lyrics switches it back and counts requests.
func TestPlexLyricsDownSwitchAndCount(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	s := New(cat, "")
	s.Lyrics = true
	s.LyricsDown = true
	srv := httptest.NewServer(s.PlexHandler())
	defer srv.Close()
	stream := fmt.Sprintf("/library/streams/%d", (cat.plexKey[cat.Tracks[0].ID]*10+1)*10+4)

	state := func() (bool, int) {
		t.Helper()
		_, data := jfGet(t, srv, "/__showcase/lyrics")
		var out struct {
			Down     bool `json:"down"`
			Requests int  `json:"requests"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("lyrics readout: %v", err)
		}
		return out.Down, out.Requests
	}

	if code, _ := jfGet(t, srv, stream); code != http.StatusNotFound {
		t.Errorf("down: lyric stream answered %d, want 404", code)
	}
	if down, n := state(); !down || n != 1 {
		t.Errorf("down: readout down=%v requests=%d, want true 1", down, n)
	}
	if resp, err := http.Post(srv.URL+"/__showcase/lyrics?down=0", "", nil); err == nil {
		resp.Body.Close()
	}
	if code, body := jfGet(t, srv, stream); code != http.StatusOK || len(body) == 0 {
		t.Errorf("back up: lyric stream answered %d (%d bytes), want 200 with text", code, len(body))
	}
	if down, n := state(); down || n != 2 {
		t.Errorf("back up: readout down=%v requests=%d, want false 2", down, n)
	}
}

// The LRCLib stand-in: off by default; on, /api/get answers one record and
// /api/search a list of one, both with timed lyrics, and they are counted.
func TestLRCLibStandIn(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	s := New(cat, "")
	srv := httptest.NewServer(s.PlexHandler())
	defer srv.Close()
	if code, _ := jfGet(t, srv, "/api/get?artist_name=A&track_name=B"); code != http.StatusNotFound {
		t.Errorf("off: /api/get answered %d, want 404", code)
	}
	s.LRCLib = true
	code, data := jfGet(t, srv, "/api/get?artist_name=A&track_name=B&duration=200")
	var got map[string]any
	if code != http.StatusOK || json.Unmarshal(data, &got) != nil {
		t.Fatalf("on: /api/get answered %d %s", code, data)
	}
	if got["trackName"] != "B" || got["artistName"] != "A" || got["syncedLyrics"] == "" || got["instrumental"] != false {
		t.Errorf("record %v", got)
	}
	code, data = jfGet(t, srv, "/api/search?track_name=B&artist_name=A")
	var list []map[string]any
	if code != http.StatusOK || json.Unmarshal(data, &list) != nil || len(list) != 1 {
		t.Errorf("search answered %d %s", code, data)
	}
	_, data = jfGet(t, srv, "/__showcase/lyrics")
	var readout struct {
		LRCLib int `json:"lrclib"`
	}
	if json.Unmarshal(data, &readout) != nil || readout.LRCLib != 2 {
		t.Errorf("readout %s, want lrclib 2", data)
	}
}
