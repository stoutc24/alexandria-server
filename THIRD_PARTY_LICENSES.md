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

---

All of the above licenses permit commercial use and distribution.

Note: never add a GPL or (especially) AGPL dependency here. The AGPL's
network clause triggers on serving users over a network, which is exactly
what this is, and would compel publishing this server's source.
