package showcase

import (
	"bytes"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
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
