#!/usr/bin/env sh
# What CI's web job checks, run here: lint, types, translations, unit tests,
# the production build, the bundle budget and the browser tests. Uses local
# npm when it is installed, otherwise the Playwright image (node_modules then
# live in a docker volume, since the host's would not run on its glibc).
set -eu
cd "$(dirname "$0")/../web"
steps='npm ci --no-audit --no-fund >/dev/null
echo "== lint" && npm run --silent lint
echo "== typecheck" && npm run --silent typecheck
echo "== translations" && npm run --silent check:i18n
echo "== unit tests" && npm test --silent
echo "== build" && npm run --silent build
echo "== bundle budget" && npm run --silent check:bundle
echo "== browser tests" && npm run --silent test:e2e'
if command -v npm >/dev/null 2>&1; then
  npx playwright install chromium >/dev/null
  CI=1 sh -ec "$steps"
else
  # the image's Playwright must match web/package-lock.json
  image=${PLAYWRIGHT_IMAGE:-mcr.microsoft.com/playwright:v1.63.0-noble}
  docker run --rm -v "$PWD:/app" -v mangarr-web-node-modules:/app/node_modules \
    -w /app -e CI=1 "$image" sh -ec "$steps"
fi
