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
