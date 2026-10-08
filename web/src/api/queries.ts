import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "./client";
import { useToast } from "../lib/toast";

export const useAuthStatus = () => useQuery({ queryKey: ["auth"], queryFn: () => unwrap(api.GET("/api/v1/auth/status")), staleTime: 60_000 });

export const useSeriesList = () => useQuery({ queryKey: ["series"], queryFn: () => unwrap(api.GET("/api/v1/series")) });

export type SeriesSearchQuery = {
  q?: string;
  filter?: "all" | "following" | "monitored" | "missing" | "ongoing" | "completed" | "unread" | "reading";
  sort?: "title" | "added" | "latest" | "missing" | "size" | "read";
  rootFolderId?: number;
  language?: string;
  genre?: string;
  page?: number;
  pageSize?: number;
};

export const useSeriesSearch = (query: SeriesSearchQuery) =>
  useQuery({
    queryKey: ["series", "search", query],
    queryFn: () => unwrap(api.GET("/api/v1/series/search", { params: { query } })),
    placeholderData: (previous) => previous,
  });

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

export const useHealth = (enabled = true) => useQuery({ queryKey: ["health"], queryFn: () => unwrap(api.GET("/api/v1/health")), enabled });
export const useCommands = () => useQuery({ queryKey: ["commands"], queryFn: () => unwrap(api.GET("/api/v1/commands", { params: { query: { limit: 50 } } })) });
export const useTasks = () => useQuery({ queryKey: ["tasks"], queryFn: () => unwrap(api.GET("/api/v1/system/tasks")) });
export const useReaders = () => useQuery({ queryKey: ["readers"], queryFn: () => unwrap(api.GET("/api/v1/readers")) });
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
