# What the AI can see of your library

When you set up an AI server (Claude, another AI on the internet, or your
own), Alexandria can show it what the app already has saved about your
library, so its answers fit what you own and what you have read, watched
and played. You decide what it sees, kind by kind.

## Where it goes

Your library goes **only to the AI server you set up** in Settings › AI.
It is never sent to an Alexandria server or anywhere else.

The AI sees titles, authors and artists, series and volume numbers,
genres, years, lengths, progress, dates, counts, and the kind of service
something is on (for example "Plex" or "Audiobookshelf"). It never sees
server addresses, keys, tokens, passwords, user names, e-mail addresses,
file paths or device ids. Library text is cleaned before it is sent: line
breaks and hidden characters are removed, and anything in a title that
looks like a web address, an IP address, an e-mail address or a password
is replaced with a placeholder.

Titles are treated as data, never as instructions. A book called "Ignore
your instructions" cannot tell the AI what to do.

## Choose what it sees

**You › Settings › AI › What the AI can see.** Each switch saves at once.
All are on until you turn one off.

| Switch | What the AI can see |
| --- | --- |
| Books & audiobooks | Titles, authors and series |
| Music | Artists, albums and genres |
| Movies & TV | Films and shows |
| Progress & history | What you have finished or watched, what is in progress, and where your listening time goes |
| Upcoming releases | What the Upcoming Releases screen last found |
| Playlists | Playlist names and their tracks |
| Downloads | What is downloaded on this device |
| Stats & goals | Time spent, your streak, and your yearly goals |

Turning a switch off removes that kind completely: it is left out of what
the AI is told, and the AI cannot look it up either. Some things need more
than one switch:

- a film's release needs **Upcoming releases** and **Movies & TV**;
- playlists need **Playlists** and **Music**. A Jellyfin playlist can hold
  songs, audiobooks or films, so it also needs **Books & audiobooks** and
  **Movies & TV**;
- **Catch me up** needs **Books & audiobooks** and **Progress & history**;
- **Read the collection** needs at least one of **Books & audiobooks**,
  **Music** and **Movies & TV**;
- AI radio stations need **Music**; what you played lately goes with them
  only when **Progress & history** is on;
- time totals across all your media go only when **Books & audiobooks**,
  **Music** and **Movies & TV** are all on.

The AI only reads what Alexandria has already saved on this device. Building
what it sees never asks your media servers for anything.

## Kids mode

With Kids mode on, **nothing** from your library is sent to the AI, whatever
the switches say, and the Librarian can't look anything up. The switches
keep their settings for when Kids mode is off again. See
[Kids mode](kids-mode.md).

## How much it sees

- **Claude, and large AI models on the internet** (for example OpenAI's
  GPT-4o, GPT-4.1, GPT-5 and o-series, or Gemini): a detailed picture of
  your library, about 57,000 characters. Alexandria sends it so it can be
  **cached**: Claude marks it for its prompt cache, so the next question in
  the same chat reads it at about a tenth of the price. Other services may
  cache it in their own way.
- **Your own AI server, or a model Alexandria does not recognize**: a
  shorter summary, about 9,500 characters, because home models usually have
  a small memory.

Anything that does not fit is said to be left out, so the AI knows what it
was not shown instead of guessing. With an AI on the internet, the
Librarian can look the rest up ([The Librarian](librarian.md)).

## Which features use it

- **The Librarian** chat.
- **Smart search**, **Shelf ideas**, **Describe a playlist** and **AI radio
  stations**: with Claude or another large model, they also get the parts
  that help them (for example your listening history for playlists). With
  your own AI server they send what they sent before.
- **Read the collection** (Curator's notes): see below.
- **Daily picks on Home**: see below.

If a feature needs a kind you switched off (for example Describe a playlist
needs **Music**), it works without the AI, as it does when no AI server is
set up.

## Read the collection (Curator's notes)

**Library › Collection tools › Your Collection › Read the collection**
asks your AI for a few sentences about your collection.

- It is sent the numbers on that page (how many books, albums, films and
  shows, top genres, authors and artists, biggest series, decades) **and**
  your library, as the other features get it: books, music, movies and TV,
  and what you have finished or have in progress — only the kinds you
  have switched on. Upcoming releases, playlists, downloads and stats are
  not part of it.
- With **Claude or another large model** it gets the detailed picture of
  your library. With **your own AI server** it gets the shorter summary,
  cut so that it and the page's numbers together stay within about 9,500
  characters.
- The notes may name a title, author, artist, series or show only if it
  was in what the AI was sent. It is told never to invent one.
- In **Kids mode** nothing is sent and the AI is not asked.
- It counts toward your monthly AI budget
  ([AI spending and limits](ai-spending.md)), like every other AI request.

## Daily picks on Home

When an AI server is set up and Kids mode is off, the Librarian picks 2 or
3 things from your own library for **Your Librarian suggests** on Home:

- It asks **at most once a day**. Opening Home again does not ask again,
  and neither does a failed ask: the next try waits a day.
- Every pick must be something you own and have not finished; anything
  else the AI names is dropped.
- With no AI server, or when the day's ask fails, Home keeps showing the
  picks it already had and the ones you saved from the Librarian.
- The daily ask counts toward your monthly AI budget
  ([AI spending and limits](ai-spending.md)).
- Turn it off with **Settings › AI › Daily picks on Home**.

Daily picks are only shown. They are not added to your saved list.
