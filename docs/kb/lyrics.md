# Lyrics on the phone, tablet and computer

Open **Now Playing** and choose the **Lyrics** tab (beside **Up Next**).
Timed lyrics follow the song; lyrics without timing show as plain text.

## Where the lyrics come from

1. Your own server: Plex (with Plex Pass and LyricFind, or a `.lrc` file
   next to the song) or Jellyfin (a `.lrc` file next to the song).
2. When your server has none, or only lyrics without timing, Alexandria
   asks [LRCLib](https://lrclib.net), a free lyrics library, for timed
   ones. Those are marked **Lyrics from LRCLib**.

Lyrics that load are kept on the device, so they open at once next time.

## When Plex can't fetch the lyrics

Plex gets most lyrics from LyricFind each time you open them, and that
service sometimes fails for a while: every song's lyrics come back "not
found", then they return. When that happens:

- The Lyrics pane says **Plex couldn't fetch these lyrics just now** and
  tries again every 15 seconds while it's open.
- The song is not marked as having no lyrics, so they show as soon as Plex
  can fetch them again.
- Songs whose lyrics loaded before still show them.

If LRCLib has the song, its lyrics show in the meantime.

Before the October 30, 2026 builds, a song opened while Plex couldn't fetch
its lyrics showed **No lyrics available** for up to a week. Those clear by
themselves within a day of updating; the refresh button at the top of the
Lyrics tab clears one at once.

For the Apple TV, see [Music on the Apple TV](apple-tv-music.md#lyrics).

Contact for questions or problems: stoutservers@gmail.com
