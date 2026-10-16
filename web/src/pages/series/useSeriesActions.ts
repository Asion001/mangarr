import { useCallback, useMemo } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { t } from "../../lib/i18n/core";
import { useToast } from "../../lib/toast";

/**
 * useSeriesActions is what the title page does to a series: monitoring it,
 * following it, grouping its edition with another title, and deleting it.
 * Failures show as a toast; the actions that change the page say whether
 * they worked, so the page can close its dialog.
 */
export function useSeriesActions(id: number) {
  const qc = useQueryClient();
  const toast = useToast();

  const setMonitored = useCallback(async (monitored: boolean) => {
    try {
      await unwrap(api.PUT("/api/v1/series/{id}", { params: { path: { id } }, body: { monitored } }));
      qc.invalidateQueries({ queryKey: ["series"] });
    } catch (e) {
      toast.fromError(e);
    }
  }, [id, qc, toast]);

  const setFollowing = useCallback(async (follow: boolean) => {
    try {
      const path = { params: { path: { id } } };
      await unwrap(follow ? api.PUT("/api/v1/series/{id}/follow", path) : api.DELETE("/api/v1/series/{id}/follow", path));
      qc.invalidateQueries({ queryKey: ["series"] });
    } catch (e) {
      toast.fromError(e);
    }
  }, [id, qc, toast]);

  const remove = useCallback(async (title: string, deleteFiles: boolean) => {
    try {
      await unwrap(api.DELETE("/api/v1/series/{id}", { params: { path: { id }, query: { deleteFiles } } }));
      toast.success(`${title} deleted`);
      qc.invalidateQueries({ queryKey: ["series"] });
      return true;
    } catch (e) {
      toast.fromError(e);
      return false;
    }
  }, [id, qc, toast]);

  /** setWork groups the edition with a work, or separates it with 0. */
  const setWork = useCallback(async (workId: number) => {
    try {
      await unwrap(api.PUT("/api/v1/series/{id}/work", { params: { path: { id } }, body: { workId } }));
      toast.success(workId ? t("Language edition grouped") : t("Language edition separated"));
      await qc.invalidateQueries({ queryKey: ["series"] });
      return true;
    } catch (e) {
      toast.fromError(e);
      return false;
    }
  }, [id, qc, toast]);

  return useMemo(() => ({ setMonitored, setFollowing, remove, setWork }), [setMonitored, setFollowing, remove, setWork]);
}
