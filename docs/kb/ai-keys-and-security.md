# How Alexandria protects your AI keys

An API key is a password that spends money. Alexandria treats it that way.

## Where the key is kept

- **iPhone and iPad:** in the device keychain, in its strictest class: the
  key can be read only while the device is unlocked, is never included in a
  backup, and never moves to another device. After you set up a new phone,
  paste the key again.
- **Android:** in encrypted storage whose key lives in the phone's Keystore.
  Alexandria's data is not included in Android backups.
- **Mac:** in your login keychain; other apps cannot read it without your
  permission.

The key is never written to Alexandria's settings files and is never sent
anywhere except the AI server you added it to.

## A saved key is never shown again

After you save, the key field stays empty. You see only the key's last four
characters ("Saved key …abcd"), the way Anthropic's own Console shows keys.

- **Replace it:** paste a new key and choose **Save**.
- **Remove it:** choose **Remove key**, then **Save**. **Undo** changes your
  mind before saving.
- The eye button shows only a key you have just typed or pasted, so you can
  check it before saving.

If you need the key again, create a new one in the Claude Console and delete
the old one there.

## Moving a server to a new address removes its key

If you change a saved server's address to a different server, Alexandria
deletes the saved key when you save, and asks you to paste the key again.
This stops anyone holding your unlocked device from sending your key to a
server of their own just by editing the address. Changing only the path of
the same server keeps the key.

## Keys never travel over plain http without your say-so

A key sent to an `http://` address crosses the network unencrypted. If you
add a key to a server whose address is not https:

1. Alexandria shows a red warning under the key.
2. **Save** and **Test connection** wait until you tick **"I understand:
   send my key to this address over http"**.
3. The approval belongs to that exact address. Edit the address and you are
   asked again.

Until you approve, Alexandria sends nothing to that server. Its row on the
AI page says "Key not sent: the address is http". Claude is always https. A
home server on your own network or VPN is the only place http belongs.

## What the AI can and cannot do

The model only ever sees text: your question and, for the Librarian, a
summary of your library. It can never change, delete, download or manage
anything on your servers, whatever anyone types into it.
