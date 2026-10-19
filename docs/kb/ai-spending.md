# AI spending and limits

Claude is billed by Anthropic for each question, by the amount of text sent
and received. A home AI server costs nothing per question. There are two
places that count and limit spending, and they do different jobs.

## The "Monthly AI budget" card (in Alexandria)

On **You › Settings › AI › AI server**, the card shows what Claude replies
cost **on this device this month**, and how many requests that was.

- It counts only replies received on this device, from version 3.2.1 (build
  1229) on. Your iPhone, iPad and Mac each keep their own count.
- It does not see anything else that uses your key: other devices, other
  apps, or the Console's playground.
- **Monthly cap (USD):** when this device's count reaches the cap, Alexandria
  stops asking Claude until the 1st of next month, and every AI feature falls
  back to its non-AI behaviour. 0 means no cap. The default is $20.

## Your whole account (at Anthropic)

- **See it:** the card's **Account spend (Cost page)** button opens
  Anthropic's Cost page (platform.claude.com/cost) in your browser. It shows
  every device and every app using your account, usually within about 5
  minutes.
- **Limit it:** the card's **Set a spend limit** button opens Anthropic's
  Billing page. A spend limit set there is enforced by Anthropic for every
  device and app. When it is reached, Alexandria shows "Anthropic spend limit
  reached" with the date access returns.
- **Safest setup:** give Alexandria its own workspace and key, with the
  workspace's own spend limit. Steps in [Use Claude in
  Alexandria](ai-claude-setup.md).

## Why Alexandria does not show your account total itself

Anthropic only gives account-wide spending to an **Admin key**, a key that
can also create and delete API keys and manage your organization's members.
Keeping a credential that powerful inside a media app on a phone would be
the opposite of a strong security posture, so Alexandria never asks for one.
Anthropic's own pages show and enforce the account total instead.

## Keeping costs down

- Use a low-cost model for quick tasks; a smarter one only for the
  Librarian, if you want one there.
- Alexandria reuses answers it already has for repeated quick tasks, and
  asks for AI radio themes at most once a day per music home.
- AI never runs on its own in the background.
