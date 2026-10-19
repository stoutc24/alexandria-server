# Use Claude in Alexandria

Alexandria's AI features (the Librarian chat, describe-a-playlist, smart
search, AI shelves and AI radio stations) can run on Claude, Anthropic's
model. You bring your own Anthropic API key; Alexandria never ships one and
never sees your Anthropic account. AI features are part of Premium.

## 1. Set up your Anthropic account safely (one time, in a browser)

1. Sign in to the Claude Console at platform.claude.com.
2. **Set a monthly spend limit for the whole account:** open **Billing**
   (platform.claude.com/settings/billing), find **Spend limits**, and choose
   **Set limit** (or **Adjust limit**). Anthropic enforces this limit for
   every app and device that uses your account, so it is your real safety
   net.
3. **Recommended — a separate workspace just for Alexandria** (available when
   your Console account is an organization and you are its admin):
   1. Open **Workspaces** (platform.claude.com/settings/workspaces) and choose
      **Create workspace**. Name it "Alexandria".
   2. Select the new workspace and open **Spend limits** to give it its own
      monthly limit, for example $20.
   3. Create your API key *inside this workspace*. If the key is ever
      exposed, the damage stops at this workspace's limit, and you can
      archive the workspace to switch the key off at once.
   The Default workspace cannot have its own limit, which is why a separate
   one is worth it.
4. Copy the key when the Console shows it. You will paste it into Alexandria
   once; after that, Alexandria never shows it again.

## 2. Add Claude in Alexandria

1. Open **You**, then the **Settings** button at the top.
2. Choose **AI**, then **AI server**. The page is called **AI
   (Experimental)**.
3. Under **Servers**, choose **Add server**.
4. **Server URL:** `https://api.anthropic.com`. The name fills in as
   "Claude".
5. **API key:** paste your key. The eye button shows what you pasted, so you
   can check it before saving.
6. Choose **Test connection**. "Connected — N model(s) available" means the
   key works.
7. Choose **Save**.

## 3. Choose models and turn AI on

1. **Quick tasks** (search, playlists, shelves, plans, saving books): pick
   Claude as the server and a fast, low-cost model. Each model shows a short
   tip with its price compared with the cheapest one.
2. **Librarian** (the chat): "Same as quick tasks", or its own model. A
   smarter model writes better answers and costs more per question.
3. Switch on **Enable AI features**.
4. Choose **Save** at the bottom of the page.

## If something goes wrong

The Librarian shows the exact reason when Claude does not answer:

| Message | What it means | What to do |
| --- | --- | --- |
| `HTTP 401: invalid x-api-key` | The key is wrong or was deleted | Paste a new key (see the security article) |
| `Anthropic spend limit reached …` | Your account or workspace hit its spend limit; the message says when access returns | Raise the limit in the Console, or wait |
| `This month's AI budget is used up` | This device reached the budget set in Alexandria | Raise the cap on the AI page, or wait for the 1st |
| `API key not sent: the server is http …` | The address is not https | Use `https://api.anthropic.com` |

See also: [AI spending and limits](ai-spending.md) ·
[How Alexandria protects your AI keys](ai-keys-and-security.md)
