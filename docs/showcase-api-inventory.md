# Showcase server — what the apps actually call

Audited 2026-10-02 from the phone (Flutter) and Apple TV (SwiftUI) clients.
This is the contract the mock server in `internal/showcase` implements:
only the endpoints below, with exactly the fields listed, in the shapes
noted. Anything else may 404.

## Cross-cutting

- **Plain `http://IP:port` works everywhere.** Phone: ATS arbitrary loads +
  local networking (ios/Runner/Info.plist), Android cleartext permitted;
  `localhost`/`127.0.0.1` are REJECTED by the phone's URL validator — use
  the LAN IP. TV: `NSAllowsArbitraryLoads`; the Plex DEBUG hook takes the
  URL verbatim (no scheme added, no trailing-slash trim); Jellyfin/Navidrome
  hooks add `https://` when no scheme is given — always pass `http://`.
- **Serve Plex at the root path, no trailing slash.** TV image URLs are
  `baseURL + path`; player artwork replaces the URL path.
- **Type strictness kills whole arrays.** Plex: `ratingKey`/`Directory.key`
  strings; `Media.id`/`Part.id`/`Stream.id` ints; `selected`/`default`/
  `forced` real booleans; phone reads `year, addedAt, duration, index,
  leafCount, viewOffset, childCount, streamType, samplingRate, bitDepth,
  playQueueID, playQueueItemID` as ints. Jellyfin: `Id` required, ticks
  Int64; phone music reads `RunTimeTicks, ChildCount, IndexNumber,
  ProductionYear, TotalRecordCount, PlayCount, BitDepth, SampleRate` as
  ints. ABS: `publishedYear` STRING, `chapter.id` int, `numBooks/numTracks/
  numAudioFiles` ints, `/api/me` must include `token`.
- **Bare arrays vs envelopes:** ABS `personalized` = bare array; Jellyfin
  `Items/Latest` and `LocalTrailers` = bare arrays; everything else
  enveloped as noted.
- **Caches:** TV paints Home/library from a 24 h disk cache first; phone
  cover cache keys on the full URL (change ids or host:port to bust).
  Phone imports ABS stats into its local goals DB once — screenshot on a
  throwaway install.
- **Phone Plex:** there is NO manual URL+token path; only the plex.tv PIN
  flow. A seeded connection must be written in the persisted format
  (SharedPreferences `connections` + `plex_<connId>_server_url` etc.,
  Keychain `plex_<connId>_auth_token`) — a DEBUG dart-define hook is the
  way. The TV seeds Plex with `-uitestPlexURL <url> -uitestPlexName <name>`
  (tokenless; image URLs then carry an EMPTY `X-Plex-Token=` — accept it).
- **TV DEBUG hooks** (AlexandriaTVApp.swift): `-uitestPlexURL/-uitestPlexName`,
  `-uitestJellyfinURL/-User/-Pass` (login runs), `-uitestNavidromeURL/-User/
  -Pass` (ping with salted md5), `-uitestRefreshHomeAfter N`,
  `-uitestOpenPlexRatingKey`, `-uitestOpenJellyfinItemId`,
  `-uitestSyntheticPreviews`, `-liveGuideFixture`. No ABS hook (UI login).
- **TV free tier:** 2 server kinds, 3 per kind (hooks bypass; the UI does
  not) — seed Plex + Jellyfin by hook, ABS by UI needs premium or the
  bypass domain.

## Audiobookshelf (phone: `lib/api/abs_api_client.dart`; TV: `Networking/ABSAPIClient.swift`)

Auth: `POST /login {username,password}` → `user.token` (legacy token; no
refresh), `user.id`, **`userDefaultLibraryId` (required on the phone)**.
Then `Authorization: Bearer <token>` on every `/api` call. Covers:
`GET /api/items/{id}/cover` (phone, no query) / `?width=800` (TV), with the
Bearer header. Streaming: `POST /api/items/{id}/play` → `audioTracks[].
contentUrl` (relative ok), fetched with the Bearer header; **must support
HTTP Range**.

Library item (LI) fields read: `id, mediaType, media.duration (s),
media.metadata{title, subtitle, description, authorName|authors[].name,
narratorName|narrators[], series[{name,sequence}]|seriesName, genres[],
publishedYear (string), publisher, isbn, asin, language}, tags[],
addedAt (ms), media.chapters[{id int, title, start, end}], media.audioFiles
[{index, ino, duration, mimeType, metadata{filename, ext}}], media.numTracks,
numAudioFiles, mediaProgress|userMediaProgress{currentTime, progress,
isFinished, ebookLocation}`, ebooks via `media.ebookFile{ino, metadata.ext,
mimeType}`.

| Endpoint | Returns |
|---|---|
| `GET /ping` | 200 |
| `POST /login` | `{user{id, username, token, mediaProgress[], bookmarks[]}, userDefaultLibraryId}` |
| `GET /api/libraries` | `{libraries[{id, name, mediaType "book", icon}]}` |
| `GET /api/libraries/{lib}/items?limit=…&minified=…&sort=…&desc=…&include=…&filter=series.{b64}` | `{results[LI], total}` |
| `GET /api/libraries/{lib}/personalized?limit=50` | **bare array** `[{id: continue-listening|continue-series|recommended|listen-again|recently-added, label, type book|series, entities[LI or {id,name,books[{id}],addedAt}]}]` |
| `GET /api/libraries/{lib}/series?limit=&sort=addedAt&desc=1[&minified=1]` | `{results[{id, name, nameIgnorePrefix, numBooks int, books[LI], addedAt ms, totalDuration}], total}` |
| `GET /api/libraries/{lib}/series/{sid}` | one series |
| `GET /api/libraries/{lib}/authors` | `{authors[{id, name, description, numBooks int, imagePath}]}` |
| `GET /api/authors/{id}` / `/image` | author / 404 |
| `GET /api/libraries/{lib}/collections` | `{results[]}` (may be empty) |
| `GET /api/playlists` | `{results[], total}` |
| `GET /api/libraries/{lib}/search?q=&limit=` | `{book[{libraryItem: LI}], podcast[]}` |
| `GET /api/items/{id}[?expanded=1]` | LI with `media.chapters`, `media.audioFiles` |
| `GET /api/me[?_t=]` | `{id, username, type, token, mediaProgress[{id, libraryItemId, episodeId, progress, currentTime, duration, isFinished, lastUpdate, finishedAt, ebookLocation}], bookmarks[{libraryItemId, title, time, createdAt}]}` |
| `GET /api/me/items-in-progress?limit=` | `{libraryItems[LI]}` |
| `GET /api/me/progress/{id}` | `{currentTime, lastUpdate ms}` or 404 |
| `PATCH /api/me/progress/{id}` | 200 |
| `GET /api/me/listening-stats` | `{totalTime, today, items{}, days{"YYYY-MM-DD": s}, dayOfWeek{}, recentSessions[S]}` |
| `GET /api/me/listening-sessions`, `/api/users/{uid}/listening-sessions?itemsPerPage=` | `{sessions[S], total}` |
| `GET /api/me/stats/year/{y}` | `{numBooksFinished, totalListeningTime, numDaysListened, topAuthors[], topGenres[], …}` |
| `GET /api/sessions/open` | `{sessions[], users[]}` |
| `GET /api/users` | 403 ok |
| `POST /api/items/{id}/play` | `{id, libraryItemId, audioTracks[{index, startOffset, duration, contentUrl, mimeType, metadata{filename, ext}}], displayTitle, displayAuthor, currentTime, duration, coverPath}` |
| `GET /api/items/{id}/file/{ino}` | audio bytes, Range |
| `POST /api/session/{sid}/sync|close`, `POST /api/session/local` | 200 |
| `POST /api/me/item/{id}/bookmark`, `DELETE …/bookmark/{time}` | 200 |

Session (S): `{id, libraryItemId, mediaType, duration, timeListening,
startedAt ms, updatedAt ms, displayTitle, displayAuthor, coverPath}`.

Phone Home batch (7 calls together): items(10000) and /api/me MUST
succeed; personalized, recently-added, series, stats failures leave shelves
empty. Phone Library filtering/sorting is on-device over the 10000 list.

## Jellyfin (phone: `jellyfin_api_client.dart`, `jellyfin_video_api.dart`; TV: `JellyfinAPIClient.swift`)

Auth header (both `Authorization` and `X-Emby-Authorization`):
`MediaBrowser Client="Alexandria[ TV]", Device="…", DeviceId="…", Version="…"[, Token="<AccessToken>"]`.
Header-less fetches (images, audio, video, HLS) carry `ApiKey=<token>` in
the query (append when missing).

| Endpoint | Returns |
|---|---|
| `GET /System/Info/Public` | `{ServerName, Version, Id, LocalAddress, StartupWizardCompleted: true}` |
| `GET /Users/Public` | bare array `[{Id, Name, HasPassword…}]` |
| `POST /Users/AuthenticateByName {Username, Pw}` | `{AccessToken, ServerId, User{Id, Name}}` |
| `GET /Users/{uid}` | `{Id, Name, Configuration{LatestItemsExcludes[]}, Policy{IsAdministrator}}` |
| `GET /QuickConnect/Enabled` | `false` |
| `GET /Users/{uid}/Views` | `{Items[{Id, Name, CollectionType music|movies|tvshows, ImageTags{Primary}}]}` |
| `GET /Users/{uid}/Items?…` | `{Items[], TotalRecordCount}` honouring `ParentId, IncludeItemTypes (MusicAlbum, AudioBook, Audio, Movie, Series, Playlist, BoxSet), Recursive, SortBy (SortName|IndexNumber|PlayCount|DateCreated), SortOrder, StartIndex, Limit, ArtistIds, GenreIds, SearchTerm, Ids` |
| `GET /Users/{uid}/Items/Latest?ParentId=&Limit=&GroupItems=` | **bare array** |
| `GET /Users/{uid}/Items/Resume?IncludeItemTypes=…|MediaTypes=Video` | `{Items[]}` |
| `GET /Shows/NextUp?UserId&Limit[&SeriesId]` | `{Items[episodes]}` |
| `GET /Users/{uid}/Items/{id}[?Fields=…]` | single item (album, track, movie, series, episode) |
| `GET /Artists?UserId&ParentId…`, `/Artists/{id}/Similar` | `{Items[{Id, Name, Overview, ImageTags, AlbumCount}]}` |
| `GET /Genres?UserId&ParentId` | `{Items[{Name, Id}]}` |
| `GET /Playlists/{id}/Items?UserId` | `{Items[tracks]}` |
| `GET /Shows/{id}/Seasons?UserId`, `/Shows/{id}/Episodes?UserId[&SeasonId]` | `{Items[]}` |
| `GET /Items/{id}/Images/Primary|Backdrop/0[?tag&maxWidth&maxHeight&ApiKey]` | JPEG |
| `GET /Audio/{id}/universal?…` | audio bytes (Range) |
| `GET /Items/{id}/Download?ApiKey` | audio bytes (Range) |
| `GET /Videos/{id}/stream?static=true&MediaSourceId=&DeviceId=&ApiKey=` | MP4 (Range) — the phone's default video path |
| `POST /Items/{id}/PlaybackInfo?UserId` | `{PlaySessionId, MediaSources[{Id, Container, Path, Size, Bitrate, RunTimeTicks, SupportsDirectPlay, TranscodingUrl "/videos/{id}/master.m3u8?…", TranscodingSubProtocol "hls", TranscodingContainer "ts", DefaultAudioStreamIndex, MediaStreams[]}]}` |
| `GET /videos/{id}/master.m3u8…` (+ segments) | HLS |
| `GET /Audio/{id}/Lyrics` | 404 |
| `GET /Users/{uid}/Items/{id}/LocalTrailers` | `[]` |
| `GET /MediaSegments/{id}` | 404 |
| `GET /LiveTv/Info`, `/LiveTv/Channels?…` | `{TotalRecordCount: 0}` |
| `POST /Sessions/Playing[/Progress|/Stopped]`, `POST /Users/{uid}/PlayedItems/{id}`, `DELETE /Videos/ActiveEncodings`, `POST /Sessions/Logout` | 204 |
| `GET /Videos/{id}/Trickplay/{w}/{i}.jpg` | (optional) tiles |

Item fields: `Id, Name, Type, MediaType, Overview, ProductionYear,
PremiereDate, DateCreated, RunTimeTicks, IndexNumber, ParentIndexNumber,
ParentId, AlbumId, Album, AlbumArtist, AlbumArtists[{Id,Name}], Artists[],
ArtistItems, Genres[], GenreItems, Tags[], OfficialRating, CommunityRating,
CriticRating, Taglines[], Studios[{Name}], People[{Id, Name, Role, Type,
PrimaryImageTag}], ChildCount, RecursiveItemCount, SongCount,
SeriesId/SeriesName/SeriesPrimaryImageTag, SeasonId/SeasonName,
ImageTags{Primary}, BackdropImageTags[], UserData{PlaybackPositionTicks,
PlayCount, Played, PlayedPercentage, LastPlayedDate, UnplayedItemCount},
Chapters[{StartPositionTicks, Name}], MediaSources[{Id, Path, Container,
Size, Bitrate, RunTimeTicks, DefaultAudioStreamIndex, MediaStreams[{Index,
Type Video|Audio|Subtitle, Codec, Language, DisplayTitle, IsDefault,
Channels, BitRate, Width, Height, BitDepth, SampleRate, RealFrameRate}]}]`.

## Plex (TV: `PlexAPIClient.swift`; phone music: `plex_api_client.dart`)

JSON (`Accept: application/json`), everything inside `{"MediaContainer":{…}}`.
Headers `X-Plex-*`; token in `X-Plex-Token` header (may be absent/empty) and
as `?X-Plex-Token=` on media/art URLs (may be empty).

| Endpoint | Returns |
|---|---|
| `GET /identity` | `{MediaContainer{machineIdentifier, version}}` |
| `GET /library/sections` | `Directory[{key "1", type movie|show|artist, title, agent, scanner, thumb, art, language}]` |
| `GET /library/onDeck`, `/library/sections/{k}/onDeck` | `Metadata[L]` |
| `GET /library/sections/{k}/recentlyAdded?type=T` | `Metadata[L]` (T 1 movie, 2 show, 8 artist, 9 album, 10 track) |
| `GET /library/sections/{k}/all?type=T[&artist.id=&sort=&includeCollections=…]` | `Metadata[L], totalSize` |
| `GET /library/sections/{k}/collections` | `Metadata[{ratingKey, type collection, title, thumb, childCount, addedAt}]` |
| `GET /library/metadata/{rk}[?includeMarkers=1&includeExtras=1&includeChildren=1]` | `Metadata[0]` full (Media/Part/Stream, Role, Director, Writer, Genre, Marker[], Extras{size 0}) |
| `GET /library/metadata/{rk}/children`, `/allLeaves`, `/similar` | `Metadata[L]` |
| `GET /playlists?playlistType=video|audio`, `/playlists/{rk}/items` | `Metadata[{ratingKey, type playlist, playlistType, title, composite, smart, leafCount, duration, addedAt}]` / items |
| `GET /hubs/search?query=` | `Hub[{type, hubIdentifier, title, Metadata[]}]` |
| `GET /library/parts/{id}/indexes/sd` | 404 (or BIF) |
| `GET /video/:/transcode/universal/decision?…` | `MediaContainer{generalDecisionCode 1001, generalDecisionText, directPlayDecisionCode, transcodeDecisionCode, Metadata[{Media[{videoCodec, audioCodec, width, height, videoResolution, Part[{Stream[{streamType, selected, decision copy, codec, channels}]}]}]}]}` |
| `GET /video/:/transcode/universal/start.m3u8?…&session=` | master → relative `session/{s}/base/index.m3u8` → fMP4/TS segments |
| `GET /music/:/transcode/universal/start.mp3?…` | audio bytes (phone transcode tiers) |
| `GET {Part.key}?download=1&X-Plex-Token=` | audio file bytes (Range) — phone Original tier |
| `GET /:/timeline?…`, `/video/:/transcode/universal/ping|stop?session=` | 200 |
| `PUT /library/parts/{id}?audioStreamID=…` | 200 |
| `POST /playQueues?…` | `{playQueueID int, Metadata[{ratingKey, playQueueItemID int}]}` |
| `GET /livetv/dvrs` | 404 |
| `GET /photo/:/transcode?url=&width=&height=` | (phone art) JPEG |
| `GET /library/metadata/{rk}/thumb/{n}`, `/art/{n}` | JPEG |
| `GET /status/sessions`, `/status/sessions/history/all` | empty MediaContainer |

L (list item): `ratingKey (string), key, type movie|show|season|episode|
artist|album|track|collection|playlist, title, parentTitle,
grandparentTitle, parentRatingKey, grandparentRatingKey, summary, year,
duration ms, viewOffset ms, viewCount, lastViewedAt, addedAt s, updatedAt,
originallyAvailableAt, thumb, art, parentThumb, grandparentThumb, index,
parentIndex, leafCount, viewedLeafCount, childCount, contentRating, rating,
audienceRating, studio, tagline, Genre[{tag}], Role[{tag, role, thumb}],
Director[{tag}], Writer[{tag}], Mood[{tag}], Media[{id int, duration,
bitrate, videoCodec, audioCodec, audioChannels, videoResolution, container,
width, height, videoFrameRate, Part[{id int, key, duration, file, size,
container, Stream[{id int, streamType 1|2|3, codec, displayTitle, title,
language, languageCode, selected bool, default bool, index, bitrate,
channels, width, height, samplingRate, bitDepth}]}]}]`.
