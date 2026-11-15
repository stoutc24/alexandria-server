# Preview pictures while you move through a video on the Apple TV

While a Plex or Jellyfin film or episode plays, move through it with the
remote: swipe on it, or hold left or right. A small picture above the
progress bar shows that moment of the video, so you can see where you are
before you let go.

## What your server needs

The pictures are made by your server, not by the Apple TV:

- **Plex:** turn on Settings › Library › "Generate video preview
  thumbnails". Plex makes the pictures in the background. A film that was in
  your library before you turned it on gets its pictures once Plex has worked
  through it, which can take a while for a big library.
- **Jellyfin:** trickplay images (Jellyfin 10.9 and later, once the server
  has made them).

A video without pictures still lets you move through it; you just see the
time and no picture.

## HDR and Dolby Vision films (Plex)

(Apple TV build 217 and later.)

4K HDR films from Plex (HDR10, HLG and most Dolby Vision films) show the pictures too,
the same as any other film. Before this update they showed none.

- **The TV switches first.** When an HDR film starts, Alexandria asks your
  TV to change to that film's HDR mode and frame rate before the film
  begins. The screen can go black for a second or two while the TV changes
  mode. That is normal.
- **Back to normal after.** When you leave the player, the Apple TV goes
  back to its usual mode.
- **Turn on Match Content.** On the Apple TV, open Settings › Video and
  Audio › Match Content and turn on **Match Dynamic Range** (and **Match
  Frame Rate**). Without it, HDR films still play, but they may show no
  pictures while you move through them.
- **If the pictures can't be used, the film plays anyway.** If the Apple TV
  won't start an HDR film with its pictures, Alexandria starts it the way it
  did before, without them. This happens by itself; there is nothing to set.

Not yet:

- Jellyfin HDR films show no pictures on the Apple TV.
- Dolby Vision films that the Apple TV can only play as HDR10 (profile 7,
  and profile 8.6 from a Blu-ray conversion) play without the pictures.

Contact for questions or problems: stoutservers@gmail.com
