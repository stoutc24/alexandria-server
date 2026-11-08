package showcase

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// InstantMix (the Apple TV's Jellyfin stations from Search, 2026-11-08):
// /Artists/{id}/InstantMix, /Playlists/{id}/InstantMix and
// /Items/{id}/InstantMix answer songs by the seed and its two catalog
// neighbours, round-robin by artist, Limit honoured — more than one artist,
// so a station can start.

func TestJellyfinInstantMix(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	s := New(cat, "")
	srv := httptest.NewServer(s.JellyfinHandler())
	defer srv.Close()

	if len(cat.Artists) < 3 {
		t.Fatalf("the Jellyfin half needs three artists, has %d", len(cat.Artists))
	}
	seed := cat.Artists[0]
	artistOf := func(items []map[string]any) []string {
		var out []string
		for _, it := range items {
			name, _ := it["AlbumArtist"].(string)
			out = append(out, name)
		}
		return out
	}

	for _, path := range []string{
		"/Artists/" + cat.JFID(seed.ID) + "/InstantMix?UserId=" + jfUserID + "&Limit=100",
		"/Items/" + cat.JFID(seed.ID) + "/InstantMix?UserId=" + jfUserID + "&Limit=100",
	} {
		status, data := jfGet(t, srv, path)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", path, status)
		}
		items := jfItems(t, data)
		names := artistOf(items)
		if len(items) == 0 || names[0] != seed.Name {
			t.Fatalf("%s: the seed leads, got %v", path, names)
		}
		distinct := map[string]bool{}
		for i, n := range names {
			distinct[n] = true
			if i > 0 && n == names[i-1] && len(distinct) > 1 && i < 3 {
				t.Fatalf("%s: round-robin by artist, got %v", path, names[:i+1])
			}
			if items[i]["Type"] != "Audio" {
				t.Fatalf("%s: songs only, got %v", path, items[i]["Type"])
			}
		}
		if len(distinct) < 2 {
			t.Fatalf("%s: a mix of one artist is no station: %v", path, names)
		}
		for n := range distinct {
			if n != seed.Name && n != cat.Artists[1].Name && n != cat.Artists[2].Name {
				t.Fatalf("%s: %q is not the seed or its two neighbours", path, n)
			}
		}
	}

	// Limit is honoured.
	_, data := jfGet(t, srv, "/Artists/"+cat.JFID(seed.ID)+"/InstantMix?Limit=3")
	if n := len(jfItems(t, data)); n != 3 {
		t.Fatalf("Limit=3 answered %d", n)
	}

	// A playlist's mix leads with its own artists.
	var audio *Playlist
	for _, pl := range cat.Playlists {
		if pl.Kind != "video" && len(pl.Tracks) > 0 {
			audio = pl
			break
		}
	}
	if audio == nil {
		t.Fatal("the Jellyfin half holds no audio playlist")
	}
	for _, path := range []string{
		"/Playlists/" + cat.JFID(audio.ID) + "/InstantMix?Limit=100",
		"/Items/" + cat.JFID(audio.ID) + "/InstantMix?Limit=100",
	} {
		status, data := jfGet(t, srv, path)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d", path, status)
		}
		names := artistOf(jfItems(t, data))
		if len(names) == 0 || names[0] != audio.Tracks[0].Album.Artist.Name {
			t.Fatalf("%s: the playlist's first artist leads, got %v", path, names)
		}
	}

	// Unknown ids are 404s, as before.
	if status, _ := jfGet(t, srv, "/Playlists/nope/InstantMix"); status != http.StatusNotFound {
		t.Fatalf("unknown playlist: %d", status)
	}
}
