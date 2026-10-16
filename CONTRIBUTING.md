# Contributing to mangarr

Thanks for helping. mangarr follows semantic versioning (see
[Versions and releases](#versions-and-releases)), so a change that would
make people do something by hand when they upgrade waits for a major version.

## Before you start

- **Bugs and ideas** go in [GitHub issues](https://github.com/Asion001/Mangarr/issues).
  Search first; the backlog lives there.
- **Security problems** don't: see [SECURITY.md](SECURITY.md).
- Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).
- For anything bigger than a small fix, open an issue first so we can agree
  on the approach before you spend time on it.

## Setting up

You need Go (the version in `go.mod`) and Node 22+ (or Docker, which
`make web` falls back to).

```bash
make web      # build the UI into web/dist
make build    # Go binary with the UI embedded
make run      # start it with ./config as the data dir
```

[docs/architecture.md](docs/architecture.md) explains how the pieces fit, and
[docs/modules.md](docs/modules.md) how to write a source, metadata, library
or notification module.

## Before you open a pull request

Run what CI runs:

```bash
scripts/gate.sh   # gofmt, vet, import rule, OpenAPI drift, Go tests
cd web && npm run lint && npm run typecheck && npm run check:i18n && npm test
```

- If you changed an API handler, regenerate the client types with
  `make web-types` and commit `web/openapi.json` and `web/src/api/schema.d.ts`.
- New UI text needs English, Russian and Ukrainian strings.
- Keep a pull request to one change, with tests for the behavior it adds or
  fixes.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org)
  (`fix(reader): …`, `feat(sources): …`), as in the existing history. The
  release notes are made from them, so pick the type a user would file it
  under, and mark a breaking change with `!` (`feat(api)!: …`).
- A schema change is a new migration in both `internal/db/migrations/sqlite`
  and `internal/db/migrations/postgres`, numbered after the newest one. Never
  edit a migration that has been released, including `00040_baseline.sql`.
- Don't put real personal data (emails, domains, usernames, your own
  library) in test fixtures or screenshots.

## Versions and releases

mangarr uses [semantic versioning](https://semver.org): `MAJOR.MINOR.PATCH`.

- **Major** (`2.0.0`): upgrading needs a step by hand. That covers removing
  or changing an endpoint or field of the HTTP API (`/api/v1`, the
  Komga-compatible API, OPDS) in a way existing clients notice; removing or
  renaming a setting or environment variable without carrying its value over;
  a database change that a migration can't make by itself; and dropping an
  image, platform or database.
- **Minor** (`1.3.0`): new features, new settings with defaults, and
  migrations that run by themselves on start. A database a newer minor
  version has migrated isn't meant for an older one: going back means
  restoring a backup.
- **Patch** (`1.3.1`): fixes only, with no new settings and no migrations
  unless a fix needs one.

Releases are `vX.Y.Z` tags on a `main` commit whose whole CI run passed. The
tag builds the images (`X.Y.Z`, `latest`, and their `-slim` variants), the
binaries and worker zips, and the GitHub release, whose notes come from the
commit messages (`scripts/release-notes.sh`). Before tagging, regenerate
`THIRD_PARTY_NOTICES.md` with `scripts/third-party-notices.sh` if
dependencies changed.

## Sources

Native site sources live in `internal/sources/sites`. Sites change often and
some block automated traffic; a fix for a broken source is always welcome.
Please don't add sources whose main purpose is bypassing paywalls of
official publishers.
