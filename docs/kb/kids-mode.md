# Kids mode: simple screens for a child

Kids mode swaps the app's normal screens for a simple Home made of big
pictures. A child sees "Hello!" (with their name when the app knows it),
one large tile for each kind of thing they have (Movies, Shows, Music,
Audiobooks), and a row to keep watching or listening. Search, Settings,
downloads, Party Play, stations and the server screens are gone until a
grown-up turns Kids mode off.

It is in the iPhone, iPad, Android, Mac and Windows apps, the Apple TV,
and the Samsung, LG and Fire TV apps.

Kids mode makes the app simple for a child. It is not a lock: see
[What it does not do](#what-it-does-not-do) and
[On a TV](#on-a-tv-profiles-matter).

## What it does not do

**Kids mode changes the screens only. It does not filter titles.** What a
child can open is still decided by their own media-server user, never by
Kids mode. (On the phone, tablet and computer apps Kids mode shows less
than that: no ebooks or comics, and no Jellyfin movies or shows yet.)

To limit *what* a child can watch, use the media server's own tools and
give the child their own user there:

- **Plex:** a managed user with restrictions (Plex Home).
- **Jellyfin:** a user with parental controls and limited libraries.
- **Audiobookshelf / Navidrome:** a user with access to chosen libraries.

Then connect the child's profile to that user. Kids mode on top of it makes
the app easy and keeps the grown-up screens out of reach.

## Two ways to turn it on

Either one is enough. Use the one that fits.

### 1. For a person, on the companion server (every device of theirs)

Best when the child has their own person on your Alexandria companion
server and their devices are linked to it.

- **In the web server manager:** open **People**, find the person, and
  press **Kids mode: off** so it reads **Kids mode: on**.
- **In the app:** go to **You › Settings › Companion Server › Manage
  server**, open the person, and turn on **Kids mode**.

Every device linked to that person shows Kids mode within about a minute,
or the next time the app is opened. It can only be turned off the same way,
by an admin: no PIN on the child's device turns it off. (A TV with other
profiles on it is a different matter: see
[On a TV](#on-a-tv-profiles-matter).)

If a device cannot reach the server, it keeps the last answer it got. Only
the server saying "off" turns Kids mode off; being offline never does.

Needs companion server 1.1.0 or later.

### 2. On one device, with a grown-up PIN (no server needed)

Best for a shared tablet or TV, or when you do not run a companion server.

- **Phone, tablet or computer:** **You › Settings › Account › Kids mode**,
  turn on **Kids mode on this device**, and choose a four-digit grown-up
  PIN (you type it twice).
- **Apple TV:** on **Who's listening?**, hold Select on the profile and
  choose Kids mode, or go to **Settings › Profile**. Choose the PIN.
- **Samsung, LG or Fire TV:** **Settings › Profile › Kids mode**. Choose
  the PIN.

To turn it off, choose **Grown-ups** on the Kids Home and type the PIN.
Five wrong tries in a row make the pad wait a minute.

The device keeps only a scrambled form of the PIN, never the PIN itself.
Keep it somewhere you will find it. If it is lost on a phone, tablet or
TV, deleting the app and installing it again turns that device's Kids mode
off (its downloads and sign-ins go with it). On a computer, write to
support.

### On a TV, profiles matter

On a TV, Kids mode belongs to one profile, and **Switch Profile** is on the
Kids Home. A child who can pick another profile is out of Kids mode. So:

- Give every grown-up profile on that TV a PIN of its own.
- Know that the first screen ("Who's listening?") also lets anyone add a
  profile. A child who knows a login of their own could add a profile and
  sign in there. A fix that asks for a grown-up PIN before a profile can be
  added, removed or changed while a kid's profile is on the TV is being
  built; until your TV app has it, treat Kids mode on a TV as a helper for
  small children, not a barrier for older ones.

## What a child sees

- **Hello, (name)!** and one big tile for each kind of thing there is.
- **Keep watching / Keep listening**, to carry on where they stopped.
- A tile opens an A-to-Z wall of large covers. Choosing one opens it.
- **Now Playing** appears while something is playing.
- **Grown-ups** (and **Switch Profile** on a TV) in the corner.

Hidden in Kids mode: Search, Settings, downloads, Party Play, the "play
next", "add to queue" and playlist actions, stations, video versions, and
everything under Manage server. Most hold and "more" menus are off too.

## Good to know

- The server's switch and the device's switch are independent. If both are
  on, turning one off leaves Kids mode on.
- On the phone, tablet and computer apps, Jellyfin movies and shows do not
  appear in Kids mode yet. Plex movies and shows, music and audiobooks do.
  The TV apps show Jellyfin video in Kids mode.
- Turning on the server's switch for the person your own device is linked
  to puts your device in Kids mode too. The app warns you first; you then
  need another admin's device or the web server manager to turn it off.
