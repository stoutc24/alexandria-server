# Alexandria Link: set someone up with a code

Alexandria Link signs a person's device in to every server you set up for
them, without them typing a single password. They read you a code from
their screen, you approve it as them, and their device signs in to their
servers on its own, the way Plex's link codes work.

It works on the iPhone, iPad, Android, Mac and Windows apps, the Apple TV,
and the Samsung, LG and Fire TV apps.

## Before you start

- Give the person their servers on your companion server: in the web server
  manager open **People**, choose **Manage** on the person, then the
  **Services** tab, and **Add a service**. (You can also do this after
  approving their code. Their device picks up changes on its own.)
- Your companion server needs a **secrets key** to keep service logins
  (`ALEXANDRIA_SECRETS_KEY`; see the server's README). Without one, a link
  still works for stats and Party Play, but brings no servers.

## On their device

1. **Phone, tablet or computer:** go to **You › Settings › Companion
   Server**, type your server's address and choose **Get a link code**.
2. **Apple TV:** on the first screen choose **Link with a Code**, or later go
   to **Settings › Companion Server**.
3. **Samsung, LG or Fire TV:** on the first screen choose **Link Companion
   Server**, or later go to **Settings › Companion Server**.

A six-character code appears, with **Waiting for approval** and how long the
code lasts (10 minutes). They read or text you the code.

The code is safe to say out loud or send. It does nothing without you, and
only the device that showed it can collect the result.

## Approving it

- **In the web server manager:** open **Waiting to connect**, find the code,
  and choose who it is.
- **In the app:** go to **You › Settings › Companion Server › Manage
  server**. Under **Waiting to connect**, choose **Approve**, check the
  code matches, then pick **Someone already here** or **Someone new**.
- **In the app, starting from the person:** open the person in **Manage
  server** and choose **Link a TV** (beside **Invite to sign in**, and on
  the **Devices** heading). Devices showing a code right now are listed:
  tap the one their screen shows, or type the code. It works for any
  device that shows a link code, not only TVs.

Approving from a phone asks for a fresh code from your authenticator
each time. The keypad puts itself away when the sixth digit is in.

Only approve a code their screen is showing.

## An invite or a code?

They are two different things, and a TV only takes one of them.

- **An invite** (**Invite to sign in** on a person) is for a **phone,
  tablet or computer**. The person opens the link, or types the invite
  code under **I have an invite**, and chooses a password. Every device
  they sign in on after that needs no approval from you.
- **A link code** is what a device shows on **its own** screen, and you
  approve it. A **TV always works this way**: there is nowhere on a TV to
  type an invite, and there is not meant to be.

So to set up a TV for someone: on the TV open **Link with a Code** (or
**Settings › Companion Server**), then approve the code the TV shows, as
that person.

## What happens next

Their device signs in to each server you set up for that person, in the
order you set on the server. The page shows what it signed in to, and what
is waiting (for example, a server that needs Premium on the free plan).

- On the **Apple TV**, **Samsung**, **LG** and **Fire TV**, the servers go
  into the TV profile that showed the code. Other people's profiles on that
  TV aren't touched.
- A link never gives Premium. On the phone, tablet, computer and Apple TV,
  a device on the free plan keeps to its two services, starting with the
  first ones in your order. (The Samsung, LG and Fire TV apps don't have
  Premium yet, so they sign in to every server.)
- Servers the person added on the device themselves stay as they are.

The device checks for changes when the app opens and when it comes back,
at most every 15 minutes, and never in the middle of something playing.
When you add a service for the person, their devices sign in to it. When you
remove one, their devices sign out of it — only servers the link signed in
to, never ones they added themselves.

## Plex away from home

When you pick someone from your Plex, their devices get every address Plex
lists for your server: the one at home and the one that reaches it from
anywhere. Plex's from-anywhere address has your home's internet address
inside it, and most homes' internet address changes now and then.
Alexandria keeps the saved addresses current by itself:

- **Your app, when it opens.** If you can manage the server, the phone,
  tablet or computer app checks your Plex servers' addresses with Plex
  (at most once an hour, and each time you open **Manage server**) and
  updates everyone you picked from that server. You see nothing unless
  you look: there's nothing to press. It needs the app signed in to Plex
  with the Plex account that owns the server.
- **The companion server, every 15 minutes.** It notices when your home's
  internet address changes and moves the saved from-anywhere address to the
  new one, once your Plex server answers there as itself. To learn the
  address it asks plex.tv, only while someone is picked from a Plex server.
  The server's README says how to turn this off.
- **Their device, before it gives up.** When it can't reach Plex with the
  addresses it has, it asks the companion server for the newest ones and
  tries once more before saying **Can't reach your Plex server from here**.

**A fixed address of your own.** If your Plex server has its own web address
(say, through a reverse proxy), add it in Plex: **Settings › Network ›
Custom server access URLs** (shown with **Show Advanced**), for example
`https://plex.example.com:443`. Then pick the person once more: open them in
**Manage server**, open their Plex service and choose **Pick again**. A new
kind of address reaches their devices only with a new pick, which carries
their Plex key again. That way nothing can quietly send their key somewhere
that isn't your Plex server.

If they still can't reach Plex away from home, check that **Remote Access**
in Plex's settings says your server is reachable from outside.

## Devices linked before Alexandria Link

A device linked with a code before this update has stats and Party Play,
but not its servers. To get them, on that device:

- **Phone, tablet or computer:** **You › Settings › Companion Server ›
  Get my servers**.
- **Apple TV, Samsung, LG, Fire TV:** **Settings › Companion Server › Get My
  Servers**.

A new code appears. Approve it as the same person.

## Unlinking, and signing a device out

- On the device, **Unlink** (phone, tablet, computer) or **Disconnect**
  (TVs) ends the link. The servers it signed in to stay on the device.
- From the server, see [Signing a device out](signing-devices-out.md): the
  device loses the link and every server login the link gave it, and keeps
  what the person added themselves.

## Keeping it safe

- The device makes its own key and sends it with the code request. The
  logins you saved for the person are sent to the device locked to that
  key, so only that device can open them. Someone who can only watch your
  network (plain http at home) can't read them.
- Nothing on the server can give a device a key later. A copied device token
  can't be used to collect someone's logins.
- For the strongest protection, link over **https** (your server's public
  https address). On plain http, someone who can change traffic on your
  network while a code is being used, not just watch it, could step in.

This needs the companion server from 8 November 2026 or later, and an app
from the same update. **Link a TV** on a person's page is in the apps from
10 November 2026. Keeping Plex addresses current needs the companion server
and the apps from the update after 14 November 2026.

Contact for questions or problems: stoutservers@gmail.com
