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

### Mac and Windows

The Mac and Windows apps don't have Picture in Picture.

Contact for questions or problems: stoutservers@gmail.com
