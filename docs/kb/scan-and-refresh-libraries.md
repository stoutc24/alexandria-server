# Scanning a Plex library, and refreshing what a TV shows

Two different things live here. They are easy to mix up:

- **Scan** asks your **Plex server** to look through a library's folders for
  new, changed or removed files. Only the server's owner can do it.
- **Refresh** asks the **app** to throw away its saved copy and read the
  library from the server again. Anyone can do it. It changes nothing on
  the server.

Added a film to the server and it is not in Plex yet? **Scan.** Plex has
it but the TV still shows the old list? **Refresh.**

## Scan a Plex library (the server's owner)

You see the scan controls only when the Plex account you signed in with
owns the server. A shared user, a managed user and Kids mode never see
them.

### Phone, tablet and computer

(Build 1262 and later.)

1. Open the list of your Plex libraries: the library menu on Plex Music's
   Home, or the side drawer on Plex Video's Home.
2. Tap **⋮** beside the library.
3. Tap **Scan library files**.

The app says **Scan started**, and the library reads **Scanning…** until
Plex has finished. The app then reads that library again, so new titles
appear.

To scan every library at once: open the side drawer on Plex's Home, tap
**Server Admin**, and on the Plex card tap **Scan libraries**. It asks
you to confirm first.

### Apple TV, Samsung, LG and Fire TV

(Apple TV build 206 and later.)

- **One library:** in the rail at the left, move to the library, **hold
  Select** (OK on a Samsung, LG or Fire TV remote), and choose **Scan
  Library Files**. On Samsung, LG and Fire TV, holding OK on a library's
  tab opens the same menu.
- **Every library:** **Settings › Servers**, and under your Plex server,
  **Server Admin › Scan All Libraries**.

The button reads **Scanning…** while Plex works, and the library reloads
when it is done.

### What you may see

| The app says | What it means |
| --- | --- |
| Scan started | Plex took the request and is looking through the folders. |
| Scanning… | A scan of that library is running. A second press does nothing. |
| Only the server's owner can scan libraries. | Plex refused: this account does not own the server. |
| Couldn't start the scan. Try again. | The server did not answer, or answered with an error. |

A scan only looks for files. Alexandria does not refresh artwork and
descriptions, empty the trash or analyse media; use Plex's own app for
those.

## Refresh what a TV shows (everyone)

The TV apps open their screens from a saved copy so they appear at once,
then catch up with the server. If a screen is behind, tell it to read
again:

- **One library:** in the rail at the left, move to the library, **hold
  Select** (OK), and choose **Refresh**. This works for every service:
  Plex, Jellyfin, Audiobookshelf and Navidrome.
- **Everything from one Plex server:** **Settings › Servers**, and under
  the Plex server, **Server Admin › Refresh Libraries**. The TV drops its
  saved copy of that server and reads its libraries again. It says
  **Libraries refreshed.** when that is done. If the server cannot be
  reached, the saved copy is still cleared, and the libraries load once
  the server answers again.

On a phone or tablet, pull a library's Home down to refresh it.

More about saved copies: [How screens load, and data on
cellular](loading-and-data-use.md).

Contact for questions or problems: stoutservers@gmail.com
