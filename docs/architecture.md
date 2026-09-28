# Architecture

How mangarr is put together, for anyone changing it. What it does for users
is in the [README](../README.md) and the [setup guide](setup.md); how to write
a module is in [modules.md](modules.md). Planned work and bugs live in
[GitHub issues](https://github.com/Asion001/mangarr/issues).

```
               ┌──────────────────────────── mangarr (Go) ─────────────────────────────┐
 Browser/API ─►│ API + embedded UI │ command queue/scheduler │ decision engine          │
 Komga apps  ─►│ Komga-compatible API (:25600) │ web reader │ SSE live updates          │
               │ pipeline: fetch pages → validate → [upscale/encode] → CBZ → import    │
               │ ─────────────────── module registry (Go interfaces) ───────────────── │
               │ source: native (own sites), suwayomi │ metadata: anilist, shikimori    │
               │ library: komga, kavita │ notify: telegram, discord, ntfy, gotify,      │
               │ apprise, webhook │ upscale: local, workers                             │
               │ worker task ledger ◄── /api/v1/worker/ ──────────────────────────────  │
               └────┬─────────────────┬───────────────┬────────────────────┬───────────┘
                    ▼                 ▼               ▼                    ▲
   manga sites (HTTP) ─► flaresolverr  metadata APIs  Komga/Kavita API    workers (pull)
   suwayomi (optional sidecar)                        (rescan + progress) download / upscale / encode
   mangarr writes /data/manga/<lang>/<Series>/*.cbz ─► Komga/Kavita (read-only mount) ─► apps
```

## Principles

- **The library works on its own.** Flat series folders, stable file names,
  `ComicInfo.xml`, `series.json` and `cover.jpg`. mangarr's database can be
  rebuilt from the files, and Komga or Kavita can read them without mangarr.
- **Everything external is a module** behind a Go interface. The core boots
  and works with none of them configured, and only
  `internal/modules/<kind>/<impl>` may talk to a vendor API
  (`scripts/import-lint.sh` enforces it).
- **Portable identities.** A source link is `(module instance, sourceId, url)`;
  for Keiyoushi-compatible sources `sourceId` is Mihon's id and the URL has no
  domain. Engine-specific ids (Suwayomi's integers) are a cache, so a library
  can move between engines (*Switch engine*) and survive a lost Suwayomi
  database.
- **SQLite and PostgreSQL are equal.** Every query goes through bun, migrations
  are kept per dialect, and CI runs every test on both.
- **Nothing lowers read progress** it didn't mean to. Progress from apps,
  library servers and the web reader is merged, never lowered by a server that
  doesn't know a chapter yet.

## Modules

Each module has a compiled implementation that registers itself, and
instances the user configures (kind, implementation, name, enabled, priority,
tags, settings JSON), in the style of Sonarr's ThingiProvider. The settings
struct's field tags become the form schema the UI renders; optional features
are capability interfaces discovered with type assertions. Details and the
interface table are in [modules.md](modules.md).

- **Sources**: `native` is mangarr's own sites, written against
  `internal/sources/sourcekit` (one file per site in `internal/sources/sites`);
  `suwayomi` drives a pinned Suwayomi for every other Keiyoushi extension.
  Catalog order, per-language and per-library priorities are resolved in
  `internal/sourcepriority`; request pacing per catalog in `internal/sourcegov`.
- **Metadata**: every enabled module is searched in parallel, results are
  joined by cross ids (AniList, MAL, MangaUpdates, …) or title, and merged
  field by field by priority (`internal/metadataagg`). Fields a user edits are
  locked against refreshes. AniList also fetches direct `ADAPTATION` relations
  to anime (TV, TV short, movie, OVA, ONA and special). These are stored in
  the series metadata JSON alongside external IDs and links; no separate
  table is needed. Series API resources expose an `adaptations` array with
  title, lowercase format, year and cover URL when known, plus `externalIds`
  (`anilist`, optionally `mal`) and `links`. Older records and series without
  AniList data return `[]`. A successful metadata refresh replaces this list,
  including clearing removed relations; a failed AniList fetch preserves the
  last successful result.
- **Media servers**: admin-configured `jellyfin` and `silo` instances resolve
  adaptations through `internal/modules/mediaserver`. Series detail resources
  add transient `watchLinks` without changing stored metadata. Matching and
  bounded caches live in the modules; the API imposes a shared deadline and
  keeps returning metadata when a server is unavailable. See
  [media-server contracts and limitations](modules.md#media-servers). There is
  no series-page UI for these links yet.

## Chapters and the pipeline

Chapter states: `missing` (wanted when monitored) → `queued` → `downloading` →
`processing` → `imported`, with `failed` beside them and `cleaned` for
chapters deleted after everyone read them (never wanted again unless
restored). Numbers come from the source; when a source has none, names are
parsed Mihon-style (`internal/chapternum`).

1. **Refresh** (`RefreshSources`, every 10 minutes) checks only the links that
   are due, with longer intervals for finished series and escalating backoff for
   failing sources.
2. **Decide** (`internal/decision`): monitored, not on disk or upgradable, not
   cleaned, blocklisted or queued, source healthy, scanlator allowed, enough
   pages, free space. Ranked by source priority, scanlator score, upload date.
3. **Download** into `/config/staging/<job>`, locally or on a worker. Every page
   is checked (magic bytes, decodes, not HTML, page count).
4. **Import**: a stored zip with `ComicInfo.xml` is written as
   `<name>.cbz.partial` in the series folder, fsynced and renamed to
   `<name>.cbz`, so readers never see half a file and no cross-mount move
   happens. History is recorded and a debounced library rescan is scheduled.
   After repeated failures the release is blocklisted and the next source is
   tried.
5. **Process** (upscale, re-encode) usually runs in the background after
   import. Upgrades and processing rename the new file over the **same path**,
   with the old one in the recycle bin, so Komga and Kavita keep read progress.

The recycle bin is indexed in `recycled_files`: every file put there by an
upgrade, reprocess, cleanup or series delete gets a row with its reason, its
chapter and a snapshot of the `chapter_files` row it replaced. System → Recycle
bin lists them, restores a version over the library file (keeping the
`chapter_files` id and remapping read progress when the page count differs),
and queues reprocessing that starts from the recycled file, the library file or
a new download, optionally with one-off processing settings. Housekeeping
purges by the recorded recycle time, not the file's mtime (a hardlinked file
keeps the library file's old mtime). Files already in the folder are indexed at
start-up and by Rescan folder.

The download manager stays in one package, with responsibilities split into
files:

- `manager.go` owns shared dependencies, startup recovery and shutdown handback.
- `dispatch.go` schedules eligible jobs; `queue_control.go` owns cancellation,
  bulk actions, claims, status transitions and persisted progress. `queue.go`
  and `rank.go` provide the persisted queue and ordering operations.
- `pipeline.go` loads a `jobCtx` and runs a local attempt. It owns staging cleanup
  and passes failures to `retry.go`, which classifies errors and chooses retry,
  release fallback or terminal failure.
- `fetch.go` downloads and validates ordered `PageFile` values; `screen.go`
  applies page rules. `process.go` also extracts existing archives for reprocess
  jobs and calls the `Processor` interface, whose `ProcessResult` contains the
  complete ordered pages and processing metadata for import.
- `import.go` writes the archive, records file/job state and history, remaps read
  progress after page splitting, and publishes events after the transaction.
- `offload.go` handles worker handoff and uploads. Worker completions use the
  same `finish` processing/import path as local downloads and clean up their
  staging directory when it returns.

These stages share the existing `Manager` dependencies. Page files stay on disk
until the attempt finishes. Stage errors retain their permanent/infrastructure
classification so moving a stage does not change retry or blocklist policy.

Download and reprocess jobs share a persisted integer `rank` (lower first).
The dispatcher selects eligible queued jobs by rank, subject to concurrency,
source, schedule and retry limits. The queue API pins importing, processing
and downloading jobs ahead of the pending order; queued and paused jobs share
that order. Series chapter resources expose the active job and the same rank.
`priority` remains an enqueue/reader-boost hint for compatibility; it does not
sort existing jobs after a manual move. Migration 27 backfills both databases
from `priority DESC, id`.

`POST /api/v1/queue/bulk` keeps its existing `ids` or `filter` selection and
`top`/`bottom` actions, and adds `before`/`after` with `anchorId`. Moves affect
only queued/paused jobs, preserving their current relative order regardless
of the order of submitted IDs. Missing or nonpending selected jobs are ignored;
an anchor must be pending and outside the selection. All changes in a move
commit together. A database singleton lock serializes moves, rank allocation
and dispatch claims across processes. A claim from an obsolete rank revision
is discarded and the dispatcher reads the queue again.

Ranks are sparse order keys, not display positions. Allocation uses indexed
neighbors and updates the selected jobs; exhausting an integer gap triggers
an order-preserving compaction in the same transaction. Compaction can change
numeric keys for all jobs, including finished jobs whose ranks are retained
for retries, without changing their relative order. Clients should read fresh
keys after a queue event. `GET /api/v1/queue` still accepts offset pagination
and additionally returns a `revision`. Sending that value on subsequent pages
returns HTTP 409 after an intervening enqueue, boost or move, so a client can
restart pagination. Each page's rows and counts use one database snapshot.
The revision covers rank changes, not job status transitions or removals;
live clients should also refresh on queue events for those changes.

The web queue shows each pending job's place in line (1 runs next), counted
from rank order across pages; running jobs show "now". Rows carry only a
checkbox, and one toolbar acts on the selection: up/down (one place past the
nearest unselected pending job, fetching at most one adjacent page with the
current revision at a page boundary), top/bottom, sort by chapter, pause,
resume, retry, blocklist and remove. Sort by chapter (`action: "sort"`)
reorders the selected pending jobs by chapter number within the ranks they
already hold; series keep the order of their first selected job. Finished jobs
are left to History, so the queue lists waiting, running and failed jobs.

## Commands, tasks and events

Long work runs as commands in a persisted queue (`internal/jobs`): duplicates
return the existing command, exclusive and disk flags limit what runs
together, and commands still running at shutdown are requeued. The scheduler
ticks every 30 seconds; its tasks (refresh, metadata refresh, read-progress
sync, processing backlog, health checks, disk scan, backup, housekeeping,
extension updates) can be paused and given an interval or time-of-day schedule
under System → Tasks. Code defaults stay separate from overrides, so restarting
does not erase user choices; see [task scheduling](task-scheduling.md).

A typed event bus feeds the SSE stream (`/api/v1/events`) the UI uses for live
updates, and the notification digests.

Database backup ZIPs are verified before they can be restored. Verification
checks the manifest, SQLite integrity, current migrations and required tables
on a temporary database copy, then records a SHA-256 checksum and row summary
beside the archive. Uploads stream to a temporary file and are capped at 4 GiB.
The system backup API exposes verification metadata and an explicit verify
operation for older archives.

The Updates feed applies account visibility and time bounds in SQL, merges
indexed chapter/title streams, and uses a timestamp, event-kind and row-id
cursor. Only the returned page gets release availability and reader-state
lookups. The performance target is a 50-row first page in under 250 ms at the
95th percentile with 100,000 recent chapters on local PostgreSQL; the dialect
regression walks a tied 600-row feed while inserts happen between pages.

Discover also exposes [individual paged shelves](discover-api.md), preserving
the combined endpoint used by the current UI. Shelf cursors retain library
order or source-page remainders in the bounded source cache; account visibility
and reader state are checked again on each request.

## Workers

A worker is the same binary with `MANGARR_MODE=worker` (or
`cmd/mangarr-worker` for builds without the server). It holds a key of its own
and polls the server for tasks from the ledger in `internal/worktasks`, so it
needs no inbound access. The server keeps everything that needs the module's
session or the site's budget: it resolves page addresses and hands each task
its share of the catalog's rate limit. Tasks have leases; a task nobody can
take comes back to the server, and a worker that dies loses its task to
another. Workers have priorities: a lower-priority worker gets a kind of task
only when every better-placed worker that can take it is full.

With `MANGARR_PROCESSING=workers` the whole processing stage is a task too
(kind `encode`, `processing.Remote`): the server writes the page list and the
profile into the task and imports what comes back, and the worker runs the
same `processing.Processor` with its own encoder and upscaling engine. A
worker with `MANGARR_WORKER_SHARED_STORAGE=true` that can see the job's
folder works on it in place; any other gets the pages as a zip and sends the
new ones back the same way.

## Stack

- **HTTP**: `chi` and `huma/v2`, which generates the OpenAPI 3.1 document
  (`/api/docs`, committed as `web/openapi.json`); the UI gets typed calls
  through `openapi-typescript` and `openapi-fetch`.
- **Database**: `uptrace/bun` on `modernc.org/sqlite` (no cgo) or Postgres,
  with embedded goose migrations in `internal/db/migrations/{sqlite,postgres}`.
- **Web**: React, Vite, TypeScript, TanStack Query, Tailwind; embedded in the
  binary with `//go:embed`. UI strings live in
  `web/src/lib/i18n/messages.json` (English, Russian, Ukrainian), checked by
  `npm run check:i18n`.
- **Images**: `docker/Dockerfile` builds the full image (Vulkan, ncnn
  upscalers, `avifenc`, `cjxl`) and the distroless slim one.

## Where things live

| Path | What |
|---|---|
| `cmd/mangarr`, `cmd/mangarr-worker` | the server (also `env`, `openapi`, `bench` subcommands) and the worker-only build |
| `internal/api` | the REST API; `internal/komgaapi` the Komga-compatible one |
| `internal/app` | wiring: builds the services and registers tasks and commands |
| `internal/modules` | the registry, field schemas and every module implementation |
| `internal/sources` | `sourcekit` (the site toolkit, a leaf package) and `sites` |
| `internal/downloads`, `internal/processing`, `internal/cbz`, `internal/comicinfo`, `internal/library` | the pipeline |
| `internal/worker`, `internal/worktasks` | the worker process and the ledger of work handed to workers |
| `internal/reading`, `internal/progress`, `internal/readsync`, `internal/cleanup` | the web reader, progress merging, library-server sync and cleanup |
| `internal/access`, `internal/auth`, `internal/sso`, `internal/requests` | accounts, groups, sign-in and requests |
| `internal/envcfg` | environment variables; generates [configuration.md](configuration.md) |
| `web/` | the UI; `web/e2e` holds the Playwright flows |
