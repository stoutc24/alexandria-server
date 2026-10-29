package showcase

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Plex dialect — see docs/showcase-api-inventory.md. JSON throughout,
// everything inside {"MediaContainer": …}. Library sections: 1 Movies,
// 2 TV Shows, 3 Music.

const (
	plexMachineID = "showcase0000000000000000000000000000abcd"
	plexMovies    = "1"
	plexShows     = "2"
	plexMusic     = "3"
)

func (s *Server) PlexHandler() http.Handler {
	return s.logging("plex", http.HandlerFunc(s.plex))
}

func mc(fields map[string]any) map[string]any {
	base := map[string]any{"size": 0, "identifier": "com.plexapp.plugins.library", "mediaTagPrefix": "/system/bundle/media/flags/", "mediaTagVersion": 1700000000}
	for k, v := range fields {
		base[k] = v
	}
	if md, isList := base["Metadata"].([]map[string]any); isList {
		base["size"] = len(md)
	} else if d, isDir := base["Directory"].([]map[string]any); isDir {
		base["size"] = len(d)
	}
	return map[string]any{"MediaContainer": base}
}

func (s *Server) plex(w http.ResponseWriter, r *http.Request) {
	p := pathParts(r.URL.Path)
	if len(p) == 0 {
		ok(w, mc(map[string]any{"friendlyName": "Alexandria Showcase", "machineIdentifier": plexMachineID, "version": "1.41.0.8992", "platform": "Linux", "myPlex": false, "allowSync": true, "transcoderVideo": true, "transcoderAudio": true}))
		return
	}
	switch p[0] {
	case "__showcase":
		// GET /__showcase/parts → the song-file requests; DELETE clears;
		// POST ?refusePlain=1|0 turns RefusePlainParts on or off (and
		// clears), so one probe run covers both.
		if len(p) != 2 || p[1] != "parts" {
			notFound(w)
			return
		}
		if r.Method == http.MethodPost {
			s.mu.Lock()
			s.RefusePlainParts = qStr(r, "refusePlain") == "1"
			s.mu.Unlock()
		}
		ok(w, map[string]any{"parts": s.recordedParts(r.Method != http.MethodGet), "refusePlain": s.refusesPlain()})
	case "identity":
		ok(w, mc(map[string]any{"claimed": true, "machineIdentifier": plexMachineID, "version": "1.41.0.8992"}))
	case "library":
		s.plexLibrary(w, r, p[1:])
	case "playlists":
		s.plexPlaylists(w, r, p[1:])
	case "hubs":
		s.plexHubs(w, r, p[1:])
	case "video", "music":
		s.plexTranscode(w, r, p)
	case "photo":
		s.plexPhoto(w, r)
	case ":":
		// /:/timeline, /:/scrobble, /:/unscrobble, /:/progress
		w.WriteHeader(http.StatusOK)
	case "playQueues":
		s.plexPlayQueue(w, r)
	case "status":
		ok(w, mc(map[string]any{"Metadata": []map[string]any{}}))
	case "livetv", "tv.plex.providers.epg.cloud:2", "media":
		notFound(w)
	case "search":
		s.plexSearch(w, r)
	case "servers":
		ok(w, mc(map[string]any{"Server": []map[string]any{{"name": "Alexandria Showcase", "host": r.Host, "machineIdentifier": plexMachineID, "version": "1.41.0.8992"}}}))
	default:
		notFound(w)
	}
}

// ---- common item JSON ------------------------------------------------------

func (s *Server) plexArt(id string) (thumb, art string) {
	k := s.cat.PlexKey(id)
	return fmt.Sprintf("/library/metadata/%s/thumb/1", k), fmt.Sprintf("/library/metadata/%s/art/1", k)
}

func tags(names []string) []map[string]any {
	var out []map[string]any
	for i, n := range names {
		out = append(out, map[string]any{"id": i + 1, "filter": "tag=" + strconv.Itoa(i+1), "tag": n})
	}
	return out
}

// plexRoles is a Role array: each person with the part they play and a
// thumb at their own rating key (a generated portrait, see Portrait).
func (s *Server) plexRoles(cast []castMember) []map[string]any {
	var out []map[string]any
	for i, c := range cast {
		k := s.cat.PlexKey(PersonID(c.Name))
		out = append(out, map[string]any{"id": 500 + i, "filter": "actor=" + strconv.Itoa(500+i), "tag": c.Name, "role": c.Role,
			"thumb": fmt.Sprintf("/library/metadata/%s/thumb/1", k)})
	}
	return out
}

func (s *Server) plexVideoMedia(id, clip string, durationMs int64, uhd bool) []map[string]any {
	w, h, res, bitrate := 1280, 720, "720", 3000
	if uhd {
		w, h, res, bitrate = 3840, 2160, "4k", 24000
	}
	partID := s.cat.plexKey[id]*10 + 1
	size := s.clipSize(clip)
	if size == 0 {
		size = durationMs * 400
	}
	return []map[string]any{{
		"id": s.cat.plexKey[id], "duration": durationMs, "bitrate": bitrate, "width": w, "height": h, "aspectRatio": 1.78,
		"audioChannels": 1, "audioCodec": "aac", "videoCodec": "h264", "videoResolution": res, "container": "mp4", "videoFrameRate": "24p",
		"videoProfile": "high", "hasVoiceActivity": false,
		"Part": []map[string]any{{
			"id": partID, "key": fmt.Sprintf("/library/parts/%d/file.mp4", partID), "duration": durationMs,
			"file": "/video/" + clip + "/clip.mp4", "size": size, "container": "mp4", "videoProfile": "high",
			"Stream": []map[string]any{
				{"id": partID*10 + 1, "streamType": 1, "default": true, "codec": "h264", "index": 0, "bitrate": bitrate, "language": "English", "languageTag": "en", "languageCode": "eng",
					"bitDepth": 8, "chromaLocation": "left", "chromaSubsampling": "4:2:0", "codedHeight": h, "codedWidth": w, "colorPrimaries": "bt709", "colorRange": "tv", "colorSpace": "bt709", "colorTrc": "bt709",
					"frameRate": 24.0, "height": h, "width": w, "level": 40, "profile": "high", "refFrames": 1, "displayTitle": fmt.Sprintf("%sp (H.264)", map[bool]string{true: "2160", false: "720"}[uhd]), "extendedDisplayTitle": fmt.Sprintf("%sp (H.264)", map[bool]string{true: "2160", false: "720"}[uhd])},
				{"id": partID*10 + 2, "streamType": 2, "selected": true, "default": true, "codec": "aac", "index": 1, "channels": 1, "bitrate": 96, "language": "English", "languageTag": "en", "languageCode": "eng",
					"audioChannelLayout": "mono", "samplingRate": 44100, "displayTitle": "English (AAC Mono)", "extendedDisplayTitle": "English (AAC Mono)"},
			},
		}},
	}}
}

func (s *Server) plexMovie(m *Movie, full bool) map[string]any {
	k := s.cat.PlexKey(m.ID)
	thumb, art := s.plexArt(m.ID)
	durMs := int64(m.Minutes) * 60000
	prog := s.progressOf(m.ID, m.Progress)
	item := map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k, "guid": "plex://movie/" + s.cat.JFID(m.ID), "studio": "Showcase Pictures", "type": "movie",
		"title": m.Title, "titleSort": strings.TrimPrefix(m.Title, "The "), "librarySectionTitle": "Movies", "librarySectionID": 1, "librarySectionKey": "/library/sections/1",
		"contentRating": m.Rating, "summary": m.Summary, "tagline": m.Tagline, "rating": m.Score, "audienceRating": m.Score + 0.3, "year": m.Year,
		"thumb": thumb, "art": art, "duration": durMs, "originallyAvailableAt": fmt.Sprintf("%d-05-10", m.Year),
		"addedAt": m.AddedAt.Unix(), "updatedAt": m.AddedAt.Unix(), "audienceRatingImage": "rottentomatoes://image.rating.upright", "ratingImage": "imdb://image.rating",
		"Genre": tags(m.Genres), "Director": tags([]string{m.Director}), "Writer": tags([]string{m.Director}),
		"Role":    s.plexRoles(m.Cast),
		"Country": tags([]string{"United States of America"}),
	}
	if prog > 0 && prog < 1 {
		item["viewOffset"] = int64(prog * float64(durMs))
		item["lastViewedAt"] = m.LastPlay.Unix()
	} else if prog >= 1 {
		item["viewCount"] = 1
		item["lastViewedAt"] = m.LastPlay.Unix()
	}
	item["Media"] = s.plexVideoMedia(m.ID, m.Clip, durMs, m.UHD)
	if full {
		item["Marker"] = []map[string]any{
			{"id": 1, "type": "intro", "startTimeOffset": 20000, "endTimeOffset": 50000, "final": false},
			{"id": 2, "type": "credits", "startTimeOffset": durMs - 90000, "endTimeOffset": durMs, "final": true},
		}
		item["Extras"] = map[string]any{"size": 0, "Metadata": []any{}}
		item["Chapter"] = []map[string]any{{"id": 1, "index": 1, "startTimeOffset": 0, "endTimeOffset": durMs / 3, "tag": "Opening"}, {"id": 2, "index": 2, "startTimeOffset": durMs / 3, "endTimeOffset": durMs, "tag": "Act Two"}}
	}
	return item
}

func (s *Server) plexShow(sh *Show) map[string]any {
	k := s.cat.PlexKey(sh.ID)
	thumb, art := s.plexArt(sh.ID)
	watched, total := sh.Counts()
	item := map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k + "/children", "guid": "plex://show/" + s.cat.JFID(sh.ID), "studio": "Showcase Television", "type": "show",
		"title": sh.Title, "titleSort": strings.TrimPrefix(sh.Title, "The "), "librarySectionTitle": "TV Shows", "librarySectionID": 2, "librarySectionKey": "/library/sections/2",
		"contentRating": sh.Rating, "summary": sh.Summary, "index": 1, "rating": 7.9, "audienceRating": 8.1, "year": sh.Year, "thumb": thumb, "art": art,
		"duration": int64(sh.Minutes) * 60000, "originallyAvailableAt": fmt.Sprintf("%d-09-12", sh.Year), "leafCount": total, "viewedLeafCount": watched, "childCount": len(sh.Seasons),
		"addedAt": sh.AddedAt.Unix(), "updatedAt": sh.AddedAt.Unix(), "Genre": tags(sh.Genres), "Role": s.plexRoles(sh.Cast),
	}
	if sh.NextUp != nil && !sh.NextUp.LastPlay.IsZero() {
		item["lastViewedAt"] = sh.NextUp.LastPlay.Unix()
	}
	return item
}

func (s *Server) plexSeason(se *Season) map[string]any {
	k := s.cat.PlexKey(se.ID)
	thumb, art := s.plexArt(se.ID)
	watched := 0
	for _, e := range se.Episodes {
		if e.Watched {
			watched++
		}
	}
	return map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k + "/children", "parentRatingKey": s.cat.PlexKey(se.Show.ID), "guid": "plex://season/" + s.cat.JFID(se.ID), "type": "season",
		"title": fmt.Sprintf("Season %d", se.Index), "parentKey": "/library/metadata/" + s.cat.PlexKey(se.Show.ID), "parentTitle": se.Show.Title, "summary": "", "index": se.Index,
		"parentIndex": 1, "year": se.Show.Year, "thumb": thumb, "art": art, "parentThumb": func() string { t, _ := s.plexArt(se.Show.ID); return t }(),
		"leafCount": len(se.Episodes), "viewedLeafCount": watched, "addedAt": se.Show.AddedAt.Unix(), "updatedAt": se.Show.AddedAt.Unix(),
		"librarySectionID": 2, "librarySectionTitle": "TV Shows",
	}
}

func (s *Server) plexEpisode(e *Episode, full bool) map[string]any {
	k := s.cat.PlexKey(e.ID)
	thumb, _ := s.plexArt(e.ID)
	showThumb, showArt := s.plexArt(e.Show.ID)
	seasonThumb, _ := s.plexArt(e.Season.ID)
	durMs := int64(e.Minutes) * 60000
	prog := s.progressOf(e.ID, e.Progress)
	item := map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k, "parentRatingKey": s.cat.PlexKey(e.Season.ID), "grandparentRatingKey": s.cat.PlexKey(e.Show.ID),
		"guid": "plex://episode/" + s.cat.JFID(e.ID), "type": "episode", "title": e.Title, "grandparentKey": "/library/metadata/" + s.cat.PlexKey(e.Show.ID),
		"parentKey": "/library/metadata/" + s.cat.PlexKey(e.Season.ID), "grandparentTitle": e.Show.Title, "parentTitle": fmt.Sprintf("Season %d", e.Season.Index),
		"contentRating": e.Show.Rating, "summary": e.Summary, "index": e.Index, "parentIndex": e.Season.Index, "year": e.Aired.Year(),
		"thumb": thumb, "art": showArt, "parentThumb": seasonThumb, "grandparentThumb": showThumb, "grandparentArt": showArt,
		"duration": durMs, "originallyAvailableAt": e.Aired.Format("2006-01-02"), "addedAt": e.Aired.Unix(), "updatedAt": e.Aired.Unix(),
		"librarySectionID": 2, "librarySectionTitle": "TV Shows", "librarySectionKey": "/library/sections/2",
	}
	if prog > 0 && prog < 1 {
		item["viewOffset"] = int64(prog * float64(durMs))
		item["lastViewedAt"] = e.lastPlay(s.cat).Unix()
	} else if e.Watched {
		item["viewCount"] = 1
		item["lastViewedAt"] = e.lastPlay(s.cat).Unix()
	}
	item["Media"] = s.plexVideoMedia(e.ID, e.Clip, durMs, false)
	if full {
		item["Marker"] = []map[string]any{{"id": 1, "type": "intro", "startTimeOffset": 15000, "endTimeOffset": 60000, "final": false}}
		item["Extras"] = map[string]any{"size": 0, "Metadata": []any{}}
	}
	return item
}

func (s *Server) plexArtist(a *Artist) map[string]any {
	k := s.cat.PlexKey(a.ID)
	thumb, art := s.plexArt(a.ID)
	return map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k + "/children", "guid": "plex://artist/" + s.cat.JFID(a.ID), "type": "artist",
		"title": a.Name, "titleSort": strings.TrimPrefix(a.Name, "The "), "summary": a.Bio, "index": 1, "thumb": thumb, "art": art,
		"addedAt": s.cat.daysAgo(380).Unix(), "updatedAt": s.cat.daysAgo(380).Unix(), "childCount": len(a.Albums),
		"librarySectionID": 3, "librarySectionTitle": "Music", "librarySectionKey": "/library/sections/3",
		"Genre": tags([]string{a.Genre}), "Country": tags([]string{"United States of America"}),
	}
}

func (s *Server) plexAlbum(a *Album) map[string]any {
	k := s.cat.PlexKey(a.ID)
	thumb, art := s.plexArt(a.ID)
	parentThumb, _ := s.plexArt(a.Artist.ID)
	item := map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k + "/children", "parentRatingKey": s.cat.PlexKey(a.Artist.ID), "guid": "plex://album/" + s.cat.JFID(a.ID),
		"parentGuid": "plex://artist/" + s.cat.JFID(a.Artist.ID), "studio": "Harbor Records", "type": "album", "title": a.Title, "titleSort": strings.TrimPrefix(a.Title, "The "),
		"parentKey": "/library/metadata/" + s.cat.PlexKey(a.Artist.ID), "parentTitle": a.Artist.Name, "summary": fmt.Sprintf("%s by %s.", a.Title, a.Artist.Name),
		"index": 1, "year": a.Year, "thumb": thumb, "art": art, "parentThumb": parentThumb, "originallyAvailableAt": fmt.Sprintf("%d-06-01", a.Year),
		"leafCount": len(a.Tracks), "addedAt": a.AddedAt.Unix(), "updatedAt": a.AddedAt.Unix(), "loudnessAnalysisVersion": "2", "duration": int64(a.Duration() * 1000),
		"librarySectionID": 3, "librarySectionTitle": "Music", "librarySectionKey": "/library/sections/3",
		"Genre": tags([]string{a.Genre}), "Mood": tags([]string{"Relaxed", "Warm"}),
	}
	if a.Played > 0 {
		item["viewCount"] = int(a.Played * float64(len(a.Tracks)))
		item["lastViewedAt"] = a.LastPlay.Unix()
	}
	return item
}

func (s *Server) plexTrack(t *Track) map[string]any {
	k := s.cat.PlexKey(t.ID)
	albumThumb, _ := s.plexArt(t.Album.ID)
	artistThumb, _ := s.plexArt(t.Album.Artist.ID)
	partID := s.cat.plexKey[t.ID]*10 + 1
	durMs := int64(t.Duration * 1000)
	item := map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k, "parentRatingKey": s.cat.PlexKey(t.Album.ID), "grandparentRatingKey": s.cat.PlexKey(t.Album.Artist.ID),
		"guid": "plex://track/" + s.cat.JFID(t.ID), "type": "track", "title": t.Title, "titleSort": t.Title, "grandparentKey": "/library/metadata/" + s.cat.PlexKey(t.Album.Artist.ID),
		"parentKey": "/library/metadata/" + s.cat.PlexKey(t.Album.ID), "grandparentTitle": t.Album.Artist.Name, "parentTitle": t.Album.Title, "originalTitle": t.Album.Artist.Name,
		"summary": "", "index": t.Index, "parentIndex": 1, "parentYear": t.Album.Year, "thumb": albumThumb, "parentThumb": albumThumb, "grandparentThumb": artistThumb,
		"duration": durMs, "addedAt": t.Album.AddedAt.Unix(), "updatedAt": t.Album.AddedAt.Unix(), "ratingCount": 100 + int(t.Seed%900),
		"librarySectionID": 3, "librarySectionTitle": "Music", "librarySectionKey": "/library/sections/3",
		"Media": []map[string]any{{
			"id": s.cat.plexKey[t.ID], "duration": durMs, "bitrate": 353, "audioChannels": 1, "audioCodec": s.plexTrackCodec(), "container": "wav",
			"Part": []map[string]any{{
				"id": partID, "key": fmt.Sprintf("/library/parts/%d/file.wav", partID), "duration": durMs, "file": "/music/" + slug(t.Album.Artist.Name) + "/" + slug(t.Album.Title) + "/" + slug(t.Title) + ".wav",
				"size": WAVSize(t.Duration), "container": "wav", "hasThumbnail": "0",
				"Stream": []map[string]any{{"id": partID*10 + 2, "streamType": 2, "selected": true, "default": true, "codec": "pcm_s16le", "index": 0, "channels": 1, "bitrate": 353,
					"audioChannelLayout": "mono", "samplingRate": 22050, "bitDepth": 16, "displayTitle": "PCM Mono", "extendedDisplayTitle": "PCM Mono"}},
			}},
		}},
	}
	if t.Plays > 0 {
		item["viewCount"] = t.Plays
		item["lastViewedAt"] = t.Album.LastPlay.Unix()
	}
	return item
}

func (s *Server) plexCollection(c *Collection) map[string]any {
	k := s.cat.PlexKey(c.ID)
	thumb, art := s.plexArt(c.ID)
	return map[string]any{
		"ratingKey": k, "key": "/library/metadata/" + k + "/children", "guid": "collection://" + s.cat.JFID(c.ID), "type": "collection", "title": c.Title,
		"titleSort": c.Title, "subtype": "movie", "summary": fmt.Sprintf("%d titles, hand-picked.", len(c.Movies)+len(c.Shows)), "index": 1, "thumb": thumb, "art": art,
		"childCount": len(c.Movies) + len(c.Shows), "addedAt": s.cat.daysAgo(60).Unix(), "updatedAt": s.cat.daysAgo(60).Unix(), "minYear": 2018, "maxYear": 2024,
		"librarySectionID": 1, "librarySectionTitle": "Movies",
	}
}

func (s *Server) plexPlaylist(p *Playlist) map[string]any {
	k := s.cat.PlexKey(p.ID)
	thumb, _ := s.plexArt(p.ID)
	var durMs int64
	for _, t := range p.Tracks {
		durMs += int64(t.Duration * 1000)
	}
	for _, m := range p.Movies {
		durMs += int64(m.Minutes) * 60000
	}
	return map[string]any{
		"ratingKey": k, "key": "/playlists/" + k + "/items", "guid": "com.plexapp.agents.none://" + s.cat.JFID(p.ID), "type": "playlist", "title": p.Title,
		"titleSort": p.Title, "summary": "", "smart": false, "playlistType": p.Kind, "composite": thumb, "icon": "playlist://image.smart",
		"duration": durMs, "leafCount": len(p.Tracks) + len(p.Movies), "addedAt": s.cat.daysAgo(30).Unix(), "updatedAt": s.cat.daysAgo(30).Unix(),
	}
}

// plexAny renders any entity by catalog id, in list form.
func (s *Server) plexAny(id string, full bool) map[string]any {
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		return s.plexMovie(v, full)
	case *Show:
		return s.plexShow(v)
	case *Season:
		return s.plexSeason(v)
	case *Episode:
		return s.plexEpisode(v, full)
	case *Artist:
		return s.plexArtist(v)
	case *Album:
		return s.plexAlbum(v)
	case *Track:
		return s.plexTrack(v)
	case *Collection:
		return s.plexCollection(v)
	case *Playlist:
		return s.plexPlaylist(v)
	}
	return nil
}

// ---- /library --------------------------------------------------------------

func (s *Server) plexSections() []map[string]any {
	sec := func(key, title, typ, agent, scanner string) map[string]any {
		return map[string]any{"allowSync": true, "art": "/:/resources/" + typ + "-fanart.jpg", "composite": "/library/sections/" + key + "/composite/1", "filters": true, "refreshing": false,
			"thumb": "/:/resources/" + typ + ".png", "key": key, "type": typ, "title": title, "agent": agent, "scanner": scanner, "language": "en-US", "uuid": s.cat.JFID("section|" + key),
			"updatedAt": s.cat.daysAgo(1).Unix(), "createdAt": s.cat.daysAgo(400).Unix(), "scannedAt": s.cat.daysAgo(1).Unix(), "content": true, "directory": true, "contentChangedAt": 1, "hidden": 0,
			"Location": []map[string]any{{"id": 1, "path": "/media/" + strings.ToLower(title)}}}
	}
	return []map[string]any{
		sec(plexMovies, "Movies", "movie", "tv.plex.agents.movie", "Plex Movie"),
		sec(plexShows, "TV Shows", "show", "tv.plex.agents.series", "Plex TV Series"),
		sec(plexMusic, "Music", "artist", "tv.plex.agents.music", "Plex Music"),
	}
}

func (s *Server) plexLibrary(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		ok(w, mc(map[string]any{"Directory": []map[string]any{{"key": "sections", "title": "sections"}, {"key": "onDeck", "title": "onDeck"}}}))
		return
	}
	switch p[0] {
	case "sections":
		s.plexSections2(w, r, p[1:])
	case "onDeck":
		ok(w, mc(map[string]any{"Metadata": s.plexOnDeck("")}))
	case "recentlyAdded":
		ok(w, mc(map[string]any{"Metadata": s.plexRecentlyAdded("", 0, 30)}))
	case "metadata":
		s.plexMetadata(w, r, p[1:])
	case "parts":
		s.plexParts(w, r, p[1:])
	case "collections", "all":
		ok(w, mc(map[string]any{"Metadata": []map[string]any{}}))
	default:
		notFound(w)
	}
}

func (s *Server) plexSections2(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		ok(w, mc(map[string]any{"title1": "Plex Library", "Directory": s.plexSections()}))
		return
	}
	key := p[0]
	if len(p) == 1 {
		ok(w, mc(map[string]any{"title1": "Library", "Directory": []map[string]any{{"key": "all", "title": "All"}, {"key": "onDeck", "title": "On Deck"}, {"key": "recentlyAdded", "title": "Recently Added"}, {"key": "collections", "title": "Collections"}}}))
		return
	}
	switch p[1] {
	case "all":
		s.plexAll(w, r, key)
	case "onDeck":
		ok(w, mc(map[string]any{"Metadata": s.plexOnDeck(key)}))
	case "recentlyAdded":
		typ := qInt(r, "type", 0)
		ok(w, mc(map[string]any{"Metadata": s.plexRecentlyAdded(key, typ, qInt(r, "X-Plex-Container-Size", 20))}))
	case "collections":
		var out []map[string]any
		if key == plexMovies || key == plexShows {
			for _, c := range s.cat.Collections {
				out = append(out, s.plexCollection(c))
			}
		}
		if out == nil {
			out = []map[string]any{}
		}
		ok(w, mc(map[string]any{"Metadata": out}))
	case "playlists":
		ok(w, mc(map[string]any{"Metadata": []map[string]any{}}))
	case "search":
		s.plexSearch(w, r)
	case "composite":
		serveJPEG(w, s.art("section|"+key, artSquare, 0))
	default:
		notFound(w)
	}
}

// plexAll answers /library/sections/{k}/all with the type filters the apps use.
func (s *Server) plexAll(w http.ResponseWriter, r *http.Request, key string) {
	typ := qInt(r, "type", 0)
	var items []map[string]any
	artistFilter := qStr(r, "artist.id")
	switch key {
	case plexMovies:
		if typ == 0 || typ == 1 {
			for _, m := range s.cat.Movies {
				items = append(items, s.plexMovie(m, false))
			}
		}
	case plexShows:
		switch typ {
		case 0, 2:
			for _, sh := range s.cat.Shows {
				items = append(items, s.plexShow(sh))
			}
		case 4:
			for _, e := range s.cat.Episodes {
				items = append(items, s.plexEpisode(e, false))
			}
		}
	case plexMusic:
		switch typ {
		case 0, 8:
			for _, a := range s.cat.Artists {
				items = append(items, s.plexArtist(a))
			}
		case 9:
			for _, a := range s.cat.Albums {
				if artistFilter != "" && s.cat.PlexKey(a.Artist.ID) != artistFilter {
					continue
				}
				items = append(items, s.plexAlbum(a))
			}
		case 10:
			for _, t := range s.cat.Tracks {
				if artistFilter != "" && s.cat.PlexKey(t.Album.Artist.ID) != artistFilter {
					continue
				}
				items = append(items, s.plexTrack(t))
			}
		case 15:
			items = []map[string]any{}
		}
	}
	switch strings.Split(qStr(r, "sort"), ":")[0] {
	case "addedAt":
		sort.SliceStable(items, func(i, j int) bool { return toInt64(items[i]["addedAt"]) > toInt64(items[j]["addedAt"]) })
	case "lastViewedAt", "viewCount", "ratingCount":
		sort.SliceStable(items, func(i, j int) bool {
			return toInt64(items[i]["lastViewedAt"])+toInt64(items[i]["viewCount"]) > toInt64(items[j]["lastViewedAt"])+toInt64(items[j]["viewCount"])
		})
	case "titleSort", "":
		// Plex answers in titleSort order, and the TV shows the server's
		// order under "by Title". Films are sorted; the other lists keep
		// catalog order, which the store tours walk by position.
		if key == plexMovies {
			sort.SliceStable(items, func(i, j int) bool {
				a, _ := items[i]["titleSort"].(string)
				b, _ := items[j]["titleSort"].(string)
				return strings.ToLower(a) < strings.ToLower(b)
			})
		}
	}
	total := len(items)
	start := qInt(r, "X-Plex-Container-Start", 0)
	size := qInt(r, "X-Plex-Container-Size", 0)
	if n, err := strconv.Atoi(qStr(r, "limit")); err == nil && n > 0 && size == 0 {
		size = n
	}
	if start > len(items) {
		start = len(items)
	}
	items = items[start:]
	if size > 0 && len(items) > size {
		items = items[:size]
	}
	if items == nil {
		items = []map[string]any{}
	}
	ok(w, mc(map[string]any{"Metadata": items, "totalSize": total, "offset": start, "librarySectionID": key, "librarySectionTitle": s.plexSectionTitle(key), "viewGroup": plexViewGroup(typ)}))
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

func (s *Server) plexSectionTitle(key string) string {
	switch key {
	case plexMovies:
		return "Movies"
	case plexShows:
		return "TV Shows"
	case plexMusic:
		return "Music"
	}
	return ""
}

func plexViewGroup(typ int) string {
	switch typ {
	case 2:
		return "show"
	case 4:
		return "episode"
	case 8:
		return "artist"
	case 9:
		return "album"
	case 10:
		return "track"
	}
	return "movie"
}

func (s *Server) plexOnDeck(section string) []map[string]any {
	var items []map[string]any
	movies, episodes := s.cat.ContinueWatching()
	if section == "" || section == plexMovies {
		for _, m := range movies {
			items = append(items, s.plexMovie(m, false))
		}
	}
	if section == "" || section == plexShows {
		for _, e := range episodes {
			items = append(items, s.plexEpisode(e, false))
		}
		for _, e := range s.cat.NextUp() {
			items = append(items, s.plexEpisode(e, false))
		}
	}
	if section == "" || section == plexMusic {
		for _, a := range s.cat.PlayedAlbums() {
			if a.Played < 1 && len(a.Tracks) > 2 {
				items = append(items, s.plexTrack(a.Tracks[2]))
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

func (s *Server) plexRecentlyAdded(section string, typ, limit int) []map[string]any {
	var items []map[string]any
	if (section == "" || section == plexMovies) && (typ == 0 || typ == 1) {
		for _, m := range s.cat.RecentMovies(limit) {
			items = append(items, s.plexMovie(m, false))
		}
	}
	if (section == "" || section == plexShows) && (typ == 0 || typ == 2) {
		for _, sh := range s.cat.RecentShows(limit) {
			items = append(items, s.plexShow(sh))
		}
	}
	if (section == "" || section == plexMusic) && (typ == 0 || typ == 9 || typ == 8) {
		for _, a := range s.cat.RecentAlbums(limit) {
			items = append(items, s.plexAlbum(a))
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return toInt64(items[i]["addedAt"]) > toInt64(items[j]["addedAt"]) })
	if len(items) > limit {
		items = items[:limit]
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

// ---- /library/metadata/{rk}[/children|/allLeaves|/similar|/thumb|/art] ----

func (s *Server) plexMetadata(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		notFound(w)
		return
	}
	id := s.cat.IDForPlexKey(p[0])
	if id == "" {
		// Unknown keys still get art (people have their own keys; see registerPeople).
		if len(p) >= 2 && (p[1] == "thumb" || p[1] == "art") {
			serveJPEG(w, s.art("person", artSquare, 0))
			return
		}
		notFound(w)
		return
	}
	if len(p) == 1 {
		item := s.plexAny(id, true)
		if item == nil {
			notFound(w)
			return
		}
		ok(w, mc(map[string]any{"Metadata": []map[string]any{item}, "librarySectionID": item["librarySectionID"], "librarySectionTitle": item["librarySectionTitle"]}))
		return
	}
	switch p[1] {
	case "thumb", "art", "composite", "poster", "banner":
		kind := artPoster
		if p[1] == "art" {
			// Backdrops carry no type: the apps set the title over them.
			kind = artBackdrop
		}
		switch s.cat.Get(id).(type) {
		case *Album, *Artist, *Playlist, *Track:
			kind = artSquare
		case *Episode:
			kind = artWide
		}
		serveJPEG(w, s.art(id, kind, qInt(r, "width", 0)))
	case "children":
		ok(w, mc(map[string]any{"Metadata": s.plexChildren(id)}))
	case "allLeaves":
		if s.AllLeavesDelay > 0 {
			time.Sleep(s.AllLeavesDelay)
		}
		var items []map[string]any
		if sh, isShow := s.cat.Get(id).(*Show); isShow {
			for _, e := range sh.AllEpisodes() {
				items = append(items, s.plexEpisode(e, false))
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		ok(w, mc(map[string]any{"Metadata": items}))
	case "similar", "related":
		var items []map[string]any
		switch v := s.cat.Get(id).(type) {
		case *Artist:
			for _, o := range s.cat.Artists {
				if o != v && o.Genre == v.Genre {
					items = append(items, s.plexArtist(o))
				}
			}
		case *Movie:
			for _, o := range s.cat.Movies {
				if o != v && len(o.Genres) > 0 && len(v.Genres) > 0 && o.Genres[0] == v.Genres[0] {
					items = append(items, s.plexMovie(o, false))
				}
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		ok(w, mc(map[string]any{"Metadata": items}))
	default:
		notFound(w)
	}
}

func (s *Server) plexChildren(id string) []map[string]any {
	var items []map[string]any
	switch v := s.cat.Get(id).(type) {
	case *Show:
		for _, se := range v.Seasons {
			items = append(items, s.plexSeason(se))
		}
	case *Season:
		for _, e := range v.Episodes {
			items = append(items, s.plexEpisode(e, false))
		}
	case *Artist:
		for _, a := range v.Albums {
			items = append(items, s.plexAlbum(a))
		}
	case *Album:
		for _, t := range v.Tracks {
			items = append(items, s.plexTrack(t))
		}
	case *Collection:
		for _, m := range v.Movies {
			items = append(items, s.plexMovie(m, false))
		}
		for _, sh := range v.Shows {
			items = append(items, s.plexShow(sh))
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

// ---- parts (media files, BIF) ----------------------------------------------

func (s *Server) plexParts(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		notFound(w)
		return
	}
	partID, _ := strconv.Atoi(p[0])
	id := s.cat.plexByKey[partID/10]
	if r.Method == http.MethodPut {
		w.WriteHeader(http.StatusOK)
		return
	}
	if len(p) >= 2 && p[1] == "indexes" {
		notFound(w) // no BIF previews in the showcase
		return
	}
	switch v := s.cat.Get(id).(type) {
	case *Track:
		download := r.URL.Query().Get("download") == "1"
		if s.refusesPlain() && !download {
			s.recordPart(partRequest{Server: "plex", Part: partID, Download: download, Status: http.StatusInternalServerError})
			http.Error(w, "refused without download=1", http.StatusInternalServerError)
			return
		}
		s.recordPart(partRequest{Server: "plex", Part: partID, Download: download, Status: http.StatusOK})
		serveWAV(w, r, v.Seed, v.Duration)
	case *Movie:
		s.serveClipFile(w, r, v.Clip, "clip.mp4")
	case *Episode:
		s.serveClipFile(w, r, v.Clip, "clip.mp4")
	default:
		notFound(w)
	}
}

// ---- playlists / hubs / search --------------------------------------------

func (s *Server) plexPlaylists(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		kind := qStr(r, "playlistType")
		var items []map[string]any
		for _, pl := range s.cat.Playlists {
			if kind == "" || pl.Kind == kind {
				items = append(items, s.plexPlaylist(pl))
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		ok(w, mc(map[string]any{"Metadata": items}))
		return
	}
	pl, _ := s.cat.Get(s.cat.IDForPlexKey(p[0])).(*Playlist)
	if pl == nil {
		notFound(w)
		return
	}
	if len(p) >= 2 && p[1] == "items" {
		var items []map[string]any
		for i, t := range pl.Tracks {
			item := s.plexTrack(t)
			item["playlistItemID"] = s.cat.plexKey[pl.ID]*100 + i
			items = append(items, item)
		}
		for i, m := range pl.Movies {
			item := s.plexMovie(m, false)
			item["playlistItemID"] = s.cat.plexKey[pl.ID]*100 + 50 + i
			items = append(items, item)
		}
		if items == nil {
			items = []map[string]any{}
		}
		ok(w, mc(map[string]any{"Metadata": items, "playlistType": pl.Kind, "title": pl.Title, "leafCount": len(items)}))
		return
	}
	ok(w, mc(map[string]any{"Metadata": []map[string]any{s.plexPlaylist(pl)}}))
}

func (s *Server) plexHubs(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) >= 1 && p[0] == "search" {
		q := strings.ToLower(qStr(r, "query"))
		var movies, shows, albums, artists []map[string]any
		for _, m := range s.cat.Movies {
			if strings.Contains(strings.ToLower(m.Title), q) {
				movies = append(movies, s.plexMovie(m, false))
			}
		}
		for _, sh := range s.cat.Shows {
			if strings.Contains(strings.ToLower(sh.Title), q) {
				shows = append(shows, s.plexShow(sh))
			}
		}
		for _, a := range s.cat.Albums {
			if strings.Contains(strings.ToLower(a.Title), q) {
				albums = append(albums, s.plexAlbum(a))
			}
		}
		for _, a := range s.cat.Artists {
			if strings.Contains(strings.ToLower(a.Name), q) {
				artists = append(artists, s.plexArtist(a))
			}
		}
		hub := func(t, id, title string, items []map[string]any) map[string]any {
			if items == nil {
				items = []map[string]any{}
			}
			return map[string]any{"title": title, "type": t, "hubIdentifier": id, "size": len(items), "more": false, "Metadata": items}
		}
		ok(w, mc(map[string]any{"Hub": []map[string]any{hub("movie", "movie", "Movies", movies), hub("show", "show", "Shows", shows), hub("album", "album", "Albums", albums), hub("artist", "artist", "Artists", artists)}}))
		return
	}
	// /hubs, /hubs/home/*, /hubs/sections/* — the TV never calls these; be kind anyway.
	ok(w, mc(map[string]any{"Hub": []map[string]any{}}))
}

func (s *Server) plexSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(qStr(r, "query"))
	typ := qInt(r, "type", 0)
	var items []map[string]any
	if typ == 0 || typ == 9 {
		for _, a := range s.cat.Albums {
			if strings.Contains(strings.ToLower(a.Title), q) || strings.Contains(strings.ToLower(a.Artist.Name), q) {
				items = append(items, s.plexAlbum(a))
			}
		}
	}
	if typ == 0 || typ == 1 {
		for _, m := range s.cat.Movies {
			if strings.Contains(strings.ToLower(m.Title), q) {
				items = append(items, s.plexMovie(m, false))
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	ok(w, mc(map[string]any{"Metadata": items}))
}

// ---- transcode ------------------------------------------------------------

func (s *Server) plexTranscode(w http.ResponseWriter, r *http.Request, p []string) {
	// p[0] is "video" or "music"; the path continues with ":" "transcode" "universal" <endpoint> …
	rest := p[1:]
	for len(rest) > 0 && (rest[0] == ":" || rest[0] == "transcode" || rest[0] == "universal") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		notFound(w)
		return
	}
	id := ""
	if path := qStr(r, "path"); path != "" {
		id = s.cat.IDForPlexKey(strings.TrimPrefix(path, "/library/metadata/"))
	}
	switch rest[0] {
	case "decision":
		s.plexDecision(w, r, id)
	case "start.m3u8":
		session := qStr(r, "session")
		clip := s.clipOf(id)
		if clip == "" {
			http.Error(w, "nothing to play", http.StatusNotFound)
			return
		}
		s.rememberSession(session, clip, s.runtimeOf(id))
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=3200000,RESOLUTION=1280x720,FRAME-RATE=24.000,CODECS=\"avc1.640028,mp4a.40.2\"\nsession/%s/base/index.m3u8\n", session)
	case "start.mp3", "start.wav":
		if t, isTrack := s.cat.Get(id).(*Track); isTrack {
			serveWAV(w, r, t.Seed, t.Duration)
			return
		}
		notFound(w)
	case "session":
		// session/{sid}/base/<file> — the clip chosen for the session's item is
		// remembered by session id (set by start.m3u8 via the sessionClip map).
		if len(rest) < 4 {
			notFound(w)
			return
		}
		clip, seconds := s.sessionClip(rest[1])
		if rest[3] == "index.m3u8" {
			s.serveRuntimePlaylist(w, r, clip, seconds)
			return
		}
		s.serveClipFile(w, r, clip, rest[3])
	case "ping", "stop":
		w.WriteHeader(http.StatusOK)
	default:
		notFound(w)
	}
}

func (s *Server) clipOf(id string) string {
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		return v.Clip
	case *Episode:
		return v.Clip
	}
	return ""
}

// runtimeOf is an item's running time in seconds (0 for non-video).
func (s *Server) runtimeOf(id string) float64 {
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		return float64(v.Minutes) * 60
	case *Episode:
		return float64(v.Minutes) * 60
	}
	return 0
}

// playSession is what a transcode session plays: the clip, and the
// item's running time, which its media playlist is stretched to.
type playSession struct {
	clip    string
	seconds float64
}

// rememberSession records which clip a transcode session plays; start.m3u8 and
// decision both set it (the decision is what the TV calls first).
func (s *Server) rememberSession(session, clip string, seconds float64) {
	if session == "" || clip == "" {
		return
	}
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = map[string]playSession{}
	}
	s.sessions[session] = playSession{clip: clip, seconds: seconds}
	s.mu.Unlock()
}

// sessionClip is the clip and running time of a session (the first clip,
// unstretched, for a session nobody announced).
func (s *Server) sessionClip(session string) (string, float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.sessions[session]; ok {
		return p.clip, p.seconds
	}
	return s.cat.Clips[0], 0
}

func (s *Server) plexDecision(w http.ResponseWriter, r *http.Request, id string) {
	item := s.plexAny(id, false)
	if item == nil {
		ok(w, mc(map[string]any{"generalDecisionCode": 2000, "generalDecisionText": "Unknown item", "Metadata": []map[string]any{}}))
		return
	}
	s.rememberSession(qStr(r, "session"), s.clipOf(id), s.runtimeOf(id))
	// Mark every stream as copied: the TV reads Part.Stream[].decision.
	if media, isList := item["Media"].([]map[string]any); isList {
		for _, m := range media {
			m["selected"] = true
			if parts, isParts := m["Part"].([]map[string]any); isParts {
				for _, part := range parts {
					part["decision"] = "directplay"
					part["selected"] = true
					if streams, isStreams := part["Stream"].([]map[string]any); isStreams {
						for _, st := range streams {
							st["decision"] = "copy"
							st["location"] = "direct"
						}
					}
				}
			}
		}
	}
	ok(w, mc(map[string]any{
		"generalDecisionCode": 1001, "generalDecisionText": "Direct play OK.",
		"directPlayDecisionCode": 1000, "directPlayDecisionText": "Direct play OK.",
		"transcodeDecisionCode": 1001, "transcodeDecisionText": "Direct play OK.",
		"mdeDecisionCode": 1000, "mdeDecisionText": "Direct play OK.",
		"Metadata": []map[string]any{item},
	}))
}

// ---- photo transcode (phone art) ------------------------------------------

func (s *Server) plexPhoto(w http.ResponseWriter, r *http.Request) {
	u := qStr(r, "url")
	// url=/library/metadata/{rk}/thumb(/1)
	parts := pathParts(strings.Split(u, "?")[0])
	if len(parts) >= 3 && parts[0] == "library" && parts[1] == "metadata" {
		id := s.cat.IDForPlexKey(parts[2])
		kind := artPoster
		switch s.cat.Get(id).(type) {
		case *Album, *Artist, *Playlist, *Track:
			kind = artSquare
		case *Episode:
			kind = artWide
		}
		if len(parts) >= 4 && parts[3] == "art" {
			kind = artBackdrop
		}
		serveJPEG(w, s.art(id, kind, qInt(r, "width", 0)))
		return
	}
	serveJPEG(w, s.art("photo", artSquare, 0))
}

// ---- play queues -----------------------------------------------------------

func (s *Server) plexPlayQueue(w http.ResponseWriter, r *http.Request) {
	uri := qStr(r, "uri")
	rk := uri[strings.LastIndex(uri, "/")+1:]
	id := s.cat.IDForPlexKey(rk)
	var items []map[string]any
	switch v := s.cat.Get(id).(type) {
	case *Album:
		for _, t := range v.Tracks {
			items = append(items, s.plexTrack(t))
		}
	case *Track:
		for _, t := range v.Album.Tracks {
			items = append(items, s.plexTrack(t))
		}
	case *Movie:
		items = append(items, s.plexMovie(v, false))
	case *Episode:
		items = append(items, s.plexEpisode(v, false))
	}
	qid := int(time.Now().Unix() % 100000)
	for i, it := range items {
		it["playQueueItemID"] = qid*100 + i
	}
	if items == nil {
		items = []map[string]any{}
	}
	ok(w, mc(map[string]any{"playQueueID": qid, "playQueueSelectedItemID": qid * 100, "playQueueSelectedItemOffset": 0, "playQueueSelectedMetadataItemID": rk,
		"playQueueShuffled": false, "playQueueSourceURI": uri, "playQueueTotalCount": len(items), "playQueueVersion": 1, "Metadata": items}))
}
