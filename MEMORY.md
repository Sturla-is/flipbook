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

- Dissociated from the previous development lineage on 2026-10-20. The only
  repository is `https://github.com/Sturla-is/flipbook`; push there. Local
  clones must not keep a remote for `jonradoff/flipbook` or
  `metavert-io/flipbook`.
- Go module path renamed to `github.com/Sturla-is/flipbook` on 2026-11-11, along
  with the README clone URL and the landing-page self-hosting link.
- On GitHub, `Sturla-is/flipbook` was created as a fork of
  `metavert-io/flipbook`, so GitHub (and tools that open PRs) default new pull
  requests to the upstream repo. Fix: repo Settings → Danger Zone → "Leave fork
  network". Until that is done, check the base repo of every new PR.
- Deployment configs (`fly.toml`, `fly.saas.toml`) were repointed to the
  current live domain on 2026-10-20.
- Still carrying the old identity (each needs an action outside the code):
  `scripts/setup-saas.sh` clones LastSaaS from `github.com/metavert-io/lastsaas.git`
  (needs a copy under our own account); Fly app names `flipbook-metavert` in
  `fly.toml` / `fly.saas.toml` (Fly apps can't be renamed: needs a new app plus
  volume move); landing-page Terms/Privacy links point at metavert.io (need our
  own policy URLs). `LICENSE` keeps "Copyright (c) 2026 Metavert LLC" because the
  MIT license requires the original notice in all copies.
