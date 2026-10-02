package showcase

import (
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Audiobookshelf dialect — see docs/showcase-api-inventory.md.

const (
	absUserID  = "usr_showcase"
	absUser    = "chris"
	absLibrary = "lib_audiobooks"
)

func (s *Server) ABSHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"success": true}) })
	mux.HandleFunc("/healthcheck", func(w http.ResponseWriter, r *http.Request) { ok(w, map[string]any{"status": "ok"}) })
	mux.HandleFunc("/status", s.absStatus)
	mux.HandleFunc("/login", s.absLogin)
	mux.HandleFunc("/api/", s.absAPI)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body><h1>Alexandria Showcase — Audiobookshelf dialect</h1></body></html>")
	})
	return s.logging("abs", mux)
}

func (s *Server) absStatus(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{"isInit": true, "language": "en-us", "authMethods": []string{"local"}, "authFormData": map[string]any{}, "serverVersion": "2.17.0"})
}

func (s *Server) absLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	body := readBody(r)
	name, _ := body["username"].(string)
	if name == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Invalid user or password"})
		return
	}
	ok(w, map[string]any{
		"user":                 s.absUser(),
		"userDefaultLibraryId": absLibrary,
		"serverSettings":       map[string]any{"id": "server-settings", "scannerFindCovers": false, "version": "2.17.0"},
		"Source":               "docker",
	})
}

func (s *Server) absUser() map[string]any {
	return map[string]any{
		"id": absUserID, "username": absUser, "type": "admin", "token": s.Token,
		"mediaProgress": s.absMediaProgress(),
		"bookmarks":     s.absBookmarks(),
		"isActive":      true, "isLocked": false, "lastSeen": ms(s.cat.Now), "createdAt": ms(s.cat.daysAgo(400)),
		"permissions":         map[string]any{"download": true, "update": true, "delete": true, "upload": true, "accessAllLibraries": true, "accessAllTags": true, "accessExplicitContent": true},
		"librariesAccessible": []string{}, "itemTagsSelected": []string{},
	}
}

func (s *Server) absMediaProgress() []map[string]any {
	var out []map[string]any
	for _, b := range s.cat.Books {
		p := s.progressOf(b.ID, b.Progress)
		if p <= 0 {
			continue
		}
		cur := p * b.Duration
		finished := p >= 1
		entry := map[string]any{
			"id": "mp_" + b.ID, "libraryItemId": b.ID, "episodeId": nil, "mediaItemId": b.ID, "mediaItemType": "book",
			"duration": b.Duration, "progress": math.Min(p, 1), "currentTime": cur, "isFinished": finished,
			"hideFromContinueListening": false, "lastUpdate": ms(b.LastPlay), "startedAt": ms(b.LastPlay.Add(-20 * 24 * time.Hour)),
		}
		if finished {
			entry["finishedAt"] = ms(b.LastPlay)
		} else {
			entry["finishedAt"] = nil
		}
		if b.Ebook {
			entry["ebookLocation"] = "epubcfi(/6/8!/4/2/1:0)"
			entry["ebookProgress"] = p
		}
		out = append(out, entry)
	}
	return out
}

func (s *Server) absBookmarks() []map[string]any {
	var out []map[string]any
	if len(s.cat.Books) > 0 {
		b := s.cat.InProgressBooks()
		if len(b) > 0 {
			out = append(out, map[string]any{"libraryItemId": b[0].ID, "title": "The lamp room", "time": 1830.0, "createdAt": ms(s.cat.daysAgo(3))})
			out = append(out, map[string]any{"libraryItemId": b[0].ID, "title": "The second log entry", "time": 5410.0, "createdAt": ms(s.cat.daysAgo(1))})
		}
	}
	return out
}

func (s *Server) absAPI(w http.ResponseWriter, r *http.Request) {
	p := pathParts(strings.TrimPrefix(r.URL.Path, "/api"))
	if len(p) == 0 {
		notFound(w)
		return
	}
	switch p[0] {
	case "libraries":
		s.absLibraries(w, r, p[1:])
	case "me":
		s.absMe(w, r, p[1:])
	case "items":
		s.absItems(w, r, p[1:])
	case "authors":
		s.absAuthors(w, r, p[1:])
	case "collections":
		if len(p) == 1 {
			ok(w, map[string]any{"results": []any{}, "total": 0})
			return
		}
		notFound(w)
	case "playlists":
		ok(w, map[string]any{"results": []any{}, "total": 0})
	case "session":
		s.absSession(w, r, p[1:])
	case "sessions":
		ok(w, map[string]any{"sessions": []any{}, "users": []map[string]any{{"id": absUserID, "username": absUser}}})
	case "users":
		if len(p) >= 3 && p[2] == "listening-sessions" {
			ok(w, map[string]any{"sessions": s.absSessions(), "total": len(s.absSessions())})
			return
		}
		ok(w, map[string]any{"users": []map[string]any{{"id": absUserID, "username": absUser}}})
	case "search":
		ok(w, map[string]any{"book": []any{}, "podcast": []any{}})
	default:
		notFound(w)
	}
}

// ---- libraries -------------------------------------------------------------

func (s *Server) absLibraryJSON() map[string]any {
	return map[string]any{
		"id": absLibrary, "name": "Audiobooks", "mediaType": "book", "icon": "audiobookshelf", "provider": "audible",
		"displayOrder": 1, "folders": []map[string]any{{"id": "fol_1", "fullPath": "/audiobooks", "libraryId": absLibrary}},
		"settings":  map[string]any{"coverAspectRatio": 1, "disableWatcher": false, "skipMatchingMediaWithAsin": false},
		"createdAt": ms(s.cat.daysAgo(400)), "lastUpdate": ms(s.cat.Now),
	}
}

func (s *Server) absLibraries(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		ok(w, map[string]any{"libraries": []map[string]any{s.absLibraryJSON()}})
		return
	}
	if p[0] != absLibrary {
		notFound(w)
		return
	}
	if len(p) == 1 {
		ok(w, s.absLibraryJSON())
		return
	}
	switch p[1] {
	case "items":
		s.absLibraryItems(w, r)
	case "personalized":
		ok(w, s.absPersonalized())
	case "series":
		if len(p) >= 3 {
			if se, _ := s.cat.Get(p[2]).(*Series); se != nil {
				ok(w, s.absSeries(se, true))
				return
			}
			notFound(w)
			return
		}
		limit := qInt(r, "limit", 20)
		var out []map[string]any
		list := s.cat.RecentSeries(limit)
		if qStr(r, "sort") != "addedAt" {
			list = append([]*Series(nil), s.cat.Series...)
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		}
		for _, se := range list {
			out = append(out, s.absSeries(se, qStr(r, "minified") == ""))
		}
		ok(w, map[string]any{"results": out, "total": len(s.cat.Series), "limit": limit, "page": 0})
	case "authors":
		var out []map[string]any
		for _, a := range s.cat.Authors {
			out = append(out, s.absAuthor(a))
		}
		ok(w, map[string]any{"authors": out})
	case "collections":
		ok(w, map[string]any{"results": []any{}, "total": 0})
	case "search":
		s.absSearch(w, r)
	case "filterdata":
		ok(w, map[string]any{"authors": []any{}, "genres": []any{}, "tags": []any{}, "series": []any{}, "narrators": []any{}, "languages": []any{}})
	case "recent-episodes":
		ok(w, map[string]any{"episodes": []any{}, "total": 0})
	case "stats":
		ok(w, map[string]any{"totalItems": len(s.cat.Books), "totalAuthors": len(s.cat.Authors), "totalGenres": 12, "totalDuration": 2.4e6})
	default:
		notFound(w)
	}
}

func (s *Server) absLibraryItems(w http.ResponseWriter, r *http.Request) {
	books := append([]*Book(nil), s.cat.Books...)
	if f := qStr(r, "filter"); strings.HasPrefix(f, "series.") {
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(f, "series."))
		sid := string(raw)
		var filtered []*Book
		for _, b := range books {
			if b.Series != nil && b.Series.ID == sid {
				filtered = append(filtered, b)
			}
		}
		books = filtered
	}
	switch qStr(r, "sort") {
	case "addedAt":
		sort.Slice(books, func(i, j int) bool { return books[i].AddedAt.Before(books[j].AddedAt) })
	case "media.metadata.title", "title":
		sort.Slice(books, func(i, j int) bool { return books[i].Title < books[j].Title })
	}
	if d := qStr(r, "desc"); d == "1" || d == "true" {
		for i, j := 0, len(books)-1; i < j; i, j = i+1, j-1 {
			books[i], books[j] = books[j], books[i]
		}
	}
	total := len(books)
	limit := qInt(r, "limit", 0)
	page := qInt(r, "page", 0)
	if limit > 0 {
		start := page * limit
		if start > len(books) {
			start = len(books)
		}
		end := start + limit
		if end > len(books) {
			end = len(books)
		}
		books = books[start:end]
	}
	minified := qStr(r, "minified") == "1" || qStr(r, "minified") == "true"
	includeProgress := strings.Contains(qStr(r, "include"), "progress")
	out := make([]map[string]any, 0, len(books))
	for _, b := range books {
		out = append(out, s.absItem(b, !minified, includeProgress))
	}
	ok(w, map[string]any{"results": out, "total": total, "limit": limit, "page": page, "sortBy": qStr(r, "sort"), "sortDesc": qStr(r, "desc") == "1", "minified": minified})
}

// ---- items -----------------------------------------------------------------

func (s *Server) absItem(b *Book, expanded, includeProgress bool) map[string]any {
	meta := map[string]any{
		"title": b.Title, "titleIgnorePrefix": strings.TrimPrefix(b.Title, "The "), "subtitle": b.Subtitle,
		"authors": []map[string]any{{"id": b.Author.ID, "name": b.Author.Name}}, "authorName": b.Author.Name,
		"narrators": []string{b.Narrator}, "narratorName": b.Narrator,
		"genres": b.Genres, "publishedYear": b.Year, "publisher": b.Publisher, "description": b.Blurb,
		"isbn": nil, "asin": nil, "language": "English", "explicit": false, "abridged": false,
		"series": []map[string]any{},
	}
	if b.Series != nil {
		meta["series"] = []map[string]any{{"id": b.Series.ID, "name": b.Series.Name, "sequence": b.Sequence}}
		meta["seriesName"] = fmt.Sprintf("%s #%s", b.Series.Name, b.Sequence)
	}
	media := map[string]any{
		"id": "media_" + b.ID, "libraryItemId": b.ID, "metadata": meta, "coverPath": "/covers/" + b.ID + ".jpg",
		"tags": []string{}, "duration": b.Duration, "size": int64(b.Duration * 32000),
		"numTracks": 1, "numAudioFiles": 1, "numChapters": len(b.Chapters), "numMissingParts": 0, "numInvalidAudioFiles": 0,
		"ebookFormat": nil,
	}
	if b.Ebook {
		media["numTracks"] = 0
		media["numAudioFiles"] = 0
		media["numChapters"] = 0
		media["duration"] = 0
		media["ebookFormat"] = "epub"
		media["ebookFile"] = map[string]any{
			"ino": b.FileIno, "ebookFormat": "epub", "addedAt": ms(b.AddedAt), "updatedAt": ms(b.AddedAt),
			"metadata": map[string]any{"filename": b.Filename, "ext": ".epub", "path": "/audiobooks/" + b.Filename, "relPath": b.Filename, "size": 820000, "mtimeMs": ms(b.AddedAt), "ctimeMs": ms(b.AddedAt), "birthtimeMs": ms(b.AddedAt)},
			"mimeType": "application/epub+zip",
		}
	}
	if expanded && !b.Ebook {
		var chapters []map[string]any
		for _, ch := range b.Chapters {
			chapters = append(chapters, map[string]any{"id": ch.ID, "start": ch.Start, "end": ch.End, "title": ch.Title})
		}
		media["chapters"] = chapters
		media["audioFiles"] = []map[string]any{s.absAudioFile(b)}
		media["tracks"] = []map[string]any{s.absTrack(b)}
	} else if expanded {
		media["chapters"] = []any{}
		media["audioFiles"] = []any{}
		media["tracks"] = []any{}
	}
	item := map[string]any{
		"id": b.ID, "ino": b.FileIno, "oldLibraryItemId": nil, "libraryId": absLibrary, "folderId": "fol_1",
		"path": "/audiobooks/" + slug(b.Title), "relPath": slug(b.Title), "isFile": false,
		"mtimeMs": ms(b.AddedAt), "ctimeMs": ms(b.AddedAt), "birthtimeMs": ms(b.AddedAt),
		"addedAt": ms(b.AddedAt), "updatedAt": ms(b.AddedAt), "lastScan": ms(s.cat.Now), "scanVersion": "2.17.0",
		"isMissing": false, "isInvalid": false, "mediaType": "book", "media": media,
		"numFiles": 2, "size": int64(b.Duration*32000) + 500000,
	}
	if expanded {
		item["libraryFiles"] = []map[string]any{{
			"ino": b.FileIno, "fileType": map[bool]string{true: "ebook", false: "audio"}[b.Ebook],
			"metadata": map[string]any{"filename": b.Filename, "ext": "." + strings.TrimPrefix(strings.ToLower(b.Filename[strings.LastIndex(b.Filename, ".")+1:]), "."), "path": "/audiobooks/" + slug(b.Title) + "/" + b.Filename, "relPath": b.Filename, "size": int64(b.Duration * 32000)},
			"addedAt":  ms(b.AddedAt), "updatedAt": ms(b.AddedAt),
		}}
	}
	if includeProgress {
		if p := s.progressOf(b.ID, b.Progress); p > 0 {
			item["userMediaProgress"] = s.absProgressEntry(b)
			item["mediaProgress"] = item["userMediaProgress"]
		}
	}
	return item
}

func (s *Server) absProgressEntry(b *Book) map[string]any {
	p := s.progressOf(b.ID, b.Progress)
	return map[string]any{
		"id": "mp_" + b.ID, "libraryItemId": b.ID, "episodeId": nil, "duration": b.Duration,
		"progress": math.Min(p, 1), "currentTime": p * b.Duration, "isFinished": p >= 1,
		"hideFromContinueListening": false, "lastUpdate": ms(b.LastPlay), "startedAt": ms(b.LastPlay.Add(-20 * 24 * time.Hour)),
	}
}

func (s *Server) absAudioFile(b *Book) map[string]any {
	return map[string]any{
		"index": 1, "ino": b.FileIno, "metadata": map[string]any{"filename": b.Filename, "ext": ".wav", "path": "/audiobooks/" + slug(b.Title) + "/" + b.Filename, "relPath": b.Filename, "size": int64(b.Duration * 44100)},
		"addedAt": ms(b.AddedAt), "updatedAt": ms(b.AddedAt), "trackNumFromMeta": 1, "discNumFromMeta": nil, "trackNumFromFilename": 1, "discNumFromFilename": nil,
		"manuallyVerified": false, "exclude": false, "error": nil, "format": "WAV / PCM", "duration": b.Duration, "bitRate": 352800, "language": nil,
		"codec": "pcm_s16le", "timeBase": "1/22050", "channels": 1, "channelLayout": "mono", "chapters": []any{}, "embeddedCoverArt": nil, "mimeType": "audio/wav",
	}
}

func (s *Server) absTrack(b *Book) map[string]any {
	return map[string]any{
		"index": 1, "startOffset": 0, "duration": b.Duration, "title": b.Filename,
		"contentUrl": fmt.Sprintf("/api/items/%s/file/%s", b.ID, b.FileIno), "mimeType": "audio/wav", "codec": "pcm_s16le",
		"metadata": map[string]any{"filename": b.Filename, "ext": ".wav", "path": "/audiobooks/" + slug(b.Title) + "/" + b.Filename, "relPath": b.Filename, "size": int64(b.Duration * 44100)},
	}
}

func (s *Server) absSeries(se *Series, withBooks bool) map[string]any {
	out := map[string]any{"id": se.ID, "name": se.Name, "nameIgnorePrefix": strings.TrimPrefix(se.Name, "The "), "description": nil,
		"addedAt": ms(se.AddedAt), "updatedAt": ms(se.AddedAt), "numBooks": len(se.Books), "libraryId": absLibrary}
	var total float64
	var books []map[string]any
	for _, b := range se.Books {
		total += b.Duration
		if withBooks {
			item := s.absItem(b, false, true)
			item["sequence"] = b.Sequence
			books = append(books, item)
		}
	}
	out["totalDuration"] = total
	if withBooks {
		out["books"] = books
	}
	return out
}

func (s *Server) absAuthor(a *Author) map[string]any {
	return map[string]any{"id": a.ID, "asin": nil, "name": a.Name, "description": a.Bio, "imagePath": nil,
		"addedAt": ms(s.cat.daysAgo(300)), "updatedAt": ms(s.cat.daysAgo(300)), "numBooks": len(a.Books), "libraryId": absLibrary}
}

func (s *Server) absPersonalized() []map[string]any {
	items := func(list []*Book) []map[string]any {
		out := []map[string]any{}
		for _, b := range list {
			out = append(out, s.absItem(b, false, true))
		}
		return out
	}
	shelves := []map[string]any{}
	if l := s.cat.InProgressBooks(); len(l) > 0 {
		shelves = append(shelves, map[string]any{"id": "continue-listening", "label": "Continue Listening", "labelStringKey": "LabelContinueListening", "type": "book", "entities": items(l), "total": len(l)})
	}
	if l := s.cat.ContinueSeries(); len(l) > 0 {
		shelves = append(shelves, map[string]any{"id": "continue-series", "label": "Continue Series", "labelStringKey": "LabelContinueSeries", "type": "book", "entities": items(l), "total": len(l)})
	}
	recent := s.cat.RecentBooks(12)
	shelves = append(shelves, map[string]any{"id": "recently-added", "label": "Recently Added", "labelStringKey": "LabelRecentlyAdded", "type": "book", "entities": items(recent), "total": len(recent)})
	var series []map[string]any
	for _, se := range s.cat.RecentSeries(6) {
		series = append(series, s.absSeries(se, true))
	}
	shelves = append(shelves, map[string]any{"id": "recent-series", "label": "Recent Series", "labelStringKey": "LabelRecentSeries", "type": "series", "entities": series, "total": len(series)})
	if l := s.cat.Recommended(10); len(l) > 0 {
		shelves = append(shelves, map[string]any{"id": "recommended", "label": "Recommended For You", "labelStringKey": "LabelRecommended", "type": "book", "entities": items(l), "total": len(l)})
	}
	if l := s.cat.FinishedBooks(); len(l) > 0 {
		shelves = append(shelves, map[string]any{"id": "listen-again", "label": "Listen Again", "labelStringKey": "LabelListenAgain", "type": "book", "entities": items(l), "total": len(l)})
	}
	return shelves
}

func (s *Server) absSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(qStr(r, "q"))
	var out []map[string]any
	for _, b := range s.cat.Books {
		if q != "" && (strings.Contains(strings.ToLower(b.Title), q) || strings.Contains(strings.ToLower(b.Author.Name), q)) {
			out = append(out, map[string]any{"libraryItem": s.absItem(b, false, true), "matchKey": "title", "matchText": b.Title})
		}
	}
	ok(w, map[string]any{"book": out, "podcast": []any{}, "authors": []any{}, "series": []any{}, "tags": []any{}, "narrators": []any{}})
}

func (s *Server) absItems(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		notFound(w)
		return
	}
	b, _ := s.cat.Get(p[0]).(*Book)
	if b == nil {
		notFound(w)
		return
	}
	if len(p) == 1 {
		ok(w, s.absItem(b, true, true))
		return
	}
	switch p[1] {
	case "cover":
		serveJPEG(w, s.art(b.ID, artPoster, qInt(r, "width", 0)))
	case "play":
		s.absPlay(w, r, b)
	case "file":
		if b.Ebook {
			http.Error(w, "ebook files are not part of the showcase yet", http.StatusNotFound)
			return
		}
		serveWAV(w, r, b.Seed, b.Duration)
	case "ebook":
		http.Error(w, "ebook files are not part of the showcase yet", http.StatusNotFound)
	default:
		notFound(w)
	}
}

func (s *Server) absPlay(w http.ResponseWriter, r *http.Request, b *Book) {
	_ = readBody(r)
	p := s.progressOf(b.ID, b.Progress)
	cur := 0.0
	if p < 1 {
		cur = p * b.Duration
	}
	ok(w, map[string]any{
		"id": "play_" + b.ID, "userId": absUserID, "libraryId": absLibrary, "libraryItemId": b.ID, "episodeId": nil,
		"mediaType": "book", "mediaMetadata": map[string]any{"title": b.Title, "authorName": b.Author.Name, "narratorName": b.Narrator},
		"chapters": func() []map[string]any {
			var out []map[string]any
			for _, ch := range b.Chapters {
				out = append(out, map[string]any{"id": ch.ID, "start": ch.Start, "end": ch.End, "title": ch.Title})
			}
			return out
		}(),
		"displayTitle": b.Title, "displayAuthor": b.Author.Name, "coverPath": "/covers/" + b.ID + ".jpg",
		"duration": b.Duration, "playMethod": 0, "mediaPlayer": "flutter",
		"deviceInfo":    map[string]any{"id": "showcase", "clientName": "Alexandria"},
		"serverVersion": "2.17.0", "date": s.cat.Now.Format("2006-01-02"), "dayOfWeek": s.cat.Now.Weekday().String(),
		"timeListening": 0, "startTime": cur, "currentTime": cur,
		"startedAt": ms(s.cat.Now), "updatedAt": ms(s.cat.Now),
		"audioTracks": []map[string]any{s.absTrack(b)},
		"videoTrack":  nil, "libraryItem": s.absItem(b, true, true),
	})
}

func (s *Server) absSession(w http.ResponseWriter, r *http.Request, p []string) {
	// /api/session/{sid}/sync|close, /api/session/local
	if len(p) >= 2 && p[1] == "sync" {
		body := readBody(r)
		if cur, ok2 := body["currentTime"].(float64); ok2 {
			if dur, ok3 := body["duration"].(float64); ok3 && dur > 0 {
				if id := strings.TrimPrefix(p[0], "play_"); id != "" {
					s.setProgress(id, cur/dur)
				}
			}
		}
	}
	ok(w, map[string]any{"success": true})
}

func (s *Server) absMe(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		ok(w, s.absUser())
		return
	}
	switch p[0] {
	case "items-in-progress":
		var out []map[string]any
		for _, b := range s.cat.InProgressBooks() {
			out = append(out, s.absItem(b, false, true))
		}
		ok(w, map[string]any{"libraryItems": out})
	case "progress":
		if len(p) < 2 {
			notFound(w)
			return
		}
		b, _ := s.cat.Get(p[1]).(*Book)
		if b == nil {
			notFound(w)
			return
		}
		if r.Method == http.MethodPatch {
			body := readBody(r)
			if prog, ok2 := body["progress"].(float64); ok2 {
				s.setProgress(b.ID, prog)
			} else if cur, ok3 := body["currentTime"].(float64); ok3 && b.Duration > 0 {
				s.setProgress(b.ID, cur/b.Duration)
			}
			if fin, _ := body["isFinished"].(bool); fin {
				s.setProgress(b.ID, 1)
			}
			ok(w, map[string]any{"success": true})
			return
		}
		if r.Method == http.MethodDelete {
			s.setProgress(b.ID, 0)
			ok(w, map[string]any{"success": true})
			return
		}
		if s.progressOf(b.ID, b.Progress) <= 0 {
			notFound(w)
			return
		}
		ok(w, s.absProgressEntry(b))
	case "listening-stats":
		ok(w, s.absListeningStats())
	case "listening-sessions":
		ok(w, map[string]any{"sessions": s.absSessions(), "total": len(s.absSessions())})
	case "stats":
		if len(p) >= 3 && p[1] == "year" {
			ok(w, s.absYearStats())
			return
		}
		notFound(w)
	case "item":
		// bookmarks: POST /api/me/item/{id}/bookmark, DELETE …/bookmark/{time}
		ok(w, map[string]any{"libraryItemId": p[1], "title": "Bookmark", "time": 0, "createdAt": ms(s.cat.Now)})
	default:
		notFound(w)
	}
}

func (s *Server) absSessions() []map[string]any {
	var out []map[string]any
	n := 0
	for _, b := range append(s.cat.InProgressBooks(), s.cat.FinishedBooks()...) {
		if n >= 14 {
			break
		}
		start := b.LastPlay.Add(-time.Duration(40+n*7) * time.Minute)
		out = append(out, map[string]any{
			"id": fmt.Sprintf("ses_%s_%d", b.ID, n), "userId": absUserID, "libraryId": absLibrary, "libraryItemId": b.ID, "episodeId": nil,
			"mediaType": "book", "mediaMetadata": map[string]any{"title": b.Title, "authorName": b.Author.Name},
			"displayTitle": b.Title, "displayAuthor": b.Author.Name, "coverPath": "/covers/" + b.ID + ".jpg",
			"duration": b.Duration, "playMethod": 0, "mediaPlayer": "flutter", "deviceInfo": map[string]any{"clientName": "Alexandria", "deviceName": "iPhone"},
			"date": start.Format("2006-01-02"), "dayOfWeek": start.Weekday().String(),
			"timeListening": float64(1800 + n*300), "startTime": math.Max(0, b.Progress*b.Duration-float64(1800+n*300)), "currentTime": b.Progress * b.Duration,
			"startedAt": ms(start), "updatedAt": ms(b.LastPlay),
		})
		n++
	}
	return out
}

func (s *Server) absListeningStats() map[string]any {
	days := map[string]float64{}
	dow := map[string]float64{}
	var total float64
	for i := 0; i < 60; i++ {
		d := s.cat.daysAgo(i)
		secs := float64(900 + (int(hash(d.Format("2006-01-02"))) % 4500))
		if i%6 == 0 {
			secs = 0
		}
		days[d.Format("2006-01-02")] = secs
		dow[d.Weekday().String()] += secs
		total += secs
	}
	items := map[string]any{}
	for _, b := range append(s.cat.InProgressBooks(), s.cat.FinishedBooks()...) {
		items[b.ID] = map[string]any{"id": b.ID, "timeListening": b.Progress * b.Duration, "mediaMetadata": map[string]any{"title": b.Title, "authorName": b.Author.Name}}
	}
	return map[string]any{
		"totalTime": total * 3, "today": days[s.cat.Now.Format("2006-01-02")], "items": items, "days": days, "dayOfWeek": dow,
		"recentSessions": s.absSessions(),
	}
}

func (s *Server) absYearStats() map[string]any {
	finished := s.cat.FinishedBooks()
	var covers []map[string]any
	for _, b := range finished {
		covers = append(covers, map[string]any{"id": b.ID, "title": b.Title, "author": b.Author.Name})
	}
	var totalListen float64
	for _, b := range s.cat.Books {
		totalListen += s.progressOf(b.ID, b.Progress) * b.Duration
	}
	authorCount := map[string]int{}
	genreCount := map[string]int{}
	for _, b := range finished {
		authorCount[b.Author.Name]++
		for _, g := range b.Genres {
			genreCount[g]++
		}
	}
	var top []map[string]any
	for name, n := range authorCount {
		top = append(top, map[string]any{"name": name, "count": n})
	}
	sort.Slice(top, func(i, j int) bool { return top[i]["count"].(int) > top[j]["count"].(int) })
	var genres []map[string]any
	for name, n := range genreCount {
		genres = append(genres, map[string]any{"genre": name, "count": n})
	}
	return map[string]any{
		"numBooksFinished": len(finished), "finishedBooksWithCovers": covers, "booksWithCovers": covers,
		"totalListeningTime": totalListen, "totalBookListeningTime": totalListen, "totalPodcastListeningTime": 0,
		"totalListeningSessions": 148, "numBooksListened": len(finished) + len(s.cat.InProgressBooks()), "numDaysListened": 171,
		"topAuthors": top, "topGenres": genres,
		"mostListenedNarrator": map[string]any{"name": narrators[0], "time": 86400},
		"mostListenedMonth":    map[string]any{"month": 5, "time": 120000},
	}
}

func (s *Server) absAuthors(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		notFound(w)
		return
	}
	a, _ := s.cat.Get(p[0]).(*Author)
	if a == nil {
		notFound(w)
		return
	}
	if len(p) >= 2 && p[1] == "image" {
		serveJPEG(w, s.art(a.ID, artSquare, qInt(r, "width", 0)))
		return
	}
	out := s.absAuthor(a)
	var items []map[string]any
	for _, b := range a.Books {
		items = append(items, s.absItem(b, false, true))
	}
	out["libraryItems"] = items
	ok(w, out)
}
