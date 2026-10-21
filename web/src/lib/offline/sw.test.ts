import { describe, expect, test } from "vitest";
import src from "../../../public/sw.js?raw";

const scope = "https://m.example/base/";
type Entry = Map<string, Response>;

/** load runs public/sw.js against fake caches and a fake network. */
function load(network: (req: Request) => Promise<Response>, stored: Record<string, Entry> = {}) {
  const caches = {
    has: async (name: string) => name in stored,
    open: async (name: string) => {
      stored[name] ??= new Map();
      const entry = stored[name];
      const key = (r: Request | string, ignoreSearch?: boolean) => {
        const u = new URL(typeof r === "string" ? new URL(r, scope).href : r.url);
        if (ignoreSearch) u.search = "";
        return u.href;
      };
      return {
        match: async (r: Request | string, o?: { ignoreSearch?: boolean }) => {
          const want = key(r, o?.ignoreSearch);
          for (const [k, v] of entry) if ((o?.ignoreSearch ? key(k, true) : k) === want) return v.clone();
          return undefined;
        },
        put: async (r: Request | string, v: Response) => void entry.set(key(r), v),
        delete: async (r: Request | string) => entry.delete(typeof r === "string" ? key(r) : r.url),
        keys: async () => [...entry.keys()].map((k) => new Request(k)),
      };
    },
  };
  return new Function("caches", "fetch", "self", `${src}; return { route, fits, handle, flush };`)(caches, network, undefined) as {
    route: (p: string) => { kind: string; chapter?: string };
    flush: () => Promise<void>;
    fits: (have: number, want: number) => boolean;
    handle: (r: Request, scope: string) => Promise<Response> | undefined;
  };
}

const down = async () => {
  throw new TypeError("Failed to fetch");
};
const saved = (body: string, width?: number) =>
  new Response(body, { headers: width === undefined ? {} : { "x-mangarr-width": String(width) } });

describe("service worker", () => {
  test("sorts requests", () => {
    const { route } = load(down);
    expect(route("api/v1/read/chapters/7/pages/3")).toEqual({ kind: "page", chapter: "7" });
    expect(route("api/v1/read/chapters/7")).toEqual({ kind: "chapter", chapter: "7" });
    expect(route("api/v1/read/chapters/7/bounds")).toEqual({ kind: "bounds", chapter: "7" });
    expect(route("api/v1/read/chapters/7/pages/3/bounds")).toEqual({ kind: "bounds", chapter: "7" });
    expect(route("api/v1/series").kind).toBe("remember");
    expect(route("api/v1/events").kind).toBe("network");
    expect(route("api/v1/read/chapters/7/file").kind).toBe("network");
    expect(route("api/v1/read/chapters/7/progress").kind).toBe("outbox");
    expect(route("assets/index-abc.js").kind).toBe("asset");
    expect(route("read/7").kind).toBe("shell");
  });

  test("a saved page stands in for any width up to the one saved", () => {
    const { fits } = load(down);
    expect(fits(1080, 720)).toBe(true);
    expect(fits(1080, 1080)).toBe(true);
    expect(fits(1080, 1440)).toBe(false);
    expect(fits(1080, 0)).toBe(false);
  });

  test("answers a saved page without asking the server", async () => {
    let asked = 0;
    const { handle } = load(async () => (asked++, new Response("net")), {
      "mangarr-chapter-7": new Map([[`${scope}api/v1/read/chapters/7/pages/3`, saved("page", 1080)]]),
    });
    const r = await handle(new Request(`${scope}api/v1/read/chapters/7/pages/3?w=720`), scope)!;
    expect(await r.text()).toBe("page");
    expect(asked).toBe(0);
  });

  test("falls back to a smaller saved page when the server is down", async () => {
    const { handle } = load(down, {
      "mangarr-chapter-7": new Map([[`${scope}api/v1/read/chapters/7/pages/3`, saved("small", 720)]]),
    });
    const r = await handle(new Request(`${scope}api/v1/read/chapters/7/pages/3?w=1440`), scope)!;
    expect(await r.text()).toBe("small");
  });

  test("chapter details come from the server first, the saved copy while it restarts", async () => {
    const stored = { "mangarr-chapter-7": new Map([[`${scope}api/v1/read/chapters/7`, saved('{"id":7}')]]) };
    const up = load(async () => new Response('{"id":7,"fresh":true}'), stored);
    expect(await (await up.handle(new Request(`${scope}api/v1/read/chapters/7`), scope)!).text()).toContain("fresh");
    const restarting = load(async () => new Response("Bad Gateway", { status: 502 }), stored);
    expect(await (await restarting.handle(new Request(`${scope}api/v1/read/chapters/7`), scope)!).text()).toBe('{"id":7}');
  });

  test("an unsaved page goes to the server and fails as before", async () => {
    const { handle } = load(down);
    await expect(handle(new Request(`${scope}api/v1/read/chapters/8/pages/1`), scope)!).rejects.toThrow();
  });

  test("leaves other requests alone", () => {
    const { handle } = load(down);
    expect(handle(new Request(`${scope}api/v1/series`, { method: "POST" }), scope)).toBeUndefined();
    expect(handle(new Request("https://cdn.example/x.png"), scope)).toBeUndefined();
  });

  test("shows the last answer while the server is away", async () => {
    const stored: Record<string, Entry> = {};
    const up = load(async () => new Response('[{"id":1}]', { headers: { "content-type": "application/json" } }), stored);
    await (await up.handle(new Request(`${scope}api/v1/series`), scope)!).text();
    await new Promise((r) => setTimeout(r));
    const restarting = load(down, stored);
    const kept = await restarting.handle(new Request(`${scope}api/v1/series`), scope)!;
    // marked as the worker's own, so the pages don't take it for the server being back
    expect(kept.headers.get("x-mangarr-offline")).toBe("1");
    expect(await kept.text()).toBe('[{"id":1}]');
    await expect(restarting.handle(new Request(`${scope}api/v1/series/9`), scope)!).rejects.toThrow();
    // asking whether the server is back never gets a remembered answer
    await (await up.handle(new Request(`${scope}api/v1/auth/status?ping=1`), scope)!).text();
    await expect(restarting.handle(new Request(`${scope}api/v1/auth/status?ping=1`), scope)!).rejects.toThrow();
  });

  test("keeps progress while the server is away and sends it later", async () => {
    const stored: Record<string, Entry> = {
      "mangarr-chapter-7": new Map([[`${scope}api/v1/read/chapters/7`, saved('{"id":7,"pages":[1,2,3,4],"progress":{"page":1}}')]]),
    };
    const offline = load(down, stored);
    const put = (page: number) => new Request(`${scope}api/v1/read/chapters/7/progress`, { method: "PUT", body: JSON.stringify({ page }), headers: { "Content-Type": "application/json" } });
    const queued = await offline.handle(put(2), scope)!;
    expect(queued.status).toBe(202);
    expect(queued.headers.get("x-mangarr-offline")).toBe("1");
    expect((await offline.handle(put(3), scope)!).status).toBe(202);
    const ch = await (await offline.handle(new Request(`${scope}api/v1/read/chapters/7`), scope)!).json();
    expect(ch.progress.page).toBe(3);

    const sent: string[] = [];
    const back = load(async (req) => (sent.push(await req.text()), new Response("{}")), stored);
    await back.flush();
    expect(sent).toEqual(['{"page":2}', '{"page":3}']);
    expect(stored["mangarr-outbox-v1"].size).toBe(0);
  });
});
