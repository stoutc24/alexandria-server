# Use your own AI server

Instead of Claude, Alexandria can use a model running on your own computer:
Ollama, LM Studio, llama.cpp, or anything that speaks the OpenAI-compatible
API. Nothing then leaves your network, and there is no per-question cost.

## Add it

1. Open **You › Settings › AI › AI server**.
2. Under **Servers**, choose **Add server**.
3. **Server URL:** your server's address and port, for example
   `http://storyteller.local:11434` (Ollama's default port is 11434).
   Alexandria adds `/v1` for you.
4. **API key:** usually empty for a home server. Fill it in only if your
   server or a proxy in front of it asks for one.
5. Choose **Test connection**, then **Save**.
6. Pick this server for **Quick tasks**, the **Librarian**, or both, choose a
   model, switch on **Enable AI features**, and choose **Save**.

A good starting model is `qwen2.5:7b-instruct`. It is quick at the
structured work Alexandria asks for and fits a 10 GB graphics card.

## http and keys

A home server is usually `http://`. That is fine with no key. If you do add
a key to an http address, Alexandria asks you to tick a box first; see [How
Alexandria protects your AI keys](ai-keys-and-security.md). Keep a home AI
server reachable only on your network or VPN.

## GPU memory

When a slot uses a home server, the AI page shows a **GPU memory** card:

- It lists what the server has loaded.
- **Release the model now** frees the graphics card, for example before you
  play a game.
- **Gaming mode** releases the model after every request. Each question then
  waits for the model to load again.

Alexandria cannot stop or start the server itself. Once stopped, nothing
would be listening to turn it back on.

## The first question can be slow

A home server unloads an idle model after a few minutes. The next question
waits while it loads again, which can take tens of seconds. If an answer
does not come, the chat says why; ask again once the model has loaded.
