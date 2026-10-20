# Workspace Memory

Persistent facts for this workspace. Read this at the start of relevant work.

## Domains

- **Live domain (current):** `https://flipbook.dev.nextdigital.is`
- **Planned move:** `https://flipbook.bodberi.com` — switch when ready (not yet).

## Git / Development Lineage

- Dissociated from the previous development lineage on 2026-10-20.
- The old remote `origin` (`https://github.com/Sturla-is/flipbook.git`), its
  remote-tracking refs, and upstream branch config were removed.
- No upstream remote is configured. Local branch: `main`.
- Deployment configs (`fly.toml`, `fly.saas.toml`) were repointed from the old
  `flipbook.metavert.io` domain to the current live domain.
- Open follow-up (not done): the Go module path `github.com/metavert-io/flipbook`
  and README references to `metavert-io` still carry the old identity; rename
  when the new repository location is decided.
- Still carrying the old identity (rename when new locations are known):
  `README.md` clone URL (`github.com/metavert-io/flipbook.git`),
  `scripts/setup-saas.sh` REPO (`github.com/metavert-io/lastsaas.git`), and the
  Fly app names `flipbook-metavert` in `fly.toml` / `fly.saas.toml`.
