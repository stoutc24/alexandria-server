# Music on the Apple TV

## The Queue

While music plays, choose **Queue** under the player's controls. The queue
opens as a row along the bottom of the screen:

- The song playing now is marked and has the focus. Songs already played sit
  to its left, dimmed; songs still to come sit to its right.
- **Left and Right** move through the row. **Select** plays that song now.
- **Shuffle**, **Repeat** and **Autoplay** sit above the row. Autoplay is the
  same switch as Settings › Keep the music going. Stations always keep going,
  so they don't show it. To reach them, go to **Stop** and press Down.
- **Up** from a song goes back to the player's controls: from a song on the
  left or in the middle it lands on **Hide Queue** (press Up again for
  Play/Pause); from a song in the right third of the screen it lands
  straight on **Play/Pause**. (Apple TV build 200 and later.)
- **Hold Select** on a song for more:
  - **Play Now**
  - **Play Next**
  - **Move Earlier** or **Move Later** (for songs still to come)
  - **Remove from Queue**

  The song playing now has no menu.
- When the next song starts, the row moves along so the new song sits in the
  middle, and the focus moves to it. If you were looking through other songs,
  it waits until you have left the remote alone for 5 seconds. If the focus
  is on the switches or the player's buttons, it stays there and only the
  row moves. (Apple TV build 190 and later.)
- Press **Menu** (or Back) to close the Queue. The music keeps playing.

It works for Plex, Jellyfin and Navidrome music, including stations.

### During a party

When the Apple TV follows someone else's Party Play, the Queue shows the
party's queue:

- **Select** asks the party to play that song.
- **Remove from Queue** asks the leading device to take a song out, if that
  device allows changes to its queue.
- Moving songs is up to the leading device.

## Leaving the app while music plays

Press the **TV** button (or Back until you reach the Home screen) and the
music keeps playing, as in Apple Music:

- **Hold the TV button** to open Control Center. It shows the song, the
  artist and the cover, with Play/Pause, Next and Previous.
- The remote's **Play/Pause** button pauses and plays the music from the
  Home screen and from anywhere in Alexandria.
- One thing plays at a time: starting a song stops an audiobook, and
  starting an audiobook stops the music.
- The Fireplace sound under the player pauses while you are outside the app
  and starts again when you come back.
- The Apple TV's Now Playing pop-up in the top corner shows each new song
  only while you are outside Alexandria. Inside the app, there's no pop-up
  (Apple TV build 193 and later).

(Apple TV build 190 and later. Audiobooks already kept playing outside the
app.)

## Lyrics

Choose **Lyrics** in the row under the player's controls (**Hide Lyrics**
puts the song details back). The button is there for every song.

- **Timed lyrics** follow the song: the line being sung is bright white, a
  third of the way down, and the lines around it are faded.
- **Lyrics without timing** are all bright and scroll along with how far the
  song has played. A note above them says so, because they can't follow the
  singing exactly.

The controls stay under the lyrics, so you can pause or skip while reading.
(The look above is Apple TV build 189 and later.)

### Where the lyrics come from

As on the phone (Apple TV build 191 and later):

1. Your own server: Plex (with Plex Pass and LyricFind, or a `.lrc` file
   next to the song) or Jellyfin.
2. When your server has none, or only lyrics without timing, the Apple TV
   asks [LRCLib](https://lrclib.net), a free lyrics library, for timed ones
   once you open **Lyrics**. Those say **Lyrics from LRCLib** above them.
   LRCLib is sent the song's title, artist, album and length, and only for
   songs whose lyrics you open.

Builds 189 and 190 showed LyricFind's lyrics as untimed, scrolling at an
even pace, because the last two lines (the credits) have no time. Build
191 follows the sung line.

The screensaver doesn't start while the lyrics are on screen. After **Hide
Lyrics**, it waits its usual time (Settings › Screensaver) before it starts.
The Screensaver button still starts it at once. (Apple TV build 190 and
later.)

The screensaver also never starts while a film, an episode, live TV or a
trailer is open, whether it is playing, paused or loading. Opening one puts a
screensaver that is up away at once. It starts only while music or an
audiobook is really playing. (Apple TV build 194 and later.)

The Apple TV doesn't go to sleep while the music player is open, playing or
paused. That includes after you start new music over what was playing:
until build 202, the old player closing could let the Apple TV fall asleep
in the middle of a song. If it does sleep mid-song, check whether the
music itself stopped (for example, your server restarted).

### When Plex can't fetch the lyrics

Plex gets most lyrics from LyricFind each time you open them, and
sometimes it answers "not found" for a while before they come back. From
Apple TV build 190:

- **Lyrics** stays in the row for a song Plex lists lyrics for, even while
  Plex can't fetch them. Open it and it says **Plex couldn't fetch these
  lyrics just now**, then tries again every 15 seconds. From build 191 it
  first shows LRCLib's lyrics for the song, if LRCLib has them.
- With the panel closed, the Apple TV tries again by itself: 3, 8, 20 and
  60 seconds into the trouble (build 191; 20 and 60 seconds in build 190).
- Lyrics that loaded once are kept on the Apple TV (the last 500 songs
  played), so those songs show their lyrics even while Plex can't.

Before build 190, one failed fetch removed the Lyrics button for the rest
of the song.

## The screensaver's song lines

While music plays, the screensaver shows:

1. The song's title.
2. The artist, with anyone featured on the song ("Justin Bieber feat. Daniel
   Caesar & GIVĒON").
3. Where the song plays from: the station's name, else the playlist's name,
   else the album's.

A song you added to a playlist's queue later shows its album, not the
playlist.

For an audiobook, the screensaver shows the book's title, then its author and
chapter.
