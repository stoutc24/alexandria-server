# Signing a device out

As the server's admin, you can sign someone's device out of Alexandria from
the server manager, one device at a time or all of a person's devices at
once. Use it for a lost phone, a device someone no longer uses, or anyone
who shouldn't have access anymore.

## Where to do it

**In the web server manager:** open **People**, choose **Manage** on the
person, then the **Devices** tab.

- **Sign out** next to a device signs out that device.
- **Sign out all devices** signs out every one of the person's devices.
- **Sign out the … not seen in 30 days** shows when some devices haven't
  been used in a month, and signs out just those.

**In the app:** go to **You › Settings › Companion Server › Manage server**
and choose the person. Each device has **Sign out**, and **Sign out all
devices** is below the list.

Each one asks you first.

## What the device loses, and what it keeps

A device signed in with an **Alexandria Account** loses:

- the Alexandria Account, and its link to your server;
- every server login the account gave it.

It keeps:

- server logins the person added on the device themselves;
- everything it downloaded.

A device that was only linked with a code (no account) loses its link to
your server. Its server logins and downloads stay.

**Sign out all devices** also signs the person out of **Manage server** on
their phones, if you gave them admin access.

Their account, their services and their listening history stay on the
server. They can sign in again.

## When the device notices

- **While the app is open:** within about 15 seconds. A message says the
  device was signed out, and what it kept. On a device signed in with an
  Alexandria Account, anything playing stops first.
- **If the app is closed:** the next time it opens.
- **If nothing is left signed in:** the app goes back to its first screen,
  where the person can sign in again with **Have an Alexandria account?**.

This needs the companion server from 30 October 2026 or later, and an app
from the same update.

Contact for questions or problems: stoutservers@gmail.com
