import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type S } from "../../api/client";
import { t } from "../../lib/i18n/core";
import { useToast } from "../../lib/toast";

export type Recycled = S["RecycledResource"];
export type ChapterFile = S["ChapterFile"];
type Tone = "default" | "ok" | "warn" | "err" | "info" | "accent";

/** Why a file is in the recycle bin, in the order the filter chips show them. */
export const REASONS = ["reprocessed", "upgraded", "cleaned", "deleted", "series_deleted", "restored_over", "unknown"] as const;

export function reasonLabel(reason: string): string {
  switch (reason) {
    case "reprocessed":
      return t("Reprocessed");
    case "upgraded":
      return t("Replaced by upgrade");
    case "cleaned":
      return t("Cleaned up");
    case "deleted":
      return t("Deleted");
    case "series_deleted":
      return t("Series deleted");
    case "restored_over":
      return t("Replaced by restore");
    default:
      return t("Found in folder");
  }
}

export function reasonTone(reason: string): Tone {
  if (reason === "reprocessed") return "info";
  if (reason === "upgraded") return "ok";
  if (reason === "series_deleted" || reason === "deleted") return "err";
  if (reason === "restored_over") return "accent";
  return "default";
}

/** processingText says what processing a chapter file went through. */
export function processingText(f?: ChapterFile | null): string {
  if (!f) return "—";
  const parts: string[] = [];
  if (f.upscaled) parts.push(f.upscaleModel ? t("Upscaled ({model})", { model: f.upscaleModel }) : t("Upscaled"));
  if (f.format === "avif" || f.format === "jxl") parts.push(f.format.toUpperCase());
  if (parts.length === 0) return t("As downloaded");
  return parts.join(" · ");
}

/** sourceText names where a file came from: "MangaDex · Ashen Scans". */
export const sourceText = (f?: ChapterFile | null) => (f ? [f.sourceName, f.scanlator].filter(Boolean).join(" · ") || "—" : "—");

/** chapterText names a recycled file's chapter: "Ch. 44", or the whole series folder. */
export function chapterText(r: Recycled): string {
  if (r.kind === "folder") return t("Whole series folder");
  if (r.chapter) return t("Ch. {number}", { number: r.chapter });
  return t("Unknown chapter");
}

export const fileName = (r: Recycled) => r.originalRelativePath.split("/").pop() ?? r.originalRelativePath;

/** daysLeft is how many whole days remain before a purge time (0 = today or overdue). */
export function daysLeft(purgeAt?: string | null, now = Date.now()): number | null {
  if (!purgeAt) return null;
  return Math.max(0, Math.ceil((Date.parse(purgeAt) - now) / 86_400_000));
}

type Result = S["RecycleResult"];

/** useRecycleActions restores, reprocesses and deletes recycled files, reporting per-file failures. */
export function useRecycleActions() {
  const qc = useQueryClient();
  const toast = useToast();
  const report = (results: Result[], done: string) => {
    const failed = results.filter((r) => r.error);
    if (failed.length === 0) toast.success(done);
    else toast.error(t("{failed} of {count} failed: {error}", { failed: failed.length, count: results.length, error: failed[0].error ?? "" }));
  };
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["recycle-bin"] });
    qc.invalidateQueries({ queryKey: ["chapters"] });
    qc.invalidateQueries({ queryKey: ["series"] });
  };
  const restore = useMutation({
    mutationFn: (ids: number[]) => unwrap(api.POST("/api/v1/recycle-bin/restore", { body: { ids } })),
    onSuccess: (r) => report(r, t("Restored")),
    onError: (e) => toast.fromError(e, t("Could not restore")),
    onSettled: refresh,
  });
  const remove = useMutation({
    mutationFn: (v: { ids?: number[]; all?: boolean }) =>
      unwrap(api.DELETE("/api/v1/recycle-bin", v.all ? { params: { query: { all: true } } } : { body: { ids: v.ids ?? [] } })),
    onSuccess: (r) => report(r, t("Deleted from the recycle bin")),
    onError: (e) => toast.fromError(e, t("Could not delete")),
    onSettled: refresh,
  });
  const reprocess = useMutation({
    mutationFn: (body: S["RecycleRun"]) => unwrap(api.POST("/api/v1/recycle-bin/reprocess", { body })),
    onSuccess: (r) => report(r, t("Queued")),
    onError: (e) => toast.fromError(e, t("Could not queue")),
    onSettled: () => {
      refresh();
      qc.invalidateQueries({ queryKey: ["queue"] });
    },
  });
  return { restore, remove, reprocess };
}
