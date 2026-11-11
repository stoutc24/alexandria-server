package repository

import (
	"errors"
	"testing"
	"time"
)

// The repository refuses anything off the lists itself, whatever its
// caller checked, and writes nothing when it does.
func TestMergeLookRefusesAnythingOffTheLists(t *testing.T) {
	r, _ := newProfileDB(t)
	p, _ := mkProfile(t, r, "Kate")
	now := time.UnixMilli(1_000_000)

	if _, _, err := r.MergeLook(p.ID, map[string]string{"color": "teal"}, now); err != nil {
		t.Fatal(err)
	}
	for _, change := range []map[string]string{
		{"color": "chartreuse"},
		{"layout": "grid"},
		{"color": "red", "font": "comic"},
		{"Color": "red"},
		{"color": "default"},
	} {
		if _, _, err := r.MergeLook(p.ID, change, now.Add(time.Hour)); !errors.Is(err, ErrBadLook) {
			t.Fatalf("%v: %v, want ErrBadLook", change, err)
		}
	}
	look, at, err := r.GetLook(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if look != (Look{Color: "teal"}) || at != 1_000_000 {
		t.Fatalf("after the refusals: %+v at %d", look, at)
	}
	if _, _, err := r.MergeLook("no-such-person", map[string]string{"color": "red"}, now); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("no such person: %v", err)
	}
}

// The time only moves forward, even when the clock does not.
func TestLookTimeOnlyMovesForward(t *testing.T) {
	r, _ := newProfileDB(t)
	p, _ := mkProfile(t, r, "Kate")
	_, first, err := r.MergeLook(p.ID, map[string]string{"color": "teal"}, time.UnixMilli(5000))
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := r.MergeLook(p.ID, map[string]string{"color": "red"}, time.UnixMilli(1000))
	if err != nil {
		t.Fatal(err)
	}
	if first != 5000 || second != 5001 {
		t.Fatalf("times %d then %d, want 5000 then 5001", first, second)
	}
}

// Every list is what the API documents.
func TestLookListsAreTheDocumentedOnes(t *testing.T) {
	colors := []string{"red", "orange", "amber", "yellow", "lime", "green", "mint",
		"teal", "cyan", "sky", "blue", "indigo", "violet", "purple",
		"magenta", "pink", "brown", "gold", "slate", "gray"}
	want := map[string][]string{
		"color":      colors,
		"accent":     append(append([]string{}, colors...), "default"),
		"background": {"midnight", "black", "charcoal", "forest", "plum", "ocean"},
		"ambiance":   {"none", "coverColors", "fireplace", "rain", "snowfall", "library", "starfield"},
		"font":       {"default", "rounded", "serif"},
		"textSize":   {"normal", "large", "extraLarge"},
	}
	names := LookFieldNames()
	if len(names) != len(want) {
		t.Fatalf("fields %v", names)
	}
	for _, name := range names {
		got := LookValues(name)
		if len(got) != len(want[name]) {
			t.Fatalf("%s: %v, want %v", name, got, want[name])
		}
		for i := range got {
			if got[i] != want[name][i] {
				t.Fatalf("%s: %v, want %v", name, got, want[name])
			}
		}
	}
	// A caller cannot change the lists through what it was handed.
	LookValues("color")[0] = "mauve"
	if LookValues("color")[0] != "red" {
		t.Fatal("LookValues hands out the list itself")
	}
	if LookValues("layout") != nil {
		t.Fatal("a field that is not one has values")
	}
}
