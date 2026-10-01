# Homelab setup

This guide assumes Docker Compose on one host (e.g. an Intel N100 box) with
the library on a shared dataset, Traefik in front, and an existing
FlareSolverr.

## 1. Folders and permissions

Pick one library root per language, e.g. `/data/storage/manga/en`. mangarr
writes there (read-write); Komga/Kavita only read it (`:ro`). Run mangarr and
the reader server with the same UID/GID (`user: "1000:1000"`) and keep the
default file mode `0664` / dir mode `0775`.

mangarr writes each CBZ as `<name>.cbz.partial` inside the series folder and
renames it when complete, so readers never see half-written files and no
cross-mount moves are needed.

## 2. Services

Start from [docker/compose.example.yml](../docker/compose.example.yml):

- **mangarr** — `/config` volume + the library.
- **suwayomi** (optional) — only for catalogs mangarr doesn't speak itself.
  It is no longer required: mangarr has sites of its own (see §4). If you do run it,
  pin the tested version (`v2.3.2243`), keep its web UI off and don't publish
  its port: extensions run as code inside it, and it doesn't need the library.
  `JAVA_TOOL_OPTIONS=-Xmx512m` + `mem_limit: 1g` keeps it around 400–700 MB.
  Only enable `KCEF_ENABLED` (Chromium WebView) if a source you need requires
  it (+ a few hundred MB RAM).
- **flaresolverr** (optional) — reuse your existing one (or Byparr). Only
  sites behind a browser check need it.
- **komga** (or kavita) — library mounted read-only.
- **workers** (optional) — other machines that download, upscale or
  re-encode; see §12.

Behind Traefik, route only mangarr (and Komga) publicly; mangarr has its own
login and API key. With an auth proxy in front you may set
`MANGARR_AUTH_DISABLED=true`.

Let the proxy terminate TLS: browsers only use HTTP/2 over HTTPS, and it's
worth having — over plain HTTP/1.1 a browser opens about six connections per
site and the live-updates stream holds one of them, which the reader feels
when it loads pages. mangarr itself speaks HTTP/1.1 and cleartext HTTP/2
(h2c), so Traefik, Caddy and nginx can forward either.

## 3. First run

1. Create the admin account in the UI.
2. Settings → Media management → add the root folder(s). Keep the default
   naming `{Series Title} Ch.{Chapter:0000}`; don't put scanlator, source or
   volume in file names.
3. Settings → Source modules → *mangarr sources* is there by default: the
   23 sites mangarr talks to itself (MangaDex, MANGA Plus, Weeb Central,
   MangaFire, MangaLib, … see §4). Set *FlareSolverr URL* only if a site you use is behind a
   browser check. For anything else, add *Suwayomi*: URL
   `http://suwayomi:4567`, *Use FlareSolverr* on, URL
   `http://flaresolverr:8191`. "Manage Suwayomi settings" turns off
   Suwayomi's own updater and auto-download (mangarr schedules everything).
   Press **Test**.
4. Sources → My catalogs → drag the catalogs you want searched first to the
   top, and open a catalog's settings (its ⋯ menu → *Catalog settings…*) for
   what it offers: the language its titles come in, which of its servers to
   read pages from, whether to include adult titles. With Suwayomi, Sources →
   Add catalogs installs more (and asks which of an extension's languages to
   turn on).
5. Settings → Metadata → *AniList* (and *Shikimori* for Russian titles and
   synopses).
6. Settings → Library servers → *Komga*:
   - URL `http://komga:25600`, an **admin** API key (Komga → Account →
     API keys).
   - Path mapping if the paths differ, e.g. `/data/manga` → `/manga`.
   - Komga has no file watcher; mangarr triggers the scan (debounced 30 s).
   - In Komga's library settings, enable "Empty trash after scan" if you use
     cleanup.
7. Settings → Notifications → e.g. *Telegram* (bot token + chat id).

## 4. Sources: where chapters come from

mangarr speaks to 23 sites itself, with no extension engine in between:
MangaDex, MANGA Plus, MangaFire, MangaDot, Manga Ball, OniSaga, XCOMIC (one
catalog per language each), Weeb Central, Atsumaru, MangaTaro, MangaCloud,
MangaKatana, VyvyManga, LikeManga, MangaK, KaliScan, MangaHub, Mangakakalot,
Manganato, Scans.gg, Dynasty Scans, and MangaLib and Senkuro in Russian.
Catalog ids match the Mihon/Keiyoushi extensions for the same sites, so a Mihon
backup links straight to them. That is the *mangarr sources* module, and it
needs nothing running beside it. Suwayomi is still
there for everything else, and the two live side by side: a series can have
links to both.

- **Catalog order.** Sources → My catalogs lists the catalogs that are on, in
  search order, with how many series use each and whether their links work
  (failing links, a pause after 429s with *Resume now*). Catalogs that are
  off are folded away underneath. Searches go top-down; downloads prefer the
  sources a series already has, in the order they were linked.
- **A catalog's own settings** are in its ⋯ menu, with its request speed:
  the language its titles come in, which of its page servers to read from,
  whether adult titles are included — whatever that site offers.
- **Search defaults** (the languages searched, hiding NSFW catalogs) are in
  Settings → Search & throttling.
- **Sites behind a browser check** need FlareSolverr; set its address on the
  module (Settings → Source modules → *mangarr sources*). Sites that don't
  need it never pay for it.
- **Throttling**: Settings → Search & throttling sets how gently sites are
  read (*Fast*, *Normal*, *Gentle*, overridable per catalog). Sites that
  answer with 429 or Cloudflare errors are paused on their own, from 5 minutes
  doubling up to 2 hours.

### Priorities per language and library

The catalog order is the global default. On Sources → My catalogs, **Order
for** picks one language or one root folder to give its own order (the
library order wins over the language order); catalogs not listed keep their
global place, and *Use the global order* drops it again. Series added
afterwards inherit it. Series that already have their own order keep it; the
banner above the list shows how many, and **Review…** switches them to the
shared order, which changes only the order: it links no source and starts no
download.

Settings → Search & throttling → **Language defaults** sets, per language,
which catalogs an added series gets, and its root folder, profile and reading
direction.

### One title, several languages

A series in English and the same series in Russian are two series in mangarr,
each with its own files, sources and progress. **Language editions** on a
series page groups them under one title, so the library shows the work once
with its editions; *Separate edition* undoes it.

### A backup source for a whole library

A series with a single source stalls whenever that source does. Select series
in the library (Select → pick them, or select all) and use **Sources…** in the
bar at the bottom:

- *Add as a fallback source* looks each series up at the catalog by title and
  links it **last**, so downloads keep preferring what the series already has.
- **Preview** first: it reports what it found for each series, how sure the
  match is, and why the rest were skipped. Applying runs as a command, since a
  search per series takes a while.
- The same action removes a catalog from the selection, or switches it off
  without unlinking it.

### Moving a library off Suwayomi

Sources → **Switch engine…** moves existing links from one source module to
another. A link moves when both modules know the catalog by the same id —
which is the case wherever a site is built in under the id its Keiyoushi
extension has, so MangaDex links move as they are. Preview shows, catalog by
catalog, what moves and what stays; nothing is re-matched and no file is
touched. Catalogs the new module doesn't have stay where they are, and a site
it has under a *different* id says so: those have to be linked again by hand.

## 5. Reading apps

There are two ways to read.

**Straight from mangarr (recommended).** mangarr has a Komga-compatible API.
Komga apps connect to mangarr as if it were a Komga server, and see **every
series and every chapter mangarr knows**, downloaded or not. Chapters that
aren't downloaded are streamed from the source; opening one also queues its
download. Progress syncs both ways.

1. Settings → **Reading apps** → *Allow Komga apps to connect*. The API
   listens on its own port, `25600` like Komga (`MANGARR_KOMGA_LISTEN`).
   Publish it, e.g. `"25601:25600"` when Komga already uses 25600 on the
   host, and set *Address apps should use* to the address the apps reach.
2. **Add device** for each app (here, or by each person under **My account →
   Reading apps**). Each one gets its own API key, so you can see
   what every device synced and revoke one without the others. Apps that only
   ask for a username and password (Paperback) take any username with the
   key as the password.
3. Connect the apps (the page has step-by-step guides with your address):

| App | How | Progress |
|---|---|---|
| Mihon (Android) | Komga extension (Keiyoushi repo): address + API key | Enable **Komga** under Settings → Tracking → enhanced services. Syncs finished chapters. |
| KMReader (iPhone, iPad) | Add server: address + API key, or username and password | Page by page, live updates. Downloaded chapters can be saved offline. |
| Paperback (iPhone, iPad) | Komga extension: address, any username and a device key as the password (or your mangarr login) | Finished chapters, through its Komga tracker |
| KOReader | Add the `/opds` catalog; set its sync server to the mangarr address | Page progress through mangarr's KOReader sync server |

For Mihon there is a shortcut: **My account → Reading apps → Set up Mihon
from a backup** downloads a backup that restores your library with the
extension already configured. See [Reading in Mihon](mihon.md).

Each device key belongs to the account that made it, so everyone's progress
stays their own; keys made before accounts existed (and keys of the API key)
use the reader under Settings → Reading apps. What every device reports is
logged under Settings → Readers → *Devices & sync*, next to what Komga and
Kavita report. mangarr passes every change on to
the library servers, so Komga's own web reader stays up to date too. It
never lowers progress, except when you mark a chapter unread in an app.

*Read ahead* (on by default) downloads the next 3 chapters after the
furthest one a reader has started, in any app or library server, even in
series that aren't monitored. The Series page shows a **Continue reading**
row with the next chapter of each series in progress. The apps get the same
list as *On deck* and as a *Continue reading* read list.

**Through Komga or Kavita.** Apps can also read the library through Komga
or Kavita. They then only see downloaded chapters:

| App | How |
|---|---|
| Mihon (Android) | Komga extension (Keiyoushi repo); Komga tracker syncs progress |
| Paperback (iOS/iPad) | built-in Komga source |
| Tachimanga (iOS) | Komga |
| Panels / Chunky | OPDS `http://komga:25600/opds/v1.2/catalog` (Panels also has a Komga integration) |

**KOReader directly from mangarr.** Enable **Settings → Reading apps → Allow
Komga apps to connect** (the same listener serves OPDS and the Komga API), then
add `http://<mangarr-host>:25600/opds` as a catalog in KOReader (the same steps are under **My account → Reading apps → KOReader**). Sign in with
your mangarr username and a reading-app device key. The catalog shows only the
libraries and series your account can access. Open a chapter to download its
CBZ; AVIF and JPEG XL pages are converted to JPEG for the downloaded copy.
JPEG XL decoding needs `djxl` (included in the full Docker image).

For progress sync, set KOReader's custom progress sync server to
`http://<mangarr-host>:25600`, then log in with your mangarr username and a
reading-app device key. Create a fresh device key after upgrading to a version
with KOReader sync support; KOReader sends the MD5 of the entered key, and
mangarr stores a verifier for keys issued after that version. Keep that key
private like a password. `/users/create` is disabled because accounts belong
to mangarr.

Leave KOReader's document matching on the default **Binary** method: each CBZ
downloaded from this catalog registers KOReader's partial-file MD5 against its
chapter for your account. **Filename** matching is not supported. Sync stores
KOReader's page number and percentage as chapter read progress. Other files
that were not downloaded through this OPDS catalog have no automatic
chapter mapping.

## 6. Accounts: reading together

Everyone who reads here gets an account, with their own progress, their own
devices and their own notifications. The library itself is shared.

1. **Settings → Users & groups → Invites → Create an invite**: pick the
   group, how many people may use it and when it expires. Send the link; they
   choose a username and password (or single sign-on, see §9) and are in. The
   invite is spent when it runs out of uses.
2. **Groups** carry the permissions and what part of the library their
   members see:

   | Permission | What it allows |
   |---|---|
   | Administrator | settings, modules, system, users |
   | Manage the library | add, edit and delete series, the queue, sources, history |
   | Handle requests | see, add and decline what people ask for |
   | Request series | ask for series to be added |
   | Reading apps | the Komga-compatible API and device keys |
   | Download files | the CBZ of a chapter |

   Reading the library they can see, their own progress and their own account
   need no permission. Built-in **Admins** and **Users** can't be deleted.
3. **What they see**: a group can be limited to series with certain tags,
   without others, or in certain root folders. Series outside it don't exist
   for its members — not in the library, the web reader, the Komga API or the
   search.
4. **Their own progress**: each account gets a reader of its own (Settings →
   Readers). Progress from their apps, the web reader and their own
   Komga/Kavita account (§10) all land there. On a series page an
   administrator sees how far everyone got; others see only themselves.
5. **Sessions**: My account lists where you're signed in, and signs other
   sessions out. Changing a password, disabling an account or *Sign out
   everywhere* ends its sessions at once.
6. **Login protection**: 10 failed attempts for the same name or address
   (30 from one address) lock signing in there for 15 minutes; it covers the
   web login, invite links and the Komga API.
7. **Language and mode**: My account → *Interface* picks the UI language
   (English, Russian or Ukrainian; *Automatic* follows the browser). People who
   may change the library start in **reading mode**, which hides the editing
   controls; the switch at the bottom of the sidebar turns on **editing
   mode**.

## 7. Requests, following series and personal notifications

**Requests** (Jellyseerr-style) let people ask for series without giving them
the run of the library.

- Someone with *Request series* opens **Requests → Request a series**,
  searches the metadata providers and asks, with a note if they like. Asking
  for something someone already asked for joins their request.
- People with *Handle requests* (or *Manage the library*) see them under
  **Requests → To handle**: **Add series** opens the usual add flow with the
  metadata filled in and links the request, **Link** points it at a series
  that's already there under another name, **Decline** gives a reason.
- A request becomes *Approved* when the series is in the library and
  *Available* when its first chapter is imported. Whoever asked follows the
  series and hears about each step.
- A group can have **Add members' requests without approval** turned on: when
  Quick search finds a confident source, the series is added straight away;
  otherwise the request waits for a person.
- The install's notification modules can send **New requests** (Settings →
  Notifications).

**Following**: the bell on a series page (and the *Following* filter in the
library). New chapters of series you follow go to your own notifications.

**Your own notifications**: My account → *Notifications* → Add, and set up
ntfy, Discord, Telegram, Gotify, Apprise or a webhook that is yours alone.
You choose whether it gets new chapters of series you follow, news about your
requests, or both. These targets can only reach public addresses — for one
inside your network, ask an administrator to add it under Settings →
Notifications.

## 8. Reading in the browser

mangarr has its own reader, so a browser is enough: open a chapter from the
chapters table, the **Continue reading** shelf or the button on a series
page (`/read/<chapter>`). Chapters that aren't downloaded are streamed from
the source and queued, as in the apps.

- **Modes**: right to left, left to right, vertical, and **webtoon** (one long
  strip, side padding, gaps on or off).
- **Pages**: fit screen, width, height or original size; two-page spreads
  (with the cover alone) or single pages; **split double pages** into two, in
  reading order.
- **Crop borders** removes the uniform white or black margins around a page,
  measured on the server, so pages keep their quality.
- **Tap zones** as in Mihon (L-shaped, Kindle-style, edges, left/right, off,
  and inverted), keyboard (arrows, space, Page Up/Down, Home/End, `f` for
  full screen, `m` for the bars, Esc to leave) and swipes.
- Progress is saved as you read and shared with everything else; the last page
  finishes the chapter and the next one follows, after a card that says what
  comes next.
- Settings are kept **per series** (like Mihon), and *Use for all series*
  makes them your default. Everything is per account.

### Discover and Updates

- **Discover** puts recommendations from your unread library (based on what
  you read and follow), recently updated series and popular titles from your
  catalogs on one page. Titles that aren't in the library can be added or
  requested from there.
- **Updates** lists new chapters and newly added series over the last 7, 30 or
  90 days, filterable by read state and whether a chapter is downloaded.

### What the reader asks the server for

Worth knowing when a page feels slow, or when you put a proxy in front:

- Pages are **streamed** from the archive with a length, an `ETag` and a
  `Last-Modified`, so a page you have already seen comes back as a 304 and
  costs nothing. Give the proxy nothing to do here: don't buffer, don't
  re-compress.
- A phone gets a **page its own size** (`?w=`), made once and kept in the
  cache; the full-size page is served to anything wide enough for it. Turn it
  off in Settings → Reading if you'd rather always serve originals.
- The blurred page you see for a moment is the thumbnail the library already
  had; the real page replaces it when it arrives.
- Every response carries `Server-Timing` and an `X-Request-Id`, so the
  browser's network panel says where a slow request went, and the same id is
  in the log.

## 9. Single sign-on (optional)

Sign in with Authentik, Authelia, Keycloak, Pocket ID, Google or any other
OpenID Connect provider: **Settings → Single sign-on**.

1. Create an OAuth2/OpenID application at the provider with the **redirect
   URL** shown on the page (set Settings → General → *Public URL* first, so
   the address is right).
2. Fill in the issuer URL, client id and secret, turn it on and save. mangarr
   checks the issuer's discovery document when you save.
3. **Accounts**: *Make an account on first sign-in* lets anyone the provider
   lets through in; leave it off and people need an invite, which they can
   redeem with the provider's account. *Sign in to an account here with the
   same username* links existing accounts — only turn it on if you control
   usernames at the provider, since anyone who can pick one there could take
   over that account.
4. **Groups**: map the provider's groups to mangarr groups. People land in
   the first group that matches, at every sign-in, so the provider stays the
   source of truth. *Turn away people in none of these groups* keeps everyone
   else out. The last administrator never loses the role this way.
5. **Passwords**: turn *Keep password sign-in for everyone* off to make
   single sign-on the only way in. Administrators may still use a password,
   so you can get back in when the provider is down.
## 10. Readers and cleanup (optional)

Cleanup deletes chapters **every reader** has finished.

1. Settings → Readers → add each person, then **Link account** with *their
   own* Komga API key (Komga only exposes progress to the user itself).
   People with an account here can do it themselves under **My account →
   Library servers**, for their own reader.
2. Cleanup → enable, keep **Dry run** on at first, check the preview.
3. Defaults: ongoing series only, keep the last read chapter (keeps Mihon's
   tracker anchored), 7-day grace period, readers who never opened a series
   don't block it, `keep` tag excludes a series, files go to the recycle bin.
4. Cleaned chapters are never downloaded again; use **Restore** on a chapter
   to get it back.

Read progress arrives **live from Komga**: each linked account keeps Komga's
event stream open, so a chapter read on any device shows up in mangarr within
seconds (the Readers page shows *live*). Kavita is checked on a timer
(Settings → Readers, every 30 minutes by default). Each series shows how far
its readers got (*Continue: ch. N*, a read bar on the Series page, and a link
to open it in Komga).

## 11. Processing: upscaling and re-encoding (optional)

Processing is configured per profile (Settings → Profiles). By default it runs
**in the background**: chapters are imported as downloaded (readable right
away) and processed later, rewritten at the same path so Komga keeps read
progress. Background work runs behind downloads (System → Workers → *Chapter
files processed at once*, one by default); use Settings → Schedule to pause it
outside the night. When you turn processing on
for a profile, mangarr asks whether to process chapters you already have.

### Upscaling

Small pages look soft on an iPad. Profiles can upscale pages narrower than a
threshold (default 1400 px) with waifu2x / Real-CUGAN / Real-ESRGAN.

1. Give it a GPU. The full image (`:latest`, amd64) contains the upscalers:
   - **On the server itself** (e.g. the N100's iGPU): pass `/dev/dri` and the
     render group (`group_add`, see the compose example). mangarr then turns
     on the *Upscale* role of **This server** automatically (when a real GPU
     is visible).
   - **On another machine** (a desktop GPU): run it as a worker with the
     upscale role (§12). NVIDIA needs the container toolkit.

   System → Workers lists this server and every worker in one table. Work
   goes to the lowest priority number that is online and has room, so you
   choose whether the server's GPU or a worker goes first (this server starts
   after the workers); chapters wait while none is available. Each row can
   also pick its own **Upscale model** instead of the profile's, for a
   machine that is better at, or only fast enough for, another model. The
   scale is adjusted to one that model has, and the chapter records the
   model that was really used.
2. Pick a model: `realesr-animevideov3` is fastest (good for colour
   webtoons), `waifu2x-cunet` cleans black & white manga well, `realcugan`
   is sharper and slower. Profile → *Preview on a chapter* shows what each
   does to your own pages.
3. Settings → Profiles → enable upscaling, choose model and widths. If the
   upscaler is offline, chapters wait and are upscaled when it's back.

### Tall webtoon pages

Profiles can split webtoon strips into shorter segments. Splitting goes by
shape, not pixels: only pages more than 3× as tall as they are wide are split
(manga pages are about 1.4×, so upscaled pages are never cut), into segments at
most 2× as tall as wide, about one phone screen. Both ratios can be changed in
the profile. The step runs after upscaling and before re-encoding, prefers a
full-width light or dark gap near each balanced cut, and falls back to a hard
cut when there is no safe gap. Segments keep the page's full width.

When re-encoding follows, segments use lossless PNG as the hand-off. Otherwise
JPEG, PNG, WebP, BMP and AVIF keep their source format. Animated images and
JPEG XL are left untouched. The CBZ is renumbered in reading order, ComicInfo's
page count is updated, and saved per-user progress moves to the corresponding
segment. The profile's existing-chapters prompt and reprocess action apply the
same split to files already in the library.

### Re-encoding (AVIF / JPEG XL)

| Format | Saves | Readers that can't open it |
|---|---|---|
| AVIF (lossy) | typically 40–70% | Chunky only through Komga's OPDS (Komga converts); 32-bit ARM Komga. KOReader reads it through mangarr's `/opds`, which converts to JPEG |
| JPEG XL (lossless, JPEG pages only) | ~20%, reversible | Kavita. KOReader reads it through mangarr's `/opds`, which converts to JPEG |

Mihon 0.17+, Tachimanga, Panels (iOS 17+) and Komga's official amd64/arm64
image read both. After the first re-encoded chapter mangarr asks Komga whether
it could read it; if not, re-encoding pauses with a health error until you
resume it (System → Status).

- Encoders: the full image and the desktop worker zips include `avifenc`,
  `cwebp` and (except macOS and Windows) `cjxl`, which use every core. The slim
  image uses a built-in AVIF encoder that works everywhere but is much slower.
- AVIF pages are written progressive (layered) whenever the encoder can: the
  full image's `avifenc` 1.4+ does, the slim built-in encoder writes plain AVIF.
  Chrome shows a low-detail page first and sharpens it as it downloads; other
  readers (Apple's decoder on iOS, libavif in Mihon) show the full-quality page
  as usual. It costs about 6% in size and 20% in encoding time.
- Page size rules (Profile → Page processing → Page size) apply to every
  page: images under the junk size (300 px on the longest side by default:
  spacers, logos, tracking pixels) are never upscaled or re-encoded and can be
  removed from the chapter, and pages wider than the limit are shrunk (a
  two-page spread may be twice as wide). A chapter made only of junk is
  blocklisted on that source and the next source is tried.
- Low-resolution releases (Profile → Releases): when most pages are narrower
  than the set width, mangarr can try another source first and keep the
  release only when no other source has the chapter, or reject it outright.
- Pages are kept as they are unless re-encoding saves at least the configured
  percentage; black-and-white pages are encoded without color.
- Try settings on your own pages: Profile → *Preview on a chapter* (shows the
  pages side by side and gives a sample CBZ to open on the iPad), or
  ```bash
  docker exec mangarr mangarr bench encode "/data/manga/en/Series/Series Ch.0001.cbz"
  ```
- Originals go to the recycle bin unless you turn that off (to free the space
  immediately).

## 12. Workers: other machines

A worker is the same image started with `MANGARR_MODE=worker`, the address of
this server and a key of its own. It asks the server for work and uploads what
it produced, so it needs **no port and no inbound access** — a worker behind
someone else's NAT works exactly like one in the same rack.

1. System → Workers → **Add worker**. Give it a name, tick what it may do, and
   copy the key: only its hash is kept here, so that is the one time it can be
   read.
2. Run it:
   ```yaml
   mangarr-worker:
     image: ghcr.io/asion001/mangarr:latest
     environment:
       MANGARR_MODE: worker
       MANGARR_SERVER_URL: http://mangarr:8787
       MANGARR_WORKER_KEY: mgw_…          # from System → Workers
     devices:
       - /dev/dri:/dev/dri                # only for upscaling
     restart: unless-stopped
   ```
   It appears online in System → Workers within a few seconds, with its build,
   its platform and (for upscaling) the models and devices it found.
   To use multiple GPUs in parallel, set `MANGARR_UPSCALER_GPU=0,1` on the
   worker (or set the built-in upscaler's **GPU** setting to `0,1`). Each
   listed index gets one concurrent batch slot. `auto` keeps one default
   device slot. The worker stats API reports the GPU index used by its batches.

The roles:

| Role | What it does | Why |
|---|---|---|
| Download | Fetches a chapter's pages and uploads them here | The requests come from the worker's address, so a second machine spreads the load a site sees — and a slow uplink at home isn't the bottleneck |
| Upscale | Runs the upscaler on batches of pages | The GPU box does the work; the server keeps the library |
| Encode | Processes downloaded pages: resizes, upscales (with its own engine), splits and re-encodes them | All image work off the server, when it runs with `MANGARR_PROCESSING=workers` (below) |

Notes:

- **Who downloads what.** With Settings → Downloads → *worker placement* on
  `auto` (the default), a chapter goes to a worker when one with the download
  role is online and is downloaded here otherwise; `workers` waits for one and
  `local` never uses them. The server still resolves the page addresses —
  that needs the source module's session — and still paces each catalog, so a
  worker cannot hammer a site harder than the throttling allows.
- **Pages are checked on arrival** and written into the same staging folder a
  local download uses, so importing, processing and history are identical
  either way.
- **A worker that dies mid-chapter** loses its task after a lease (two
  minutes) and another worker — or this server — picks it up. After three
  tries the chapter is failed as an infrastructure error and retried later,
  never blocklisted.
- **Priority and limits.** Each worker has a priority (lower first): a
  lower-priority worker gets a kind of task only when every better-placed
  online worker that can take it is full, so a GPU box can go first and a
  spare machine only catches the overflow. System → Workers → *Worker
  concurrency* caps tasks per worker (overridable per worker) and across all
  of them. Each row also sets *Pages at a time*, how many pages one of its
  downloads fetches at once (0 leaves it to the worker's
  `MANGARR_WORKER_PAGE_CONCURRENCY`, 4 by default; a catalog's own
  politeness limit still caps it). A running worker picks up both on its
  next request for work, so no restart is needed.
  The *This server* row takes the same two limits for the work it does
  itself: *Concurrent tasks* caps the downloads and chapter files it works
  on at once (0 leaves only the other limits), and *Pages at a time* is
  Settings → Downloads' page concurrency.
- **Switching one off** in System → Workers stops it being given work at
  once; removing it invalidates its key.
- **Shared storage.** A worker in the same compose file as the server can
  mount the server's data folder at the same path (`/config`) and set
  `MANGARR_WORKER_SHARED_STORAGE=true`. It then reads and writes the pages of
  its upscale and processing tasks in place instead of sending them over
  HTTP. A task whose files it can't see falls back to HTTP, so a wrong mount
  is slower, not broken. Run it as the same user as the server.
- `MANGARR_MODE=upscaler` still starts a worker (it says so), but the old
  push-based node with its own port and the server's admin key is gone.

### A worker on a desktop (Windows, macOS, Linux)

A gaming PC makes a good upscaler, and it doesn't need Docker. Each release
has a `mangarr-worker` zip for Windows (x64), macOS (Apple silicon) and
Linux (x64): the program, with the waifu2x / Real-CUGAN / Real-ESRGAN
upscalers in an `upscalers` folder and the native encoders (`avifenc`,
`cwebp`, and `cjxl` on Linux) in an `encoders` folder next to it (Windows's
`avifenc` needs the Visual C++ runtime; without it the worker falls back to
the slower built-in encoder). The upscalers use the computer's own GPU
driver through Vulkan, so NVIDIA, AMD and Intel cards all work (Linux needs
the Vulkan loader, `libvulkan1`, installed). Every build of `main` leaves the same zips on its CI run, under Artifacts.

1. System → Workers → **Add worker** and copy the key.
2. Unzip anywhere and start `mangarr-worker` (`mangarr-worker.exe` on
   Windows). It opens its status page, http://127.0.0.1:8790, in the
   browser.
3. Enter the server's address and the key, tick the roles, and save. The
   page then shows what the worker is doing (each task with its progress),
   totals since it started, the last tasks and its log. **Stop** hands back
   what it holds; closing the window or Ctrl+C stops it too.

**Settings** (top right) changes them later. With several graphics cards,
tick each under *GPUs*: each runs its own batch, so set *Tasks at once* (or
the worker's row in System → Workers) to at least the number of cards. While
the worker is switched on in System → Workers it keeps the computer from
going to sleep (the screen may still turn off); switched off there, or
stopped on the page, the computer sleeps as usual, and the worker picks up
again when it is switched back on. *Keep this computer awake* turns that
off.

Settings are kept in the user's config folder (`%AppData%\mangarr-worker`
on Windows) and can still be set as environment variables, which win and
show as locked on the page. `-listen` (or `MANGARR_WORKER_UI_LISTEN`) moves
the page, `-no-browser` doesn't open it, and `-headless` runs from the
environment alone without a page. The page only answers on this computer.

On macOS, a downloaded zip is quarantined: run
`xattr -dr com.apple.quarantine` on the unzipped folder once, or the
upscalers won't start.

### Keeping workers up to date

A worker says which version it runs when it connects, and System → Workers
marks one on an older release with *Update to …*. While System → Workers →
*Update workers with this server* is on (the default), the server also tells
the worker on every request for work. Only tagged releases (`v1.2.3`) take
part: a server or worker built from a branch or a local checkout is left
alone, and nothing ever moves to an older version. A worker on another
version keeps getting work until it updates.

**Desktop workers** update themselves. The worker stops taking new tasks,
finishes the ones it holds, downloads the release's zip for its platform,
checks it against the `.sha256` published next to it, runs the new program
once to check it starts and reports the expected version, and only then puts
it and the zip's `encoders` folder in place of the old ones (kept next to
them as `mangarr-worker.old` and `encoders.old`) and restarts. The
`upscalers` folder is left as it is. The status page shows *Updating* meanwhile and reconnects on its
own. If the new version starts three times without reaching the server, the
old program and encoders are put back and that version is not tried again on that machine
(`mangarr-worker.skip` next to the program; delete it to try again). A failed
download or check leaves everything as it was, and the worker carries on.
The program's folder has to be writable by the user running it. Untick
*Update automatically when the server is updated* in its settings (or set
`MANGARR_WORKER_AUTO_UPDATE=false`) to update it by hand instead. The
update downloads straight from the project's GitHub releases, so the machine
needs to reach github.com.

**Workers in a container** never replace themselves (they have no access
to Docker, and shouldn't): the worker only logs that the server is newer,
and keeps working. Update its image the way you update the server's, for
example from cron on the machine that runs it:

```sh
docker compose pull mangarr-worker && docker compose up -d mangarr-worker
```

or with an image-update tool that recreates containers when their tag moves.
Keep the worker on the same tag as the server (`latest`, or the same pinned
version) so both move together.

### Processing in a separate container

Upscaling and re-encoding are the heaviest things mangarr does, and in the
default setup (`MANGARR_MODE=integrated`, `MANGARR_PROCESSING=local`) they
run inside the server: a GPU driver crash or a huge page that runs the
container out of memory takes the web UI down with it. To keep the server
light, move all of it into a worker next to it:

1. System → Workers → **Add worker**, with the **Encode** role (and
   **Upscale** when profiles upscale: the worker upscales with its own engine,
   so it needs `/dev/dri` instead of the server).
2. Start the server with `MANGARR_MODE=server` (no built-in upscaler, even one
   set up earlier) and `MANGARR_PROCESSING=workers` (the processing stage is
   handed to a worker instead of running here).
3. Add the worker to the same compose file, sharing the data folder:
   ```yaml
   mangarr-worker:
     image: ghcr.io/asion001/mangarr:latest
     user: "1000:1000"                    # the same user as the server
     environment:
       MANGARR_MODE: worker
       MANGARR_SERVER_URL: http://mangarr:8787
       MANGARR_WORKER_KEY: ${MANGARR_WORKER_KEY}
       MANGARR_WORKER_ROLES: encode,upscale
       MANGARR_WORKER_SHARED_STORAGE: "true"
     volumes:
       - ./mangarr:/config                # the server's data folder, same path
     devices:
       - /dev/dri:/dev/dri                # only for upscaling
     mem_limit: 4g                        # a crash here restarts the worker only
     restart: unless-stopped
   ```

Instead of `MANGARR_PROCESSING=workers` you can switch the **This server**
row off in System → Workers (the same as *Concurrent tasks* `-1`). That keeps
all work off the server without a restart: downloads wait for a worker with
the download role, and processing goes to one with the encode role. Only
sources whose pages can't be fetched by a worker then stop downloading.

While no such worker is online, new chapters are imported unprocessed (the
health page says why) and processed once it is back; reprocessing jobs wait
and retry. Previews on the profile page still run on the server.

## 13. Moving and renaming

- **Move a series** to another root folder or folder name: Edit on the series
  page, or select several on the Series page (*Select* → *Move…*). Files move
  in the background (copied, verified and then deleted when the destination is
  on another disk); downloads for that series wait meanwhile. Turn off *Move
  the files* when you already moved them by hand.
- **Move a whole root folder**: Settings → Media management → the folder icon
  on a root folder. Update library server path mappings afterwards.
- **Rename files** after changing the naming format: series page → *Rename
  files* (or several at once from the Series page), with a preview. Folders can
  follow title changes (Media management → *Rename a series folder when its
  title changes*).
- **Read progress**: moving files to another Komga/Kavita library (or renaming
  them) can reset progress there. mangarr keeps each reader's progress and
  writes it back once the server has scanned the new files (readers need linked
  accounts, see section 10). It never lowers progress on the server.

## 14. Importing from Mihon, Tachiyomi, Suwayomi or Aidoku

Import library → upload a backup:

- **Mihon, Tachiyomi and forks (J2K, SY, …), Suwayomi**: `.tachibk` or
  `.proto.gz` (Mihon: More → Backup and restore → Create backup; Suwayomi:
  Settings → Backup). Old Tachiyomi JSON backups aren't supported: restore
  them in Mihon and make a new backup.
- **Aidoku**: `.aib` (Settings → Backups).

Nothing is added until you start the import. mangarr first matches every
manga in the background:

- **Source**: Mihon/Suwayomi source ids are the same as Keiyoushi's, so a
  manga maps exactly to its catalog. When the catalog's extension isn't
  installed, *Install extensions* installs the missing ones and matches those
  manga again. Aidoku sources are matched by name; MangaDex links are converted
  directly, others are checked at the catalog or found by title. Anything
  unsure is marked *Needs review* with the best suggestion: *Accept* it or
  *Pick source* (the usual search).
- **Metadata**: AniList from the backup's tracker (MAL ids are converted
  through AniList), otherwise a confident title match. You can pick or remove
  it per manga.
- **Already in the library**: the source is added to the existing series and
  read chapters are merged. Manga that appear twice (the same series at two
  sources) become one series with both sources.

Options (per import): root folder and profile (overridable per category),
categories as tags, only library manga (not history), and **monitoring from
the first unread chapter**, so read chapters aren't downloaded again. Read
chapters are imported for a reader (a new "Mihon backup" reader by default;
pick your own to let cleanup use them). With *Mark them read in
Komga/Kavita*, chapters you read that get downloaded later are marked read on
your library server too. Scanlators you excluded in the app stay blocked for
that series (Edit series → *Blocked scanlators*).

Importing fetches each series' chapter list from its source, with the usual
request throttling, so a library of hundreds of series takes a while; the page
shows progress and can be left. Running an import again only picks up
entries that aren't imported yet (and retries failed ones).

## 15. PostgreSQL (optional)

mangarr starts on SQLite. To move to PostgreSQL (13 or newer), create an
empty database and a user for mangarr, then open **System → Database**:
enter the server's details, **Test connection**, then **Move data and
switch**. mangarr pauses downloads and tasks, copies everything (usually
seconds), and restarts on PostgreSQL. The address is kept in
`/config/database.dsn`. The SQLite file stays in `/config`; **Move to SQLite**
on the same page copies the data back.

If you set `MANGARR_DB` yourself, the page still copies the data, and then
tells you to change the variable and restart the container.

## 16. Logs, caches and bug reports

- **Logs** are written to `/config/logs/mangarr.txt` (rotated at 5 MB, 5 files
  kept; `MANGARR_LOG_DIR=off` disables them). System → Logs downloads them as a
  zip. API keys, passwords, tokens and module secrets are masked in the page,
  in copies and in downloads.
- **Download diagnostics** (System → Status) bundles logs, status, health,
  modules, settings and the queue for a bug report, masked the same way.
  Series titles and folder names are included, so look through it before
  posting it publicly.
- **Image cache**: thumbnails and covers are stored as 768px JPEGs (sources
  often send multi-megapixel originals, which also upset iOS Safari). The
  cache stays under Settings → General → *image cache limit* (512 MB by
  default) by removing the oldest images; System → Status shows its size and
  can *Compact* or clear it.

## 17. Backups & upgrades

Daily backups go to `/config/backups` (System → Backups). They hold the
whole database as a SQLite file on both SQLite and PostgreSQL installs, so a
backup restores into either, also on another install: **Add a backup file**,
then **Restore** (mangarr replaces its data and restarts; your library files
aren't touched). Suwayomi's own data is disposable: mangarr keeps the
real identity of every series (`sourceId` + URL) and re-links if Suwayomi's
database is lost. When upgrading Suwayomi, mangarr shows a health warning if
the version differs from the tested one.
