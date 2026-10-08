# Troubleshooting

Start with **System → Status**: it lists health problems with what to do
about them. **Download diagnostics** on the same page bundles logs, status,
health, modules, settings and the queue for a bug report, with keys and
passwords masked (series titles and folder names are included, so look
through it before posting it). Logs are in `/config/logs/mangarr.txt` and
under System → Logs.

## A site doesn't load, or searches come back empty

- **Behind a browser check.** Some sites answer with a Cloudflare or similar
  challenge. Set the FlareSolverr address on Settings → Source modules →
  *mangarr sources* (and turn on *Use FlareSolverr* on a Suwayomi module).
  Only sites that need it go through it.
- **Paused after errors.** A site that answers with 429 or Cloudflare errors
  is paused on its own, from 5 minutes doubling up to 2 hours. Sources → My
  catalogs shows the pause with *Resume now*.
- **Too fast.** Settings → Search & throttling sets how gently sites are
  read (*Fast*, *Normal*, *Gentle*), also per catalog.
- **Language off.** Search only asks catalogs in the languages you read
  (Settings → Search & throttling) and catalogs that are on (Sources → My
  catalogs).

## Downloaded chapters don't show up in Komga or Kavita

- **Path mapping.** If Komga mounts the library at another path than
  mangarr (`/data/manga` in mangarr, `/manga` in Komga), add a path mapping
  on the library server (Settings → Library servers).
- **Scan.** Komga has no file watcher; mangarr asks it to scan, debounced by
  30 seconds. The library server needs an **admin** API key for that.
- **Read-only mount.** Komga and Kavita only need to read the library;
  mount it `:ro` there.

## Permission denied when writing the library

mangarr and the reader server should run as the same user
(`user: "1000:1000"` in compose) and the library folders must be writable by
that user. Files are written with mode `0664` and folders with `0775`. Each
CBZ is written as `<name>.cbz.partial` in the series folder and renamed when
complete, so a failure there is almost always the folder's owner or mode.

## A reading app can't connect

- Settings → Reading apps → *Allow Komga apps to connect* must be on; the
  API then listens on port `25600` (`MANGARR_KOMGA_LISTEN`). Publish that port
  (e.g. `"25601:25600"` when Komga already uses 25600 on the host) or route
  it through your proxy ([reverse proxy](reverse-proxy.md)).
- Set *Address apps should use* to the address the phone reaches, not
  `localhost` or the container name.
- Each device needs its own key (**Add device**, or My account → Reading
  apps). Apps that only ask for a username and password take any username
  with the key as the password.
- KOReader: leave document matching on **Binary**; filename matching isn't
  supported. See `docs/setup.md` §5.

## The page looks broken after an update

A tab left open across a server update keeps running the old web app.
mangarr shows a *mangarr was updated* notice with **Update** when it sees the
server come back as a new build; reload the page if the notice was
dismissed.

## The live updates stop behind a proxy

The UI keeps a Server-Sent Events stream open on `/api/v1/events`. A proxy
that buffers responses holds the events back, and one with a short read
timeout drops the stream. See the examples in [reverse proxy](reverse-proxy.md).

## Restoring a backup

System → Backups → **Add a backup file**, then **Restore**. A backup holds
the whole database and restores into SQLite or PostgreSQL alike, also on
another install. Library files aren't touched. Every backup is verified when
it's made or uploaded; the page shows the result and has **Verify**.

## Finding what is slow

System › Performance shows how fast the server answers over the last hour,
day or week, which endpoints take the most time, and how the caches and
the database are doing. The numbers are kept in memory and start again at
every restart.

- Every answer carries a `Server-Timing` header with its phases, so the
  browser's network panel shows where one slow request spent its time;
  answers slower than a second are logged as warnings with their request id.
- Prometheus can scrape `/api/v1/system/metrics/prometheus` with the API
  key as a bearer token.
- With `MANGARR_PPROF=true`, admins can use Go's profiler at
  `/api/v1/system/pprof/` (for example `go tool pprof
  https://manga.example.com/api/v1/system/pprof/profile?seconds=30` with the
  API key in an `Authorization: Bearer` header).
