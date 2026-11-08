# Browsing films and shows: Recommended, filters and collections

This covers Plex films and shows in Alexandria on the iPhone, iPad, Android,
Mac and Windows apps (phone build 1255 and later), and Plex and Jellyfin
libraries on the Apple TV (Apple TV build 199 and later).

## Plex Video Home (phone, tablet and computer)

Plex Video's **Home** shows, from the top:

1. **Continue Watching**: what you are partway through.
2. **The shelves Plex shows on its own Home**, in the order your server
   gives them. For example: Recently Released Movies, Top Movies in a
   genre, Top Unwatched Movies, Top Rated TV. Plex now and then swaps a
   genre shelf for another. The new one takes the old one's place, so
   nothing below it moves.
3. **Recently Added** and **Collections** for each library, as before.

Plex's own Continue Watching and Recently Added shelves are left out of
step 2, because Home already has them. Plex's music and photo shelves are
left out too. Tap a shelf's title to see everything on it.

## A library's page

Open a library from the menu (phone) or the sidebar (Apple TV). Its page
has three tabs, and opens on **Recommended**.

### Recommended

The shelves Plex shows on the library's own page, in the order set on your
server: Continue Watching, Recently Released, Recently Added, Top in a
genre, Top Unwatched, and any shelves you added on the server.

A collection your Plex server shows on the library's Recommended page (you
choose which ones in Plex) appears as its own shelf of films. For example,
a **Comedies** collection shows as a Comedies shelf.

On the Apple TV the page is laid out like Home:

- The banner at the top shows what you are partway through, then what is
  new, with **Resume** (or **Play**) and **More Info** under it.
- The shelves under it use Home's posters.
- The banner shrinks to a title strip while you are in the shelves, and
  stays that size as you move from shelf to shelf. It grows back when you
  go Up onto it. (Apple TV build 200 and later; in build 199 it grew and
  shrank again on every move.)
- From the sidebar, **Right** goes into a library just as Select does, even
  where nothing on the page sits level with the library's name (a music
  library's banner, for example). (Apple TV build 200 and later.)

Until your server's shelves arrive, or if it has none, Recommended shows
the library's Recently Added and its collections.

Jellyfin libraries on the Apple TV also open on Recommended, with Home's
banner and posters. Their shelves are unchanged: Continue Watching,
Next Up, Recently Released and Recently Added.

### Library

Every film or show in the library, with filters above it:

| Filter | What it does |
| --- | --- |
| **All / Unwatched / In Progress** | By what you have watched. |
| **Genre** | The genres your library has. |
| **Decade** | 2020s, 2010s, and so on. |
| **Rating** | The content ratings in your library (PG-13, TV-MA…). |
| **Quality** | A resolution (4K, 1080p…), HDR or Dolby Vision. |
| **Sort** | Title, Year, Recently Added, Rating or Length. The Apple TV also has Recently Watched. |

- Only the filters your server offers for that library appear. TV-show
  libraries usually have fewer: no Quality.
- Your Plex server matches Genre, Decade, Rating and Quality, so a genre
  finds every film tagged with it, not only those whose first two genres
  match.
- **Clear** removes every filter.
- On the phone, each filter opens a list to pick from. On the Apple TV,
  each is a menu in the row under the **All ▾ Movies by Title ▾** line.
- The A–Z bar beside the phone's grid shows only while it is sorted by
  Title.

### Collections

All of the library's collections, as a grid. Select one to open it.

## What changed

- Phone builds before 1255 had no Plex shelves on Home and no filters. A
  library's page was a Collections row fixed above the grid.
- Apple TV builds before 199 opened a library on the Library grid.
  Recommended had three shelves (Continue Watching, Recently Released,
  Recently Added) under a different banner. The filters were All /
  Unwatched / In Progress.

## Data

Plex's shelves are saved on the device like the rest of Home. They open
from the saved copy, then refresh about every 15 minutes, or at once when
the library changes. A filter asks your server once per pick. The lists of
genres, decades and ratings are read when you first open a library's page.
