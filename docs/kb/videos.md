# Watching videos on the phone, tablet and computer

This covers Plex and Jellyfin films and shows in the Alexandria app on
iPhone, iPad, Android, Mac and Windows. The Apple TV has its own player:
see [TV shows on the Apple TV](apple-tv-shows.md).

## Moving through a video

Tap the video to show the controls. The bar along the bottom shows where
you are.

- **Drag the dot** along the bar. The dot and the time follow your finger,
  and the video stays where it is until you let go. Then it moves once, to
  where you let go.
- **Tap a point** on the bar to go straight there.
- **With a keyboard** (Mac, Windows, or an iPad with a keyboard): press Tab
  until the bar is selected, then use the left and right arrow keys to go
  back or forward 10 seconds.
- The buttons either side of Play/Pause go back or forward 10 seconds.

The controls stay on screen while you drag.

(Build 1246 and later. Before that, the video jumped around while you
dragged.)

### Preview pictures

While you drag, a small picture above the dot shows that moment of the
video, with its time underneath. Until the picture for that moment
arrives, you see the nearest one already loaded, or the time on its own.

The pictures come from, in this order:

1. **Plex's preview thumbnails**, if your Plex server makes them. This is
   the Plex setting Settings › Library › "Generate video preview
   thumbnails". Each picture is small (about 10 KB).
2. **Jellyfin's trickplay images** (Jellyfin 10.9 and later, once the
   server has made them).
3. **The video itself.** When the server has neither, Alexandria reads the
   picture straight from the video. Each new point you drag to reads
   roughly 1–3 MB of the video, more for a large 4K film. This happens on
   Wi-Fi and on mobile data alike. A downloaded video uses its own file and
   no data.

Live TV has no preview pictures.

## Volume

The speaker at the bottom left of the player is the volume. It is on Plex
and Jellyfin videos and on Live TV. Tap the video first if the controls
are hidden. (Build 1262 and later.)

### Mac and Windows

- **Drag the slider** beside the speaker. This is Alexandria's own volume,
  the same one as the slider on the music's Now Playing, and the app
  remembers it. Your computer's volume is not changed.
- **Click the speaker** to mute the video. Click it again to hear it. The
  mute lasts for this viewing only.
- **Up and Down arrow keys**, or the **mouse wheel** over the picture, turn
  the volume up and down. A small badge at the top shows the new level for
  a moment. Turning it up also takes the mute off.

Before build 1262 a video always played at full volume on a computer,
whatever the slider on Now Playing said.

### iPhone, iPad and Android

Tap the speaker. A small slider opens above it: this is the device's own
volume, the same as the buttons on its side. There is no mute button; use
the device's buttons.

More about the two kinds of volume: [Volume on Now Playing](volume.md).

## Subtitles

Pick a language in the player's subtitles menu, or **Off**.

- **Off** clears the line that is on screen straight away. (Build 1262 and
  later. Before that, the line showing at that moment stayed on screen.)
- **Off stays off** for the rest of the video, including when the app has
  to open the stream again (a change of quality or audio, for example).
  (Build 1263 and later. Before that, a video with a "default" subtitle
  track could switch subtitles back on by itself.)

One thing the app does not do: it does not save "Off" back to your Plex
server. If Plex is set to show subtitles automatically, the next episode
can start with them on again. That choice is made in Plex's own audio and
subtitle settings for your account.

## Picture in Picture

### iPhone and iPad

- **Swipe up** (or go to the Home Screen) while a video is playing. It
  carries on in a small window over your other apps. A paused video
  doesn't open a window. (Build 1246 and later.)
- Or tap the **Picture in Picture** button at the top of the player.
- The window can show black for a moment while your server starts sending
  the picture.
- To come back, tap the window's button that returns to the app. The
  video carries on in Alexandria from where the window got to.
- The window's **×** stops the video.
- Downloaded videos don't open in a window.

Before build 1246, coming back from the window could end the episode (and
Play All moved on to the next one), whatever the time was. That is fixed.

### Android

- **Go to the Home screen** (swipe up, or press Home) while a video is
  playing, or tap the **Picture in Picture** button at the top of the
  player. The player shrinks into a window that keeps playing. (Android 8
  and later, build 1246 and later.)
- The window has a **Play/Pause** button. Tap the window for Android's own
  buttons: one makes it full size again, and **×** stops the video.
- Going to the Home screen from anywhere else in Alexandria doesn't open a
  window, and neither does a paused video.
- If Picture in Picture is switched off for Alexandria in Android's
  settings, the player doesn't show the button.

### Mac and Windows: the mini window

(Build 1270 and later.)

- Click the **Picture in Picture** button at the top of the player.
  Alexandria's window shrinks into a small video window in the
  bottom-right corner of your screen. It stays on top of every other
  window, so you can keep watching while you work in other apps. On a Mac
  it also shows over other apps that are in full screen.
- **Move it:** drag the picture anywhere on the screen.
- **Resize it:** drag an edge or a corner. It keeps the shape of a TV
  picture (16:9).
- **Buttons:** move the mouse over the picture to see **Play/Pause**,
  **Back to full size** and **Close**. On a touch screen, tap the picture
  to show them for a few seconds.
- **Back to full size** (or the **Esc** key) puts Alexandria's window back
  exactly where and how big it was. If the Mac window was in full screen,
  it goes back to full screen.
- **Close** stops the video and puts the window back.
- The next time you open the mini window, it comes back where you left it,
  at the size you left it.
- On a Mac, closing the mini window with **⌘W** pauses the video, as
  closing the full window does. Click Alexandria in the Dock to get the
  window back at full size.
- Every video that plays in Alexandria plays in the mini window, downloaded
  ones and Live TV too: it is the same player, just smaller.

### A whole season in the window

When an episode ends while it is in Picture in Picture, the next episode
starts in the same window, and so on to the end of the season (or of the
list you pressed Play All on). This works on iPhone, iPad, Android, Mac and
Windows, for Plex and Jellyfin. (Build 1270 and later; before that, an
iPhone or iPad window closed after one episode.)

- The episode that finished is marked as watched on your server, the same
  as when it ends in the full player.
- If you set the sleep timer to **After current item**, the video stops
  after that episode instead.
- After the last episode the video stops. On an iPhone or iPad the window
  closes, as before.
- On an iPhone or iPad the window can show black for a moment while your
  server starts sending the next episode.

## Video quality

How big a stream Alexandria asks your server for. (Build 1271 and later,
on iPhone, iPad, Android, Mac and Windows, for Plex and Jellyfin.)

- **Settings › Playback › Video quality** sets it for this device: every
  video starts at it. Each device keeps its own, so an older iPad can stay
  at 4 Mbps while your phone stays at Original.
- **Quality** in the player's bottom bar changes it for the video you are
  watching only. The next video starts at the device's setting again.
- The choices: **Original** (the file itself), 20, 12 or 8 Mbps at 1080p,
  4 or 3 Mbps at 720p, and 2 Mbps at 480p.
- Below Original, your server converts the video to fit. That uses your
  server's processor. A file that is already inside the limit is sent as
  it is.
- Original keeps HDR and Dolby Vision. A lower quality may be converted to
  standard colour.
- With a lower quality, a subtitle you turn on is drawn into the picture by
  the server, so switching subtitles or audio takes a moment.
- Downloaded videos and Live TV are not affected.

Try a lower quality when a video keeps stopping to load, or on an older
device or a slow connection.

On the Apple TV and on Samsung, LG and Fire TV, the same setting is under
**Settings › Playback › Video quality**, for that TV and every profile on
it. The player's **Quality** row (in its Audio & Video panel) still changes
just the video that is playing. Trailers and Live TV play at their own
quality.

## Up next: the next episode at the credits

When an episode's end credits start, a card appears in the corner: **Up
next**, with the next episode's picture and name. (Build 1271 and later, on
iPhone, iPad, Android, Mac and Windows, for Plex and Jellyfin.)

- It counts down from 5, then the next episode starts by itself. The count
  only runs while the video plays: pause, and it waits.
- Tap the card or **Play now** to start the next episode straight away.
- Tap **Watch credits** to hide the card and watch them. The next episode
  starts when they end, as before.
- The episode you leave is marked as watched on your server, as if it had
  played to its very end.
- Drag back out of the credits and the card goes; it comes back when the
  credits start again.
- No card after the last episode in the list, or with the sleep timer set
  to **After current item**.
- In Picture in Picture there is no card. On Android, Mac and Windows the
  next episode starts 5 seconds into the credits; on an iPhone or iPad the
  window plays the credits to the end, then moves on.

The card needs your server to know where the credits start:

- **Plex**: a Plex Pass server finds the credits itself (Plex's credits
  detection).
- **Jellyfin**: version 10.10 or later, with a plugin that finds credits,
  such as Intro Skipper.

Without that, the credits play to the end and the next episode follows.
Either way, Alexandria asks your server about the next episode while the
last one is ending, so it opens a little sooner.

The Apple TV has its own way: a **Skip Credits** button, or skipping them
always, set for each profile under **Settings › Playback**.

Contact for questions or problems: stoutservers@gmail.com
