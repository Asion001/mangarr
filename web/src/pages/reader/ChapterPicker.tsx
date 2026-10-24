import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Check, Download, Search, X } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Spinner } from "../../components/ui";
import { t } from "../../lib/i18n/core";
import { DeviceIcon } from "../../components/OnDevice";
import { isSaved, useSavedChapters } from "../../lib/offline/store";

export function ChapterPicker({ seriesId, currentId, onPick, onClose }: { seriesId: number; currentId: number; onPick: (id: number) => void; onClose: () => void }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  useEffect(() => {
    if (draft.trim() === query) return;
    const timeout = window.setTimeout(() => {
      setQuery(draft.trim());
      setPage(draft.trim() ? 1 : 0);
    }, 200);
    return () => window.clearTimeout(timeout);
  }, [draft, query]);
  const saved = useSavedChapters();
  const result = useQuery({
    queryKey: ["read-chapter-picker", seriesId, currentId, query, page],
    queryFn: () => unwrap(api.GET("/api/v1/read/series/{id}/chapters", { params: { path: { id: seriesId }, query: { q: query || undefined, currentId, page, pageSize: 50 } } })),
    placeholderData: (previous) => previous,
  });
  const data = result.data;
  const actualPage = data?.page ?? 1;
  const pages = Math.max(1, Math.ceil((data?.total ?? 0) / 50));
  // typed a number: go straight to that chapter (Enter), the matches stay below
  const match = query === draft.trim() && data?.match?.available ? data.match : undefined;
  const list = useRef<HTMLDivElement>(null);
  const centered = useRef(false);
  useEffect(() => {
    // the list opens on the chapter being read, in the middle of the box
    if (centered.current || !data || query) return;
    const box = list.current;
    const current = box?.querySelector<HTMLElement>("[data-current]");
    if (!box || !current) return;
    centered.current = true;
    box.scrollTop = current.offsetTop - box.clientHeight / 2 + current.offsetHeight / 2;
  }, [data, query]);
  const shortcuts = [
    { label: t("Up next"), chapter: data?.upNext },
    { label: t("First"), chapter: data?.first },
    { label: t("Latest"), chapter: data?.latest },
  ].filter((shortcut) => shortcut.chapter);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-3 text-fg" onPointerDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className="flex max-h-[85dvh] w-full max-w-xl flex-col overflow-hidden rounded-xl border border-border bg-panel shadow-2xl">
        <div className="flex flex-col gap-2.5 border-b border-border p-3">
          <div className="flex items-center gap-2">
            <form
              className="relative min-w-0 flex-1"
              onSubmit={(event) => {
                event.preventDefault();
                if (match) onPick(match.id);
              }}
            >
              <Search className="absolute left-2.5 top-2.5 size-4 text-muted" />
              <input autoFocus value={draft} onChange={(event) => setDraft(event.target.value)} enterKeyHint="go" placeholder={t("Find a chapter by number or title…")} aria-label={t("Find a chapter by number or title…")} className="w-full rounded-md border border-border bg-bg py-2 pl-8 pr-3 text-sm outline-none focus:border-accent" />
            </form>
            <button type="button" className="rounded p-2 hover:bg-panel-2" onClick={onClose} aria-label={t("Close chapter picker")}><X className="size-5" /></button>
          </div>
          {shortcuts.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {shortcuts.map(({ label, chapter }, index) => (
                <button
                  key={label}
                  type="button"
                  disabled={!chapter!.available || chapter!.id === currentId}
                  onClick={() => onPick(chapter!.id)}
                  className={`h-8 rounded-full px-3 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-40 ${index === 0 && data?.upNext === chapter ? "bg-accent/20 text-accent-2 hover:bg-accent/30" : "border border-border hover:bg-panel-2"}`}
                >
                  {label} · {chapter!.number}
                </button>
              ))}
            </div>
          )}
        </div>
        <div ref={list} className="relative min-h-40 flex-1 overflow-y-auto p-2">
          {match && (
            <button type="button" onClick={() => onPick(match.id)} className="mb-2 flex w-full items-center gap-3 rounded-lg bg-primary px-3 py-2.5 text-left text-sm font-semibold text-white hover:bg-primary-hover">
              <span className="flex-1">{t("Go to chapter {number}", { number: match.number })}</span>
              <ArrowRight className="size-4" aria-hidden="true" />
            </button>
          )}
          {result.isFetching && !data && <div className="flex justify-center p-8"><Spinner /></div>}
          {result.error && <p className="p-3 text-sm text-err">{String(result.error)}</p>}
          {data?.items.map((chapter) => (
            <button
              key={chapter.id}
              type="button"
              data-current={chapter.id === currentId || undefined}
              disabled={!chapter.available}
              onClick={() => onPick(chapter.id)}
              className={`flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left ${chapter.id === currentId ? "bg-accent/20 text-accent-2" : "hover:bg-panel-2"} disabled:cursor-not-allowed disabled:opacity-40`}
            >
              <span className="w-20 shrink-0 font-mono text-sm">{chapter.volume ? `v${chapter.volume} · ` : ""}{chapter.number}</span>
              <span className="min-w-0 flex-1 truncate text-sm">{chapter.title || `${t("Chapter")} ${chapter.number}`}</span>
              {isSaved(chapter.id) && <span aria-label={t("Saved on this device")}><DeviceIcon chapter={saved[chapter.id]} className="size-3.5" /></span>}
              {chapter.downloaded && <Download className="size-3.5 text-muted" aria-label={t("Downloaded")} />}
              {chapter.completed && <Check className="size-4 text-ok" aria-label={t("Read")} />}
              {!chapter.available && <span className="text-xs text-muted">{t("unavailable")}</span>}
            </button>
          ))}
          {data && data.items.length === 0 && <p className="p-6 text-center text-sm text-muted">{t("No chapters match this search.")}</p>}
        </div>
        <div className="flex items-center justify-between border-t border-border px-3 py-2 text-sm text-muted">
          <button type="button" disabled={actualPage <= 1} onClick={() => setPage(actualPage - 1)} className="rounded px-3 py-1.5 hover:bg-panel-2 disabled:opacity-30">{t("Previous")}</button>
          <span>{data?.total ?? 0} {t("chapters")} · {t("Page")} {actualPage} {t("of")} {pages}</span>
          <button type="button" disabled={actualPage >= pages} onClick={() => setPage(actualPage + 1)} className="rounded px-3 py-1.5 hover:bg-panel-2 disabled:opacity-30">{t("Next")}</button>
        </div>
      </div>
    </div>
  );
}
