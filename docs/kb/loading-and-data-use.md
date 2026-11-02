# How screens load, and data on cellular

From Alexandria 3.2.1 build 1249 on the phone, tablet and computer.

## Screens open from a saved copy

Each service's screens open at once from a copy saved on your device. This
covers Plex (music, audiobooks and videos), Jellyfin, Audiobookshelf,
Navidrome, OPDS and Stump, Kavita, Komga, Calibre, Calibre-Web, Grimmory and
Storyteller. Alexandria then checks your server for changes behind the screen
and updates only what changed.

- **The first visit** after signing in has nothing saved yet. Grey shapes
  hold each shelf's place until it arrives.
- **Shelves stay put.** A shelf that changes while you look at it waits. The
  change shows when you scroll away from it, come back to the screen, or come
  back to the app from another app. Closing Control Center doesn't count.
- **Pull down to refresh** to get everything fresh at once, on any network.

## On cellular and Data Saver

On cellular, a hotspot or a metered Wi-Fi (Android), and with Data Saver
(Android) or Low Data Mode (iPhone), Alexandria uses data only for small
updates:

- It opens from the saved copy and asks your server whether anything changed.
  Each of those checks is a small request.
- Small lists still refresh: Continue, On Deck, Recently Added, your
  listening positions.
- **Whole-library downloads wait for Wi-Fi.** That means every album or every
  book in a library. On cellular, Alexandria fetches only the newest
  additions, where the server allows it.
- **Pull down to refresh** downloads everything now, cellular included.

The Mac and Windows apps count every network as Wi-Fi.

## Discover

**Discover** on the Audiobookshelf Home and in Grimmory shows new picks every
time you open the screen. They come from the saved library, so they use no
data. They stay put while you look. Pull down to refresh draws new ones.

## Covers

Jellyfin and Navidrome covers are kept on your device. Before build 1249, they
downloaded again every time a screen opened.

## Your accounts

- Each account has its own saved copy, and so does each Plex Home user.
- Signing out of a service, or removing it, deletes that account's saved
  copy.
- Saved copies use at most 150 MB in the app's cache. If your device clears
  the cache to make room, the screens download again the next time you open
  them.

Contact for questions or problems: stoutservers@gmail.com
