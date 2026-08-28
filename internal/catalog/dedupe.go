// Package catalog holds the merged, deduplicated view of every library
// the household runs.
//
// The dedupe here MIRRORS the client's CrossServiceDedupe (see
// lib/services/catalog/cross_service_dedupe.dart in the app repo). The
// two must agree: if the server collapses a pair the app would not, the
// app shows one thing and the server reports another, and the user is
// left deciding which lied.
//
// The asymmetry is deliberate and load-bearing: a WRONG merge hides
// media, while a MISSED merge only shows a duplicate. So anything we
// cannot key confidently is left alone.
package catalog

import (
	"strings"
	"unicode"
)

// Item is one row from one media server.
type Item struct {
	ItemID    string `json:"itemId"`
	Source    string `json:"source"`
	MediaType string `json:"mediaType"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	Series    string `json:"series,omitempty"`
	Genre     string `json:"genre,omitempty"`
	Year      int    `json:"year,omitempty"`
	DurationS int    `json:"durationSec,omitempty"`
}

// Group is one logical work and every copy of it found.
type Group struct {
	Key            string `json:"key"`
	Representative Item   `json:"representative"`
	Copies         []Item `json:"copies"`
}

// Sources lists the distinct servers holding this work.
func (g Group) Sources() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range g.Copies {
		if !seen[c.Source] {
			seen[c.Source] = true
			out = append(out, c.Source)
		}
	}
	return out
}

// normalize reduces a string to its comparable core: lowercase, no
// punctuation, single-spaced. Mirrors MediaMatch on the client.
func normalize(s string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
		default:
			// punctuation dropped entirely
		}
	}
	return strings.TrimSpace(b.String())
}

// ContentKey is the cross-service identity for an item, or "" when the
// item cannot be grouped safely.
//
// Empty when the creator does not normalise to something real:
// placeholders ("Unknown", "Various Artists", "N/A") would otherwise
// stitch unrelated works together, which is the one failure mode worse
// than a duplicate.
func ContentKey(it Item) string {
	creator := normalize(it.Author)
	if creator == "" || isPlaceholderCreator(creator) {
		return ""
	}
	title := normalize(it.Title)
	if title == "" {
		return ""
	}
	return title + "|" + creator
}

var placeholderCreators = map[string]bool{
	"unknown":         true,
	"unknown author":  true,
	"unknown artist":  true,
	"various":         true,
	"various artists": true,
	"va":              true,
	"na":              true, // "N/A" — punctuation is dropped, not spaced
	"n a":             true,
	"none":            true,
	"no author":       true,
	"anonymous":       true,
}

func isPlaceholderCreator(normalized string) bool {
	return placeholderCreators[normalized]
}

// Collapse groups items that are the same work on different servers.
//
// prefer ranks sources when choosing which copy represents the group —
// callers pass the source the user is actually playing from, so the
// representative stays usable there.
//
// Items that cannot be keyed are each returned as their own group:
// refusing to guess beats merging two different things.
func Collapse(items []Item, prefer []string) []Group {
	rank := func(source string) int {
		for i, p := range prefer {
			if p == source {
				return i
			}
		}
		return len(prefer)
	}

	byKey := map[string]int{} // key -> index in groups
	groups := []Group{}

	for _, it := range items {
		key := ContentKey(it)
		if key == "" {
			groups = append(groups, Group{
				Key:            "",
				Representative: it,
				Copies:         []Item{it},
			})
			continue
		}
		if idx, ok := byKey[key]; ok {
			groups[idx].Copies = append(groups[idx].Copies, it)
			if rank(it.Source) < rank(groups[idx].Representative.Source) {
				groups[idx].Representative = it
			}
			continue
		}
		byKey[key] = len(groups)
		groups = append(groups, Group{
			Key:            key,
			Representative: it,
			Copies:         []Item{it},
		})
	}
	return groups
}

// Stats summarises a collapse without shipping every row.
type Stats struct {
	RawItems   int `json:"rawItems"`
	Distinct   int `json:"distinct"`
	Duplicates int `json:"duplicates"`
	Ungrouped  int `json:"ungrouped"`
}

// Summarize counts what a collapse found.
func Summarize(items []Item) Stats {
	groups := Collapse(items, nil)
	s := Stats{RawItems: len(items), Distinct: len(groups)}
	for _, g := range groups {
		if g.Key == "" {
			s.Ungrouped++
			continue
		}
		s.Duplicates += len(g.Copies) - 1
	}
	return s
}
