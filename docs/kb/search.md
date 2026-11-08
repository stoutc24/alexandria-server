# Finding songs, artists, albums and playlists

## Stations at the top of search

When what you type matches a music artist or a music playlist, search
starts with a **Stations** row:

- **"<Artist> Radio"** (Artist station): that artist and the artists like
  them.
- **"<Playlist> Radio"** (Playlist station): the artists on that playlist
  and the artists like them. It plays their records from your library, not
  only the playlist's own songs. See
  [Radio stations](radio-stations.md#playlist-stations).

Artists come first, then playlists, at most ten. An artist or playlist on
more than one of your servers gets one tile. Its station plays from the
first server that has it, in this order: Plex, Jellyfin, Navidrome.

Select a tile to start the station. Now Playing opens. If your library has
too little music like it, you see "Not enough similar music for … Radio."
and nothing changes.

Where it is:

- **iPhone, iPad, Android, Mac and Windows** (Alexandria 3.2.1 build 1256
  and later): at the top of **Search**, and of the search in a Plex or
  Navidrome music library.
- **Apple TV** (build 201 and later), **Samsung TV** and **Fire TV**:
  above the Music results in **Search**.

Video playlists, and authors in an audiobooks library, never get a tile.

## Search in Plex music

Tap the **search** icon at the top of a Plex music library and type.
The Stations row comes first. Then the results come in three groups. A
group with nothing in it is left out.

- **Songs:** tap a song to play it right away. That artist's radio station
  plays after it (see [Radio stations](radio-stations.md)). Now Playing
  opens.
- **Artists:** tap one to open the artist's page.
- **Albums:** tap one to open the album.

In a Plex library you have marked as audiobooks, search finds books, as
before.

## Search across all your libraries

**Search** (the bottom bar or the sidebar) looks through every server at once.
Plex songs and artists show up there too. Each is marked **Song** or
**Artist** beside the word Plex. A song plays the same way: the song first,
then its artist's station.

From build 1256 search also finds:

- Music playlists on Plex, Jellyfin and Navidrome, marked **Playlist**.
  Tap one to open it.
- Jellyfin artists.
- Navidrome artists, albums and playlists. Navidrome wasn't in this search
  before.

On the Apple TV (build 201), Samsung TV and Fire TV, the Music results
include the same playlists and, for the first time, Navidrome.

## When the station can't start

If your library doesn't have enough music like the song's artist, the song
plays on its own. You'll see "Not enough similar music for … Radio." The
queue then carries on the way it always does when it runs out.
