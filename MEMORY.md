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
- `APP_NAME` is `Penninn Midlun` in `deploy/supervisord.conf`;
  `.env.docker` still carries `APP_NAME=Flipbook` (file is access-blocked here —
  update manually).
- The SaaS runtime branding may still be stored in the LastSaaS database
  (`branding` API); code-level defaults now fall back to Penninn Midlun.
- Technical identifiers deliberately unchanged: Mongo `DATABASE_NAME`, and outbound `User-Agent` headers
  (`Flipbook-Webhooks/1.0`, `Flipbook-Sales/1.0`, `Flipbook-Engagement/1.0`) —
  rename these only when integrations/repos are migrated.

## Git / Development Lineage

- Dissociated from the previous development lineage on 2026-10-20. The only
  repository is `https://github.com/Sturla-is/flipbook`; push there. Local
  clones must not keep a remote for `jonradoff/flipbook` or
  `metavert-io/flipbook`.
- On 2026-11-11 every upstream reference was removed: Go module path is
  `github.com/Sturla-is/flipbook`, `scripts/setup-saas.sh` clones LastSaaS from
  `github.com/Sturla-is/lastsaas` (our mirror), the landing page no longer links
  to upstream Terms/Privacy pages, and the unused Fly configs were deleted.
  `LICENSE` keeps "Copyright (c) 2026 Metavert LLC" because the MIT license
  requires the original notice in all copies.
- On GitHub, `Sturla-is/flipbook` was created as a fork of
  `metavert-io/flipbook`, so GitHub (and tools that open PRs) default new pull
  requests to the upstream repo. Fix: repo Settings → Danger Zone → "Leave fork
  network". Until that is done, check the base repo of every new PR.

## Deployment

- Production runs on Coolify (not Fly) from `Dockerfile.saas`, which needs the
  `lastsaas/` checkout from `scripts/setup-saas.sh` in the build context.
- Point the platform health check at `GET /readyz` (MongoDB primary ping; fails
  when the driver wedges) rather than `/health`.
- Environment the deleted `fly.saas.toml` set, for reference:
  `FLIPBOOK_BASE_URL=https://flipbook.dev.nextdigital.is`, `FLIPBOOK_DATA_DIR=/data`
  (persistent volume), `FLIPBOOK_MONGO_DB=flipbook`, `FLIPBOOK_SAAS_ENABLED=true`,
  `FLIPBOOK_FRONTEND_DIR=/app/frontend-dist`, `FLIPBOOK_GRACE_PERIOD_DAYS=7`,
  `LASTSAAS_ENV=prod`, `LASTSAAS_CONFIG_DIR=/app/lastsaas-config`,
  `DATABASE_NAME=flipbook`, `FRONTEND_STATIC_DIR=/dev/null`, `SERVER_PORT=8091`,
  `FRONTEND_URL=https://bok.bodberi.com`, `APP_NAME=Penninn Midlun`. The Fly VM
  was shared-cpu-2x with 2 GB RAM.
