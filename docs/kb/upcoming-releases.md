# Upcoming releases: books, films, shows and music

The Upcoming Releases page shows what is coming out soon, and what just
came out, from the things in your libraries: new books from your authors,
sequels to your films, new episodes of your shows and new albums from your
artists. It is in the phone, tablet, Mac and Windows apps (not yet on the
TV apps).

(Films, shows and music are new in the app update after phone build 1272.
Books work as before.)

## Where to find it

- **Library** › **Collection tools** › **Upcoming releases** (under
  Reading Plans).
- **You** tab › **Coming up** › **See all** (or the **Upcoming releases**
  row).
- In the older home layouts: the **Releases** page to the right of Home
  (swipe left from Home).

(Until the app update after phone build 1273 it was also an **Upcoming**
entry in the Audiobookshelf home's menu; it moved to Collection tools.)

## The tabs

A tab bar across the top has one tab for each kind of thing in your
libraries: **Books · Movies · TV · Music**. A kind you don't have gets no
tab: with no films in Plex or Jellyfin there is no Movies tab. Tap a tab or
swipe between them. (On the Releases page beside Home, tap the tabs: a
swipe there still moves between the pages.)

With books only, the page is the same as before, with no tab bar.

Each tab has two lists, as Books always had:

- **Recently Released**: out in the last few days.
- **Releasing Soon**: coming out next, soonest first, with how long to go
  ("Releases in 12 days").

Each card shows a picture, the title, why it is there, and the date. Tap a
card to see its description and, when the show, film or artist is in your
library, a button to open it there (**Open show**, **Open movie**, **Open
artist**).

### Books

Unchanged: new and upcoming audiobooks from the authors in your
Audiobookshelf library.

### Movies

For your films in Plex and Jellyfin:

- the other films in the same series, coming out or just out: "Sequel to
  Dune";
- new films from a director who made two or more of your films: "New film
  from Denis Villeneuve".

Dates are for your country (your device's region setting): a film that
opens in the US a week before it opens in Germany shows the German date on
a phone set to Germany. Films due in the next three years, and films out in
the last 30 days, are listed.

### TV

For your shows in Plex and Jellyfin, the next episode when it airs in the
next four months (120 days), and the latest one when it aired in the last
14 days. The first episode of a new season is marked **Season 4 premiere**
(with its own number). The card says "New episode of Bluey · S4 E1".

### Music

For the artists with the most albums in your Plex, Jellyfin or Navidrome
music: new albums and EPs out in the last 30 days or due in the coming
year. "New album from AJR", "New EP from AJR". Compilations, remixes and
similar releases are left out, and so is an album you already have.

The artists come from Alexandria's index of your music, which is built in
the background (once a day on Wi-Fi). Right after you add a music server,
the Music tab can take a day to appear.

## How often it checks

The page checks when you open it, if it is due:

- **TV**: once a day.
- **Movies** and **Music**: once a week.

Pull down on a tab to check that tab now.

Each check looks at up to 60 shows and 60 films (the ones you watched or
added most recently) and 40 artists (the ones with the most albums). The
first Music check can take a minute or two: MusicBrainz allows one question
a second, so Alexandria waits between them. Later checks are quicker,
because the answers that never change (which artist on MusicBrainz is
yours, which TVmaze show is yours) are remembered.

If a check fails (no internet, a service down), the last list stays on
screen and the page tries again an hour later, or when you pull down.

## Alerts when something comes out

Alexandria can send you a notification when a new book, episode, film or
album comes out. Alerts are **off** until you turn them on. They are in the
iPhone, iPad, Android and Mac apps (not in the Windows app yet).

Open them with the **bell** at the top of Upcoming Releases, or **Settings**
› **Reading** › **Release alerts**.

- **Alert me about**: turn on **Books**, **TV**, **Movies** or **Music**.
  The first time you turn something on, your device asks whether Alexandria
  may send notifications. If you say no, the switch stays off; you can allow
  notifications later in your device's settings.
- **When**: **On release day** (on by default) and **A week before**. Alerts
  come at 9 in the morning, your time.

### One title at a time

Each card has a small bell. Tap it to choose, for that card's show, book
series (or author, for a book without a series), artist or film:

- **Always alert me**: you get alerts for it even with its type turned off.
  Following a single show is a way to get TV alerts for that show only.
- **Never alert me**: no alerts for it, even with its type turned on.
- **As set for TV** (or Books, Movies, Music): back to the type's switch.

A filled bell means always, a crossed-out bell means never. The titles you
chose are listed at the bottom of the alerts page; the **✕** beside one
puts it back to its type's switch.

### How alerts are planned

The alerts are planned on your device from the dates already on the
Upcoming Releases page. They are planned again whenever the page's lists
are checked, and when the app starts. Nothing extra is downloaded for them
and nothing runs in the background.

- Only dates that are still ahead get an alert.
- At most 40 alerts are planned at a time, the soonest first (your device
  keeps a limited number of planned notifications, and reading reminders
  need room too). Later ones are planned as the earlier ones pass.
- A date that moves, or a release that drops off the list, changes or
  removes its alert the next time the lists are checked.
- Book alerts use your own authors and series, not the genre suggestions
  in **Discover**.

Tapping an alert opens Upcoming Releases on that kind's tab.

## Where the information comes from, and what is sent

The lists come from three free public services. Alexandria asks them
directly from your device.

- **TV: [TVmaze](https://www.tvmaze.com/).** For each show: its TheTVDB or
  IMDb number when your server knows it, otherwise the show's title.
- **Music: [MusicBrainz](https://musicbrainz.org/) and the
  [Cover Art Archive](https://coverartarchive.org/).** The first time for
  each artist: the artist's name and the title of one or two of their
  albums (to find the right artist when several share a name). After that,
  only MusicBrainz's own artist numbers. MusicBrainz asks every app to say
  who it is, so the request carries "Alexandria" with its version and the
  public contact address stoutservers@gmail.com. Album covers load from
  the Cover Art Archive.
- **Movies: [TMDB](https://www.themoviedb.org/).** For each film: its TMDB
  or IMDb number when your server knows it, otherwise its title and year;
  then the numbers of its series and director, and your country code for
  the dates. This uses Alexandria's own TMDB key.

Nothing else is sent: not your server's address, your user names,
passwords or sign-ins, and no play counts or times. The titles asked about
are your most recently watched or added ones, so the services see which
titles those are, the same as any app looking them up.

Your own server is asked for one short list per library (the most recently
watched and added shows and films), the same way the app reads your
library elsewhere. The answers are kept on the device.

## Kids mode

The Upcoming Releases page is not part of Kids mode's Home, so it does not
show there.

## Credits

TV data from TVmaze (CC BY-SA). Release data from MusicBrainz; cover art
from the Cover Art Archive. This product uses the TMDB API but is not
endorsed or certified by TMDB. The same credits are under each tab and in
the app's **About** › **Attributions**.

Questions or problems: stoutservers@gmail.com
