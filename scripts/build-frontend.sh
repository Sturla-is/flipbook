#!/bin/bash
# build-frontend.sh — Build the SaaS frontend by merging LastSaaS frontend
# with our flipbook-specific overlay files.
#
# The overlay pattern:
#   1. Copy lastsaas/frontend/ into .frontend-build/
#   2. Copy frontend-overlay/ files on top (replacing/adding)
#   3. npm install && npm run build
#   4. Output lands in frontend-dist/ (served by flipbook or reverse proxy)
#
# When upgrading LastSaaS:
#   1. Run ./scripts/setup-saas.sh to pull the latest
#   2. Review frontend-overlay/MANIFEST.md for files that replace LastSaaS originals
#   3. Diff each overlay file against the new LastSaaS version
#   4. Resolve conflicts, then re-run this script

set -euo pipefail
cd "$(dirname "$0")/.."

LASTSAAS_FRONTEND="lastsaas/frontend"
OVERLAY="frontend-overlay"
BUILD_DIR=".frontend-build"
OUTPUT_DIR="frontend-dist"

if [ ! -d "$LASTSAAS_FRONTEND" ]; then
    echo "Error: $LASTSAAS_FRONTEND not found."
    echo "Run ./scripts/setup-saas.sh first."
    exit 1
fi

echo "==> Preparing build directory..."
rm -rf "$BUILD_DIR"
cp -r "$LASTSAAS_FRONTEND" "$BUILD_DIR"

echo "==> Applying flipbook overlay..."
# Use rsync if available (preserves structure), fall back to cp
if command -v rsync &>/dev/null; then
    rsync -a "$OVERLAY/" "$BUILD_DIR/"
else
    cp -r "$OVERLAY/"* "$BUILD_DIR/" 2>/dev/null || true
fi

echo "==> Installing dependencies..."
cd "$BUILD_DIR"
npm install --legacy-peer-deps 2>/dev/null || npm install

echo "==> Building frontend..."
npm run build

echo "==> Moving output to $OUTPUT_DIR..."
cd ..
rm -rf "$OUTPUT_DIR"
mv "$BUILD_DIR/dist" "$OUTPUT_DIR"

echo ""
echo "Frontend built successfully at ./$OUTPUT_DIR/"
echo "Serve it with FLIPBOOK_FRONTEND_DIR=$OUTPUT_DIR in production."
