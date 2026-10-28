# Adding and removing songs at a party

At a Party Play party, the device that leads plays its queue and every other
device follows it. Any device in the party, and any phone or Apple TV on the
Remote, can add songs to that queue and take songs out. The leading device
makes the change; everyone sees the new queue a moment later.

## Adding songs

**On a phone following the party:** use **Play Next** or **Add to Queue** as
you normally would (on an album, a playlist, or a song's long-press menu).
The songs go into the party's queue instead of only your phone's.

**On a phone with the Remote open, or not in the party:** long-press an album,
playlist or song and choose **Add to Party Queue**. The songs go at the end of
the party's queue. The option shows while a party is playing on another device.

**On an Apple TV following the party:** **Play Next** and **Add to Queue** go to
the party's queue. On an Apple TV that isn't in the party, hold Select on an
album, playlist or song and choose **Add to Party Queue**.

A message confirms each add: "Playing next at the party" or "Added to the
party's queue". One add takes up to 100 songs. For a longer playlist, the first
100 go in and the message says so.

## Removing songs

- **Phone following the party:** open Now Playing. **Up Next** shows the
  party's queue. Tap **✕** on a song to take it out.
- **Remote (phone or Apple TV):** the Up Next list has a **✕** beside each
  song.

The song that is playing has no ✕. To move past it, skip to the next song.

## What can join the queue

For now, only **Plex** songs can join a party's queue. The leading device finds
each song on its own Plex server, the same way a device that joins a party does.
A Jellyfin or Navidrome song gets the message "Only Plex songs can join a party's
queue for now".

## If a change doesn't go through

| Message | What it means |
| --- | --- |
| Update Alexandria Server to change a party's queue | Your Alexandria Server is older than this feature. Update it, then try again. |
| *Device* needs an Alexandria update to change its queue | The leading device runs an older Alexandria. Update the app on it. |
| The party's queue just changed — try again | The queue moved while you were choosing. Look at it again and repeat. |
| The party is still catching up — try again in a moment | Many changes are waiting on the leading device. Give it a moment. |
| No party is playing right now | The party ended, or the leading device stopped. |

Contact for questions or problems: stoutservers@gmail.com
