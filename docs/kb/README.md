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
| [The Librarian](librarian.md) | Asking about your library, private chat, history, commands |
| [The Mac app and wide windows](mac-app.md) | Closing the window, menu bar Now Playing, the docked player |

Contact for questions or problems: stoutservers@gmail.com

## Check on a real device before publishing

These were proven in tests and simulators, not yet on hardware:

- [ ] Mac: closing the window keeps music playing; the Dock icon brings it
      back; a video pauses on close (build 1230).
- [ ] Mac: pause and skip from Now Playing in the menu bar.
- [ ] Mac and iPad: the docked player sits under every page (build 1231).
- [ ] iPhone: a key saved before 1231 still works after the update (it moves
      to the strictest keychain class on first launch).
