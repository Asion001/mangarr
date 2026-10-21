import { useEffect, useId, useState, type ReactNode } from "react";
import { Check, MoreHorizontal, Search, Smartphone, Trash2 } from "lucide-react";
import clsx from "clsx";
import type { Chapter } from "../../api/client";
import { t } from "../../lib/i18n/core";
import { useToast } from "../../lib/toast";
import { queueSave, removeChapter, useSavedChapters } from "../../lib/offline/store";
import { useOfflineReady } from "../../components/OnDevice";
import { deletableFile, readable } from "./ChapterRows";
import type { QueueAction } from "./useChapterActions";

type Actions = {
  monitor: (ids: number[], monitored: boolean) => unknown;
  search: (ids: number[]) => unknown;
  process: (ids: number[]) => unknown;
  markMany: (chapters: Chapter[], read: boolean) => unknown;
  queueBulk: (action: QueueAction) => unknown;
  deleteFiles: (ids: number[]) => void;
};

/**
 * ChapterSelectionBar holds what can be done to the selected chapters. It
 * sits along the bottom of the screen, over the phone's tab bar, for as long
 * as anything is selected; the rarer actions open in a sheet from More.
 */
export function ChapterSelectionBar({ chapters, seriesTitle, deletes, queues, actions }: { chapters: Chapter[]; seriesTitle?: string; deletes: boolean; queues: boolean; actions: Actions }) {
  const [more, setMore] = useState(false);
  const ready = useOfflineReady();
  const saved = useSavedChapters();
  const toast = useToast();
  const ids = chapters.map((c) => c.id);
  const files = chapters.filter(deletableFile).map((c) => c.id);
  const toSave = chapters.filter(readable).sort((a, b) => Number(a.number) - Number(b.number)).map((c) => ({ id: c.id, seriesId: c.seriesId, seriesTitle, number: c.number }));
  const onDevice = chapters.filter((c) => saved[c.id]).map((c) => c.id);
  const queued = chapters.some((c) => c.job);
  const save = () => {
    const added = queueSave(toSave);
    if (added === 0) return toast.info(t("Already on this device"));
    toast.info(t("Saving {count} chapters on this device", { count: added }));
  };
  const close = (fn: () => unknown) => () => {
    setMore(false);
    void fn();
  };
  return (
    <>
      {/* room for the bar below the last rows */}
      <div aria-hidden className="h-20" />
      <div role="toolbar" aria-label={t("Selected chapters")} className="fixed inset-x-0 bottom-0 z-30 border-t border-border bg-panel px-2 pt-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] shadow-lg md:left-(--nav-width)">
        <div className="mx-auto grid max-w-xl auto-cols-fr grid-flow-col gap-1">
          {ready && <BarButton icon={<Smartphone className="size-5" />} label={t("Save on device")} onClick={save} />}
          <BarButton icon={<Check className="size-5" />} label={t("Mark read")} onClick={() => void actions.markMany(chapters, true)} />
          <BarButton icon={<Search className="size-5" />} label={t("Search")} onClick={() => void actions.search(ids)} />
          {deletes && files.length > 0 && <BarButton danger icon={<Trash2 className="size-5" />} label={t("Delete files")} onClick={() => actions.deleteFiles(files)} />}
          <BarButton icon={<MoreHorizontal className="size-5" />} label={t("More")} onClick={() => setMore(true)} expanded={more} />
        </div>
      </div>
      {more && (
        <Sheet title={t("{count} chapters selected", { count: chapters.length })} onClose={() => setMore(false)}>
          <SheetGroup title={t("Reading")}>
            <SheetItem onClick={close(() => actions.markMany(chapters, false))}>{t("Mark unread")}</SheetItem>
            {ready && onDevice.length > 0 && <SheetItem onClick={close(() => Promise.all(onDevice.map(removeChapter)))}>{t("Remove from this device")}</SheetItem>}
          </SheetGroup>
          <SheetGroup title={t("Downloads")}>
            <SheetItem onClick={close(() => actions.monitor(ids, true))}>{t("Monitor")}</SheetItem>
            <SheetItem onClick={close(() => actions.monitor(ids, false))}>{t("Unmonitor")}</SheetItem>
            <SheetItem onClick={close(() => actions.process(ids))}>{t("Process again")}</SheetItem>
          </SheetGroup>
          {queues && queued && (
            <SheetGroup title={t("In the queue")} grid>
              {(["top", "bottom", "pause", "resume", "retry", "remove"] as const).map((action) => (
                <SheetItem key={action} grid danger={action === "remove"} onClick={close(() => actions.queueBulk(action))}>
                  {{ top: t("Top"), bottom: t("Bottom"), pause: t("Pause"), resume: t("Resume"), retry: t("Retry"), remove: t("Remove") }[action]}
                </SheetItem>
              ))}
            </SheetGroup>
          )}
        </Sheet>
      )}
    </>
  );
}

function BarButton({ icon, label, onClick, danger, expanded }: { icon: ReactNode; label: string; onClick: () => void; danger?: boolean; expanded?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-expanded={expanded}
      className={clsx("flex min-h-12 flex-col items-center justify-center gap-1 rounded-md px-1 text-[11px] leading-tight active:bg-panel-2 hover:bg-panel-2", danger ? "text-err" : "text-fg")}
    >
      {icon}
      <span className="max-w-full truncate">{label}</span>
    </button>
  );
}

/** Sheet is a panel that slides up from the bottom of the screen (centered on wider ones). */
function Sheet({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const titleId = useId();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/60 md:items-center" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(e) => e.stopPropagation()}
        className="flex max-h-[85dvh] w-full max-w-md flex-col gap-4 overflow-y-auto rounded-t-2xl border border-border bg-panel px-4 pt-3 pb-[max(1.25rem,env(safe-area-inset-bottom))] md:rounded-2xl"
      >
        <div aria-hidden className="mx-auto h-1.5 w-10 rounded-full bg-border md:hidden" />
        <div className="flex items-center justify-between">
          <h2 id={titleId} className="font-semibold">{title}</h2>
          <button type="button" className="px-1 py-1 text-sm font-medium text-accent-2" onClick={onClose}>{t("Done")}</button>
        </div>
        {children}
      </div>
    </div>
  );
}

function SheetGroup({ title, grid, children }: { title: string; grid?: boolean; children: ReactNode }) {
  return (
    <section role="group" aria-label={title} className="flex flex-col gap-1.5">
      <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted">{title}</h3>
      <div className={grid ? "grid grid-cols-3 gap-2" : "flex flex-col divide-y divide-border overflow-hidden rounded-lg bg-panel-2"}>{children}</div>
    </section>
  );
}

function SheetItem({ onClick, grid, danger, children }: { onClick: () => void; grid?: boolean; danger?: boolean; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={clsx(grid ? "rounded-lg bg-panel-2 py-3 text-center" : "px-3.5 py-3 text-left", "text-[15px] hover:bg-border active:bg-border", danger ? "text-err" : "text-fg")}
    >
      {children}
    </button>
  );
}
