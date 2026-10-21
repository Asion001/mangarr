import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";
import { getLocale, t } from "../lib/i18n/core";
import { checkServer, serverAnswered, serverGone, setPingUrl } from "../lib/connection";

/** Path prefix when mangarr runs under a URL base (from <base href>). */
export const basePath = new URL(document.baseURI).pathname.replace(/\/$/, "");
setPingUrl(basePath + "/ping");

export class ApiError extends Error {
  status: number;
  /** unreachable: mangarr wasn't there to answer (restarting), not an answer of its own */
  unreachable: boolean;
  constructor(status: number, message: string, unreachable = false) {
    super(message);
    this.status = status;
    this.unreachable = unreachable;
  }
}

/**
 * proxyGap is a 502-504 from a proxy in front while mangarr is down. mangarr's
 * own errors (a source that's unavailable) come as JSON problems.
 */
export const proxyGap = (response: Response) =>
  response.status >= 502 && response.status <= 504 && !/json/.test(response.headers.get("Content-Type") ?? "");

const onUnauthorized: Middleware = {
  async onResponse({ response }) {
    if (response.status === 401 && !location.pathname.endsWith("/login")) {
      window.dispatchEvent(new CustomEvent("mangarr:unauthorized"));
    }
    return response;
  },
};

/**
 * While mangarr restarts nothing answers, or a proxy in front answers 502-504:
 * note it (pages then retry quietly) and give it a readable error.
 */
const restarts: Middleware = {
  onResponse({ response }) {
    // a proxy's 502-504 can also be a slow request timing out: only a /ping
    // that fails too means mangarr is gone
    if (proxyGap(response)) void checkServer();
    else serverAnswered();
    return response;
  },
  onError({ error }) {
    if ((error as Error)?.name === "AbortError") return;
    serverGone();
    return new ApiError(0, t("Can't reach mangarr right now"), true);
  },
};

/** The interface language picks which language edition a title shows in lists. */
const interfaceLanguage: Middleware = {
  onRequest({ request }) {
    request.headers.set("Accept-Language", getLocale());
    return request;
  },
};

export const api = createClient<paths>({ baseUrl: window.location.origin + basePath });
api.use(onUnauthorized, restarts, interfaceLanguage);

type ErrorBody = { title?: string; detail?: string; errors?: { message?: string; location?: string }[] } | undefined;

/** unwrap turns an openapi-fetch result into data or a thrown ApiError. */
export async function unwrap<T>(p: Promise<{ data?: T; error?: unknown; response: Response }>): Promise<T> {
  const { data, error, response } = await p;
  if (error !== undefined || !response.ok) {
    const e = error as ErrorBody;
    let msg = e?.detail || e?.title || response.statusText || "Request failed";
    if (e?.errors?.length) msg += ": " + e.errors.map((x) => x.message).filter(Boolean).join(", ");
    throw new ApiError(response.status, msg, proxyGap(response));
  }
  return data as T;
}

export type S = components["schemas"];
export type Series = S["SeriesResource"];
export type Chapter = S["ChapterResource"];
export type SeriesSource = S["SeriesSource"];
export type ModuleResource = S["ModuleResource"];
export type Implementation = S["ImplementationResource"];
export type ModuleField = S["ModuleField"];
export type Profile = S["Profile"];
export type RootFolder = S["RootFolderResource"];
export type Tag = S["Tag"];
export type Job = S["JobView"];
export type History = S["History"];
export type Command = S["Command"];
export type Reader = S["ReaderResource"];
export type SourceInfo = S["Catalog"];
export type Catalog = S["Catalog"];
export type ThrottleConfig = S["ThrottleConfig"];
export type SearchGroup = S["SearchResultGroup"];
export type SourceManga = S["SourceManga"];
export type LookupResult = S["LookupResult"];
export type HealthCheck = S["HealthCheck"];
export type Extension = S["SourceExtension"];

/** URL helper for image endpoints (proxied thumbnails, covers). */
export function apiUrl(path: string, params?: Record<string, string | number | undefined>) {
  const u = new URL(window.location.origin + basePath + "/" + path.replace(/^\//, ""));
  for (const [k, v] of Object.entries(params ?? {})) if (v !== undefined && v !== "") u.searchParams.set(k, String(v));
  return u.toString();
}

export type AddRequest = S["AddRequest"];
export type AddEditionsRequest = S["AddEditionsRequest"];
export type EditionOptions = S["EditionOptions"];
export type UpdateRequest = S["UpdateRequest"];
