# Describe a playlist

Type what you want to hear, such as "AJR radio", "chill jazz for studying" or
"2 hours of 70s rock", and Alexandria builds a playlist from music you own.

## Open it

- On a music server's home, scroll **Stations for you** to the end and tap
  **Describe a playlist**.
- In the Librarian, tap the playlist button at the top, or type `/playlist`.

Then type your request and tap Build. **Play** starts it, **Remix** builds a
different mix from the same request, and **Save as playlist** keeps it in
Alexandria or on one of your servers.

## Who picks the music

- **With Claude, OpenAI or another AI on the internet:** the AI gets a list
  of your music albums (artist, album, year and genre) and picks the albums,
  and sometimes particular songs, itself. Anything it knows about music
  counts: "AJR radio" gets the artists in your library that sound like AJR.
  The result says **Picked by AI**. It can only pick albums you own.
- **With an AI server at home:** home models are usually too small to read
  your whole library. The AI reads the words of your request (artists,
  genres, decades, moods, length), and Alexandria picks the albums.
- **With no AI:** Alexandria reads the request itself.

## What the words mean

- **"X radio", "X station", "like X", "sounds like X":** X and the artists
  like X. X brings about a third of the music. Without an AI on the
  internet, the artists like X come from the same lists stations use (your
  Plex server and Last.fm).
- **"2 hours of X", "X and Y":** only those artists.
- **"no X", "no 80s":** left out.
- **A length:** "25 songs", "2 hours" or "45 minutes". Otherwise about 30
  songs.

## What it costs

Each build with Claude sends your album list once. About 250 albums is about
3,500 tokens: roughly 1¢ a build on Sonnet and well under 1¢ on Haiku. A
second build within 5 minutes pays a tenth of that for the list. The monthly
cap under **You › Settings › AI** still applies; see [AI spending and
limits](ai-spending.md). A library of more than 3,000 albums is not sent:
the AI then reads only the request.

## When it says no

- **"Only one artist matched":** nothing else in your library is close
  enough to make a mix. Try a genre or a decade.
- **"Nothing in your library fits that":** the AI found nothing it could
  use; its reason is shown.
- **"The media index has no music yet":** rebuild it under **You ›
  Settings › Media index**.

Contact for questions or problems: stoutservers@gmail.com
