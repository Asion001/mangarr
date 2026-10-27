import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "./client";
import type { paths } from "./schema";
import { accountKey, forgetCopy, readCopy, saveCopy } from "./persist";
import { unreachable } from "../lib/connection";
import { useToast } from "../lib/toast";

const fetchAuthStatus = () => unwrap(api.GET("/api/v1/auth/status"));

/**
 * useAuthStatus is who is signed in. The last signed-in answer is kept in
 * this browser, so with no server at all (opened offline, with nothing the
 * service worker kept) the app still opens what it has saved instead of a
 * spinner; the server's answer replaces it once it comes.
 */
export const useAuthStatus = () =>
  useQuery({
    queryKey: ["auth"],
    queryFn: async () => {
      try {
        const status = await fetchAuthStatus();
        if (status.authenticated) saveCopy("auth", "", status);
        else forgetCopy("auth");
        return status;
      } catch (err) {
        const kept = unreachable(err) ? readCopy<Awaited<ReturnType<typeof fetchAuthStatus>>>("auth", "") : undefined;
        if (kept) return kept;
        throw err;
      }
    },
    staleTime: 60_000,
  });

const fetchSeriesList = () => unwrap(api.GET("/api/v1/series"));

/**
 * useSeriesList is the library. The last copy this browser saw shows at once
 * while the current one loads, so the library opens without waiting on the
 * server; the answer replaces it as soon as it comes.
 */
export const useSeriesList = () => {
  const { data: auth } = useAuthStatus();
  const name = auth ? `series:${accountKey(auth.account)}` : "";
  return useQuery({
    queryKey: ["series"],
    queryFn: async () => {
      const list = await fetchSeriesList();
      if (name) saveCopy(name, "", list);
      return list;
    },
    initialData: () => (name ? readCopy<Awaited<ReturnType<typeof fetchSeriesList>>>(name, "") : undefined),
    initialDataUpdatedAt: 0, // always stale: the real list is fetched right away
  });
};

export type SeriesSearchQuery = NonNullable<paths["/api/v1/series/search"]["get"]["parameters"]["query"]>;

const fetchSeriesPage = (query: SeriesSearchQuery) => unwrap(api.GET("/api/v1/series/search", { params: { query } }));

/** useSeriesSearch is a page of the library; the last page this browser saw for the same filters shows first. */
export const useSeriesSearch = (query: SeriesSearchQuery) => {
  const { data: auth } = useAuthStatus();
  const name = auth ? `series-search:${accountKey(auth.account)}` : "";
  const match = JSON.stringify(query);
  return useQuery({
    queryKey: ["series", "search", query],
    queryFn: async () => {
      const page = await fetchSeriesPage(query);
      if (name) saveCopy(name, match, page);
      return page;
    },
    initialData: () => (name ? readCopy<Awaited<ReturnType<typeof fetchSeriesPage>>>(name, match) : undefined),
    initialDataUpdatedAt: 0,
    placeholderData: (previous) => previous,
  });
};

export const useSeries = (id: number) =>
  useQuery({ queryKey: ["series", id], queryFn: () => unwrap(api.GET("/api/v1/series/{id}", { params: { path: { id } } })), enabled: id > 0 });

export const useChapters = (id: number) =>
  useQuery({
    queryKey: ["series", id, "chapters"],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/chapters", { params: { path: { id } } })),
    enabled: id > 0,
  });

export const useProfiles = () => useQuery({ queryKey: ["profiles"], queryFn: () => unwrap(api.GET("/api/v1/profiles")) });
export const useRootFolders = () => useQuery({ queryKey: ["rootfolders"], queryFn: () => unwrap(api.GET("/api/v1/rootfolders")) });
export const useTags = () => useQuery({ queryKey: ["tags"], queryFn: () => unwrap(api.GET("/api/v1/tags")) });

export const useModules = (kind?: string) =>
  useQuery({ queryKey: ["modules", kind ?? "all"], queryFn: () => unwrap(api.GET("/api/v1/modules", { params: { query: { kind } } })) });

export const useSchema = (kind?: string) =>
  useQuery({
    queryKey: ["modules", "schema", kind ?? "all"],
    queryFn: () => unwrap(api.GET("/api/v1/modules/schema", { params: { query: { kind } } })),
    staleTime: 5 * 60_000,
  });

export type QueueFilter = { status?: string[]; kind?: "" | "download" | "reprocess"; q?: string; seriesId?: number; includeDone?: boolean; page?: number; pageSize?: number };

export const useQueue = (f: QueueFilter = {}, enabled = true) =>
  useQuery({ queryKey: ["queue", f], queryFn: () => unwrap(api.GET("/api/v1/queue", { params: { query: f } })), placeholderData: (prev) => prev, enabled });

/** useUpdatesUnseen counts the updates of the last 30 days you haven't seen (for the menu). */
export const useUpdatesUnseen = (enabled = true) =>
  useQuery({ queryKey: ["updates", "unseen"], queryFn: () => unwrap(api.GET("/api/v1/updates/unseen")), staleTime: 60_000, enabled });

export const useHealth = (enabled = true) => useQuery({ queryKey: ["health"], queryFn: () => unwrap(api.GET("/api/v1/health")), enabled });
export const useCommands = () => useQuery({ queryKey: ["commands"], queryFn: () => unwrap(api.GET("/api/v1/commands", { params: { query: { limit: 50 } } })) });
export const useTasks = () => useQuery({ queryKey: ["tasks"], queryFn: () => unwrap(api.GET("/api/v1/system/tasks")) });
export const useUsers = () => useQuery({ queryKey: ["users"], queryFn: () => unwrap(api.GET("/api/v1/users")) });
/** useCatalogs lists every catalog with preferences and the catalogs generation. */
export const useCatalogs = () => useQuery({ queryKey: ["catalogs"], queryFn: () => unwrap(api.GET("/api/v1/catalogs")), staleTime: 30_000 });

export const useSources = () => useQuery({ queryKey: ["sources"], queryFn: () => unwrap(api.GET("/api/v1/sources")), staleTime: 60_000 });

/** usePushCommand queues a server command and toasts the result. */
export function usePushCommand() {
  const toast = useToast();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { name: string; body?: Record<string, unknown>; label?: string }) =>
      unwrap(api.POST("/api/v1/commands", { body: { name: v.name, body: v.body } })),
    onSuccess: (_d, v) => {
      toast.info(v.label ?? `${v.name} queued`);
      qc.invalidateQueries({ queryKey: ["commands"] });
    },
    onError: (e) => toast.fromError(e, "Command failed"),
  });
}

const commandEnded = new Set(["completed", "failed", "orphaned"]);

/**
 * followCommand waits for a queued command to end and toasts how it went. It
 * outlives the component that queued it, since dialogs close right away.
 */
export async function followCommand(id: number, title: string, toast: ReturnType<typeof useToast>, onDone?: () => void) {
  const deadline = Date.now() + 30 * 60_000;
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 2000));
    const c = await unwrap(api.GET("/api/v1/commands/{id}", { params: { path: { id } } })).catch(() => null);
    if (!c || !commandEnded.has(c.status)) continue;
    onDone?.();
    if (c.status === "completed") toast.success(title, c.message || undefined);
    else toast.error(title, c.error || c.message || c.status);
    return;
  }
}

/** usePendingRequests counts requests waiting for a manager. */
export const usePendingRequests = (enabled: boolean) =>
  useQuery({ queryKey: ["requests", "count"], queryFn: () => unwrap(api.GET("/api/v1/requests/count")), enabled, staleTime: 60_000 });
