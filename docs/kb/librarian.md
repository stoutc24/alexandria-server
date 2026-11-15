# The Librarian

The Librarian is a chat about your own collection, or about anything at all.
It runs on the AI server you set up (Claude or your own); see [Use Claude in
Alexandria](ai-claude-setup.md) or [Use your own AI server](ai-home-server.md).

## Open it

- **Ask the Librarian** in the sidebar (iPad and Mac) or on the Search tab.
- Or **You › Settings › AI › The Librarian**.

## Two modes

The switch under the title chooses:

- **The Librarian** (book icon): answers grounded in your library — books,
  music, films and TV, what you have finished or watched, what is in
  progress, upcoming releases, playlists, downloads and your stats, as far
  as you allow in Settings › AI ([What the AI can see](ai-what-it-sees.md)).
- **Private chat** (speech bubble): a plain assistant. None of your library
  is sent.

The chip beside it shows the model in use. Tap it to change the model for
the Librarian.

## Asking

- Type a question and press send, or tap one of the starter cards on a new
  conversation.
- Under each answer: **Copy**, **Save books** (puts the recommended books on
  your reading list) and **Make a plan** (turns the ones you own into a
  reading plan). Both show what they found first; nothing is saved until you
  confirm.
- The playlist button at the top builds a real playlist from music you own.

## Conversations

Every conversation is kept on this device. **⋮ › New conversation** starts
fresh; **⋮ › History** reopens an old one. Long conversations are summarized
as they grow, so the Librarian keeps the thread without resending all of it.

## Commands

Type `/` to see them: `/new`, `/history`, `/briefing` (exactly what the
Librarian knows about you), `/playlist`, `/save`, `/plan`, `/mode`, `/style`
(Parchment or Console look) and `/help`.

## When it does not answer

The reason is shown in the chat, for example Claude's own error or "the
budget is used up". The table in [Use Claude in
Alexandria](ai-claude-setup.md#if-something-goes-wrong) explains each one.
On a home server, the first question after a quiet spell can take a while,
because the model is loading.

## Looking things up (Claude, OpenAI and other AI on the internet)

With an AI on the internet, the Librarian can also look things up in your
library while it answers, instead of guessing from the summary:

- search your library by title or by author or artist;
- everything by one author or artist: series in order, other books, albums;
- one series: the volumes you have, which you have finished, and the volume
  numbers you don't have;
- what you have finished or have in progress;
- browse by kind (books, audiobooks, ebooks, comics, music, films, TV), by
  genre and by year, or only what you have not finished or watched yet;
- one title's details: length, whether it is finished or in progress,
  whether it is downloaded on this device, and which services have it;
- your playlists and their tracks;
- upcoming releases the app has saved;
- what is downloaded on this device;
- your stats: time spent, streak and goals.

While it looks, the line above the answer says what it is checking, for
example "checking the Cradle series". It makes at most 8 lookups for one
answer. Lookups read only what is on this device; they can't change
anything. Each lookup adds a little to the cost of that answer, and the
monthly cap still applies ([AI spending and limits](ai-spending.md)).

A kind you switched off in **Settings › AI › What the AI can see** can't be
looked up either, and in Kids mode nothing can.

A home AI server and Private chat don't look anything up.

## How much it knows

With Claude or another large AI on the internet, the Librarian is given a
detailed picture of your library (about 57,000 characters), sent so Claude
can cache it: later questions in the same chat cost less. With your own AI
server it gets a shorter summary (about 9,500 characters). Either way it is
told what was left out. **/briefing** shows exactly what it was given.

## Limits

- Without lookups (a home AI server), the Librarian sees a summary of your
  library, not every row. For music it knows artists and album counts, not
  individual songs.
- With Kids mode on, it is given nothing from your library.
- It cannot play, change, delete or download anything. Saving books and
  making plans happen only when you confirm.
