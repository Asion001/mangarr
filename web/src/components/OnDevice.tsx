import { t } from "../lib/i18n/core";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Check, Smartphone, WifiOff } from "lucide-react";
import clsx from "clsx";
import { Menu } from "./ui";
import { bytes } from "../lib/format";
import { useToast } from "../lib/toast";
import { estimateBytes, offlineReady, queueSave, removeChapter, saveChapterById, savedChapters, saving, savingNow, useSavedChapters, type QueuedChapter, type SavedChapter } from "../lib/offline/store";
import { useReachable } from "../lib/offline/connection";

/** useOfflineReady is true once a service worker answers this page, so chapters can be saved on the device. */
export function useOfflineReady() {
  const [ready, setReady] = useState(offlineReady);
  useEffect(() => {
    const on = () => setReady(offlineReady());
    navigator.serviceWorker?.addEventListener("controllerchange", on);
    return () => navigator.serviceWorker?.removeEventListener("controllerchange", on);
  }, []);
  return ready;
}

/** DeviceIcon is a phone: a tick once a chapter is all on this device, a ring filling while it saves. */
export function DeviceIcon({ chapter, className = "size-4" }: { chapter?: SavedChapter; className?: string }) {
  const done = !!chapter && !saving(chapter);
  const part = chapter && saving(chapter) ? Math.round((100 * chapter.saved) / Math.max(1, chapter.pages)) : 0;
  return (
    <span className={clsx("relative inline-grid place-items-center", className, done ? "text-ok" : chapter ? "text-accent-2" : "text-muted")}>
      <Smartphone className="size-full" />
      {done && <Check className="absolute size-[55%]" strokeWidth={3} />}
      {chapter && !done && (
        <span className="absolute bottom-[18%] left-[34%] right-[34%] h-[8%] overflow-hidden rounded-full bg-border">
          <span className="block h-full bg-accent" style={{ width: `${part}%` }} />
        </span>
      )}
    </span>
  );
}

/** SaveChapterButton saves a chapter on this device, or removes it once saved. */
export function SaveChapterButton({ chapterId }: { chapterId: number }) {
  const ready = useOfflineReady();
  const saved = useSavedChapters()[chapterId];
  const toast = useToast();
  if (!ready) return null;
  const done = !!saved && !saving(saved);
  const label = done ? t("Remove from this device") : saved ? t("Saving on this device…") : t("Save on this device");
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      className="inline-grid size-7 shrink-0 place-items-center rounded-md hover:bg-panel-2"
      onClick={() => {
        if (done) void removeChapter(chapterId);
        // a save that stopped (the connection dropped) carries on from here
        else if (!savingNow(chapterId)) saveChapterById(chapterId).catch(() => toast.error(t("Couldn't save the chapter on this device")));
      }}
    >
      <DeviceIcon chapter={saved} />
    </button>
  );
}

/** SaveSeriesMenu queues the next unread chapters, or all of them, to be saved on this device. */
export function SaveSeriesMenu({ unread, all }: { unread: QueuedChapter[]; all: QueuedChapter[] }) {
  const ready = useOfflineReady();
  const saved = useSavedChapters();
  const toast = useToast();
  if (!ready) return null;
  const save = (chapters: QueuedChapter[]) => {
    const added = queueSave(chapters);
    if (added === 0) return toast.info(t("Already on this device"));
    toast.info(t("Saving {count} chapters on this device", { count: added }));
  };
  const next = unread.slice(0, 5);
  const here = all.filter((c) => saved[c.id]).map((c) => c.id);
  return (
    <Menu
      size="sm"
      label={t("Save on device")}
      icon={<Smartphone className="size-3.5" />}
      items={[
        { label: `${t("Next 5 unread")} · ~${bytes(estimateBytes(savedChapters(), next.length))}`, onSelect: () => save(next), hidden: next.length === 0 },
        { label: `${t("All unread ({count})", { count: unread.length })} · ~${bytes(estimateBytes(savedChapters(), unread.length))}`, onSelect: () => save(unread), hidden: unread.length <= 5 },
        { label: t("Remove from this device"), danger: true, onSelect: () => void Promise.all(here.map(removeChapter)), hidden: here.length === 0 },
      ]}
    />
  );
}

/** OfflineBanner shows at the top of the app while the server doesn't answer. */
export function OfflineBanner() {
  const reachable = useReachable();
  const ready = useOfflineReady();
  // without a service worker nothing shows offline, so there's nothing to point at
  if (reachable || !ready) return null;
  return (
    <div role="status" className="mb-4 flex items-center gap-2 rounded-lg border border-warn/45 bg-warn/15 px-3 py-2 text-sm">
      <WifiOff className="size-4 shrink-0 text-warn" />
      <span className="min-w-0 flex-1">{t("You're offline. Saved chapters still open.")}</span>
      <Link to="/on-device" className="shrink-0 font-medium text-accent-2 hover:underline">{t("On this device")}</Link>
    </div>
  );
}
