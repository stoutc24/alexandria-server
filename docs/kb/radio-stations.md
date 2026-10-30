# Radio stations

A station plays music from your own servers without end: an artist and the
artists like them, a genre, a decade, or a theme your AI named. Stations sit
under **Stations for you** on each music server's home, and on each music
page in the car.

## What an artist station plays

"Ye Radio" plays, from what your servers hold:

- Ye, about a quarter of the records;
- artists your Plex server or Last.fm call similar to Ye, when you have
  their music;
- on Plex, records that share Ye's **styles**: the finer tags Plex keeps on
  each album, such as "Midwest Rap" or "Pop-Rap". A style that many of
  your albums carry counts for less than a rare one.

A genre alone ("Rap", "Pop/Rock") is too broad to go by: on Plex it no
longer brings a record in. On Jellyfin and Navidrome, which have no styles,
a record that only shares a genre joins only when your library holds fewer
than five albums by similar artists.

The heart of the station comes first: the artist, the artists like them
and the records closest in style. Records that only just share the
artist's styles come later. When the station has played everything it can,
it starts over in a new order rather than stopping.

**Stations for you** offers an artist's station only when your servers
hold at least six records it would play, by more than that one artist.

## Start a station from an artist's page

Open an artist on a Plex, Jellyfin or Navidrome music server and tap
**Start Station** under their name. The station plays from that server's
music and Now Playing opens. On the Apple TV and Samsung TV the button is
on Plex artist pages.

The button isn't shown for an author in an audiobooks library: stations
play music only. If that artist's station would be nothing but their own
records, you see "Not enough similar music for …" and nothing changes.

## Genre, decade and AI stations

- **Genre** ("Rap Radio"): every artist with that genre.
- **Decade** ("1990s Radio"): records released in those ten years.
- **AI radio**: the genres and artists the AI named for that theme, and only
  those.

## Intros and skits are skipped

Stations leave out the pieces that open or link the tracks on an album:

- anything under one minute;
- tracks whose title marks them as one in brackets, at any length: for
  example "Phone Call (Skit)" or "Song [Interlude]";
- tracks called just Intro, Interlude, Skit or Outro that are under two
  and a half minutes. A longer one is usually a full song, so it plays.

Play the album itself to hear every track.

## A station that seems wrong

- **Odd artists in an artist station:** the similar-artist lists come from
  your Plex server and Last.fm. Last.fm is used only when it answers for
  the same artist: asked about "Ye", it answers for the band Yes, so its
  list is left out. On Jellyfin and Navidrome an artist with little
  similar data falls back to shared genre tags.
- **"Not enough similar music for …":** your library has too little music
  near that station. Pick another one. If the same station works on
  another device, open **You › Settings › Media index** on this one: the
  line under it counts the items indexed. Tap it to index everything now.
  Alexandria refreshes the index by itself only on Wi-Fi or a cable; a
  computer on a network cable was missed before the next builds.
- **No stations at all:** stations are built from your media index. Rebuild
  it under **You › Settings › Media index**.

Contact for questions or problems: stoutservers@gmail.com
