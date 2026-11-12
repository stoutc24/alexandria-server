package showcase

import (
	"fmt"
	"net/http"
	"strconv"
)

// Chapters for films and episodes (owner, 2026-11-11: "chapter support for
// movies and shows", pictures "when the server has them"). Every film and
// episode gets four to six, the same each run: the first at 0:00, the rest
// spread over the running time. Every third chapter has no picture, so a
// list shows both a picture and a plain card; an episode's fourth chapter
// has no name, as files ripped without chapter titles do (the apps call it
// "Chapter 4").
//
// Plex sends them as `Chapter` only when the detail request asks with
// includeChapters=1 (as Plex Media Server does), each picture at
// /library/media/{media id}/chapterImages/{index}. Jellyfin sends
// `Chapters` with the item's full detail, a picture's ImageTag, and the
// picture at /Items/{id}/Images/Chapter/{position in the list}. A chapter
// without a picture answers 404 on both, as the real servers do.
//
// With Server.ClipChapters (SHOWCASE_CLIP_CHAPTERS=1) they are spread over
// the clip's first clipChapterSpan seconds instead, evenly, for players
// that play the clip file itself.

type videoChapter struct {
	Name    string  // "" = unnamed
	Start   float64 // seconds
	End     float64 // seconds
	Picture bool
}

var filmChapterNames = []string{
	"The Arrival", "Night Crossing", "Old Friends", "The Long Way Round",
	"Turning Point", "Into the Storm", "Homecoming", "Last Light",
}

var episodeChapterNames = []string{"Act One", "Act Two", "Act Three", "Act Four"}

// clipChapterSpan: where ClipChapters puts the chapters — inside every
// rendered clip (45 to 48 s).
const clipChapterSpan = 40.0

// chaptersOf is the chapters of a film or an episode, nil for anything else.
func (s *Server) chaptersOf(id string) []videoChapter {
	var dur float64
	episode := false
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		dur = float64(v.Minutes) * 60
	case *Episode:
		dur = float64(v.Minutes) * 60
		episode = true
	default:
		return nil
	}
	if dur <= 0 {
		return nil
	}
	if s.ClipChapters {
		dur = clipChapterSpan
	}
	h := hash("chapters|" + id)
	n := 4 + int(h%3)
	out := make([]videoChapter, n)
	for i := range out {
		start := 0.0
		if i > 0 {
			// Evenly spread, nudged by up to half a minute so no two titles
			// line up exactly.
			start = float64(int(dur*float64(i)/float64(n))) + float64((h>>uint(i))%5)*7
			if s.ClipChapters {
				start = float64(int(dur * float64(i) / float64(n)))
			}
		}
		var name string
		switch {
		case i == 0 && episode:
			name = "Cold Open"
		case i == 0:
			name = "Opening"
		case i == n-1 && episode:
			name = "Tag"
		case i == n-1:
			name = "Finale"
		case episode:
			name = episodeChapterNames[(i-1)%len(episodeChapterNames)]
		default:
			name = filmChapterNames[(int(h>>4)+i)%len(filmChapterNames)]
		}
		if episode && i == 3 {
			name = ""
		}
		out[i] = videoChapter{Name: name, Start: start, Picture: i%3 != 2}
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].End = out[i+1].Start
		} else {
			out[i].End = dur
		}
	}
	return out
}

// plexChapters is the item's `Chapter` array (the detail's, with
// includeChapters=1).
func (s *Server) plexChapters(id string) []map[string]any {
	chapters := s.chaptersOf(id)
	if chapters == nil {
		return nil
	}
	media := s.cat.plexKey[id]
	out := make([]map[string]any, 0, len(chapters))
	for i, c := range chapters {
		ch := map[string]any{
			"id": media*100 + i + 1, "index": i + 1, "filter": fmt.Sprintf("chapter=%d", media*100+i+1),
			"startTimeOffset": int64(c.Start * 1000), "endTimeOffset": int64(c.End * 1000),
		}
		if c.Name != "" {
			ch["tag"] = c.Name
		}
		if c.Picture {
			ch["thumb"] = fmt.Sprintf("/library/media/%d/chapterImages/%d", media, i+1)
		}
		out = append(out, ch)
	}
	return out
}

// jfChapters is the item's `Chapters` array (the full detail's).
func (s *Server) jfChapters(id string) []map[string]any {
	out := []map[string]any{}
	for i, c := range s.chaptersOf(id) {
		ch := map[string]any{"StartPositionTicks": ticks(c.Start)}
		if c.Name != "" {
			ch["Name"] = c.Name
		}
		if c.Picture {
			ch["ImageTag"] = fmt.Sprintf("ch%d%s", i, s.cat.JFID(id)[:8])
			ch["ImageDateModified"] = jfDate(s.cat.daysAgo(30))
		}
		out = append(out, ch)
	}
	return out
}

// chapterPicture answers a chapter's picture: a 16:9 still in the title's
// own colours, different for each chapter, or 404 when that chapter has
// none (or there is no such chapter). position counts from 0.
func (s *Server) chapterPicture(w http.ResponseWriter, id string, position, width int) {
	chapters := s.chaptersOf(id)
	if position < 0 || position >= len(chapters) || !chapters[position].Picture {
		notFound(w)
		return
	}
	if width <= 0 || width > 1280 {
		width = 1280
	}
	key := fmt.Sprintf("chapter|%s|%d|%d", id, position, width)
	if v, ok := s.covers.Load(key); ok {
		serveJPEG(w, v.([]byte))
		return
	}
	spec := s.coverSpec(id)
	// The chapter's name and number seed the colours and the figure; Bare:
	// a frame of the film, not a poster (the apps print the name beside it).
	spec.Title = spec.Title + " · " + strconv.Itoa(position+1) + " " + chapters[position].Name
	spec.Bare = true
	spec.Width, spec.Height = width, width*9/16
	data, err := Cover(spec)
	if err != nil {
		serveJPEG(w, nil)
		return
	}
	s.covers.Store(key, data)
	serveJPEG(w, data)
}

// plexChapterImage serves /library/media/{media id}/chapterImages/{index}
// (p is the path after /library/media/). Plex's media id here is the
// item's own key (plexVideoMedia).
func (s *Server) plexChapterImage(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) != 3 || p[1] != "chapterImages" {
		notFound(w)
		return
	}
	index, err := strconv.Atoi(p[2])
	id := s.cat.IDForPlexKey(p[0])
	if err != nil || id == "" {
		notFound(w)
		return
	}
	s.chapterPicture(w, id, index-1, qInt(r, "width", 0))
}
