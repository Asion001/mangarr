# mangarr

[![CI](https://github.com/Asion001/Mangarr/actions/workflows/ci.yml/badge.svg)](https://github.com/Asion001/Mangarr/actions/workflows/ci.yml)
[![Image](https://img.shields.io/badge/image-ghcr.io%2Fasion001%2Fmangarr-blue)](https://github.com/Asion001/Mangarr/pkgs/container/mangarr)
![Go](https://img.shields.io/github/go-mod/go-version/Asion001/Mangarr)
[![License: AGPL-3.0-or-later](https://img.shields.io/badge/license-AGPL--3.0--or--later-blue)](LICENSE)

A self-hosted, Sonarr-style PVR for manga. mangarr monitors series, finds new
chapters on the sites it speaks itself or on
[Keiyoushi](https://keiyoushi.github.io) (Mihon/Tachiyomi) extension sources,
downloads them as CBZ files with `ComicInfo.xml`, and keeps a complete,
self-contained local library. You read in Mihon, KMReader or Paperback
straight from mangarr, through its Komga-compatible API: every chapter it
knows, downloaded or not, with progress synced both ways. Or you read
through [Komga](https://komga.org) or [Kavita](https://www.kavitareader.com),
which mangarr tells to rescan after every change.

```
 mangarr ──► its own sites (MangaDex, Weeb Central, …) ───────────────► sites
    ├──► Suwayomi, optional (runs Keiyoushi extensions) ──► FlareSolverr ──►
    ├──► /data/manga/<lang>/<Series>/*.cbz  ◄── Komga / Kavita ◄── your apps
    │
    └──► Komga-compatible API (:25600) ◄── Mihon, KMReader, Paperback
```

<p>
  <img src="docs/screenshots/library.png" alt="The library: a grid of series covers with download and read counts" width="100%">
</p>
<p>
  <img src="docs/screenshots/title.png" alt="A title page: cover, description, sources in order and the chapter list" width="74%">
  <img src="docs/screenshots/title-phone.png" alt="The same title page on a phone" width="24%">
</p>

The titles and covers above are made up (`npm run screenshots` in `web/`
draws them).

## Features

- **Sonarr semantics** — monitored series and chapters, monitor options
  (all / future / latest N / from chapter N / none), wanted/missing, queue,
  history, blocklist, upgrades, health checks, backups.
- **Multiple sources per series** with priorities: if a release fails it is
  blocklisted and the next source is tried; with upgrades on, a better
  source or preferred scanlator replaces the file **in place** (read
  progress in Komga/Kavita survives). Source order can differ per language
  and per root folder, and a fallback source can be added to a whole library
  at once.
- **Several languages** — each language edition is a series of its own, with
  its own files and sources; editions can be grouped under one title.
  Per-language defaults pick the catalogs, root folder and profile of a new
  series.
- **Everything external is a module** behind a Go interface — see
  [docs/modules.md](docs/modules.md):
  - `source` — **mangarr's own sites** (23 of them: MangaDex, MANGA Plus,
    Weeb Central, MangaFire, MangaLib, … — no extension engine to run) and **Suwayomi**
    (Keiyoushi extensions, FlareSolverr, extension manager, per-source
    settings) for the rest. Identities are portable `(sourceId, url)`, so
    *Switch engine* moves an existing library from one to the other.
  - `metadata` — **AniList**, **MangaUpdates** and **Shikimori**; all
    metadata modules are searched by priority and merged field by field with
    provenance and user locks.
  - `library` — **Komga**, **Kavita** (rescans, path mappings, per-user
    read progress).
  - `mediaserver` — **Jellyfin** and **Silo**: anime and film adaptations
    of a series, with a link to watch them where your server has them.
  - `notify` — **Telegram, Discord, ntfy, Gotify, Apprise, Webhook**, with
    per-series digests ("One Piece: 3 new chapters (1120–1122)").
  - `upscale` — waifu2x / Real-CUGAN / Real-ESRGAN via ncnn + Vulkan, built
    into the server or on a worker with the upscale role (e.g. a desktop GPU
    used whenever it's on).
- **Workers** — other machines that download, upscale and re-encode. Each
  has a key of its own and asks the server for work, so it needs no port and
  no inbound access; downloading from a worker also spreads the requests a
  site sees across addresses. Worker priorities and task limits decide which
  machine goes first.
- **Library that works on its own** — flat series folders, stable file names,
  `ComicInfo.xml` (validated against the v2.1 schema), `series.json` for
  Komga, `cover.jpg`. Files are written atomically.
- **Read-based cleanup** (off by default) — syncs each user's progress from
  Komga/Kavita and deletes chapters everyone finished: keep the last N, grace
  period, ignore readers who never opened a series, `keep` tag, dry run,
  recycle bin, restore.
- **Page upscaling** (off by default) — pages narrower than a threshold are
  upscaled 2×–4×; works on already downloaded chapters too.
- **Fast webtoons** (off by default) — tall strips are split near quiet rows
  after upscaling, so readers can download and decode smaller pages sooner.
- **Re-encoding to save space** (off by default) — AVIF (typically 40–70%
  smaller) or JPEG XL (lossless, ~20% and reversible, or lossy at a set
  quality). Chapters are readable right
  away and processed in the background, at the same path; mangarr checks that
  Komga can read the new format before continuing.
- **Share it with friends** — accounts with their own progress, devices and
  notifications on one shared library. Groups carry permissions (manage the
  library, handle requests, request series, reading apps, downloads) and what
  part of the library their members see (tags, root folders); invite links,
  sessions you can end, login protection, and optional **single sign-on**
  (OpenID Connect: Authentik, Authelia, Keycloak, Google, ...) with group
  mapping.
- **Requests** (Jellyseerr-style) — friends search the metadata providers and
  ask for a series; whoever handles requests adds it in the usual flow,
  links it to a series that's already there, or declines with a reason. A
  group's requests can be added automatically when Quick search finds a
  confident source. Requesters follow the series and hear when it arrives.
- **Follow series and get private messages** — follow what you care about
  and get its new chapters, and news about your requests, from the server's
  Telegram or Discord bot: right away or as a daily digest. Link your account
  once; only the admin handles bot tokens.
- **Discover and Updates** — recommendations from your unread library,
  recently updated series and popular titles from your catalogs, and a feed
  of new chapters and titles.
- **Web reader** — read in the browser with Mihon's comforts: right to left,
  left to right, vertical and webtoon modes, two-page spreads, split double
  pages, crop borders, tap zones, keyboard and swipes, chapter transitions
  with auto-advance, and settings kept per series. Progress is shared with
  everything else.
- **Read from mangarr in Komga apps** (off by default) — Mihon's Komga
  extension, KMReader and Paperback connect to mangarr's Komga-compatible
  API and see the whole library; KOReader uses its OPDS catalog and sync
  server. Chapters that aren't downloaded are
  streamed from the source and queued for download, and the next chapters
  are downloaded while you read. Each device gets its own API key, and Mihon
  can be set up in one step from a backup mangarr writes for you.
- **mangarr as the progress hub** — progress from apps and from Komga
  (live) or Kavita (on a timer) is merged, never lowered by a server that
  doesn't know a chapter yet, and passed on to every server. A *Continue
  reading* shelf and *Devices & sync* per reader show where everyone is and
  which device reported what.
- **Operations** — live download and processing progress (pages, speed, ETA),
  a space-saved history, rotating log files, and a one-click diagnostics zip
  with secrets masked.
- **Import from Mihon, Tachiyomi, Suwayomi or Aidoku** — upload a backup,
  review how each manga maps to your catalogs (exact for Keiyoushi sources,
  missing extensions installed for you), and import it with read chapters,
  categories and trackers; monitoring starts after the last chapter you read.
- **Import from a manga site account** (Senkuro, MangaLib) — the account's
  lists and how far you read each title, through the same review. Users bring
  their own library too: progress for titles on the server at once, the rest
  as one import for an admin to review by list.
- **UI in English, Russian and Ukrainian**, chosen per account, with a
  reading mode that keeps the editing controls out of the way.
- SQLite by default, **PostgreSQL** with one button (System → Database
  copies everything and restarts on it; backups hold the whole database
  either way and restore into either). Single ~30 MB static binary
  (distroless image) with the web UI embedded. OpenAPI docs at `/api/docs`.

## Quick start (Docker)

### The first 10 minutes

mangarr needs nothing else to run. Save this as `compose.yml`, with your
manga folder on the left of the second volume:

```yaml
services:
  mangarr:
    image: ghcr.io/asion001/mangarr:latest
    user: "1000:1000"   # who owns the manga folder
    volumes:
      - ./config:/config
      - /path/to/manga:/data/manga
    ports:
      - "8787:8787"
    restart: unless-stopped
```

1. `docker compose up -d`, open `http://<host>:8787` and create the admin
   account.
2. The **Series** page shows a setup checklist. Press *Add mangarr sources*
   (MangaDex, MANGA Plus, Weeb Central, MangaFire and 19 more sites), pick the
   languages you read, add a root folder such as `/data/manga/en`, and add
   *AniList* for titles, covers and synopses.
3. **Add series**: search for a title, pick one or more sources and what to
   monitor. Chapters land in `/data/manga/en/<Series>/` as CBZ files.
4. Read them in the browser right away, or turn on **Settings → Reading
   apps** and connect Mihon, KMReader, Paperback or KOReader
   ([docs/mihon.md](docs/mihon.md)).

### When you want more

[docker/compose.example.yml](docker/compose.example.yml) adds the optional
services, and the [setup guide](docs/setup.md) explains each one:

- **Komga or Kavita** to serve the library to more apps: mount it read-only
  there and add it under **Settings → Library servers** (an admin API key,
  and a path mapping if it sees the folder at another path).
- **Suwayomi** (pinned version, no published port) for sites mangarr doesn't
  speak itself, through Keiyoushi extensions, and **FlareSolverr** for sites
  behind a browser check. Add Suwayomi under **Settings → Source modules**
  and install extensions under **Sources → Add catalogs**.
- **PostgreSQL** instead of SQLite: System → Database moves the data over
  while mangarr runs.
- **Notifications**: a Telegram or Discord bot that messages each person,
  and broadcast targets (ntfy, webhooks, …) under **Settings →
  Notifications**.
- **Workers** on other machines for downloading, upscaling and re-encoding
  (below).

## Images

| Image | Arch | Notes |
|---|---|---|
| `ghcr.io/asion001/mangarr:latest` | amd64, arm64 | Every role (`MANGARR_MODE=integrated`, `server`, `worker`): Mesa Vulkan, avifenc/cjxl and — on amd64 — the ncnn upscalers |
| `ghcr.io/asion001/mangarr:slim` | amd64, arm64 | Distroless server only (~40 MB); re-encodes with the slower built-in AVIF encoder |

Tags: `X.Y.Z` (and `X.Y.Z-slim`, e.g. `1.2.0-slim`) pin a release, `latest` and `slim` follow
the newest release, and `main` and `main-slim` are built from every commit
to `main` (the newest features, and the newest bugs). See
[upgrading](docs/setup.md#upgrading-mangarr) before moving between versions.

A second machine that downloads, upscales or re-encodes is the same image
with `MANGARR_MODE=worker`, a server address and a key from System →
Workers. It asks the server for work, so it needs no port of its own.
(`MANGARR_MODE=upscaler` still starts it, and says so.) To keep upscaling and
re-encoding out of the server container entirely, run the server with
`MANGARR_MODE=server` and `MANGARR_PROCESSING=workers` and a worker next to it
that shares its data folder (`docs/setup.md`, "Processing in a separate
container").

Without Docker, a desktop (Windows, macOS or Linux) runs the
`mangarr-worker` zip from a release: the program with the upscalers next to
it, and a status page in the browser to set it up and watch its progress
(`docs/setup.md`, "A worker on a desktop").

## Configuration

| Variable | Default | Description |
|---|---|---|
| `MANGARR_LISTEN` | `:8787` | HTTP listen address |
| `MANGARR_KOMGA_LISTEN` | `:25600` | Komga-compatible API for reading apps (only while enabled in Settings → Reading apps) |
| `MANGARR_DATA_DIR` | `./config` (`/config` in Docker) | database, staging, backups, recycle bin, caches |
| `MANGARR_DB` | `sqlite://$DATA_DIR/mangarr.db` | or `postgres://user:pass@host:5432/mangarr?sslmode=disable` |
| `MANGARR_LOG_LEVEL` | `info` | debug, info, warn, error |
| `MANGARR_URL_BASE` | | serve under a sub path, e.g. `/mangarr` |
| `MANGARR_AUTH_DISABLED` | `false` | disable login (only behind an auth proxy) |

Everything else is configured in the UI, or pinned with environment
variables: every settings field (`MANGARR_DOWNLOADS_MAX_CONCURRENT=2`), the API
key (`MANGARR_API_KEY`), root folders (`MANGARR_ROOT_FOLDERS`) and whole module
instances (`MANGARR_MODULE_KOMGA_IMPL=library/komga`,
`MANGARR_MODULE_KOMGA_URL=…`). Pinned values show a lock in the UI. See
[docs/configuration.md](docs/configuration.md) or run `mangarr env`.

The API accepts the `X-Api-Key` header (Settings → General).

## Documentation

- [Setup guide](docs/setup.md): everything from folders and first run to
  accounts, reading apps, workers, processing, imports and backups.
- [Reading in Mihon](docs/mihon.md).
- [Reverse proxy](docs/reverse-proxy.md): Caddy, nginx and Traefik, a sub
  path, and the reading-app port.
- [Troubleshooting](docs/troubleshooting.md): sites that don't load, files
  Komga doesn't see, permissions, reading apps that can't connect.
- [Configuration](docs/configuration.md): every environment variable
  (generated by `mangarr env --markdown`).
- [Architecture](docs/architecture.md): how mangarr is put together.
- [Writing modules](docs/modules.md).

Planned work and bugs are tracked in
[GitHub issues](https://github.com/Asion001/Mangarr/issues).

## Development

```bash
make build        # Go binary (UI embedded from web/dist)
make build-worker # worker-only binary (cmd/mangarr-worker)
make web          # build the UI (uses Docker if npm is not installed)
make web-types    # regenerate web/openapi.json and web/src/api/schema.d.ts
make test         # unit + app tests (SQLite)
make test-pg      # also against Postgres (MANGARR_TEST_POSTGRES)
scripts/gate.sh   # what CI checks: gofmt, vet, import rule, OpenAPI drift, tests
scripts/integration.sh   # real Suwayomi + Komga in Docker
```

In `web/`: `npm run typecheck`, `npm run check:i18n` (every UI string has a
Russian and Ukrainian translation), `npm test` (Vitest) and `npm run test:e2e`
(Playwright). After changing a settings field or environment variable,
regenerate [docs/configuration.md](docs/configuration.md) with
`go run ./cmd/mangarr env --markdown > docs/configuration.md`.

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request (it
also explains how versions are numbered), the
[code of conduct](CODE_OF_CONDUCT.md), and [SECURITY.md](SECURITY.md) to
report a vulnerability.

## License

mangarr is free software under the
[GNU Affero General Public License v3.0 or later](LICENSE). If you run a
modified version for other people over a network, you have to offer them its
source code.

`internal/chapternum` is a port of Mihon's chapter recognition, which is
under the Apache License 2.0. The licences of the libraries and tools that
mangarr bundles are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
