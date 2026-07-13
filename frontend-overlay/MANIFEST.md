# Frontend Overlay Manifest

This directory contains files that **replace or extend** the LastSaaS React
frontend for the Flipbook SaaS application. The build script
(`scripts/build-frontend.sh`) copies the base LastSaaS frontend and then
overlays these files on top.

## How upgrades work

1. Run `./scripts/setup-saas.sh` to pull the latest LastSaaS
2. Review each file below against the new LastSaaS version
3. Resolve any conflicts (LastSaaS may have changed the base files)
4. Run `./scripts/build-frontend.sh` to rebuild

## Modified files (replace LastSaaS originals)

| Overlay path | Replaces | What changed |
|---|---|---|
| `src/App.tsx` | `src/App.tsx` | Added flipbook routes (`/flipbooks`, `/flipbooks/upload`, `/flipbooks/:id`). Replaced landing page with FlipbookLanding. Added redirect logic for users without active plan. |
| `src/pages/app/DashboardPage.tsx` | `src/pages/app/DashboardPage.tsx` | Replaced generic dashboard with flipbook-centric dashboard showing recent flipbooks, usage stats, and quick upload. |
| `src/components/Layout.tsx` | `src/components/Layout.tsx` | Updated sidebar navigation to include "My Flipbooks", "Upload", "Usage" links. |

## New files (added, no LastSaaS equivalent)

| Overlay path | Purpose |
|---|---|
| `src/pages/public/FlipbookLanding.tsx` | Custom landing page with flipbook branding, upload teaser, pricing |
| `src/pages/app/FlipbooksPage.tsx` | Grid of user's flipbooks with status, thumbnails, actions |
| `src/pages/app/FlipbookUpload.tsx` | Drag-and-drop upload with progress + conversion tracking |
| `src/pages/app/FlipbookDetail.tsx` | Detail view for a single flipbook (embed code, share, delete) |
| `src/api/flipbook.ts` | API client for flipbook-specific endpoints (`/api/saas/flipbooks/*`) |
