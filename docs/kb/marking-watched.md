# Marking films and shows watched or unwatched

You can tell your Plex or Jellyfin server that you have watched a film, an
episode, a season or a whole show, or that you have not. It works in the
phone, tablet and computer apps, on the Apple TV, and on Samsung, LG and
Fire TV.

The mark is saved on your server, for your user. Every other app signed in
as you sees it too: Plex's and Jellyfin's own apps, and Alexandria on your
other devices.

(New in the app update after phone build 1263 and Apple TV build 206.)

## What the button says

- **Mark watched** shows on something you have not started.
- **Mark unwatched** shows on something you have watched.
- **Both** show on something partly watched: a film or an episode you
  stopped part-way through, or a show or a season with some of its
  episodes watched. Mark unwatched there also takes it off **Continue
  Watching**.

(The TV apps capitalise it: **Mark Watched**, **Mark Unwatched**.)

A film or an episode counts as watched once it has been played to the end
(or nearly: 95%), or marked. A season or a show counts as watched only when
**every** episode is.

## On a page

- **A film or an episode:** open its page. The button is in the row with
  Play.
- **A whole show:** open the show. The button is near the top, with the
  show's play button (beside **Play All** on a TV).
- **One season:** open the show and pick the season. The button is with
  the season's play button (beside **Play Season** on a TV).
- **One episode from the show's page:** on the phone, tablet and computer,
  each episode row has its own tick button. On a TV, hold Select (OK) on
  the row.

## From a poster, without opening it

- **Phone and tablet:** touch and hold a poster or an episode row.
- **Mac and Windows:** click and hold.
- **Apple TV, Samsung, LG and Fire TV:** move to the poster and **hold
  Select** (OK).

Then choose **Mark watched** or **Mark unwatched**.

This works on the film and show posters of a library, its shelves, a
collection, and on episode rows. On the TV apps it also works on Home's
shelves and in search results.

## A whole show or a season asks first

Marking a show or a season changes many episodes at once, so the app asks:
"Mark the whole show as watched?" (or "this season", or "unwatched"), with
the number of episodes when it knows it. **Cancel** changes nothing.

A film or a single episode is marked straight away.

## What happens next

- The tick, the label and the counts change at once.
- The app then tells your server. When the server has saved it, the app
  says **Marked watched** or **Marked unwatched**.
- Something you marked watched leaves **Continue Watching**. For a show,
  **Next Up** and **On Deck** move on to the next unwatched episode. These
  lists come from your server, so they update a moment later.
- If the server says no or cannot be reached, the app says **Couldn't
  update. Try again.** and puts the screen back the way it was. Nothing
  was changed.

Marking something unwatched also clears the place you had got to in it.

## Good to know

- **Kids mode** has no Mark watched button and no menu for it.
- Your watch history belongs to your server user. If several people share
  one Plex or Jellyfin user, they share its watched marks too. Give each
  person their own user to keep them apart.
- With the **Unwatched** filter on, a film you mark watched leaves the
  list: at once on the Apple TV, and when the list next loads on Samsung,
  LG and Fire TV.
- Music, audiobooks and ebooks are not part of this. Audiobooks have their
  own "finished" marks.

Contact for questions or problems: stoutservers@gmail.com
