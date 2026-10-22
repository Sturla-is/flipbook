# Workspace Memory

Persistent facts for this workspace. Read this at the start of relevant work.

## Domains

- **Live domain (current):** `https://flipbook.dev.nextdigital.is`
- **Planned move:** `https://flipbook.bodberi.com` — switch when ready (not yet).

## Branding

- On 2026-10-22 the product was renamed to **Penninn Midlun** with the Penninn
  Eymundsson logo (`web/static/img/penninn-midlun-logo.svg`, sourced from
  penninn.is) used as the site logo across the Go templates and the React SPA
  overlay.
- `APP_NAME` is `Penninn Midlun` in `fly.saas.toml` and `deploy/supervisord.conf`;
  `.env.docker` still carries `APP_NAME=Flipbook` (file is access-blocked here —
  update manually).
- The SaaS runtime branding may still be stored in the LastSaaS database
  (`branding` API); code-level defaults now fall back to Penninn Midlun.
- Technical identifiers deliberately unchanged: Go module path, Fly app names,
  Mongo `DATABASE_NAME`, and outbound `User-Agent` headers
  (`Flipbook-Webhooks/1.0`, `Flipbook-Sales/1.0`, `Flipbook-Engagement/1.0`) —
  rename these only when integrations/repos are migrated.

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
