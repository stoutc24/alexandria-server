# Third Party Licenses

Alexandria Server uses the following open source libraries:

## Go Libraries

### Fiber
- **License:** MIT
- **Source:** https://github.com/gofiber/fiber
- **Copyright:** Copyright (c) 2020-present Fenny and Contributors

### google/uuid
- **License:** BSD-3-Clause
- **Source:** https://github.com/google/uuid
- **Copyright:** Copyright (c) 2009, 2014 Google Inc.

### modernc.org/sqlite
- **License:** BSD-3-Clause
- **Source:** https://gitlab.com/cznic/sqlite
- **Copyright:** Copyright (c) 2017 The Sqlite Authors

### golang.org/x/crypto (argon2, for the admin password)
- **License:** BSD-3-Clause
- **Source:** https://go.googlesource.com/crypto
- **Copyright:** Copyright 2009 The Go Authors

These are the direct dependencies. The complete notices, for the Go
standard library and every module compiled into the server (including
the ones these pull in), are generated at image-build time and ship in
the image as `/app/THIRD_PARTY_NOTICES.txt`:

```bash
docker run --rm --entrypoint cat ghcr.io/stoutc24/alexandria-server:latest /app/THIRD_PARTY_NOTICES.txt
```

`golang.org/x/image` (BSD-3-Clause) is used only by `cmd/showcase`, the
mock media server behind the store screenshots; it is not in the image.

---

All of the above licenses permit commercial use and distribution.

Note: never add a GPL or (especially) AGPL dependency here. The AGPL's
network clause triggers on serving users over a network, which is exactly
what this is, and would compel publishing this server's source.
