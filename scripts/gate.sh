#!/usr/bin/env sh
# What CI checks, run here: formatting, vet, the import rule and the tests
# on both databases. Postgres comes from MANGARR_TEST_POSTGRES when it is
# set.
set -eu
cd "$(dirname "$0")/.."
echo "== gofmt"
test -z "$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }
echo "== vet"
CGO_ENABLED=${CGO_ENABLED:-0} go vet ./...
echo "== imports"
./scripts/import-lint.sh
echo "== openapi"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
CGO_ENABLED=0 go build -o "$tmp/mangarr" ./cmd/mangarr
"$tmp/mangarr" openapi > "$tmp/openapi.json"
cmp -s "$tmp/openapi.json" web/openapi.json || { echo "web/openapi.json is stale: ./bin/mangarr openapi > web/openapi.json, then regenerate the types"; exit 1; }
echo "== tests"
CGO_ENABLED=${CGO_ENABLED:-0} go test -count=1 "$@" ./...
