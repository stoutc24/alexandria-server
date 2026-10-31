# Alexandria help articles

Plain how-to pages for people using the Alexandria app. Each matches what the
app shows today, with menu paths and button labels as they appear on screen.
They are updated with every app release that changes what they describe. For
installing and running the Alexandria companion server itself, see the
[README](../../README.md).

| Article | Covers |
| --- | --- |
| [Use Claude in Alexandria](ai-claude-setup.md) | Get a key, the safest Anthropic setup, add Claude, pick models |
| [AI spending and limits](ai-spending.md) | The monthly budget card, your whole account's spend, spend limits |
| [How Alexandria protects your AI keys](ai-keys-and-security.md) | Where keys are kept, why a saved key is never shown, http |
| [Use your own AI server](ai-home-server.md) | Ollama, LM Studio, llama.cpp on your network |
| [The Librarian](librarian.md) | Asking about your library, looking things up, private chat, history, commands |
| [The Mac and Windows apps and wide windows](mac-app.md) | Scrolling with a mouse or trackpad, closing the window, menu bar Now Playing, media keys on Windows, the docked player, how pages fit a wide window |
| [Alexandria in the car](car.md) | CarPlay and Android Auto: the tabs, radio stations, artists, going deeper |
| [Describe a playlist](playlist-builder.md) | "AJR radio", moods, lengths; when the AI picks the albums; what it costs |
| [Finding songs, artists and albums](search.md) | Plex music search, searching every library, what a song tap plays |
| [Radio stations](radio-stations.md) | What artist, genre, decade and AI stations play; Start Station on an artist page; intros and skits skipped |
| [Up Next on Now Playing](now-playing-up-next.md) | Where the playing song sits in the queue; tap, swipe and drag; the Artist tab; a party on a big screen |
| [Adding and removing songs at a party](party-queue.md) | Play Next, Add to Party Queue and ✕ from party devices and the Remote; Plex songs; messages |
| [Choosing who leads a party](party-leader.md) | Make leader from the leading device or any follower; taking the lead; the old leader's message |
| [Signing a device out](signing-devices-out.md) | Sign out one device or all of a person's, on the web or in the app; what the device loses and keeps; when it notices |
| [Alexandria on the Apple TV Home screen](apple-tv-home-screen.md) | The Top Shelf: Continue and Recently Added above the icons, and how to get it showing |
| [Music on the Apple TV](apple-tv-music.md) | The Queue (browse, play, move, remove, Shuffle/Repeat/Autoplay; at a party); leaving the app while music plays (Control Center); lyrics, and when Plex can't fetch them; the screensaver's song lines |
| [Lyrics on the phone, tablet and computer](lyrics.md) | Where lyrics come from (your server, then LRCLib); when Plex can't fetch them; lyrics kept on the device |
| [TV shows on the Apple TV](apple-tv-shows.md) | Which season a show opens on, Play All (Specials left out), Play Season and Auto-play |

Contact for questions or problems: stoutservers@gmail.com

## Check on a real device before publishing

These were proven in tests and simulators, not yet on hardware:

- [ ] Mac: closing the window keeps music playing; the Dock icon brings it
      back; a video pauses on close (build 1230).
- [ ] Mac: pause and skip from Now Playing in the menu bar.
- [ ] iPhone, Mac and Windows: Plex songs found from the Plex music search
      and from Search; a song plays, then its artist's station (added
      2026-10-29; search proven on the owner's Plex read-only, playback in
      the simulator; a station behind a song only in unit tests).
- [ ] Windows: the keyboard's play/pause, next and previous keys, and the
      song and cover in the volume pop-up (added 2026-10-29; proven in unit
      tests, the Windows half only compiled).
- [ ] Mac and iPad: the docked player sits under every page (build 1231).
- [ ] Windows and Mac with a mouse: dragging scrolls lists and rows of
      covers; dragging down at the top refreshes (added 2026-10-28).
- [ ] Every app: Start Station on a Plex, Jellyfin and Navidrome artist
      page plays a mix of artists (added 2026-10-28).
- [ ] Describe a playlist with Claude: "AJR radio" gives AJR and similar
      artists, marked Picked by AI (added 2026-10-28).
- [ ] The Librarian with Claude: a question about a series shows
      "checking the … series" and answers from it (added 2026-10-28).
- [ ] Windows, Mac and iPad: Now Playing is one centred block; album, book,
      Home and Search pages stop at their widths (added 2026-10-28).
- [ ] iPhone: a key saved before 1231 still works after the update (it moves
      to the strictest keychain class on first launch).
- [ ] CarPlay and Android Auto: "Stations for you" leads each music page,
      and a station tapped there plays and keeps going (added 2026-10-20).
- [ ] CarPlay: music › artist › album › a song › Up Next no longer closes
      the app, and Up Next opens the Queue tab (fixed 2026-10-20).
- [ ] CarPlay and Android Auto: a Plex artist's page shows records Plex
      lists under Compilations (fixed 2026-10-20).
- [ ] Stations skip intros and skits, and an artist station with enough
      similar artists plays no records that only share a genre tag
      (2026-10-26; proven in unit tests, not on a real library).
- [ ] iPhone: Up Next glides to the playing song when you scroll into it
      and at each new song (build 1236; proven on the Android emulator).
- [ ] Stations skip a track labelled "(Skit)" or "[Interlude]" at any
      length (build 1236, Apple TV next build; proven in unit tests).
- [ ] Artist stations follow Plex's styles and similar artists: Ye Radio
      plays rap, not Led Zeppelin; an artist station is offered only with
      six related records (next builds; proven on the owner's Plex data,
      read-only).
- [ ] Now Playing › Artist: the artist's picture opens their page (next
      phone build; proven in the simulator for Plex and Jellyfin).
- [ ] Party queue: Play Next, Add to Party Queue and ✕ from a following
      phone, a Remote and an Apple TV (next builds and server fc9ae97;
      proven in the simulators for a phone and an Apple TV leading and a
      phone following; the Apple TV's following path is untested).
- [ ] Windows, Mac and iPad: Now Playing during a party keeps the whole
      player and its tabs (next builds; proven in a unit test only).
- [ ] Apple TV: the music player's Queue — Select plays a song, hold Select
      to move or remove one; at a party, Select and Remove go to the leader
      (added 2026-10-29; proven in the simulator on the showcase, the party
      path untested).
- [ ] Apple TV Top Shelf: Continue and Recently Added above the icons with
      Alexandria in the top row (added 2026-10-30; proven in the simulator).
      Builds 187-189 showed only the logo on a real Apple TV; build 190
      saves the rows where an Apple TV allows it (fix not yet seen on one).
- [ ] Apple TV music outside the app: keeps playing on the Home screen;
      Control Center shows the song with its cover; Play/Pause, Next and
      Previous work; a song stops a book and the reverse (build 190).
- [ ] Apple TV: no screensaver while lyrics are open; the Queue row follows
      the playing song; the Lyrics button keeps the focus at a song change
      (build 190; proven in the simulator).
- [ ] Lyrics while Plex can't fetch them: the Apple TV keeps the Lyrics
      button and both say "Plex couldn't fetch these lyrics just now", then
      show them once Plex can; songs that loaded before keep their lyrics
      (Apple TV 190 and the next phone builds; proven in the Apple TV and
      iPhone simulators against a test server that fails the way a real
      Plex did on 2026-10-30).
- [ ] Apple TV following a party on its first song: a Queue pick, Next and
      Previous steer the party (fixed 2026-10-30; proven with the iPhone app
      leading in the simulator).
- [ ] Make leader from a following phone, Mac or PC: the new leader takes
      over, the old one keeps playing and shows who moved the lead; an
      Apple TV made the leader leads; a leading Apple TV whose lead is
      given away follows (added 2026-10-30; proven on the party rig with
      the iPhone and Apple TV simulators).
- [ ] Party Play with MP3/AAC songs: phones and computers stay in sync as
      with FLAC, and a device joining lands on the party's position (added
      2026-10-30; measured on the Android emulator: a follower's drift
      471 ms → 3 ms; not on an iPhone, Windows or in a room).
- [ ] Signing a device out: the device notices within about 15 seconds,
      loses its account's logins and keeps hand-added ones and downloads;
      a closed app notices on its next open (added 2026-10-30; proven on
      the party rig in the iPhone simulator; the closed-app case untested).
- [ ] Windows on a network cable: Justin Bieber Radio plays Justin Bieber
      and similar artists, as on the Apple TV (fixed 2026-10-30; proven in
      unit tests that a cable counts as Wi-Fi, not on the owner's PC).
- [ ] Apple TV screensaver: song, then artist with features, then the
      station, playlist or album (added 2026-10-29).
- [ ] Apple TV shows: Play All runs the whole show without stopping, leaves
      out the Specials, and opens on the right season; Play Season follows
      Auto-play (next TV build; proven in the simulator on the showcase).
