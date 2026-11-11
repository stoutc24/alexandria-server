package showcase

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Jellyfin dialect — see docs/showcase-api-inventory.md.

const (
	jfServerID  = "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	jfUserID    = "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	jfUserName  = "chris"
	jfMusicView = "view_music"
	jfMovieView = "view_movies"
	jfShowView  = "view_shows"
	// The audiobooks view, served only with Server.Books (SHOWCASE_JELLYFIN_BOOKS=1).
	jfBooksView = "view_books"
)

const ticksPerSec = 10_000_000

func ticks(sec float64) int64 { return int64(sec * ticksPerSec) }

func jfDate(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.0000000Z") }

func (s *Server) JellyfinHandler() http.Handler {
	return s.logging("jellyfin", http.HandlerFunc(s.jellyfin))
}

func (s *Server) jellyfin(w http.ResponseWriter, r *http.Request) {
	p := pathParts(r.URL.Path)
	// The song-file log, in every mode (the Books-only readouts below stay
	// as they were): GET /__showcase/parts → this dialect's requests (the
	// command runs each dialect as its own Server, with its own log);
	// DELETE clears; POST ?refuseDownload=1|0 switches
	// RefuseJellyfinDownload (and clears).
	if len(p) == 2 && p[0] == "__showcase" && p[1] == "parts" {
		if r.Method == http.MethodPost {
			s.mu.Lock()
			s.RefuseJellyfinDownload = qStr(r, "refuseDownload") == "1"
			s.mu.Unlock()
		}
		ok(w, map[string]any{"parts": s.recordedParts(r.Method != http.MethodGet), "refuseDownload": s.refusesDownload()})
		return
	}
	// Mark Watched / Mark Unwatched (watched.go), in every mode: the
	// readout, and POST / DELETE /UserPlayedItems/{id} for a film, an
	// episode, a season or a series. Anything else on that route is
	// answered below as it always was.
	if len(p) == 2 && p[0] == "__showcase" && p[1] == "watched" {
		s.watchedControl(w, r)
		return
	}
	if s.jfWatchedRoute(w, r, p) {
		return
	}
	// Tolerate a path prefix (the demo uses /stable): drop leading segments
	// until a known root appears.
	roots := map[string]bool{"System": true, "Users": true, "QuickConnect": true, "Items": true, "Artists": true, "Genres": true,
		"Playlists": true, "Shows": true, "Audio": true, "Videos": true, "videos": true, "Sessions": true, "LiveTv": true, "MediaSegments": true,
		"Branding": true, "Library": true, "Persons": true, "Studios": true, "DisplayPreferences": true}
	if s.Books {
		// The 10.9+ write routes the TV's audiobook arm uses, and the probe's
		// readout. Off, these names fall through to the landing page as before.
		roots["UserItems"] = true
		roots["UserPlayedItems"] = true
		roots["__showcase"] = true
	}
	for len(p) > 0 && !roots[p[0]] {
		p = p[1:]
	}
	if len(p) == 0 {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><body><h1>Alexandria Showcase — Jellyfin dialect</h1></body></html>")
		return
	}
	// Case-insensitive roots for the ones AVPlayer builds lowercase.
	switch strings.ToLower(p[0]) {
	case "system":
		if len(p) >= 3 && p[1] == "Info" {
			ok(w, map[string]any{"ServerName": "Alexandria Showcase", "Version": "10.10.3", "Id": jfServerID,
				"LocalAddress": "http://" + r.Host, "StartupWizardCompleted": true, "ProductName": "Jellyfin Server", "OperatingSystem": "Linux"})
			return
		}
		if len(p) >= 2 && p[1] == "Ping" {
			ok(w, "Jellyfin Server")
			return
		}
		notFound(w)
	case "quickconnect":
		if len(p) >= 2 && p[1] == "Enabled" {
			ok(w, false)
			return
		}
		notFound(w)
	case "branding":
		ok(w, map[string]any{"LoginDisclaimer": "", "CustomCss": "", "SplashscreenEnabled": false})
	case "users":
		s.jfUsers(w, r, p[1:])
	case "items":
		s.jfItemsRoot(w, r, p[1:])
	case "artists":
		s.jfArtists(w, r, p[1:])
	case "genres":
		s.jfGenres(w, r)
	case "playlists":
		s.jfPlaylists(w, r, p[1:])
	case "shows":
		s.jfShows(w, r, p[1:])
	case "audio":
		s.jfAudio(w, r, p[1:])
	case "videos":
		s.jfVideos(w, r, p[1:])
	case "sessions":
		if s.Books {
			s.jfSessionReport(r, p[1:])
		}
		w.WriteHeader(http.StatusNoContent)
	case "useritems":
		// POST /UserItems/{id}/UserData?userId= (Books only; see roots).
		s.jfUserDataWrite(w, r, p[1:])
	case "userplayeditems":
		// POST /UserPlayedItems/{id}?userId= (Books only; see roots).
		s.jfPlayedWrite(w, r, p[1:])
	case "__showcase":
		// GET /__showcase/reports → what was posted; DELETE clears (Books only).
		if len(p) >= 2 && p[1] == "reports" {
			ok(w, map[string]any{"reports": s.recorded(r.Method == http.MethodDelete)})
			return
		}
		notFound(w)
	case "livetv":
		ok(w, map[string]any{"TotalRecordCount": 0, "Items": []any{}, "IsEnabled": false, "Services": []any{}})
	case "mediasegments":
		notFound(w)
	case "displaypreferences":
		ok(w, map[string]any{"Id": "usersettings", "CustomPrefs": map[string]any{}})
	default:
		notFound(w)
	}
}

// ---- users -----------------------------------------------------------------

func (s *Server) jfUserJSON() map[string]any {
	return map[string]any{
		"Name": jfUserName, "ServerId": jfServerID, "Id": jfUserID, "HasPassword": true, "HasConfiguredPassword": true,
		"HasConfiguredEasyPassword": false, "EnableAutoLogin": false, "LastLoginDate": jfDate(s.cat.Now), "LastActivityDate": jfDate(s.cat.Now),
		"Configuration": map[string]any{"PlayDefaultAudioTrack": true, "SubtitleLanguagePreference": "", "DisplayMissingEpisodes": false,
			"GroupedFolders": []any{}, "SubtitleMode": "Default", "DisplayCollectionsView": true, "EnableLocalPassword": false,
			"OrderedViews": []any{}, "LatestItemsExcludes": []any{}, "MyMediaExcludes": []any{}, "HidePlayedInLatest": true,
			"RememberAudioSelections": true, "RememberSubtitleSelections": true, "EnableNextEpisodeAutoPlay": true},
		"Policy": map[string]any{"IsAdministrator": true, "IsHidden": false, "IsDisabled": false, "EnableAllFolders": true,
			"EnableMediaPlayback": true, "EnableAudioPlaybackTranscoding": true, "EnableVideoPlaybackTranscoding": true,
			"EnablePlaybackRemuxing": true, "EnableContentDownloading": true, "EnableRemoteAccess": true, "AuthenticationProviderId": "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider"},
	}
}

func (s *Server) jfUsers(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		ok(w, []map[string]any{s.jfUserJSON()})
		return
	}
	switch p[0] {
	case "Public":
		ok(w, []map[string]any{{"Name": jfUserName, "ServerId": jfServerID, "Id": jfUserID, "HasPassword": true, "HasConfiguredPassword": true, "LastLoginDate": jfDate(s.cat.Now)}})
		return
	case "AuthenticateByName":
		body := readBody(r)
		name, _ := body["Username"].(string)
		if name == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Invalid username or password"})
			return
		}
		ok(w, map[string]any{"User": s.jfUserJSON(), "SessionInfo": map[string]any{"Id": "sess_showcase", "UserId": jfUserID, "Client": "Alexandria"},
			"AccessToken": s.Token, "ServerId": jfServerID})
		return
	case "AuthenticateWithQuickConnect":
		ok(w, map[string]any{"User": s.jfUserJSON(), "AccessToken": s.Token, "ServerId": jfServerID})
		return
	case "Me":
		ok(w, s.jfUserJSON())
		return
	}
	// /Users/{uid}/...
	if len(p) == 1 {
		ok(w, s.jfUserJSON())
		return
	}
	switch p[1] {
	case "Views":
		views := []map[string]any{
			s.jfView(jfMusicView, "Music", "music"),
			s.jfView(jfMovieView, "Movies", "movies"),
			s.jfView(jfShowView, "TV Shows", "tvshows"),
		}
		if s.Books {
			views = append(views, s.jfView(jfBooksView, "Audiobooks", "books"))
		}
		ok(w, map[string]any{"Items": views, "TotalRecordCount": len(views), "StartIndex": 0})
	case "Items":
		s.jfUserItems(w, r, p[2:])
	case "Images":
		serveJPEG(w, s.art("user", artSquare, 300))
	case "PlayedItems", "FavoriteItems":
		// The legacy played route, for a video: the mark is kept (watched.go).
		if p[1] == "PlayedItems" && len(p) >= 3 && s.jfWatchedWrite(w, r, p[2], false) {
			return
		}
		ok(w, map[string]any{"PlaybackPositionTicks": 0, "PlayCount": 1, "Played": true})
	case "PlayingItems":
		w.WriteHeader(http.StatusNoContent)
	default:
		notFound(w)
	}
}

func (s *Server) jfView(id, name, collectionType string) map[string]any {
	return map[string]any{"Name": name, "ServerId": jfServerID, "Id": id, "Etag": id + "etag", "DateCreated": jfDate(s.cat.daysAgo(400)),
		"CanDelete": false, "CanDownload": false, "SortName": strings.ToLower(name), "ChannelId": nil, "IsFolder": true, "Type": "CollectionFolder",
		"CollectionType": collectionType, "ImageTags": map[string]any{"Primary": id}, "BackdropImageTags": []any{}, "LocationType": "FileSystem",
		"MediaType": "Unknown", "ParentId": "root", "UserData": map[string]any{"PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": false}}
}

// ---- item JSON -------------------------------------------------------------

func (s *Server) jfUserData(id string, progress float64, durationSec float64, plays int, lastPlay time.Time) map[string]any {
	ud := map[string]any{"PlaybackPositionTicks": int64(0), "PlayCount": plays, "IsFavorite": false, "Played": progress >= 1, "Key": s.cat.JFID(id)}
	if progress > 0 && progress < 1 {
		ud["PlaybackPositionTicks"] = ticks(progress * durationSec)
		ud["PlayedPercentage"] = progress * 100
	}
	if !lastPlay.IsZero() {
		ud["LastPlayedDate"] = jfDate(lastPlay)
	}
	return ud
}

func (s *Server) jfAlbum(a *Album) map[string]any {
	id := s.cat.JFID(a.ID)
	artistID := s.cat.JFID(a.Artist.ID)
	return map[string]any{
		"Name": a.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(a.AddedAt), "CanDelete": false,
		"SortName": strings.ToLower(a.Title), "PremiereDate": jfDate(time.Date(a.Year, 6, 1, 0, 0, 0, 0, time.UTC)), "ProductionYear": a.Year,
		"Genres": []string{a.Genre}, "GenreItems": []map[string]any{{"Name": a.Genre, "Id": s.cat.JFID("genre|" + a.Genre)}},
		"RunTimeTicks": ticks(a.Duration()), "ChildCount": len(a.Tracks), "SongCount": len(a.Tracks),
		"IsFolder": true, "Type": "MusicAlbum", "MediaType": "Unknown", "ParentId": jfMusicView,
		"AlbumArtist": a.Artist.Name, "AlbumArtists": []map[string]any{{"Name": a.Artist.Name, "Id": artistID}},
		"Artists": []string{a.Artist.Name}, "ArtistItems": []map[string]any{{"Name": a.Artist.Name, "Id": artistID}},
		"ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{}, "LocationType": "FileSystem", "Tags": []any{},
		"Overview": fmt.Sprintf("%s by %s (%d).", a.Title, a.Artist.Name, a.Year),
		"UserData": s.jfUserData(a.ID, 0, a.Duration(), int(a.Played*float64(len(a.Tracks))), a.LastPlay),
	}
}

func (s *Server) jfTrack(t *Track, withSources bool) map[string]any {
	id := s.cat.JFID(t.ID)
	albumID := s.cat.JFID(t.Album.ID)
	artistID := s.cat.JFID(t.Album.Artist.ID)
	item := map[string]any{
		"Name": t.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(t.Album.AddedAt), "CanDelete": false,
		"SortName": strings.ToLower(t.Title), "ProductionYear": t.Album.Year, "IndexNumber": s.discTrack(t), "ParentIndexNumber": s.disc(t),
		"Genres": []string{t.Album.Genre}, "RunTimeTicks": ticks(t.Duration), "IsFolder": false, "Type": "Audio", "MediaType": "Audio",
		"ParentId": albumID, "AlbumId": albumID, "Album": t.Album.Title, "AlbumPrimaryImageTag": "tag" + albumID[:8],
		"AlbumArtist": t.Album.Artist.Name, "AlbumArtists": []map[string]any{{"Name": t.Album.Artist.Name, "Id": artistID}},
		"Artists": []string{t.Album.Artist.Name}, "ArtistItems": []map[string]any{{"Name": t.Album.Artist.Name, "Id": artistID}},
		"ImageTags": map[string]any{"Primary": "tag" + albumID[:8]}, "BackdropImageTags": []any{}, "LocationType": "FileSystem",
		"Container": "wav", "UserData": s.jfUserData(t.ID, 0, t.Duration, t.Plays, time.Time{}),
	}
	if withSources {
		item["MediaSources"] = []map[string]any{s.jfAudioSource(t)}
	}
	return item
}

func (s *Server) jfAudioSource(t *Track) map[string]any {
	id := s.cat.JFID(t.ID)
	return map[string]any{
		"Protocol": "File", "Id": id, "Path": "/music/" + slug(t.Album.Artist.Name) + "/" + slug(t.Album.Title) + "/" + slug(t.Title) + ".wav",
		"Type": "Default", "Container": "wav", "Size": WAVSize(t.Duration), "Name": slug(t.Title), "IsRemote": false, "ETag": id[:8],
		"RunTimeTicks": ticks(t.Duration), "ReadAtNativeFramerate": false, "IgnoreDts": false, "IgnoreIndex": false, "GenPtsInput": false,
		"SupportsTranscoding": true, "SupportsDirectStream": true, "SupportsDirectPlay": true, "IsInfiniteStream": false, "RequiresOpening": false,
		"RequiresClosing": false, "RequiresLooping": false, "SupportsProbing": true, "VideoType": nil,
		"MediaStreams": []map[string]any{{"Codec": "pcm_s16le", "Type": "Audio", "Index": 0, "Channels": 1, "SampleRate": 22050, "BitDepth": 16,
			"BitRate": 352800, "IsDefault": true, "IsForced": false, "IsExternal": false, "DisplayTitle": "PCM - Mono - Default", "Language": "und",
			"IsInterlaced": false, "IsTextSubtitleStream": false, "SupportsExternalStream": false, "Level": 0}},
		"MediaAttachments": []any{}, "Formats": []any{}, "Bitrate": 352800, "RequiredHttpHeaders": map[string]any{}, "DefaultAudioStreamIndex": 0,
	}
}

func (s *Server) jfArtist(a *Artist) map[string]any {
	id := s.cat.JFID(a.ID)
	return map[string]any{
		"Name": a.Name, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(s.cat.daysAgo(380)), "SortName": strings.ToLower(a.Name),
		"Overview": a.Bio, "Genres": []string{a.Genre}, "IsFolder": true, "Type": "MusicArtist", "MediaType": "Unknown",
		"ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{}, "LocationType": "FileSystem",
		"AlbumCount": len(a.Albums), "ChildCount": len(a.Albums), "UserData": s.jfUserData(a.ID, 0, 0, 0, time.Time{}),
	}
}

// ---- audiobooks (Books on) ---------------------------------------------------

// jfAudioBook renders a catalog Book as a Jellyfin AudioBook item (the shape
// a books library hands out: an Audio subclass with MediaType "Audio", the
// author as AlbumArtist, Chapters and one MediaSource when `full`). The
// ebooks are never rendered here; the callers skip them.
func (s *Server) jfAudioBook(b *Book, full bool) map[string]any {
	id := s.cat.JFID(b.ID)
	authorID := s.cat.JFID(b.Author.ID)
	prog := s.progressOf(b.ID, b.Progress)
	year, _ := strconv.Atoi(b.Year)
	plays := 0
	if prog >= 1 {
		plays = 1
	}
	item := map[string]any{
		"Name": b.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(b.AddedAt), "CanDelete": false,
		"SortName": strings.ToLower(strings.TrimPrefix(b.Title, "The ")), "ProductionYear": year,
		"Overview": b.Blurb, "Genres": b.Genres, "GenreItems": genreItems(s, b.Genres), "RunTimeTicks": ticks(b.Duration),
		"IsFolder": false, "Type": "AudioBook", "MediaType": "Audio", "ParentId": jfBooksView, "LocationType": "FileSystem",
		"Container": "wav", "Path": "/audiobooks/" + slug(b.Title) + "/" + b.Filename,
		"AlbumArtist": b.Author.Name, "AlbumArtists": []map[string]any{{"Name": b.Author.Name, "Id": authorID}},
		"Artists": []string{b.Author.Name}, "ArtistItems": []map[string]any{{"Name": b.Author.Name, "Id": authorID}},
		"ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{}, "Tags": []any{},
		"UserData": s.jfUserData(b.ID, prog, b.Duration, plays, b.LastPlay),
	}
	if full {
		item["MediaSources"] = []map[string]any{s.jfBookSource(b)}
		item["MediaStreams"] = item["MediaSources"].([]map[string]any)[0]["MediaStreams"]
		chapters := []map[string]any{}
		for _, ch := range b.Chapters {
			chapters = append(chapters, map[string]any{"StartPositionTicks": ticks(ch.Start), "Name": ch.Title})
		}
		item["Chapters"] = chapters
	}
	return item
}

// jfBookSource is jfAudioSource's shape for a book's one WAV.
func (s *Server) jfBookSource(b *Book) map[string]any {
	id := s.cat.JFID(b.ID)
	return map[string]any{
		"Protocol": "File", "Id": id, "Path": "/audiobooks/" + slug(b.Title) + "/" + b.Filename,
		"Type": "Default", "Container": "wav", "Size": WAVSize(b.Duration), "Name": slug(b.Title), "IsRemote": false, "ETag": id[:8],
		"RunTimeTicks": ticks(b.Duration), "ReadAtNativeFramerate": false, "IgnoreDts": false, "IgnoreIndex": false, "GenPtsInput": false,
		"SupportsTranscoding": true, "SupportsDirectStream": true, "SupportsDirectPlay": true, "IsInfiniteStream": false, "RequiresOpening": false,
		"RequiresClosing": false, "RequiresLooping": false, "SupportsProbing": true, "VideoType": nil,
		"MediaStreams": []map[string]any{{"Codec": "pcm_s16le", "Type": "Audio", "Index": 0, "Channels": 1, "SampleRate": 22050, "BitDepth": 16,
			"BitRate": 352800, "IsDefault": true, "IsForced": false, "IsExternal": false, "DisplayTitle": "PCM - Mono - Default", "Language": "und",
			"IsInterlaced": false, "IsTextSubtitleStream": false, "SupportsExternalStream": false, "Level": 0}},
		"MediaAttachments": []any{}, "Formats": []any{}, "Bitrate": 352800, "RequiredHttpHeaders": map[string]any{}, "DefaultAudioStreamIndex": 0,
	}
}

// jfAuthor renders a catalog Author in the MusicArtist shape (Jellyfin lists
// a books library's authors through /Artists/AlbumArtists, as album artists).
// AlbumCount counts the audiobooks only.
func (s *Server) jfAuthor(a *Author) map[string]any {
	id := s.cat.JFID(a.ID)
	n := 0
	for _, b := range a.Books {
		if !b.Ebook {
			n++
		}
	}
	return map[string]any{
		"Name": a.Name, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(s.cat.daysAgo(300)), "SortName": strings.ToLower(a.Name),
		"Overview": a.Bio, "IsFolder": true, "Type": "MusicArtist", "MediaType": "Unknown", "ParentId": jfBooksView,
		"ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{}, "LocationType": "FileSystem",
		"AlbumCount": n, "ChildCount": n, "UserData": s.jfUserData(a.ID, 0, 0, 0, time.Time{}),
	}
}

// jfAudioBooksAuthors: the authors with at least one audiobook, A–Z.
func (s *Server) jfAudioBookAuthors() []map[string]any {
	items := []map[string]any{}
	for _, a := range s.cat.Authors {
		item := s.jfAuthor(a)
		if item["AlbumCount"].(int) > 0 {
			items = append(items, item)
		}
	}
	return items
}

// jfSessionReport records a /Sessions/Playing[/Progress|/Stopped] body and,
// for a book, moves its progress the way a real server would on Stopped.
func (s *Server) jfSessionReport(r *http.Request, p []string) {
	if r.Method != http.MethodPost || len(p) == 0 || p[0] != "Playing" {
		return
	}
	route := "Sessions/Playing"
	if len(p) >= 2 {
		route += "/" + p[1]
	}
	body := readBody(r)
	itemID, _ := body["ItemId"].(string)
	sess, _ := body["PlaySessionId"].(string)
	pos, _ := body["PositionTicks"].(float64)
	paused, _ := body["IsPaused"].(bool)
	s.record(reportRecord{Route: route, ItemID: itemID, PlaySessionID: sess, PositionTicks: int64(pos), IsPaused: paused})
	if route == "Sessions/Playing/Stopped" {
		if b, isBook := s.cat.Get(s.cat.IDForJF(itemID)).(*Book); isBook && !b.Ebook && b.Duration > 0 {
			s.setProgress(b.ID, (pos/ticksPerSec)/b.Duration)
		}
	}
}

// jfUserDataWrite: POST /UserItems/{id}/UserData?userId= {PlaybackPositionTicks?, Played?}
// — applied verbatim, as Jellyfin's SaveUserData does, and echoed back.
func (s *Server) jfUserDataWrite(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != http.MethodPost || len(p) < 2 || p[1] != "UserData" {
		notFound(w)
		return
	}
	b, isBook := s.cat.Get(s.cat.IDForJF(p[0])).(*Book)
	if !isBook {
		notFound(w)
		return
	}
	body := readBody(r)
	pos, hasPos := body["PlaybackPositionTicks"].(float64)
	played, hasPlayed := body["Played"].(bool)
	s.record(reportRecord{Route: "UserItems/UserData", ItemID: s.cat.JFID(b.ID), PositionTicks: int64(pos)})
	if hasPlayed && played {
		s.setProgress(b.ID, 1)
	} else if hasPos && b.Duration > 0 {
		s.setProgress(b.ID, (pos/ticksPerSec)/b.Duration)
	}
	ok(w, s.jfUserData(b.ID, s.progressOf(b.ID, b.Progress), b.Duration, 0, b.LastPlay))
}

// jfPlayedWrite: POST /UserPlayedItems/{id}?userId= marks a book finished.
func (s *Server) jfPlayedWrite(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != http.MethodPost || len(p) < 1 {
		notFound(w)
		return
	}
	b, isBook := s.cat.Get(s.cat.IDForJF(p[0])).(*Book)
	if !isBook {
		notFound(w)
		return
	}
	s.record(reportRecord{Route: "UserPlayedItems", ItemID: s.cat.JFID(b.ID)})
	s.setProgress(b.ID, 1)
	ok(w, s.jfUserData(b.ID, 1, b.Duration, 1, s.cat.Now))
}

// jfPeople: each person with their own id and a Primary image (a generated
// portrait, see Portrait), which the apps fetch at /Items/{id}/Images/Primary.
func (s *Server) jfPeople(cast []castMember, director string) []map[string]any {
	out := []map[string]any{}
	person := func(name, role, typ string) map[string]any {
		id := s.cat.JFID(PersonID(name))
		return map[string]any{"Name": name, "Id": id, "Role": role, "Type": typ, "PrimaryImageTag": "pt" + id[:8]}
	}
	if director != "" {
		out = append(out, person(director, "", "Director"))
	}
	for _, c := range cast {
		out = append(out, person(c.Name, c.Role, "Actor"))
	}
	return out
}

func (s *Server) jfVideoSource(id string, clip string, durationSec float64, uhd bool) map[string]any {
	width, height := 1280, 720
	if uhd {
		width, height = 3840, 2160
	}
	size := s.clipSize(clip)
	if size == 0 {
		size = int64(durationSec * 400000)
	}
	return map[string]any{
		"Protocol": "File", "Id": id, "Path": "/video/" + clip + "/clip.mp4", "Type": "Default", "Container": "mp4", "Size": size,
		"Name": clip, "IsRemote": false, "ETag": id[:8], "RunTimeTicks": ticks(durationSec), "SupportsTranscoding": true,
		"SupportsDirectStream": true, "SupportsDirectPlay": true, "IsInfiniteStream": false, "RequiresOpening": false, "RequiresClosing": false,
		"RequiresLooping": false, "SupportsProbing": true, "VideoType": "VideoFile",
		"MediaStreams": []map[string]any{
			{"Codec": "h264", "Type": "Video", "Index": 0, "Width": width, "Height": height, "BitRate": 3000000, "BitDepth": 8, "RealFrameRate": 24.0,
				"AverageFrameRate": 24.0, "IsDefault": true, "IsForced": false, "IsExternal": false, "DisplayTitle": "1080p H264", "Profile": "High",
				"Level": 40, "AspectRatio": "16:9", "VideoRange": "SDR", "VideoRangeType": "SDR", "ColorTransfer": "bt709", "ColorPrimaries": "bt709",
				"IsInterlaced": false, "IsAVC": true, "IsTextSubtitleStream": false, "SupportsExternalStream": false, "PixelFormat": "yuv420p", "RefFrames": 1},
			{"Codec": "aac", "Type": "Audio", "Index": 1, "Channels": 1, "SampleRate": 44100, "BitRate": 96000, "IsDefault": true, "IsForced": false,
				"IsExternal": false, "DisplayTitle": "AAC - Mono - Default", "Language": "eng", "ChannelLayout": "mono", "IsInterlaced": false,
				"IsTextSubtitleStream": false, "SupportsExternalStream": false, "Level": 0},
		},
		"MediaAttachments": []any{}, "Formats": []any{}, "Bitrate": 3096000, "RequiredHttpHeaders": map[string]any{}, "DefaultAudioStreamIndex": 1,
	}
}

func (s *Server) jfMovie(m *Movie, full bool) map[string]any {
	id := s.cat.JFID(m.ID)
	dur := float64(m.Minutes) * 60
	prog := s.movieProgress(m)
	item := map[string]any{
		"Name": m.Title, "OriginalTitle": m.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(m.AddedAt), "CanDelete": false,
		"SortName": strings.ToLower(strings.TrimPrefix(m.Title, "The ")), "PremiereDate": jfDate(time.Date(m.Year, 5, 10, 0, 0, 0, 0, time.UTC)),
		"ProductionYear": m.Year, "OfficialRating": m.Rating, "CommunityRating": m.Score, "CriticRating": int(m.Score * 10),
		"Overview": m.Summary, "Taglines": []string{m.Tagline}, "Genres": m.Genres, "RunTimeTicks": ticks(dur), "IsFolder": false,
		"Type": "Movie", "MediaType": "Video", "ParentId": jfMovieView, "LocationType": "FileSystem", "Container": "mp4",
		"ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []string{"bd" + id[:8]}, "Tags": []any{},
		"People": s.jfPeople(m.Cast, m.Director), "Studios": []map[string]any{{"Name": "Showcase Pictures", "Id": s.cat.JFID("studio")}},
		"UserData": s.jfUserData(m.ID, prog, dur, map[bool]int{true: 1, false: 0}[prog >= 1], m.LastPlay),
		"Width":    1280, "Height": 720, "Path": "/video/" + m.Clip + "/clip.mp4",
	}
	if m.UHD {
		item["Width"], item["Height"] = 3840, 2160
	}
	if full {
		item["MediaSources"] = []map[string]any{s.jfVideoSource(id, m.Clip, dur, m.UHD)}
		item["MediaStreams"] = item["MediaSources"].([]map[string]any)[0]["MediaStreams"]
		item["Chapters"] = []map[string]any{{"StartPositionTicks": 0, "Name": "Opening"}, {"StartPositionTicks": ticks(dur * 0.35), "Name": "Act Two"}, {"StartPositionTicks": ticks(dur * 0.8), "Name": "Finale"}}
		item["GenreItems"] = genreItems(s, m.Genres)
	}
	return item
}

func genreItems(s *Server, genres []string) []map[string]any {
	var out []map[string]any
	for _, g := range genres {
		out = append(out, map[string]any{"Name": g, "Id": s.cat.JFID("genre|" + g)})
	}
	return out
}

func (s *Server) jfShow(sh *Show, full bool) map[string]any {
	id := s.cat.JFID(sh.ID)
	watched, total := s.showCounts(sh)
	item := map[string]any{
		"Name": sh.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(sh.AddedAt), "CanDelete": false,
		"SortName": strings.ToLower(strings.TrimPrefix(sh.Title, "The ")), "PremiereDate": jfDate(time.Date(sh.Year, 9, 12, 0, 0, 0, 0, time.UTC)),
		"ProductionYear": sh.Year, "OfficialRating": sh.Rating, "CommunityRating": 7.9, "Overview": sh.Summary, "Genres": sh.Genres,
		"RunTimeTicks": ticks(float64(sh.Minutes) * 60), "IsFolder": true, "Type": "Series", "MediaType": "Unknown", "ParentId": jfShowView,
		"LocationType": "FileSystem", "ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []string{"bd" + id[:8]},
		"ChildCount": len(sh.Seasons), "RecursiveItemCount": total, "Status": "Continuing", "AirDays": []string{"Thursday"}, "Tags": []any{},
		"People": s.jfPeople(sh.Cast, ""), "Studios": []map[string]any{{"Name": "Showcase Television", "Id": s.cat.JFID("studio2")}},
		"UserData": map[string]any{"UnplayedItemCount": total - watched, "PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": watched == total, "Key": id},
	}
	if full {
		item["GenreItems"] = genreItems(s, sh.Genres)
	}
	return item
}

func (s *Server) jfSeason(se *Season) map[string]any {
	id := s.cat.JFID(se.ID)
	showID := s.cat.JFID(se.Show.ID)
	watched := s.seasonWatched(se)
	return map[string]any{
		"Name": fmt.Sprintf("Season %d", se.Index), "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(se.Show.AddedAt),
		"SortName": fmt.Sprintf("%04d", se.Index), "IndexNumber": se.Index, "ProductionYear": se.Show.Year, "IsFolder": true, "Type": "Season",
		"MediaType": "Unknown", "ParentId": showID, "SeriesId": showID, "SeriesName": se.Show.Title, "SeriesPrimaryImageTag": "tag" + showID[:8],
		"ChildCount": len(se.Episodes), "LocationType": "FileSystem", "ImageTags": map[string]any{"Primary": "tag" + id[:8]},
		"BackdropImageTags": []any{}, "UserData": map[string]any{"UnplayedItemCount": len(se.Episodes) - watched, "PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": watched == len(se.Episodes)},
	}
}

func (s *Server) jfEpisode(e *Episode, full bool) map[string]any {
	id := s.cat.JFID(e.ID)
	showID := s.cat.JFID(e.Show.ID)
	seasonID := s.cat.JFID(e.Season.ID)
	dur := float64(e.Minutes) * 60
	prog := s.episodeProgress(e)
	seen := s.episodeWatched(e)
	item := map[string]any{
		"Name": e.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(e.Aired), "CanDelete": false,
		"SortName": fmt.Sprintf("%04d-%04d", e.Season.Index, e.Index), "PremiereDate": jfDate(e.Aired), "ProductionYear": e.Aired.Year(),
		"OfficialRating": e.Show.Rating, "Overview": e.Summary, "IndexNumber": e.Index, "ParentIndexNumber": e.Season.Index,
		"RunTimeTicks": ticks(dur), "IsFolder": false, "Type": "Episode", "MediaType": "Video", "ParentId": seasonID,
		"SeriesId": showID, "SeriesName": e.Show.Title, "SeriesPrimaryImageTag": "tag" + showID[:8], "SeasonId": seasonID, "SeasonName": fmt.Sprintf("Season %d", e.Season.Index),
		"ParentBackdropItemId": showID, "ParentBackdropImageTags": []string{"bd" + showID[:8]}, "ParentLogoItemId": nil,
		"LocationType": "FileSystem", "Container": "mp4", "ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{},
		"UserData": s.jfUserData(e.ID, prog, dur, map[bool]int{true: 1, false: 0}[seen], e.lastPlay(s.cat)),
		"Width":    1280, "Height": 720, "Path": "/video/" + e.Clip + "/clip.mp4",
	}
	if seen {
		item["UserData"].(map[string]any)["Played"] = true
	}
	if full {
		item["MediaSources"] = []map[string]any{s.jfVideoSource(id, e.Clip, dur, false)}
		item["MediaStreams"] = item["MediaSources"].([]map[string]any)[0]["MediaStreams"]
		item["Chapters"] = []map[string]any{{"StartPositionTicks": 0, "Name": "Cold Open"}, {"StartPositionTicks": ticks(dur * 0.5), "Name": "Act Two"}}
		item["GenreItems"] = genreItems(s, e.Show.Genres)
		item["Genres"] = e.Show.Genres
	}
	return item
}

func (s *Server) jfBoxSet(c *Collection) map[string]any {
	id := s.cat.JFID(c.ID)
	n := len(c.Movies) + len(c.Shows)
	return map[string]any{
		"Name": c.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(s.cat.daysAgo(60)), "SortName": strings.ToLower(c.Title),
		"Overview": fmt.Sprintf("%d titles, hand-picked.", n), "IsFolder": true, "Type": "BoxSet", "MediaType": "Unknown", "ParentId": jfMovieView,
		"LocationType": "FileSystem", "ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{}, "ChildCount": n, "RecursiveItemCount": n,
		"ProductionYear": 2024, "UserData": map[string]any{"PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": false},
	}
}

func (s *Server) jfPlaylist(p *Playlist) map[string]any {
	id := s.cat.JFID(p.ID)
	var dur float64
	for _, t := range p.Tracks {
		dur += t.Duration
	}
	for _, m := range p.Movies {
		dur += float64(m.Minutes) * 60
	}
	mediaType := "Audio"
	if p.Kind == "video" {
		mediaType = "Video"
	}
	return map[string]any{
		"Name": p.Title, "ServerId": jfServerID, "Id": id, "Etag": id, "DateCreated": jfDate(s.cat.daysAgo(30)), "SortName": strings.ToLower(p.Title),
		"Overview": "", "IsFolder": true, "Type": "Playlist", "MediaType": mediaType, "ChildCount": len(p.Tracks) + len(p.Movies),
		"RunTimeTicks": ticks(dur), "LocationType": "FileSystem", "ImageTags": map[string]any{"Primary": "tag" + id[:8]}, "BackdropImageTags": []any{},
		"UserData": map[string]any{"PlaybackPositionTicks": 0, "PlayCount": 0, "IsFavorite": false, "Played": false},
	}
}

// jfAny renders any entity by catalog id.
func (s *Server) jfAny(id string, full bool) map[string]any {
	switch v := s.cat.Get(id).(type) {
	case *Album:
		return s.jfAlbum(v)
	case *Track:
		return s.jfTrack(v, true)
	case *Artist:
		return s.jfArtist(v)
	case *Movie:
		return s.jfMovie(v, full)
	case *Show:
		return s.jfShow(v, full)
	case *Season:
		return s.jfSeason(v)
	case *Episode:
		return s.jfEpisode(v, full)
	case *Collection:
		return s.jfBoxSet(v)
	case *Playlist:
		return s.jfPlaylist(v)
	case *Book:
		if s.Books && !v.Ebook {
			return s.jfAudioBook(v, full)
		}
	case *Author:
		if s.Books {
			return s.jfAuthor(v)
		}
	}
	return nil
}

// ---- /Users/{uid}/Items … -------------------------------------------------

func (s *Server) jfUserItems(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		s.jfQuery(w, r)
		return
	}
	switch p[0] {
	case "Latest":
		ok(w, s.jfLatest(r))
	case "Resume":
		ok(w, s.jfResume(r))
	default:
		id := s.cat.IDForJF(p[0])
		if id == "" {
			notFound(w)
			return
		}
		if len(p) >= 2 {
			switch p[1] {
			case "LocalTrailers", "SpecialFeatures":
				ok(w, []any{})
			case "Intros":
				ok(w, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
			default:
				notFound(w)
			}
			return
		}
		item := s.jfAny(id, true)
		if item == nil {
			notFound(w)
			return
		}
		ok(w, item)
	}
}

type jfQuery struct {
	parent    string
	types     map[string]bool
	recursive bool
	sortBy    string
	desc      bool
	start     int
	limit     int
	artist    string
	genre     string
	search    string
	ids       map[string]bool
	mediaType string
}

func parseJFQuery(r *http.Request) jfQuery {
	q := jfQuery{parent: qStr(r, "ParentId"), recursive: strings.EqualFold(qStr(r, "Recursive"), "true"),
		sortBy: strings.Split(qStr(r, "SortBy"), ",")[0], desc: strings.EqualFold(qStr(r, "SortOrder"), "Descending"),
		start: qInt(r, "StartIndex", 0), limit: qInt(r, "Limit", 0), artist: qStr(r, "ArtistIds"), genre: qStr(r, "GenreIds"),
		search: strings.ToLower(qStr(r, "SearchTerm")), mediaType: qStr(r, "MediaTypes")}
	if t := qStr(r, "IncludeItemTypes"); t != "" {
		q.types = map[string]bool{}
		for _, x := range strings.Split(t, ",") {
			q.types[strings.TrimSpace(x)] = true
		}
	}
	if ids := qStr(r, "Ids"); ids != "" {
		q.ids = map[string]bool{}
		for _, x := range strings.Split(ids, ",") {
			q.ids[strings.ToLower(strings.TrimSpace(x))] = true
		}
	}
	return q
}

func (q jfQuery) wants(t string) bool { return q.types == nil || q.types[t] }

// jfQuery answers /Users/{uid}/Items with filters the apps use.
func (s *Server) jfQuery(w http.ResponseWriter, r *http.Request) {
	q := parseJFQuery(r)
	var items []map[string]any
	add := func(m map[string]any) {
		if q.search != "" && !strings.Contains(strings.ToLower(m["Name"].(string)), q.search) {
			return
		}
		if q.ids != nil && !q.ids[strings.ToLower(m["Id"].(string))] {
			return
		}
		items = append(items, m)
	}
	parentID := s.cat.IDForJF(q.parent)
	parentIsAlbum := false
	if al, isAlbum := s.cat.Get(parentID).(*Album); isAlbum && q.parent != "" {
		parentIsAlbum = true
		if q.wants("Audio") {
			for _, t := range al.Tracks {
				add(s.jfTrack(t, true))
			}
		}
	}
	if co, isBox := s.cat.Get(parentID).(*Collection); isBox {
		for _, m := range co.Movies {
			add(s.jfMovie(m, false))
		}
		for _, sh := range co.Shows {
			add(s.jfShow(sh, false))
		}
	}
	musicParent := q.parent == "" || q.parent == jfMusicView
	movieParent := q.parent == "" || q.parent == jfMovieView
	showParent := q.parent == "" || q.parent == jfShowView
	if !parentIsAlbum && s.cat.Get(parentID) == nil {
		if q.wants("MusicAlbum") && musicParent && q.mediaType == "" {
			for _, a := range s.cat.Albums {
				if q.artist != "" && s.cat.JFID(a.Artist.ID) != strings.ToLower(q.artist) {
					continue
				}
				if q.genre != "" && s.cat.JFID("genre|"+a.Genre) != strings.ToLower(q.genre) {
					continue
				}
				add(s.jfAlbum(a))
			}
		}
		if q.wants("Audio") && musicParent && q.types != nil && q.mediaType == "" {
			for _, t := range s.cat.Tracks {
				if q.artist != "" && s.cat.JFID(t.Album.Artist.ID) != strings.ToLower(q.artist) {
					continue
				}
				add(s.jfTrack(t, true))
			}
		}
		if q.wants("MusicArtist") && q.types != nil && q.types["MusicArtist"] {
			for _, a := range s.cat.Artists {
				add(s.jfArtist(a))
			}
		}
		if q.wants("Playlist") && q.types != nil && q.types["Playlist"] {
			for _, pl := range s.cat.Playlists {
				add(s.jfPlaylist(pl))
			}
		}
		if q.wants("BoxSet") && q.types != nil && q.types["BoxSet"] {
			for _, co := range s.cat.Collections {
				add(s.jfBoxSet(co))
			}
		}
		if (q.wants("Movie") && q.types != nil) && movieParent {
			for _, m := range s.cat.Movies {
				add(s.jfMovie(m, strings.Contains(qStr(r, "Fields"), "MediaSources")))
			}
		}
		if (q.wants("Series") && q.types != nil) && showParent {
			for _, sh := range s.cat.Shows {
				add(s.jfShow(sh, false))
			}
		}
		if q.wants("Episode") && q.types != nil && q.types["Episode"] {
			for _, e := range s.cat.Episodes {
				add(s.jfEpisode(e, false))
			}
		}
		// Audiobooks (Books on): the books view, or a server-wide AudioBook
		// query; `MusicAlbum` counts too since a real books library holds
		// multi-file books as albums. ArtistIds = the author. A music query
		// (no view, MusicAlbum only) never reaches here.
		booksParent := q.parent == jfBooksView || (q.parent == "" && q.types != nil && q.types["AudioBook"])
		if s.Books && booksParent && q.types != nil && (q.types["AudioBook"] || q.types["MusicAlbum"]) && q.mediaType == "" {
			full := strings.Contains(qStr(r, "Fields"), "MediaSources")
			for _, b := range s.cat.Books {
				if b.Ebook {
					continue
				}
				if q.artist != "" && s.cat.JFID(b.Author.ID) != strings.ToLower(q.artist) {
					continue
				}
				add(s.jfAudioBook(b, full))
			}
		}
		if q.types == nil && q.ids != nil {
			for id := range q.ids {
				if cid := s.cat.IDForJF(id); cid != "" {
					if m := s.jfAny(cid, false); m != nil {
						items = append(items, m)
					}
				}
			}
		}
	}
	sortItems(items, q.sortBy, q.desc)
	total := len(items)
	if q.start > len(items) {
		q.start = len(items)
	}
	items = items[q.start:]
	if q.limit > 0 && len(items) > q.limit {
		items = items[:q.limit]
	}
	if items == nil {
		items = []map[string]any{}
	}
	ok(w, map[string]any{"Items": items, "TotalRecordCount": total, "StartIndex": q.start})
}

func sortItems(items []map[string]any, by string, desc bool) {
	key := func(m map[string]any) string {
		switch by {
		case "SortName", "Name", "":
			if v, ok := m["SortName"].(string); ok {
				return v
			}
			return strings.ToLower(m["Name"].(string))
		case "DateCreated", "DatePlayed":
			v, _ := m["DateCreated"].(string)
			return v
		case "PremiereDate", "ProductionYear":
			v, _ := m["PremiereDate"].(string)
			return v
		case "IndexNumber":
			n, _ := m["IndexNumber"].(int)
			return fmt.Sprintf("%06d", n)
		case "PlayCount":
			ud, _ := m["UserData"].(map[string]any)
			n, _ := ud["PlayCount"].(int)
			return fmt.Sprintf("%06d", n)
		case "Random":
			return m["Id"].(string)[:6]
		}
		return strings.ToLower(m["Name"].(string))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if desc {
			return key(items[i]) > key(items[j])
		}
		return key(items[i]) < key(items[j])
	})
}

func (s *Server) jfLatest(r *http.Request) []map[string]any {
	limit := qInt(r, "Limit", 20)
	var items []map[string]any
	switch qStr(r, "ParentId") {
	case jfMusicView, "":
		for _, a := range s.cat.RecentAlbums(limit) {
			items = append(items, s.jfAlbum(a))
		}
	case jfMovieView:
		for _, m := range s.cat.RecentMovies(limit) {
			items = append(items, s.jfMovie(m, false))
		}
	case jfShowView:
		for _, sh := range s.cat.RecentShows(limit) {
			items = append(items, s.jfShow(sh, false))
		}
	case jfBooksView:
		if s.Books {
			for _, b := range s.cat.RecentBooks(len(s.cat.Books)) {
				if b.Ebook {
					continue
				}
				items = append(items, s.jfAudioBook(b, false))
				if len(items) >= limit {
					break
				}
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

func (s *Server) jfResume(r *http.Request) map[string]any {
	var items []map[string]any
	types := qStr(r, "IncludeItemTypes")
	if s.Books && qStr(r, "ParentId") == jfBooksView {
		// Continue listening: the books part-way through, most recent first
		// (the Audiobookshelf dialect's list, in Jellyfin's shape).
		for _, b := range s.cat.InProgressBooks() {
			if !b.Ebook {
				items = append(items, s.jfAudioBook(b, strings.Contains(qStr(r, "Fields"), "MediaSources")))
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		if limit := qInt(r, "Limit", 0); limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		return map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0}
	}
	video := qStr(r, "MediaTypes") == "Video" || strings.Contains(types, "Movie") || strings.Contains(types, "Episode")
	if video || types == "" {
		movies, episodes := s.continueWatching()
		for _, m := range movies {
			items = append(items, s.jfMovie(m, strings.Contains(qStr(r, "Fields"), "MediaSources")))
		}
		for _, e := range episodes {
			items = append(items, s.jfEpisode(e, strings.Contains(qStr(r, "Fields"), "MediaSources")))
		}
	}
	if strings.Contains(types, "Audio") || strings.Contains(types, "MusicAlbum") {
		for _, a := range s.cat.PlayedAlbums() {
			if a.Played < 1 {
				items = append(items, s.jfAlbum(a))
			}
		}
	}
	if items == nil {
		items = []map[string]any{}
	}
	if limit := qInt(r, "Limit", 0); limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0}
}

// ---- /Items/{id}/… (images, playback info) --------------------------------

func (s *Server) jfItemsRoot(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 0 {
		s.jfQuery(w, r)
		return
	}
	id := s.cat.IDForJF(p[0])
	if id == "" && p[0] != jfMusicView && p[0] != jfMovieView && p[0] != jfShowView && p[0] != "user" && !(s.Books && p[0] == jfBooksView) {
		notFound(w)
		return
	}
	if len(p) == 1 {
		if item := s.jfAny(id, true); item != nil {
			ok(w, item)
			return
		}
		notFound(w)
		return
	}
	switch p[1] {
	case "Images":
		kind := artPoster
		if len(p) >= 3 && strings.EqualFold(p[2], "Backdrop") {
			// Backdrops carry no type: the apps set the title over them.
			kind = artBackdrop
		}
		switch v := s.cat.Get(id).(type) {
		case *Album, *Artist, *Playlist, *Track:
			kind = artSquare
			if t, isTrack := v.(*Track); isTrack {
				id = t.Album.ID
			}
		case *Episode:
			kind = artWide
		case *Author:
			// Books on: an author's art is square, as the Audiobookshelf
			// dialect serves it (off, nothing asks for an author's image).
			if s.Books {
				kind = artSquare
			}
		}
		if id == "" {
			id = p[0] // a view
		}
		serveJPEG(w, s.art(id, kind, qInt(r, "maxWidth", 0)))
	case "PlaybackInfo":
		s.jfPlaybackInfo(w, r, id)
	case "Download":
		if t, isTrack := s.cat.Get(id).(*Track); isTrack {
			if s.refusesDownload() {
				s.recordPart(partRequest{Server: "jellyfin", Item: s.cat.JFID(t.ID), Route: "Download", Download: true, Status: http.StatusForbidden})
				http.Error(w, "no download permission", http.StatusForbidden)
				return
			}
			s.recordPart(partRequest{Server: "jellyfin", Item: s.cat.JFID(t.ID), Route: "Download", Download: true, Status: http.StatusOK})
			serveWAV(w, r, t.Seed, t.Duration)
			return
		}
		notFound(w)
	case "Similar":
		ok(w, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
	case "InstantMix":
		// /Items/{id}/InstantMix: around an artist, a playlist's artists, an
		// album's or a song's artist.
		var seeds []*Artist
		switch v := s.cat.Get(id).(type) {
		case *Artist:
			seeds = []*Artist{v}
		case *Playlist:
			seeds = playlistArtists(v)
		case *Album:
			seeds = []*Artist{v.Artist}
		case *Track:
			seeds = []*Artist{v.Album.Artist}
		default:
			notFound(w)
			return
		}
		s.jfInstantMix(w, r, seeds)
	case "ThemeMedia":
		ok(w, map[string]any{"ThemeVideosResult": map[string]any{"Items": []any{}}, "ThemeSongsResult": map[string]any{"Items": []any{}}})
	default:
		notFound(w)
	}
}

func (s *Server) jfPlaybackInfo(w http.ResponseWriter, r *http.Request, id string) {
	_ = readBody(r)
	jf := s.cat.JFID(id)
	var source map[string]any
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		source = s.jfVideoSource(jf, v.Clip, float64(v.Minutes)*60, v.UHD)
	case *Episode:
		source = s.jfVideoSource(jf, v.Clip, float64(v.Minutes)*60, false)
	case *Track:
		source = s.jfAudioSource(v)
	default:
		notFound(w)
		return
	}
	// Offer the HLS master too: the TV asks for transcoding and plays the
	// TranscodingUrl; the phone direct-plays /Videos/{id}/stream.
	source["TranscodingUrl"] = fmt.Sprintf("/videos/%s/master.m3u8?MediaSourceId=%s&PlaySessionId=ps_%s&ApiKey=%s", jf, jf, jf[:8], s.Token)
	source["TranscodingSubProtocol"] = "hls"
	source["TranscodingContainer"] = "mp4"
	source["TranscodeReasons"] = []string{"ContainerNotSupported"}
	ok(w, map[string]any{"MediaSources": []map[string]any{source}, "PlaySessionId": "ps_" + jf[:8]})
}

// ---- artists / genres / playlists / shows ----------------------------------

func (s *Server) jfArtists(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) >= 1 && p[0] != "AlbumArtists" {
		id := s.cat.IDForJF(p[0])
		if id == "" && len(p) == 1 {
			// Jellyfin also answers /Artists/{name} with that artist — the
			// phone's Now Playing reaches an artist page that way.
			for _, a := range s.cat.Artists {
				if strings.EqualFold(a.Name, p[0]) {
					id = a.ID
					break
				}
			}
		}
		if a, isArtist := s.cat.Get(id).(*Artist); isArtist {
			if len(p) >= 2 && p[1] == "InstantMix" {
				s.jfInstantMix(w, r, []*Artist{a})
				return
			}
			if len(p) >= 2 && p[1] == "Similar" {
				var items []map[string]any
				for _, o := range s.cat.Artists {
					if o != a && o.Genre == a.Genre {
						items = append(items, s.jfArtist(o))
					}
				}
				if items == nil {
					items = []map[string]any{}
				}
				ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items)})
				return
			}
			ok(w, s.jfArtist(a))
			return
		}
		if au, isAuthor := s.cat.Get(id).(*Author); isAuthor && s.Books {
			if len(p) >= 2 && p[1] == "Images" {
				serveJPEG(w, s.art(id, artSquare, qInt(r, "maxWidth", 0)))
				return
			}
			ok(w, s.jfAuthor(au))
			return
		}
		if len(p) >= 2 && p[1] == "Images" {
			serveJPEG(w, s.art(id, artSquare, qInt(r, "maxWidth", 0)))
			return
		}
	}
	var items []map[string]any
	if s.Books && qStr(r, "ParentId") == jfBooksView {
		// The books view's album artists are its authors (Books on).
		items = s.jfAudioBookAuthors()
		q := parseJFQuery(r)
		sortItems(items, q.sortBy, q.desc)
		ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
		return
	}
	for _, a := range s.cat.Artists {
		items = append(items, s.jfArtist(a))
	}
	q := parseJFQuery(r)
	sortItems(items, q.sortBy, q.desc)
	ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

func (s *Server) jfGenres(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	var items []map[string]any
	for _, a := range s.cat.Artists {
		if !seen[a.Genre] {
			seen[a.Genre] = true
			items = append(items, map[string]any{"Name": a.Genre, "Id": s.cat.JFID("genre|" + a.Genre), "ServerId": jfServerID, "Type": "MusicGenre"})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["Name"].(string) < items[j]["Name"].(string) })
	ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

// jfInstantMix answers Jellyfin's InstantMix routes. A real server mixes by
// the seed's GENRES; every showcase artist has a genre of its own, so a
// faithful genre mix would hold the seed alone. Here the seeds' songs are
// joined by the songs of the two artists after each seed in catalog order
// (the neighbours stand in for genre-mates), round-robin by artist, then cut
// to Limit (default 100). Deterministic, so a probe can count on it.
func (s *Server) jfInstantMix(w http.ResponseWriter, r *http.Request, seeds []*Artist) {
	index := map[*Artist]int{}
	for i, a := range s.cat.Artists {
		index[a] = i
	}
	var artists []*Artist
	seen := map[*Artist]bool{}
	add := func(a *Artist) {
		if a != nil && !seen[a] {
			seen[a] = true
			artists = append(artists, a)
		}
	}
	for _, a := range seeds {
		add(a)
	}
	if n := len(s.cat.Artists); n > 0 {
		for _, a := range seeds {
			i, held := index[a]
			if !held {
				continue
			}
			add(s.cat.Artists[(i+1)%n])
			add(s.cat.Artists[(i+2)%n])
		}
	}
	var lists [][]*Track
	for _, a := range artists {
		var tracks []*Track
		for _, al := range a.Albums {
			tracks = append(tracks, al.Tracks...)
		}
		lists = append(lists, tracks)
	}
	limit := qInt(r, "Limit", 100)
	if limit <= 0 {
		limit = 100
	}
	items := []map[string]any{}
	for i := 0; len(items) < limit; i++ {
		took := false
		for _, l := range lists {
			if i < len(l) && len(items) < limit {
				items = append(items, s.jfTrack(l[i], false))
				took = true
			}
		}
		if !took {
			break
		}
	}
	ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
}

// playlistArtists is a playlist's songs' artists, in the order first met.
func playlistArtists(pl *Playlist) []*Artist {
	var out []*Artist
	seen := map[*Artist]bool{}
	for _, t := range pl.Tracks {
		if a := t.Album.Artist; !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

func (s *Server) jfPlaylists(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) >= 2 && p[1] == "InstantMix" {
		pl, _ := s.cat.Get(s.cat.IDForJF(p[0])).(*Playlist)
		if pl == nil {
			notFound(w)
			return
		}
		s.jfInstantMix(w, r, playlistArtists(pl))
		return
	}
	if len(p) >= 2 && p[1] == "Items" {
		pl, _ := s.cat.Get(s.cat.IDForJF(p[0])).(*Playlist)
		if pl == nil {
			notFound(w)
			return
		}
		var items []map[string]any
		for _, t := range pl.Tracks {
			item := s.jfTrack(t, true)
			item["PlaylistItemId"] = s.cat.JFID(pl.ID + "|" + t.ID)
			items = append(items, item)
		}
		for _, m := range pl.Movies {
			items = append(items, s.jfMovie(m, false))
		}
		if items == nil {
			items = []map[string]any{}
		}
		ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
		return
	}
	notFound(w)
}

func (s *Server) jfShows(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) >= 1 && p[0] == "NextUp" {
		var items []map[string]any
		seriesFilter := s.cat.IDForJF(qStr(r, "SeriesId"))
		full := strings.Contains(qStr(r, "Fields"), "MediaSources")
		for _, e := range s.nextUp() {
			if seriesFilter != "" && e.Show.ID != seriesFilter {
				continue
			}
			items = append(items, s.jfEpisode(e, full))
		}
		if strings.EqualFold(qStr(r, "EnableResumable"), "true") {
			_, eps := s.continueWatching()
			for _, e := range eps {
				items = append(items, s.jfEpisode(e, full))
			}
		}
		if items == nil {
			items = []map[string]any{}
		}
		if limit := qInt(r, "Limit", 0); limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
		return
	}
	if len(p) < 2 {
		notFound(w)
		return
	}
	sh, _ := s.cat.Get(s.cat.IDForJF(p[0])).(*Show)
	if sh == nil {
		notFound(w)
		return
	}
	switch p[1] {
	case "Seasons":
		var items []map[string]any
		for _, se := range sh.Seasons {
			items = append(items, s.jfSeason(se))
		}
		ok(w, map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0})
	case "Episodes":
		seasonFilter := s.cat.IDForJF(qStr(r, "SeasonId"))
		full := strings.Contains(qStr(r, "Fields"), "MediaSources")
		var items []map[string]any
		for _, se := range sh.Seasons {
			if seasonFilter != "" && se.ID != seasonFilter {
				continue
			}
			for _, e := range se.Episodes {
				items = append(items, s.jfEpisode(e, full))
			}
		}
		start := qInt(r, "StartIndex", 0)
		if start > len(items) {
			start = len(items)
		}
		total := len(items)
		items = items[start:]
		if limit := qInt(r, "Limit", 0); limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		ok(w, map[string]any{"Items": items, "TotalRecordCount": total, "StartIndex": start})
	default:
		notFound(w)
	}
}

// ---- media -----------------------------------------------------------------

func (s *Server) jfAudio(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) < 2 {
		notFound(w)
		return
	}
	t, _ := s.cat.Get(s.cat.IDForJF(p[0])).(*Track)
	if t == nil {
		// Books on: a book's one WAV streams through the same audio routes.
		if b, isBook := s.cat.Get(s.cat.IDForJF(p[0])).(*Book); isBook && s.Books && !b.Ebook {
			switch strings.ToLower(p[1]) {
			case "universal", "stream", "stream.wav":
				serveWAV(w, r, b.Seed, b.Duration)
				return
			}
		}
		notFound(w)
		return
	}
	switch strings.ToLower(p[1]) {
	case "universal", "stream", "stream.wav":
		s.recordPart(partRequest{Server: "jellyfin", Item: s.cat.JFID(t.ID), Route: strings.ToLower(p[1]), Status: http.StatusOK})
		serveWAV(w, r, t.Seed, t.Duration)
	case "lyrics":
		notFound(w)
	default:
		notFound(w)
	}
}

func (s *Server) jfVideos(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) >= 1 && p[0] == "ActiveEncodings" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(p) < 2 {
		notFound(w)
		return
	}
	id := s.cat.IDForJF(p[0])
	clip := ""
	switch v := s.cat.Get(id).(type) {
	case *Movie:
		clip = v.Clip
	case *Episode:
		clip = v.Clip
	default:
		notFound(w)
		return
	}
	file := p[1]
	switch {
	case strings.HasPrefix(strings.ToLower(file), "stream"):
		s.serveClipFile(w, r, clip, "clip.mp4")
	case strings.EqualFold(file, "master.m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-STREAM-INF:BANDWIDTH=3200000,AVERAGE-BANDWIDTH=3000000,RESOLUTION=1280x720,FRAME-RATE=24.000,CODECS=\"avc1.640028,mp4a.40.2\"\nmain.m3u8?ApiKey=%s\n", s.Token)
	case strings.EqualFold(file, "main.m3u8"):
		s.serveRuntimePlaylist(w, r, clip, s.runtimeOf(id))
	case strings.EqualFold(file, "init.mp4"), strings.HasPrefix(file, "seg") && strings.HasSuffix(file, ".m4s"):
		s.serveClipFile(w, r, clip, file)
	case strings.EqualFold(file, "Trickplay"):
		notFound(w)
	default:
		notFound(w)
	}
}

// jfInt parses an int from a query or returns def (unused helper kept for symmetry).
func jfInt(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}
