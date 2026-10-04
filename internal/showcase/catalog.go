package showcase

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// The catalog: one in-memory world built deterministically from names.go,
// read through three API dialects (abs.go, jellyfin.go, plex.go). IDs are
// stable across runs so caches and screenshots line up.

type Chapter struct {
	ID    int
	Title string
	Start float64
	End   float64
}

type Book struct {
	ID        string
	Index     int
	Title     string
	Subtitle  string
	Author    *Author
	Narrator  string
	Series    *Series
	Sequence  string
	Genres    []string
	Year      string
	Publisher string
	Blurb     string
	Duration  float64 // seconds
	Chapters  []Chapter
	AddedAt   time.Time
	Progress  float64 // 0 unstarted, 1 finished, else fraction
	LastPlay  time.Time
	Ebook     bool
	Seed      uint32
	FileIno   string
	Filename  string
}

type Author struct {
	ID    string
	Name  string
	Bio   string
	Books []*Book
}

type Series struct {
	ID      string
	Name    string
	Books   []*Book
	AddedAt time.Time
}

type Artist struct {
	ID     string
	Index  int
	Name   string
	Genre  string
	Bio    string
	Albums []*Album
}

type Album struct {
	ID       string
	Index    int
	Title    string
	Artist   *Artist
	Year     int
	Genre    string
	Tracks   []*Track
	AddedAt  time.Time
	Played   float64
	LastPlay time.Time
}

type Track struct {
	ID       string
	Album    *Album
	Index    int
	Title    string
	Duration float64
	Seed     uint32
	Plays    int
}

type Movie struct {
	ID       string
	Index    int
	Title    string
	Tagline  string
	Summary  string
	Rating   string
	Director string
	Year     int
	Minutes  int
	Genres   []string
	Cast     []castMember
	Score    float64
	Progress float64
	LastPlay time.Time
	AddedAt  time.Time
	UHD      bool
	Clip     string
}

type Episode struct {
	ID       string
	Show     *Show
	Season   *Season
	Index    int
	Title    string
	Summary  string
	Minutes  int
	Progress float64
	Watched  bool
	LastPlay time.Time
	Aired    time.Time
	Clip     string
}

type Season struct {
	ID       string
	Show     *Show
	Index    int
	Episodes []*Episode
}

type Show struct {
	ID      string
	Index   int
	Title   string
	Summary string
	Rating  string
	Year    int
	Minutes int
	Genres  []string
	Cast    []castMember
	Seasons []*Season
	AddedAt time.Time
	NextUp  *Episode
}

type Collection struct {
	ID     string
	Title  string
	Movies []*Movie
	Shows  []*Show
}

type Playlist struct {
	ID     string
	Title  string
	Kind   string
	Tracks []*Track
	Movies []*Movie
}

type Catalog struct {
	Now         time.Time
	Books       []*Book
	Authors     []*Author
	Series      []*Series
	Artists     []*Artist
	Albums      []*Album
	Tracks      []*Track
	Movies      []*Movie
	Shows       []*Show
	Episodes    []*Episode
	Collections []*Collection
	Playlists   []*Playlist

	byID map[string]any
	// Plex wants small integer rating keys; every entity gets one.
	plexKey   map[string]int
	plexByKey map[int]string
	// Jellyfin wants 32-hex ids.
	jfID   map[string]string
	jfByID map[string]string
	// The clips available for video (ids under the clips directory).
	Clips []string
}

// Build the world. `clips` are the directory names available for video
// playback; empty means every video item points at "ambient", which the
// media layer answers with its built-in placeholder.
func Build(now time.Time, clips []string) *Catalog {
	c := &Catalog{Now: now, byID: map[string]any{}, plexKey: map[string]int{}, plexByKey: map[int]string{},
		jfID: map[string]string{}, jfByID: map[string]string{}, Clips: clips}
	if len(c.Clips) == 0 {
		c.Clips = []string{"ambient"}
	}
	c.buildBooks()
	c.buildMusic()
	c.buildVideo()
	c.buildCollections()
	c.assignKeys()
	c.registerPeople()
	return c
}

func (c *Catalog) daysAgo(d int) time.Time { return c.Now.Add(-time.Duration(d) * 24 * time.Hour) }

func (c *Catalog) register(id string, v any) { c.byID[id] = v }

// Get looks any entity up by its catalog id.
func (c *Catalog) Get(id string) any { return c.byID[id] }

// ---- books -----------------------------------------------------------------

func (c *Catalog) buildBooks() {
	for i, a := range authors {
		au := &Author{ID: fmt.Sprintf("au%02d", i+1), Name: a.Name, Bio: a.Bio}
		c.Authors = append(c.Authors, au)
		c.register(au.ID, au)
	}
	seriesByName := map[string]*Series{}
	inProgressRank := 0
	for i, s := range books {
		b := &Book{
			ID: fmt.Sprintf("li_%02d", i+1), Index: i, Title: s.Title, Author: c.Authors[s.Author],
			Narrator: narrators[s.Narrator%len(narrators)], Sequence: s.Sequence, Genres: s.Genres,
			Year: s.Year, Publisher: s.Publisher, Blurb: s.Blurb, Duration: math.Round(s.Hours * 3600),
			AddedAt: c.daysAgo(s.AddedDays), Progress: s.Progress, Ebook: s.Ebook,
			Seed: hash("book|" + s.Title), FileIno: fmt.Sprintf("ino%03d", 1000+i),
		}
		b.Filename = slug(b.Title) + ".wav"
		if b.Ebook {
			b.Filename = slug(b.Title) + ".epub"
			b.Duration = 0
		}
		if b.Progress > 0 && b.Progress < 1 {
			b.LastPlay = c.Now.Add(-time.Duration(2+inProgressRank*7) * time.Hour)
			inProgressRank++
		} else if b.Progress >= 1 {
			b.LastPlay = c.daysAgo(10 + i*3)
		}
		b.Chapters = chaptersFor(b)
		if s.Series != "" {
			se := seriesByName[s.Series]
			if se == nil {
				se = &Series{ID: fmt.Sprintf("se%02d", len(c.Series)+1), Name: s.Series}
				seriesByName[s.Series] = se
				c.Series = append(c.Series, se)
				c.register(se.ID, se)
			}
			se.Books = append(se.Books, b)
			if se.AddedAt.IsZero() || b.AddedAt.After(se.AddedAt) {
				se.AddedAt = b.AddedAt
			}
			b.Series = se
		}
		b.Author.Books = append(b.Author.Books, b)
		c.Books = append(c.Books, b)
		c.register(b.ID, b)
	}
}

func chaptersFor(b *Book) []Chapter {
	if b.Ebook || b.Duration <= 0 {
		return nil
	}
	n := int(b.Duration/2400) + 3 // a chapter every ~40 min
	if n > 40 {
		n = 40
	}
	var out []Chapter
	step := b.Duration / float64(n)
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("Chapter %d", i+1)
		switch i {
		case 0:
			title = "Opening Credits"
		case n - 1:
			title = "Closing Credits"
		}
		out = append(out, Chapter{ID: i, Title: title, Start: math.Round(step * float64(i)), End: math.Round(step * float64(i+1))})
	}
	return out
}

// ---- music -----------------------------------------------------------------

func (c *Catalog) buildMusic() {
	for i, a := range artists {
		ar := &Artist{ID: fmt.Sprintf("ar%02d", i+1), Index: i, Name: a.Name, Genre: a.Genre, Bio: a.Bio}
		c.Artists = append(c.Artists, ar)
		c.register(ar.ID, ar)
	}
	playedRank := 0
	for i, a := range albums {
		al := &Album{ID: fmt.Sprintf("al%02d", i+1), Index: i, Title: a.Title, Artist: c.Artists[a.Artist], Year: a.Year,
			Genre: c.Artists[a.Artist].Genre, AddedAt: c.daysAgo(a.Added), Played: a.Played}
		if a.Played > 0 {
			al.LastPlay = c.Now.Add(-time.Duration(1+playedRank*5) * time.Hour)
			playedRank++
		}
		for j, t := range a.Tracks {
			seed := hash("track|" + a.Title + "|" + t)
			tr := &Track{ID: fmt.Sprintf("tr%02d%02d", i+1, j+1), Album: al, Index: j + 1, Title: t,
				Duration: 150 + float64(seed%230), Seed: seed}
			if a.Played > 0 {
				tr.Plays = 1 + int(seed%9)
			}
			al.Tracks = append(al.Tracks, tr)
			c.Tracks = append(c.Tracks, tr)
			c.register(tr.ID, tr)
		}
		al.Artist.Albums = append(al.Artist.Albums, al)
		c.Albums = append(c.Albums, al)
		c.register(al.ID, al)
	}
}

// Duration of an album in seconds.
func (a *Album) Duration() float64 {
	var d float64
	for _, t := range a.Tracks {
		d += t.Duration
	}
	return d
}

// ---- video -----------------------------------------------------------------

func (c *Catalog) buildVideo() {
	watchRank := 0
	for i, m := range movies {
		mv := &Movie{ID: fmt.Sprintf("mv%02d", i+1), Index: i, Title: m.Title, Tagline: m.Tagline, Summary: m.Summary,
			Rating: m.Rating, Director: m.Director, Year: m.Year, Minutes: m.Minutes, Genres: m.Genres, Cast: m.Cast,
			Score: m.Score, Progress: m.Progress, AddedAt: c.daysAgo(m.Added), UHD: m.UHD}
		if mv.Progress > 0 && mv.Progress < 1 {
			mv.LastPlay = c.Now.Add(-time.Duration(3+watchRank*20) * time.Hour)
			watchRank++
		} else if mv.Progress >= 1 {
			mv.LastPlay = c.daysAgo(14 + i*5)
		}
		mv.Clip = c.clipFor("movie:" + mv.Title)
		c.Movies = append(c.Movies, mv)
		c.register(mv.ID, mv)
	}
	for i, s := range shows {
		sh := &Show{ID: fmt.Sprintf("sh%02d", i+1), Index: i, Title: s.Title, Summary: s.Summary, Rating: s.Rating,
			Year: s.Year, Minutes: s.Minutes, Genres: s.Genres, Cast: showCast[s.Title], AddedAt: c.daysAgo(s.Added)}
		for si, eps := range s.Seasons {
			se := &Season{ID: fmt.Sprintf("%ss%02d", sh.ID, si+1), Show: sh, Index: si + 1}
			for ei, title := range eps {
				ep := &Episode{ID: fmt.Sprintf("%se%02d", se.ID, ei+1), Show: sh, Season: se, Index: ei + 1, Title: title,
					Summary: episodeSummary(s.Title, si, ei, title),
					Minutes: s.Minutes, Aired: c.daysAgo(s.Added + (len(eps)-ei)*7 + (len(s.Seasons)-si-1)*120)}
				if si < s.WatchedS || (si == s.WatchedS && ei < s.WatchedE) {
					ep.Watched = true
					ep.Progress = 1
				}
				ep.Clip = c.clipFor("episode:" + ep.ID)
				se.Episodes = append(se.Episodes, ep)
				c.Episodes = append(c.Episodes, ep)
				c.register(ep.ID, ep)
			}
			sh.Seasons = append(sh.Seasons, se)
			c.register(se.ID, se)
		}
		// Next up: the first unwatched episode after a watched one; a show
		// nobody started has none.
		if s.WatchedS > 0 || s.WatchedE > 0 {
			for _, se := range sh.Seasons {
				for _, ep := range se.Episodes {
					if !ep.Watched {
						sh.NextUp = ep
						break
					}
				}
				if sh.NextUp != nil {
					break
				}
			}
			if sh.NextUp != nil && i%2 == 0 {
				// Half of them were left part-way through.
				sh.NextUp.Progress = 0.3 + float64(i%3)*0.15
				sh.NextUp.LastPlay = c.Now.Add(-time.Duration(5+i*9) * time.Hour)
			}
		}
		c.Shows = append(c.Shows, sh)
		c.register(sh.ID, sh)
	}
}

func (e *Episode) lastPlay(c *Catalog) time.Time {
	if !e.LastPlay.IsZero() {
		return e.LastPlay
	}
	return c.Now.Add(-48 * time.Hour)
}

func (c *Catalog) clipFor(key string) string {
	// Hero items that have their own clip keep it; everything else shares.
	want := slug(strings.TrimPrefix(strings.TrimPrefix(key, "movie:"), "episode:"))
	for _, id := range c.Clips {
		if strings.EqualFold(id, want) {
			return id
		}
	}
	return c.Clips[int(hash(key))%len(c.Clips)]
}

func (c *Catalog) buildCollections() {
	for i, s := range collections {
		co := &Collection{ID: fmt.Sprintf("co%02d", i+1), Title: s.Title}
		for _, mi := range s.Movies {
			if mi < len(c.Movies) {
				co.Movies = append(co.Movies, c.Movies[mi])
			}
		}
		for _, si := range s.Shows {
			if si < len(c.Shows) {
				co.Shows = append(co.Shows, c.Shows[si])
			}
		}
		c.Collections = append(c.Collections, co)
		c.register(co.ID, co)
	}
	for i, s := range playlists {
		pl := &Playlist{ID: fmt.Sprintf("pl%02d", i+1), Title: s.Title, Kind: s.Kind}
		for _, ai := range s.Albums {
			if ai < len(c.Albums) && len(c.Albums[ai].Tracks) > 0 {
				pl.Tracks = append(pl.Tracks, c.Albums[ai].Tracks[0])
				if len(c.Albums[ai].Tracks) > 3 {
					pl.Tracks = append(pl.Tracks, c.Albums[ai].Tracks[3])
				}
			}
		}
		for _, mi := range s.Movies {
			if mi < len(c.Movies) {
				pl.Movies = append(pl.Movies, c.Movies[mi])
			}
		}
		c.Playlists = append(c.Playlists, pl)
		c.register(pl.ID, pl)
	}
}

// ---- keys ------------------------------------------------------------------

func (c *Catalog) assignKeys() {
	ids := make([]string, 0, len(c.byID))
	for id := range c.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		k := 1000 + i
		c.plexKey[id] = k
		c.plexByKey[k] = id
		h := md5.Sum([]byte("alexandria-showcase|" + id))
		j := hex.EncodeToString(h[:])
		c.jfID[id] = j
		c.jfByID[j] = id
	}
}

// personKeyBase starts the rating keys of people (cast and directors), far
// above the entity keys so the two ranges never meet.
const personKeyBase = 90000

// personPrefix marks a person's catalog id: "person|<name>". People get
// keys and Jellyfin ids (their portraits are fetched by them) but are not
// entities: Get returns nil for them.
const personPrefix = "person|"

// PersonID is the catalog id of a credited person.
func PersonID(name string) string { return personPrefix + name }

// registerPeople gives every credited person (film and series cast, film
// directors) a Plex rating key and a Jellyfin id, so each one's portrait
// has its own address.
func (c *Catalog) registerPeople() {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, m := range c.Movies {
		add(m.Director)
		for _, p := range m.Cast {
			add(p.Name)
		}
	}
	for _, s := range c.Shows {
		for _, p := range s.Cast {
			add(p.Name)
		}
	}
	sort.Strings(names)
	for i, n := range names {
		id := PersonID(n)
		k := personKeyBase + i
		c.plexKey[id] = k
		c.plexByKey[k] = id
		h := md5.Sum([]byte("alexandria-showcase|" + id))
		j := hex.EncodeToString(h[:])
		c.jfID[id] = j
		c.jfByID[j] = id
	}
}

// PlexKey is the numeric rating key for a catalog id (as a string).
func (c *Catalog) PlexKey(id string) string { return fmt.Sprintf("%d", c.plexKey[id]) }

// IDForPlexKey reverses PlexKey; "" when unknown.
func (c *Catalog) IDForPlexKey(key string) string {
	var k int
	if _, err := fmt.Sscanf(key, "%d", &k); err != nil {
		return ""
	}
	return c.plexByKey[k]
}

// JFID is the Jellyfin id for a catalog id.
func (c *Catalog) JFID(id string) string { return c.jfID[id] }

// IDForJF reverses JFID; also accepts a raw catalog id.
func (c *Catalog) IDForJF(j string) string {
	if id, ok := c.jfByID[strings.ToLower(j)]; ok {
		return id
	}
	if _, ok := c.byID[j]; ok {
		return j
	}
	return ""
}

// ---- helpers ---------------------------------------------------------------

func slug(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// InProgressBooks: unfinished, most recent first.
func (c *Catalog) InProgressBooks() []*Book {
	var out []*Book
	for _, b := range c.Books {
		if b.Progress > 0 && b.Progress < 1 {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastPlay.After(out[j].LastPlay) })
	return out
}

// FinishedBooks: most recently finished first.
func (c *Catalog) FinishedBooks() []*Book {
	var out []*Book
	for _, b := range c.Books {
		if b.Progress >= 1 {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastPlay.After(out[j].LastPlay) })
	return out
}

// RecentBooks: newest additions first.
func (c *Catalog) RecentBooks(n int) []*Book {
	out := append([]*Book(nil), c.Books...)
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// RecentSeries: by newest book.
func (c *Catalog) RecentSeries(n int) []*Series {
	out := append([]*Series(nil), c.Series...)
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ContinueSeries: the next unread book in a series the listener finished a book of.
func (c *Catalog) ContinueSeries() []*Book {
	var out []*Book
	for _, se := range c.Series {
		finishedOne := false
		for _, b := range se.Books {
			if b.Progress >= 1 {
				finishedOne = true
			} else if finishedOne && b.Progress == 0 {
				out = append(out, b)
				break
			}
		}
	}
	return out
}

// Recommended: unstarted books by authors the listener has finished, then others.
func (c *Catalog) Recommended(n int) []*Book {
	liked := map[*Author]bool{}
	for _, b := range c.FinishedBooks() {
		liked[b.Author] = true
	}
	var out []*Book
	for _, b := range c.Books {
		if b.Progress == 0 && liked[b.Author] {
			out = append(out, b)
		}
	}
	for _, b := range c.Books {
		if len(out) >= n {
			break
		}
		if b.Progress == 0 && !liked[b.Author] {
			out = append(out, b)
		}
	}
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// ContinueWatching: movies and episodes part-way through, most recent first.
func (c *Catalog) ContinueWatching() (movies []*Movie, episodes []*Episode) {
	for _, m := range c.Movies {
		if m.Progress > 0 && m.Progress < 1 {
			movies = append(movies, m)
		}
	}
	sort.Slice(movies, func(i, j int) bool { return movies[i].LastPlay.After(movies[j].LastPlay) })
	for _, s := range c.Shows {
		if s.NextUp != nil && s.NextUp.Progress > 0 {
			episodes = append(episodes, s.NextUp)
		}
	}
	sort.Slice(episodes, func(i, j int) bool { return episodes[i].lastPlay(c).After(episodes[j].lastPlay(c)) })
	return
}

// NextUp: the next unwatched episode per show the viewer is into, not yet started.
func (c *Catalog) NextUp() []*Episode {
	var out []*Episode
	for _, s := range c.Shows {
		if s.NextUp != nil && s.NextUp.Progress == 0 {
			out = append(out, s.NextUp)
		}
	}
	return out
}

func (c *Catalog) RecentMovies(n int) []*Movie {
	out := append([]*Movie(nil), c.Movies...)
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (c *Catalog) RecentShows(n int) []*Show {
	out := append([]*Show(nil), c.Shows...)
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (c *Catalog) RecentAlbums(n int) []*Album {
	out := append([]*Album(nil), c.Albums...)
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.After(out[j].AddedAt) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// PlayedAlbums: lately played, most recent first.
func (c *Catalog) PlayedAlbums() []*Album {
	var out []*Album
	for _, a := range c.Albums {
		if a.Played > 0 {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastPlay.After(out[j].LastPlay) })
	return out
}

// AllEpisodes returns every episode of a show in order.
func (s *Show) AllEpisodes() []*Episode {
	var out []*Episode
	for _, se := range s.Seasons {
		out = append(out, se.Episodes...)
	}
	return out
}

// Counts of watched and total episodes.
func (s *Show) Counts() (watched, total int) {
	for _, e := range s.AllEpisodes() {
		total++
		if e.Watched {
			watched++
		}
	}
	return
}

// Half is a view of the catalog holding every other film, show and artist
// (which = 0 the even ones, 1 the odd), so the Plex and Jellyfin dialects
// served from one catalog look like two different servers instead of the
// same library twice on a Home that merges both. Books are shared: only
// the Audiobookshelf dialect serves them. Collections and playlists are
// copied with the members that fall in this half, and dropped when empty.
func (c *Catalog) Half(which int) *Catalog {
	v := *c
	v.byID = make(map[string]any, len(c.byID))
	for k, val := range c.byID {
		v.byID[k] = val
	}
	v.Movies, v.Shows, v.Episodes, v.Artists, v.Albums, v.Tracks = nil, nil, nil, nil, nil, nil
	keepMovie := map[*Movie]bool{}
	for i, m := range c.Movies {
		if i%2 == which {
			v.Movies = append(v.Movies, m)
			keepMovie[m] = true
		}
	}
	keepShow := map[*Show]bool{}
	for i, s := range c.Shows {
		if i%2 == which {
			v.Shows = append(v.Shows, s)
			keepShow[s] = true
			v.Episodes = append(v.Episodes, s.AllEpisodes()...)
		}
	}
	keepTrack := map[*Track]bool{}
	for i, a := range c.Artists {
		if i%2 == which {
			v.Artists = append(v.Artists, a)
			for _, al := range a.Albums {
				v.Albums = append(v.Albums, al)
				for _, t := range al.Tracks {
					v.Tracks = append(v.Tracks, t)
					keepTrack[t] = true
				}
			}
		}
	}
	v.Collections = nil
	for _, co := range c.Collections {
		nc := &Collection{ID: co.ID, Title: co.Title}
		for _, m := range co.Movies {
			if keepMovie[m] {
				nc.Movies = append(nc.Movies, m)
			}
		}
		for _, s := range co.Shows {
			if keepShow[s] {
				nc.Shows = append(nc.Shows, s)
			}
		}
		if len(nc.Movies)+len(nc.Shows) == 0 {
			continue
		}
		v.Collections = append(v.Collections, nc)
		v.byID[nc.ID] = nc
	}
	v.Playlists = nil
	for _, pl := range c.Playlists {
		np := &Playlist{ID: pl.ID, Title: pl.Title, Kind: pl.Kind}
		for _, t := range pl.Tracks {
			if keepTrack[t] {
				np.Tracks = append(np.Tracks, t)
			}
		}
		for _, m := range pl.Movies {
			if keepMovie[m] {
				np.Movies = append(np.Movies, m)
			}
		}
		if len(np.Tracks)+len(np.Movies) == 0 {
			continue
		}
		v.Playlists = append(v.Playlists, np)
		v.byID[np.ID] = np
	}
	return &v
}
