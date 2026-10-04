package showcase

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCoversRenderAndAreDeterministic(t *testing.T) {
	a, err := Cover(CoverSpec{Title: "The Lighthouse at Sable Point", Subtitle: "Marisol Vane", Kind: "book"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Cover(CoverSpec{Title: "The Lighthouse at Sable Point", Subtitle: "Marisol Vane", Kind: "book"})
	if !bytes.Equal(a, b) {
		t.Fatal("same spec must give the same bytes")
	}
	img, err := jpeg.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 600 || img.Bounds().Dy() != 900 {
		t.Fatalf("poster size %v", img.Bounds())
	}
	// A sample sheet for the eye, when SHOWCASE_SAMPLES points somewhere.
	if dir := os.Getenv("SHOWCASE_SAMPLES"); dir != "" {
		specs := []CoverSpec{
			{Title: "The Lighthouse at Sable Point", Subtitle: "Marisol Vane", Kind: "book"},
			{Title: "Nine Winters in Tallow", Subtitle: "Ezra Quill", Kind: "book"},
			{Title: "Copperline", Subtitle: "The Harbor Lights · 2024", Kind: "album", Width: 800, Height: 800},
			{Title: "Orbit of Small Hours", Subtitle: "Juniper Vale", Kind: "album", Width: 800, Height: 800},
			{Title: "The Cartographer's Daughter", Subtitle: "2023 · 1h 58m", Kind: "film"},
			{Title: "Low Tide at Ferris Cove", Subtitle: "Season 2", Kind: "show"},
			{Title: "Signals from the Quiet Belt", Subtitle: "Episode 14", Kind: "podcast"},
			{Title: "A Field Guide to Vanished Rivers", Subtitle: "Odette Marsh", Kind: "ebook"},
		}
		for i, s := range specs {
			data, err := Cover(s)
			if err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(dir, "cover-"+string(rune('a'+i))+".jpg"), data, 0o644)
		}
		bd, _ := Backdrop("The Cartographer's Daughter", "2023 · Adventure", "film")
		_ = os.WriteFile(filepath.Join(dir, "backdrop.jpg"), bd, 0o644)
	}
}

func TestPortraitsAreDistinctAndDeterministic(t *testing.T) {
	cat := Build(time.Now(), nil)
	seen := map[string]string{}
	for _, m := range cat.Movies {
		for _, c := range m.Cast {
			a, err := Portrait(c.Name, 400)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := Portrait(c.Name, 400)
			if !bytes.Equal(a, b) {
				t.Fatalf("%s: same name must give the same portrait", c.Name)
			}
			img, err := jpeg.Decode(bytes.NewReader(a))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 400 {
				t.Fatalf("%s: portrait size %v", c.Name, img.Bounds())
			}
			if other, dup := seen[string(a)]; dup && other != c.Name {
				t.Fatalf("%s and %s share a portrait", c.Name, other)
			}
			seen[string(a)] = c.Name
		}
	}
	// Every credited person resolves both ways, Plex and Jellyfin.
	for _, m := range cat.Movies {
		for _, c := range m.Cast {
			id := PersonID(c.Name)
			if cat.IDForPlexKey(cat.PlexKey(id)) != id || cat.IDForJF(cat.JFID(id)) != id {
				t.Fatalf("%s has no stable key", c.Name)
			}
		}
	}
	if dir := os.Getenv("SHOWCASE_SAMPLES"); dir != "" {
		for _, n := range seen {
			data, _ := Portrait(n, 400)
			_ = os.WriteFile(filepath.Join(dir, "portrait-"+slug(n)+".jpg"), data, 0o644)
		}
		bare, _ := Cover(CoverSpec{Title: "Harbor Bell", Subtitle: "2021 · Family, Comedy", Kind: "film", Width: 1280, Height: 720, Bare: true})
		_ = os.WriteFile(filepath.Join(dir, "backdrop-bare.jpg"), bare, 0o644)
	}
}

func TestBareBackdropDropsTheType(t *testing.T) {
	spec := CoverSpec{Title: "Harbor Bell", Subtitle: "2021 · Family, Comedy", Kind: "film", Width: 1280, Height: 720}
	titled, _ := Cover(spec)
	spec.Bare = true
	bare, err := Cover(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(titled, bare) {
		t.Fatal("a bare backdrop must differ from the titled one")
	}
}

func TestStretchPlaylistReachesTheRuntime(t *testing.T) {
	src := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n" +
		"#EXTINF:6.000,\nseg0.m4s\n#EXTINF:6.000,\nseg1.m4s\n#EXTINF:6.000,\nseg2.m4s\n#EXTINF:6.000,\nseg3.m4s\n" +
		"#EXTINF:6.000,\nseg4.m4s\n#EXTINF:6.000,\nseg5.m4s\n#EXTINF:6.000,\nseg6.m4s\n#EXTINF:3.000,\nseg7.m4s\n#EXT-X-ENDLIST\n"
	if _, ok := stretchPlaylist(src, 45); ok {
		t.Fatal("a playlist already that long is served as it is")
	}
	out, ok := stretchPlaylist(src, 104*60)
	if !ok {
		t.Fatal("not stretched")
	}
	total := 0.0
	for _, line := range strings.Split(out, "\n") {
		if v, found := strings.CutPrefix(line, "#EXTINF:"); found {
			d, err := strconv.ParseFloat(strings.TrimSuffix(v, ","), 64)
			if err != nil {
				t.Fatal(err)
			}
			total += d
		}
	}
	if total < 104*60 || total >= 104*60+6 {
		t.Fatalf("stretched to %.0f s, want 6240–6245", total)
	}
	passes := strings.Count(out, "#EXT-X-MAP:")
	if strings.Count(out, "#EXT-X-DISCONTINUITY") != passes-1 || !strings.HasSuffix(out, "#EXT-X-ENDLIST\n") {
		t.Fatalf("%d passes, %d discontinuities", passes, strings.Count(out, "#EXT-X-DISCONTINUITY"))
	}
}

func TestWAVRangesAreConsistent(t *testing.T) {
	var whole bytes.Buffer
	if err := WriteWAVRange(&whole, 42, 3, 0, WAVSize(3)); err != nil {
		t.Fatal(err)
	}
	if int64(whole.Len()) != WAVSize(3) {
		t.Fatalf("size %d want %d", whole.Len(), WAVSize(3))
	}
	if string(whole.Bytes()[:4]) != "RIFF" || string(whole.Bytes()[8:12]) != "WAVE" {
		t.Fatal("not a WAV")
	}
	var part bytes.Buffer
	if err := WriteWAVRange(&part, 42, 3, 1001, 5000); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(part.Bytes(), whole.Bytes()[1001:5000]) {
		t.Fatal("a byte range must match the same bytes of the whole file")
	}
	if dir := os.Getenv("SHOWCASE_SAMPLES"); dir != "" {
		var f bytes.Buffer
		_ = WriteWAVRange(&f, 7, 12, 0, WAVSize(12))
		_ = os.WriteFile(filepath.Join(dir, "sample.wav"), f.Bytes(), 0o644)
	}
}
