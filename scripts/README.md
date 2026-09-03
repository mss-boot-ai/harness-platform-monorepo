# Scripts

Repository scripts must be deterministic, fail closed, and safe to rerun. They may verify or import the exact pinned Platform upstream, validate protocol sources, and orchestrate local checks.

Rules:

- no script may silently follow an upstream `main` or `latest` ref;
- no secret may be printed or persisted;
- a verification warning must not be treated as success;
- scripts that mutate the tree must document their expected clean-tree precondition and output;
- generated or imported code is committed only after provenance checks.
