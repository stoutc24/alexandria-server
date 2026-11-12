package showcase

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Films and episodes carry four to six chapters, the same each run, the
// first at 0:00 and each starting after the one before; every third has no
// picture; an episode's fourth has no name.
func TestChaptersShape(t *testing.T) {
	cat := Build(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), nil)
	s := New(cat, "")
	var ids []string
	for _, m := range cat.Movies {
		ids = append(ids, m.ID)
	}
	for _, e := range cat.Episodes {
		ids = append(ids, e.ID)
	}
	if len(ids) == 0 {
		t.Fatal("no films or episodes")
	}
	counts := map[int]int{}
	for _, id := range ids {
		chapters := s.chaptersOf(id)
		counts[len(chapters)]++
		if len(chapters) < 4 || len(chapters) > 6 {
			t.Fatalf("%s: %d chapters, want 4-6", id, len(chapters))
		}
		if chapters[0].Start != 0 {
			t.Fatalf("%s: first chapter at %v, want 0", id, chapters[0].Start)
		}
		pictures, plain := 0, 0
		for i, c := range chapters {
			if i > 0 && c.Start <= chapters[i-1].Start {
				t.Fatalf("%s: chapter %d starts at %v, not after %v", id, i+1, c.Start, chapters[i-1].Start)
			}
			if c.End <= c.Start {
				t.Fatalf("%s: chapter %d ends at %v, starts at %v", id, i+1, c.End, c.Start)
			}
			if i+1 < len(chapters) && c.End != chapters[i+1].Start {
				t.Fatalf("%s: chapter %d ends at %v, the next starts at %v", id, i+1, c.End, chapters[i+1].Start)
			}
			if c.Picture {
				pictures++
			} else {
				plain++
			}
		}
		if pictures == 0 || plain == 0 {
			t.Fatalf("%s: %d with a picture, %d without; want both", id, pictures, plain)
		}
		_, episode := cat.Get(id).(*Episode)
		if episode && chapters[3].Name != "" {
			t.Fatalf("%s: an episode's fourth chapter is unnamed, got %q", id, chapters[3].Name)
		}
		for i, c := range chapters {
			if c.Name == "" && !(episode && i == 3) {
				t.Fatalf("%s: chapter %d has no name", id, i+1)
			}
		}
		again := s.chaptersOf(id)
		if fmt.Sprint(again) != fmt.Sprint(chapters) {
			t.Fatalf("%s: chapters differ between calls", id)
		}
	}
	if len(counts) < 2 {
		t.Errorf("every title has the same number of chapters: %v", counts)
	}
	if s.chaptersOf(cat.Shows[0].ID) != nil {
		t.Error("a show has chapters of its own")
	}
}

// Plex: a detail carries `Chapter` only with includeChapters=1; each picture
// is served at its thumb (and through the photo transcoder); a chapter
// without one answers 404.
func TestPlexChapters(t *testing.T) {
	cat := Build(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), nil).Half(0)
	s := New(cat, "")
	srv := httptest.NewServer(s.PlexHandler())
	defer srv.Close()

	check := func(id, what string) {
		key := cat.PlexKey(id)
		if _, has := plexItem(t, srv, key)["Chapter"]; has {
			t.Fatalf("%s: chapters without includeChapters=1", what)
		}
		items := plexItems(t, srv, "/library/metadata/"+key+"?includeChapters=1&includeMarkers=1")
		raw, _ := items[0]["Chapter"].([]any)
		want := s.chaptersOf(id)
		if len(raw) != len(want) {
			t.Fatalf("%s: %d chapters, want %d", what, len(raw), len(want))
		}
		thumbs, plain := 0, 0
		for i, r := range raw {
			c := r.(map[string]any)
			if num(c["index"]) != i+1 {
				t.Fatalf("%s: chapter %d has index %v", what, i+1, c["index"])
			}
			if int64(num(c["startTimeOffset"])) != int64(want[i].Start*1000) || int64(num(c["endTimeOffset"])) != int64(want[i].End*1000) {
				t.Fatalf("%s: chapter %d offsets %v-%v", what, i+1, c["startTimeOffset"], c["endTimeOffset"])
			}
			tag, named := c["tag"].(string)
			if named != (want[i].Name != "") || tag != want[i].Name {
				t.Fatalf("%s: chapter %d tag %v, want %q", what, i+1, c["tag"], want[i].Name)
			}
			path := fmt.Sprintf("/library/media/%s/chapterImages/%d", key, i+1)
			thumb, hasThumb := c["thumb"].(string)
			if hasThumb != want[i].Picture {
				t.Fatalf("%s: chapter %d thumb %v, picture %v", what, i+1, c["thumb"], want[i].Picture)
			}
			code, ctype, size := fetch(t, srv.URL+path+"?X-Plex-Token=")
			if hasThumb {
				thumbs++
				if thumb != path {
					t.Fatalf("%s: chapter %d thumb %q, want %q", what, i+1, thumb, path)
				}
				if code != http.StatusOK || ctype != "image/jpeg" || size < 1000 {
					t.Fatalf("%s: chapter %d picture: %d %s %d bytes", what, i+1, code, ctype, size)
				}
				code, ctype, _ = fetch(t, srv.URL+"/photo/:/transcode?width=480&height=270&minSize=1&upscale=1&url="+path)
				if code != http.StatusOK || ctype != "image/jpeg" {
					t.Fatalf("%s: chapter %d picture through the photo transcoder: %d %s", what, i+1, code, ctype)
				}
			} else {
				plain++
				if code != http.StatusNotFound {
					t.Fatalf("%s: chapter %d has no picture, its address answered %d", what, i+1, code)
				}
			}
		}
		if thumbs == 0 || plain == 0 {
			t.Fatalf("%s: %d thumbs, %d plain", what, thumbs, plain)
		}
		if code, _, _ := fetch(t, srv.URL+fmt.Sprintf("/library/media/%s/chapterImages/%d", key, len(want)+1)); code != http.StatusNotFound {
			t.Fatalf("%s: a chapter past the last answered %d", what, code)
		}
	}
	check(cat.Movies[0].ID, "film")
	check(cat.Episodes[0].ID, "episode")

	// Two chapters' pictures differ.
	key := cat.PlexKey(cat.Movies[0].ID)
	_, _, a := fetchBody(t, srv.URL+"/library/media/"+key+"/chapterImages/1")
	_, _, b := fetchBody(t, srv.URL+"/library/media/"+key+"/chapterImages/2")
	if string(a) == string(b) {
		t.Error("chapters 1 and 2 share a picture")
	}
	// Lists never carry chapters.
	for _, item := range plexItems(t, srv, "/library/sections/1/all?includeChapters=1") {
		if _, has := item["Chapter"]; has {
			t.Fatal("a library list row carries chapters")
		}
	}
}

// Jellyfin: the full detail carries `Chapters` (Name, StartPositionTicks, an
// ImageTag on those with a picture); /Items/{id}/Images/Chapter/{n} serves
// the picture, 404 for a chapter without one.
func TestJellyfinChapters(t *testing.T) {
	cat := Build(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), nil).Half(1)
	s := New(cat, "")
	srv := httptest.NewServer(s.JellyfinHandler())
	defer srv.Close()

	check := func(id, what string) {
		jf := cat.JFID(id)
		code, data := jfGet(t, srv, "/Users/"+jfUserID+"/Items/"+jf)
		if code != http.StatusOK {
			t.Fatalf("%s: detail answered %d", what, code)
		}
		var item struct {
			Chapters []struct {
				StartPositionTicks int64
				Name               *string
				ImageTag           *string
			}
		}
		if err := json.Unmarshal(data, &item); err != nil {
			t.Fatal(err)
		}
		want := s.chaptersOf(id)
		if len(item.Chapters) != len(want) {
			t.Fatalf("%s: %d chapters, want %d", what, len(item.Chapters), len(want))
		}
		tags, plain := 0, 0
		for i, c := range item.Chapters {
			if c.StartPositionTicks != ticks(want[i].Start) {
				t.Fatalf("%s: chapter %d at %d ticks", what, i+1, c.StartPositionTicks)
			}
			if (c.Name != nil) != (want[i].Name != "") || (c.Name != nil && *c.Name != want[i].Name) {
				t.Fatalf("%s: chapter %d name %v, want %q", what, i+1, c.Name, want[i].Name)
			}
			path := fmt.Sprintf("/Items/%s/Images/Chapter/%d", jf, i)
			if (c.ImageTag != nil) != want[i].Picture {
				t.Fatalf("%s: chapter %d image tag %v, picture %v", what, i+1, c.ImageTag, want[i].Picture)
			}
			code, ctype, size := fetch(t, srv.URL+path+"?maxWidth=480&ApiKey=x")
			if c.ImageTag != nil {
				tags++
				if code != http.StatusOK || ctype != "image/jpeg" || size < 1000 {
					t.Fatalf("%s: chapter %d picture: %d %s %d bytes", what, i+1, code, ctype, size)
				}
			} else {
				plain++
				if code != http.StatusNotFound {
					t.Fatalf("%s: chapter %d has no picture, its address answered %d", what, i+1, code)
				}
			}
		}
		if tags == 0 || plain == 0 {
			t.Fatalf("%s: %d tagged, %d plain", what, tags, plain)
		}
		for _, bad := range []string{fmt.Sprint(len(want)), "-1", "x", ""} {
			if code, _, _ := fetch(t, srv.URL+"/Items/"+jf+"/Images/Chapter/"+bad); code != http.StatusNotFound {
				t.Fatalf("%s: Images/Chapter/%s answered %d", what, bad, code)
			}
		}
		// The item's other pictures are untouched.
		if code, ctype, _ := fetch(t, srv.URL+"/Items/"+jf+"/Images/Primary"); code != http.StatusOK || ctype != "image/jpeg" {
			t.Fatalf("%s: Primary answered %d %s", what, code, ctype)
		}
	}
	check(cat.Movies[0].ID, "film")
	check(cat.Episodes[0].ID, "episode")
}

func fetch(t *testing.T, url string) (int, string, int) {
	t.Helper()
	code, ctype, body := fetchBody(t, url)
	return code, ctype, len(body)
}

func fetchBody(t *testing.T, url string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

// ClipChapters (SHOWCASE_CLIP_CHAPTERS=1): the same chapters, spread evenly
// over the clip's first 40 s, so a player of the clip file reaches each.
func TestClipChapters(t *testing.T) {
	cat := Build(time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), nil)
	plain := New(cat, "")
	clip := New(cat, "")
	clip.ClipChapters = true
	for _, id := range []string{cat.Movies[0].ID, cat.Episodes[0].ID} {
		long, short := plain.chaptersOf(id), clip.chaptersOf(id)
		if len(short) != len(long) {
			t.Fatalf("%s: %d chapters, %d without the switch", id, len(short), len(long))
		}
		for i, c := range short {
			want := float64(int(clipChapterSpan * float64(i) / float64(len(short))))
			if c.Start != want || c.Name != long[i].Name || c.Picture != long[i].Picture {
				t.Fatalf("%s: chapter %d = %+v, want start %v and the rest as %+v", id, i+1, c, want, long[i])
			}
		}
		if short[len(short)-1].End != clipChapterSpan {
			t.Fatalf("%s: the last ends at %v", id, short[len(short)-1].End)
		}
	}
}
