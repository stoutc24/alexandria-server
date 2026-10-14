# Security policy

## Reporting a vulnerability

Please report security problems **privately**, never in a public issue,
discussion or Reddit post.

- **Email** stoutservers@gmail.com with "Security" in the subject; or
- **GitHub**: the repository's **Security** tab → **Report a
  vulnerability**, where it is shown.

Please include:

- the server version (`version` in `/health`);
- what an attacker can do;
- how to reproduce it.

Do not include real tokens or codes. We will acknowledge your report and
keep you updated while it is fixed.

## Supported versions

Only the latest image (`ghcr.io/stoutc24/alexandria-server:latest`)
receives fixes. Stay current with `docker compose pull && docker compose
up -d`, or the update script in the [README](README.md#updating).

## How the server protects itself

The [README's Security section](README.md#security) describes what is
stored and how, and which routes answer without a token. In short:

- the server refuses to start without an admin token of 16 characters or
  more;
- device tokens and sessions are stored only as hashes;
- the admin page needs a password and an authenticator code;
- it connects only to your own servers, and only to create or reset an
  account you asked for (with the Server Admin sign-in you saved there);
- it runs as a non-root user.

The image is built by GitHub Actions from this repository only:

- every third-party action is pinned to a commit;
- base images are pinned by digest;
- pull requests from forks never run.
