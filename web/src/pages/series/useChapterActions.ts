import { useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Chapter } from "../../api/client";
import { t } from "../../lib/i18n/core";
import { useToast } from "../../lib/toast";

export type QueueAction = "top" | "bottom" | "pause" | "resume" | "retry" | "remove";

/**
 * useChapterActions is what can be done to a series' chapters: monitoring,
 * searching, marking read, restoring, processing, deleting files and moving
 * their queue entries. Each action reports failures as a toast and refreshes
 * the series (and the queue, when it touched it).
 */
export function useChapterActions(seriesId: number) {
  const qc = useQueryClient();
  const toast = useToast();
  const refresh = useCallback(() => qc.invalidateQueries({ queryKey: ["series", seriesId] }), [qc, seriesId]);
  const refreshQueue = useCallback(() => {
    refresh();
    qc.invalidateQueries({ queryKey: ["queue"] });
  }, [qc, refresh]);

  const monitor = useCallback(async (ids: number[], monitored: boolean) => {
    try {
      await unwrap(api.PUT("/api/v1/chapters/monitor", { body: { chapterIds: ids, monitored } }));
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  }, [refresh, toast]);
  const search = useCallback(async (ids: number[]) => {
    try {
      await unwrap(api.POST("/api/v1/series/{id}/search", { params: { path: { id: seriesId } }, body: { chapterIds: ids } }));
      toast.info(`Searching ${ids.length} chapter${ids.length === 1 ? "" : "s"}`);
    } catch (e) {
      toast.fromError(e);
    }
  }, [seriesId, toast]);
  const restore = useCallback(async (c: Chapter) => {
    try {
      await unwrap(api.POST("/api/v1/chapters/{id}/restore", { params: { path: { id: c.id } } }));
      toast.info(`Restoring chapter ${c.number}`);
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  }, [refresh, toast]);
  const mark = useCallback(async (c: Chapter, read: boolean, scope: "chapter" | "previous" = "chapter") => {
    try {
      await unwrap(api.PUT("/api/v1/read/chapters/{id}/mark", { params: { path: { id: c.id } }, body: { read, scope } }));
      refresh();
      toast.success(scope === "previous"
        ? (read ? t("Earlier chapters marked read") : t("Earlier chapters marked unread"))
        : (read ? t("Chapter {number} marked read", { number: c.number }) : t("Chapter {number} marked unread", { number: c.number })));
    } catch (e) {
      toast.fromError(e);
    }
  }, [refresh, toast]);
  /** markMany marks several chapters read or unread, one toast for all of them. */
  const markMany = useCallback(async (chapters: Chapter[], read: boolean) => {
    try {
      for (const c of chapters) await unwrap(api.PUT("/api/v1/read/chapters/{id}/mark", { params: { path: { id: c.id } }, body: { read, scope: "chapter" } }));
      toast.success(read ? t("{count} chapters marked read", { count: chapters.length }) : t("{count} chapters marked unread", { count: chapters.length }));
    } catch (e) {
      toast.fromError(e);
    } finally {
      refresh();
    }
  }, [refresh, toast]);
  const queueAction = useCallback(async (jobID: number, action: "top" | "bottom" | "pause" | "resume") => {
    try {
      await unwrap(api.POST("/api/v1/queue/bulk", { body: { action, ids: [jobID] } }));
      refreshQueue();
    } catch (e) {
      toast.fromError(e);
    }
  }, [refreshQueue, toast]);
  const queueBulk = useCallback(async (action: QueueAction, jobIDs: number[]) => {
    if (jobIDs.length === 0) return;
    try {
      const result = await unwrap(api.POST("/api/v1/queue/bulk", { body: { action, ids: jobIDs } }));
      toast.success(`${result.affected} ${result.affected === 1 ? "queue entry" : "queue entries"} updated`);
      refreshQueue();
    } catch (e) {
      toast.fromError(e);
    }
  }, [refreshQueue, toast]);
  const process = useCallback(async (ids: number[]) => {
    try {
      // force: "again" includes chapters already processed with the current settings
      await unwrap(api.POST("/api/v1/commands", { body: { name: "ProcessExisting", body: { seriesId, chapterIds: ids, force: true } } }));
      toast.info(`Queued processing for ${ids.length} chapter${ids.length === 1 ? "" : "s"}`);
      refreshQueue();
    } catch (e) {
      toast.fromError(e);
    }
  }, [refreshQueue, seriesId, toast]);
  /** deleteFiles removes the chapters' files and says whether it worked. */
  const deleteFiles = useCallback(async (ids: number[]) => {
    try {
      const result = await unwrap(api.POST("/api/v1/chapters/delete", { body: { chapterIds: ids } }));
      toast.success(t("Deleted chapter files: {count}", { count: result.removed }));
      refresh();
      return true;
    } catch (e) {
      toast.fromError(e);
      return false;
    }
  }, [refresh, toast]);

  return useMemo(() => ({ monitor, search, restore, mark, markMany, queueAction, queueBulk, process, deleteFiles }), [monitor, search, restore, mark, markMany, queueAction, queueBulk, process, deleteFiles]);
}
