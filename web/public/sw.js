// Mangarr's service worker: opens the app without the server, shows what
// was last loaded (library, title pages, covers), answers the reader from
// chapters saved on this device (src/lib/offline), and queues reading
// progress until the server is back, so a server restart or a dropped
// network doesn't stop reading.
//
// Kept as plain JavaScript in public/ so it is served as is next to
// index.html; the tests load this file and call its functions directly.

const SHELL = "mangarr-shell-v1";
/** The last answer to each API request, shown while the server is away. */
const REMEMBERED = "mangarr-api-v1";
/** Progress and reading time sent while the server was away, oldest first. */
const OUTBOX = "mangarr-outbox-v1";
/** How long a request may take before a remembered answer is shown instead. */
const PATIENCE = 4000;
/** Answers bigger than this (file downloads) aren't remembered. */
const REMEMBER_MAX = 5e6;
/** A saved chapter's cache; src/lib/offline/store.ts writes it. */
const CHAPTER_PREFIX = "mangarr-chapter-";
/** The width a saved page was stored at, on the cached response. */
const WIDTH_HEADER = "x-mangarr-width";

/** route sorts a request path (below the app's base) into what answers it. */
function route(path) {
  let m = path.match(/^api\/v1\/read\/chapters\/(\d+)\/pages\/(\d+)$/);
  if (m) return { kind: "page", chapter: m[1] };
  m = path.match(/^api\/v1\/read\/chapters\/(\d+)(\/bounds|\/pages\/\d+\/bounds)?$/);
  if (m) return { kind: m[2] ? "bounds" : "chapter", chapter: m[1] };
  if (/^api\/v1\/read\/chapters\/\d+\/(progress|time)$/.test(path)) return { kind: "outbox" };
  // live events, signed page links and chapter files go straight to the server
  if (path === "api/v1/events" || path.startsWith("api/v1/img/p/") || /\/file$/.test(path)) return { kind: "network" };
  if (path.startsWith("api/")) return { kind: "remember" };
  if (path.startsWith("assets/")) return { kind: "asset" };
  return { kind: "shell" };
}

/** fits: a saved page stored at width `have` can stand in for a request at `want` (0: the full scan). */
function fits(have, want) {
  if (!want) return false;
  return have === 0 || have >= want;
}

async function chapterCache(id) {
  const name = CHAPTER_PREFIX + id;
  return (await caches.has(name)) ? caches.open(name) : null;
}

/** answered is a response the server actually gave (a restarting server's proxy answers 502). */
const answered = (r) => r && r.status < 500;

/** Marks an answer this worker made itself, so the pages don't take it for the server being back (src/lib/connection.ts). Must match OFFLINE_HEADER there. */
const OFFLINE_HEADER = "x-mangarr-offline";
function mine(r) {
  const headers = new Headers(r.headers);
  headers.set(OFFLINE_HEADER, "1");
  return new Response(r.body, { status: r.status, statusText: r.statusText, headers });
}

/** Whether the server answered last time; the pages show an offline pill while it doesn't (src/lib/offline/connection.ts). */
let reachable = true;
function heard(ok) {
  if (ok && queued) void flush();
  if (ok === reachable || typeof self === "undefined" || !self.clients) return;
  reachable = ok;
  void self.clients.matchAll().then((list) => list.forEach((c) => c.postMessage({ type: "mangarr:reachable", reachable: ok })));
}

async function networkOr(request, fallback) {
  try {
    const r = await fetch(request);
    heard(answered(r));
    if (answered(r)) return r;
    const f = await fallback();
    return f ? mine(f) : r;
  } catch (err) {
    heard(false);
    const r = await fallback();
    if (r) return mine(r);
    throw err;
  }
}

async function page(request, chapter) {
  const cache = await chapterCache(chapter);
  const saved = cache && (await cache.match(request, { ignoreSearch: true }));
  if (!saved) return networkOr(request, async () => undefined);
  const want = Number(new URL(request.url).searchParams.get("w") || 0);
  if (fits(Number(saved.headers.get(WIDTH_HEADER) || 0), want)) return mine(saved);
  return networkOr(request, async () => saved);
}

async function chapterData(request, chapter, cacheFirst) {
  const cache = await chapterCache(chapter);
  const saved = cache && (await cache.match(request, { ignoreSearch: true }));
  if (saved && cacheFirst) return mine(saved);
  return networkOr(request, async () => saved && withQueuedProgress(saved, chapter));
}

/** withQueuedProgress puts the progress still waiting in the outbox into a saved chapter's details, so it opens where you stopped. */
async function withQueuedProgress(saved, chapter) {
  let page = 0;
  for (const item of await outboxItems()) {
    if (item.url.endsWith(`/read/chapters/${chapter}/progress`)) page = JSON.parse(item.body || "{}").page || page;
  }
  if (!page) return saved;
  const ch = await saved.json();
  if (page > (ch.progress?.page || 0)) ch.progress = { ...ch.progress, page, completed: page >= (ch.pages?.length || Infinity) };
  return new Response(JSON.stringify(ch), { headers: { "Content-Type": "application/json" } });
}

/** keep: an answer worth remembering (not a download). */
function keep(r) {
  if (r.status !== 200) return false;
  const type = r.headers.get("content-type") || "";
  if (!type.includes("json") && !type.startsWith("image/") && !type.includes("text/html")) return false;
  return Number(r.headers.get("content-length") || 0) <= REMEMBER_MAX;
}

/** fresh asks the server and keeps a good answer in `cache`, falling back to the kept one while the server is away or slow. */
async function fresh(request, cache, key = request) {
  const hit = cache.match(key);
  const net = fetch(request).then(
    (r) => {
      heard(answered(r));
      if (keep(r)) void cache.put(key, r.clone());
      return r;
    },
    (err) => {
      heard(false);
      throw err;
    },
  );
  const slow = new Promise((resolve) => setTimeout(resolve, PATIENCE, "slow"));
  try {
    const r = await Promise.race([net, hit.then((h) => (h ? slow : net))]);
    if (r !== "slow" && answered(r)) return r;
    const h = await hit;
    return h ? mine(h) : r;
  } catch (err) {
    const h = await hit;
    if (h) return mine(h);
    throw err;
  }
}

async function remember(request) {
  const cache = await caches.open(REMEMBERED);
  const r = await fresh(request, cache);
  // signed out: what was remembered for that account goes too
  if (r.status === 200 && new URL(request.url).pathname.endsWith("/auth/status")) {
    r.clone().json().then((a) => a && a.authenticated === false && caches.delete(REMEMBERED), () => undefined);
  }
  return r;
}

let queued = true; // checked once at start: an earlier run may have left some
let flushing = null;

async function outboxItems() {
  if (!(await caches.has(OUTBOX))) return [];
  const cache = await caches.open(OUTBOX);
  const keys = await cache.keys();
  const items = [];
  for (const k of keys) {
    const r = await cache.match(k);
    items.push({ key: k, url: r.headers.get("x-url"), method: r.headers.get("x-method"), type: r.headers.get("content-type"), body: await r.text() });
  }
  return items.sort((a, b) => (a.key.url < b.key.url ? -1 : 1));
}

/** flush sends what the outbox holds, oldest first; it stops at the first that can't go yet. */
function flush() {
  flushing ??= (async () => {
    const cache = await caches.open(OUTBOX);
    for (const item of await outboxItems()) {
      let r;
      try {
        r = await fetch(new Request(item.url, { method: item.method, body: item.body, headers: { "Content-Type": item.type || "application/json" }, credentials: "same-origin" }));
      } catch {
        return;
      }
      // not signed in yet, or still restarting: wait for the next chance
      if (r.status === 401 || !answered(r)) return;
      await cache.delete(item.key);
    }
    queued = false;
  })().finally(() => (flushing = null));
  return flushing;
}

let seq = 0;
/** outbox sends progress or reading time, or keeps it to send later when the server doesn't answer. */
async function outbox(request) {
  const body = await request.clone().text();
  try {
    const r = await fetch(request);
    heard(answered(r));
    if (answered(r)) return r;
  } catch {
    heard(false);
  }
  const cache = await caches.open(OUTBOX);
  const key = `${new URL(request.url).origin}/__outbox/${String(Date.now()).padStart(15, "0")}-${String(seq++).padStart(6, "0")}`;
  await cache.put(key, new Response(body, { headers: { "x-url": request.url, "x-method": request.method, "content-type": request.headers.get("content-type") || "application/json" } }));
  queued = true;
  return new Response(JSON.stringify({ queued: true }), { status: 202, headers: { "Content-Type": "application/json", [OFFLINE_HEADER]: "1" } });
}

async function shell(request) {
  // every page of the app is index.html; the router takes it from there
  return fresh(request, await caches.open(SHELL), "./index.html");
}

async function asset(request) {
  const cache = await caches.open(SHELL);
  const hit = await cache.match(request);
  if (hit) return hit;
  const r = await fetch(request);
  if (r.ok) void cache.put(request, r.clone());
  return r;
}

/** handle answers one fetch, or returns undefined to leave it to the browser. */
function handle(request, scope) {
  if (!request.url.startsWith(scope)) return undefined;
  const path = new URL(request.url).pathname.slice(new URL(scope).pathname.length);
  const r = route(path);
  if (r.kind === "outbox" && request.method !== "GET") return outbox(request);
  if (request.method !== "GET") return undefined;
  // the pages asking whether the server is back (src/lib/offline/connection.ts): only the server may answer
  if (new URL(request.url).searchParams.has("ping")) return networkOr(request, async () => undefined);
  if (r.kind === "page") return page(request, r.chapter);
  if (r.kind === "bounds") return chapterData(request, r.chapter, true);
  if (r.kind === "chapter") return chapterData(request, r.chapter, false);
  if (r.kind === "asset") return asset(request);
  if (r.kind === "remember") return remember(request);
  if (r.kind === "shell" && request.mode === "navigate") return shell(request);
  return undefined;
}

if (typeof self !== "undefined" && typeof self.registration !== "undefined") {
  self.addEventListener("install", (event) => {
    // the app's own files, so it opens offline (the build lists them, vite.config.ts)
    event.waitUntil(
      (async () => {
        const cache = await caches.open(SHELL);
        const list = await fetch("offline-manifest.json").then((r) => (r.ok ? r.json() : [])).catch(() => []);
        await cache.addAll(["./index.html", ...list]).catch(() => undefined);
        await self.skipWaiting();
      })(),
    );
  });
  self.addEventListener("activate", (event) => {
    event.waitUntil(
      (async () => {
        // an older build's files are of no use once this one runs
        const cache = await caches.open(SHELL);
        const list = new Set(await fetch("offline-manifest.json").then((r) => (r.ok ? r.json() : null)).catch(() => null));
        if (list.size) {
          for (const req of await cache.keys()) {
            const path = new URL(req.url).pathname.slice(new URL(self.registration.scope).pathname.length);
            if (path.startsWith("assets/") && !list.has(path)) await cache.delete(req);
          }
        }
        await self.clients.claim();
        void flush();
      })(),
    );
  });
  self.addEventListener("fetch", (event) => {
    const r = handle(event.request, self.registration.scope);
    if (r) event.respondWith(r);
  });
}
