import { t } from "../../lib/i18n/core";
import { useUIOption } from "../../lib/uiPreferences";
import { OtherLanguageChapters } from "./OtherLanguageChapters";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ArrowDownToLine, ArrowDownUp, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Hash } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap, type Chapter, type S } from "../../api/client";
import { useChapters, useQueue } from "../../api/queries";
import { Button, Card, Confirm, ErrorBox, Loading, Table, Th } from "../../components/ui";
import { useListParam, useStoredListParam } from "../../lib/urlState";
import { useAccount } from "../../lib/account";
import { scrollToTop } from "../../lib/scroll";
import { RecycledDrawer, ReprocessModal, VersionsModal } from "../system/RecycleDialogs";
import type { Recycled } from "../system/recycle";
import { ChapterMobileRow, ChapterRow, readable } from "./ChapterRows";
import { ChapterSelectionBar } from "./ChapterSelectionBar";
import { SaveSeriesMenu } from "../../components/OnDevice";
import { useReachable } from "../../lib/offline/connection";
import { useSavedChapters } from "../../lib/offline/store";
import { DecisionModal } from "./ChapterDecision";
import { useChapterActions, type QueueAction } from "./useChapterActions";

export { readable } from "./ChapterRows";

const chapterPageSizes = ["25", "50", "100"] as const;
const chapterSorts = ["desc", "asc"] as const;

/** closestChapter is the chapter numbered `number`, or the nearest one to it. */
export function closestChapter<T extends { number: string }>(list: T[], number: string): T | undefined {
  const wanted = number.trim();
  const exact = list.find((chapter) => chapter.number === wanted);
  if (exact || !wanted) return exact;
  const n = Number(wanted.replace(",", "."));
  if (!Number.isFinite(n)) return undefined;
  let best: T | undefined;
  for (const chapter of list) {
    const distance = Math.abs(Number(chapter.number) - n);
    if (Number.isFinite(distance) && (!best || distance < Math.abs(Number(best.number) - n))) best = chapter;
  }
  return best;
}

const toggle = (set: Set<number>, id: number) => {
  const next = new Set(set);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
};

/** nextChapterId is the chapter to read next: the one row with a primary Continue. */
export function ChaptersTable({ seriesId, seriesTitle, manage = true, nextChapterId, editions = [] }: { seriesId: number; seriesTitle?: string; manage?: boolean; nextChapterId?: number; editions?: S["EditionSummary"][] }) {
  const { data, isLoading, error } = useChapters(seriesId);
  const borrow = useUIOption("otherLanguageChapters", true) && editions.length > 1;
  // same query as the sidebar's queue count, so no extra request
  const queuePaused = !!useQueue({ pageSize: 1 }, manage).data?.state?.paused;
  const { account, isAdmin, can } = useAccount();
  // deleting files and handling the queue are permissions of their own
  const deletes = can("library.delete");
  const queues = can("queue.manage");
  // earlier versions of chapters, from the recycle bin (admins only)
  const { data: recycled } = useQuery({
    queryKey: ["recycle-bin", "series", seriesId],
    queryFn: () => unwrap(api.GET("/api/v1/recycle-bin", { params: { query: { series: seriesId, pageSize: 200 } } })),
    enabled: manage && isAdmin,
  });
  const versions = useMemo(() => {
    const out = new Map<number, number>();
    for (const r of recycled?.items ?? []) if (r.chapterId && r.kind === "file") out.set(r.chapterId, (out.get(r.chapterId) ?? 0) + 1);
    return out;
  }, [recycled]);
  const [versionsOf, setVersionsOf] = useState<Chapter | null>(null);
  const [compare, setCompare] = useState<Recycled | null>(null);
  const [reprocessFrom, setReprocessFrom] = useState<Recycled | null>(null);
  const tableTop = useRef<HTMLDivElement>(null);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [explain, setExplain] = useState<Chapter | null>(null);
  const [deleteIds, setDeleteIds] = useState<number[]>([]);
  const [deleting, setDeleting] = useState(false);
  // the page is in the URL, so a reload or Back returns to it
  const [pageParam, setPageParam] = useListParam("chapterPage", "");
  const setPage = useCallback((next: number) => setPageParam(String(next)), [setPageParam]);
  const [filterParam, setFilter] = useListParam("chapters", "all");
  const [pageSizeParam, setPageSize] = useStoredListParam("chapterPageSize", "25", "mangarr:chapters:page-size", chapterPageSizes);
  // the server sends newest first; oldest first reads like a book's contents
  const [sortParam, setSort] = useStoredListParam("chapterSort", "desc", "mangarr:chapters:sort", chapterSorts);
  const oldestFirst = sortParam === "asc";
  const [jump, setJump] = useState("");
  const [found, setFound] = useState<number | null>(null);
  const filter = filterParam as "all" | "missing" | "downloaded";
  const chaptersPerPage = Number(pageSizeParam);

  // offline, only what this device has saved can be read: the rest waits for the server
  const reachable = useReachable();
  const saved = useSavedChapters();
  const [showAll, setShowAll] = useState(false);
  const offlineOnly = !reachable && !showAll && Object.keys(saved).length > 0;
  const list = useMemo(() => {
    let l = data ?? [];
    if (offlineOnly) l = l.filter((c) => saved[c.id]);
    if (filter === "missing") l = l.filter((c) => !c.file && c.state !== "cleaned");
    else if (filter === "downloaded") l = l.filter((c) => c.file);
    return oldestFirst ? [...l].reverse() : l;
  }, [data, filter, offlineOnly, saved, oldestFirst]);
  // with no page asked for, the list opens on the page with the chapter to read next
  const nextIndex = nextChapterId ? list.findIndex((chapter) => chapter.id === nextChapterId) : -1;
  const nextPage = nextIndex < 0 ? 1 : Math.floor(nextIndex / chaptersPerPage) + 1;
  const page = Math.max(1, Number(pageParam) || nextPage);
  const pages = Math.max(1, Math.ceil(list.length / chaptersPerPage));
  const currentPage = Math.min(page, pages);
  const pageStart = (currentPage - 1) * chaptersPerPage;
  const visible = useMemo(() => list.slice(pageStart, pageStart + chaptersPerPage), [list, pageStart, chaptersPerPage]);

  const { monitor, search, restore, mark, markMany, queueAction, queueBulk: queueJobs, process, deleteFiles: removeFiles } = useChapterActions(seriesId);
  const queueBulk = (action: QueueAction) => queueJobs(action, list.filter((chapter) => selected.has(chapter.id) && chapter.job).map((chapter) => chapter.job!.id));
  const deleteFiles = useCallback(async () => {
    setDeleting(true);
    if (await removeFiles(deleteIds)) {
      setSelected((current) => {
        const next = new Set(current);
        deleteIds.forEach((id) => next.delete(id));
        return next;
      });
      setDeleteIds([]);
    }
    setDeleting(false);
  }, [deleteIds, removeFiles]);
  const toggleSelected = useCallback((id: number) => setSelected((current) => toggle(current, id)), []);
  const toggleExpanded = useCallback((id: number) => setExpanded((current) => toggle(current, id)), []);
  const monitorOne = useCallback((id: number, monitored: boolean) => monitor([id], monitored), [monitor]);
  const searchOne = useCallback((id: number) => search([id]), [search]);
  const goToPage = useCallback((next: number) => {
    setFound(null);
    setPage(next);
    requestAnimationFrame(() => tableTop.current && scrollToTop(tableTop.current));
  }, [setPage]);
  // showChapter opens the page holding the chapter and outlines its row
  const showChapter = useCallback((chapter: Chapter) => {
    const index = list.indexOf(chapter);
    if (index < 0) return;
    setPage(Math.floor(index / chaptersPerPage) + 1);
    setFound(chapter.id);
  }, [list, chaptersPerPage, setPage]);
  useEffect(() => {
    if (found === null) return;
    const frame = requestAnimationFrame(() => {
      // the table and the phone rows are both there; one of them is hidden
      const row = [...(tableTop.current?.querySelectorAll<HTMLElement>(`[data-chapter-id="${found}"]`) ?? [])].find((el) => el.offsetParent !== null);
      if (row) scrollToTop(row, 96);
    });
    return () => cancelAnimationFrame(frame);
  }, [found, currentPage]);
  const jumpTo = () => {
    const chapter = closestChapter(list, jump);
    if (chapter) showChapter(chapter);
  };
  const ranges = useMemo(() => {
    const out: string[] = [];
    for (let start = 0; start < list.length; start += chaptersPerPage) {
      const first = list[start].number;
      const last = list[Math.min(start + chaptersPerPage, list.length) - 1].number;
      out.push(first === last ? t("Ch. {number}", { number: first }) : t("Ch. {first}–{last}", { first, last }));
    }
    return out;
  }, [list, chaptersPerPage]);
  const pagerButton = "inline-flex size-8 items-center justify-center rounded-md border border-border text-muted hover:bg-panel-2 hover:text-fg disabled:pointer-events-none disabled:opacity-30";

  const sel = [...selected];
  return (
    <Card
      title={`Chapters (${data?.length ?? 0})`}
      actions={
        <div className="flex flex-wrap items-center gap-2">
          {manage && sel.length > 0 ? (
            <>
              <span className="text-xs font-medium">{t("{count} selected", { count: sel.length })}</span>
              {sel.length < list.length && (
                <Button size="sm" variant="ghost" onClick={() => setSelected(new Set(list.map((chapter) => chapter.id)))}>{t("Select all {count}", { count: list.length })}</Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>{t("Cancel")}</Button>
            </>
          ) : manage && list.length > 0 && (
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set(list.map((chapter) => chapter.id)))}>{t("Select all {count}", { count: list.length })}</Button>
          )}
          {sel.length === 0 && data && <SaveSeriesMenu {...saveable(data, seriesTitle)} />}
          {sel.length === 0 && list.length > 1 && (
            <Button size="sm" variant="ghost" icon={<ArrowDownUp className="size-3.5" />} onClick={() => (setSort(oldestFirst ? "desc" : "asc"), setPage(1))} title={t("Change the order")}>
              {oldestFirst ? t("Oldest first") : t("Newest first")}
            </Button>
          )}
          {sel.length === 0 && <select
            className="rounded-md border border-border bg-bg bg-size-[0.875rem] bg-position-[right_0.5rem_center] py-1 pl-2 pr-7 text-xs"
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setPage(1);
            }}
          >
            <option value="all">{t("All")}</option>
            <option value="missing">{t("Missing")}</option>
            <option value="downloaded">{t("Downloaded")}</option>
          </select>}
        </div>
      }
    >
      {!reachable && Object.keys(saved).length > 0 && (
        <div role="status" className="mb-3 flex flex-col gap-2 rounded-lg border border-border bg-panel-2 px-3.5 py-3 text-sm">
          <span className="font-medium">{t("No connection to the server")}</span>
          <span className="text-muted">{offlineOnly ? t("Showing the chapters saved on this device. Everything else comes back when the server answers.") : t("Chapters not saved on this device can't be opened until the server answers.")}</span>
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={showAll} onChange={(e) => setShowAll(e.target.checked)} />
            {t("Show all chapters anyway")}
          </label>
        </div>
      )}
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {borrow && data && filter !== "missing" && <OtherLanguageChapters seriesId={seriesId} editions={editions} own={data} />}
      {data && data.length === 0 && <p className="text-sm text-muted">{t("No chapters yet. Refresh the series to fetch the chapter list.")}</p>}
      {pages > 1 && (
        <div className="-mx-4 -mt-1 mb-3 flex flex-col gap-2 border-b border-border px-4 pb-3 sm:flex-row sm:flex-wrap sm:items-center">
          <div className="flex gap-2">
            <form
              className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-bg pl-2.5 pr-1 text-sm focus-within:border-accent sm:w-56 sm:flex-none"
              onSubmit={(event) => {
                event.preventDefault();
                jumpTo();
              }}
            >
              <Hash className="size-4 shrink-0 text-muted" aria-hidden="true" />
              <input
                value={jump}
                onChange={(event) => setJump(event.target.value)}
                inputMode="decimal"
                enterKeyHint="go"
                placeholder={t("Jump to chapter")}
                aria-label={t("Jump to chapter")}
                className="min-w-0 flex-1 bg-transparent font-mono outline-none placeholder:font-sans placeholder:text-muted"
              />
              {jump.trim() && <button type="submit" className="rounded px-2 py-1 text-xs font-medium hover:bg-panel-2">{t("Go")}</button>}
            </form>
            {nextIndex >= 0 && (
              <Button size="sm" className="h-9 shrink-0" icon={<ArrowDownToLine className="size-4" />} onClick={() => showChapter(list[nextIndex])}>
                {t("Up next: {number}", { number: list[nextIndex].number })}
              </Button>
            )}
          </div>
          <div className="flex items-center gap-1.5 sm:ml-auto">
            <button type="button" className={pagerButton} disabled={currentPage <= 1} onClick={() => goToPage(1)} aria-label={t("First page")}><ChevronsLeft className="size-4" /></button>
            <button type="button" className={pagerButton} disabled={currentPage <= 1} onClick={() => goToPage(currentPage - 1)} aria-label={t("Previous page")}><ChevronLeft className="size-4" /></button>
            <select
              className="h-8 min-w-0 flex-1 rounded-md border border-border bg-bg bg-size-[0.875rem] bg-position-[right_0.5rem_center] py-1 pl-2 pr-7 text-xs font-medium sm:flex-none"
              aria-label={t("Chapters on this page")}
              value={currentPage}
              onChange={(event) => goToPage(Number(event.target.value))}
            >
              {ranges.map((range, index) => (
                <option key={index} value={index + 1}>
                  {range}{nextIndex >= 0 && Math.floor(nextIndex / chaptersPerPage) === index ? " · " + t("up next") : ""}
                </option>
              ))}
            </select>
            <button type="button" className={pagerButton} disabled={currentPage >= pages} onClick={() => goToPage(currentPage + 1)} aria-label={t("Next page")}><ChevronRight className="size-4" /></button>
            <button type="button" className={pagerButton} disabled={currentPage >= pages} onClick={() => goToPage(pages)} aria-label={t("Last page")}><ChevronsRight className="size-4" /></button>
            <span className="ml-1 whitespace-nowrap text-xs text-muted">{t("{page} of {pages}", { page: currentPage, pages })}</span>
          </div>
        </div>
      )}
      {list.length > 0 && (
        <div ref={tableTop}>
          {/* rows reach the card's edges, so the Up next tint fills the whole row */}
          <div className="-mx-4 divide-y divide-border xl:hidden">
            {visible.map((chapter) => (
              <ChapterMobileRow
                key={chapter.id}
                chapter={chapter}
                manage={manage}
                selected={selected.has(chapter.id)}
                open={expanded.has(chapter.id)}
                accountKind={account?.kind}
                onSelect={toggleSelected}
                onExpand={toggleExpanded}
                onMonitor={monitorOne}
                onSearch={searchOne}
                onRestore={restore}
                onDelete={deletes ? (id) => setDeleteIds([id]) : undefined}
                onMark={mark}
                onQueueAction={queueAction}
                onExplain={setExplain}
                queuePaused={queuePaused}
                next={chapter.id === nextChapterId}
                found={chapter.id === found}
              />
            ))}
          </div>
          <Table className="hidden border-0 xl:block">
            <thead>
              <tr>
                <Th className="w-8">
                  {/* selecting is for managing; readers have nothing to do with a selection */}
                  {manage && (
                    <input
                      type="checkbox"
                      checked={sel.length === list.length && list.length > 0}
                      onChange={(e) => setSelected(e.target.checked ? new Set(list.map((c) => c.id)) : new Set())}
                    />
                  )}
                </Th>
                <Th className="w-8" />
                <Th>#</Th>
                <Th>{t("Title")}</Th>
                <Th>{t("Released")}</Th>
                <Th>{t("State")}</Th>
                <Th>{t("File")}</Th>
                <Th className="w-48" />
              </tr>
            </thead>
            <tbody>
              {visible.map((chapter) => (
                <ChapterRow
                  key={chapter.id}
                  chapter={chapter}
                  manage={manage}
                  selected={selected.has(chapter.id)}
                  open={expanded.has(chapter.id)}
                  accountKind={account?.kind}
                  onSelect={toggleSelected}
                  onExpand={toggleExpanded}
                  onMonitor={monitorOne}
                  onSearch={searchOne}
                  onRestore={restore}
                  onDelete={deletes ? (id) => setDeleteIds([id]) : undefined}
                  onMark={mark}
                  onQueueAction={queueAction}
                  onExplain={setExplain}
                  versions={versions.get(chapter.id) ?? 0}
                  onVersions={setVersionsOf}
                  queuePaused={queuePaused}
                  next={chapter.id === nextChapterId}
                  found={chapter.id === found}
                />
              ))}
            </tbody>
          </Table>
        </div>
      )}
      {list.length > Number(chapterPageSizes[0]) && (
        <div className="mt-3 flex flex-wrap items-center justify-end gap-3 text-sm">
          <span className="text-muted">
            {pageStart + 1}–{Math.min(pageStart + chaptersPerPage, list.length)} / {list.length} · {t("Page") + " "}{currentPage}{" " + t("of") + " "}{pages}
          </span>
          <Button size="sm" disabled={currentPage <= 1} onClick={() => goToPage(currentPage - 1)}>{t("Previous")}</Button>
          <Button size="sm" disabled={currentPage >= pages} onClick={() => goToPage(currentPage + 1)}>{t("Next")}</Button>
          {data && <SaveSeriesMenu {...saveable(data, seriesTitle)} />}
          <select
            className="rounded-md border border-border bg-bg bg-size-[0.875rem] bg-position-[right_0.5rem_center] py-1 pl-2 pr-7 text-xs"
            aria-label={t("per page")}
            value={pageSizeParam}
            onChange={(event) => {
              setPageSize(event.target.value);
              setPage(1);
            }}
          >
            {chapterPageSizes.map((size) => <option key={size} value={size}>{size} {t("per page")}</option>)}
          </select>
        </div>
      )}
      {manage && sel.length > 0 && (
        <ChapterSelectionBar
          chapters={list.filter((chapter) => selected.has(chapter.id))}
          seriesTitle={seriesTitle}
          deletes={deletes}
          queues={queues}
          actions={{ monitor, search, process, markMany, queueBulk, deleteFiles: setDeleteIds }}
        />
      )}
      {explain && <DecisionModal seriesId={seriesId} chapter={explain} onClose={() => setExplain(null)} />}
      {versionsOf && (
        <VersionsModal
          chapterId={versionsOf.id}
          label={t("chapter {number}", { number: versionsOf.number })}
          onClose={() => setVersionsOf(null)}
          onCompare={(r) => (setVersionsOf(null), setCompare(r))}
        />
      )}
      {compare && <RecycledDrawer item={compare} onClose={() => setCompare(null)} onReprocess={(r) => (setCompare(null), setReprocessFrom(r))} />}
      {reprocessFrom && <ReprocessModal items={[reprocessFrom]} onClose={() => setReprocessFrom(null)} />}
      <Confirm
        open={deleteIds.length > 0}
        title={t("Delete chapter files")}
        message={t("Delete {count} chapter files? They will stay in the chapter list and will not download again until restored.", { count: deleteIds.length })}
        confirmLabel={t("Delete")}
        danger
        loading={deleting}
        onConfirm={deleteFiles}
        onClose={() => setDeleteIds([])}
      />
    </Card>
  );
}

/** saveable is the chapters that can be saved on the device, in reading order, and which of them are unread. */
function saveable(chapters: Chapter[], seriesTitle?: string) {
  const list = chapters.filter(readable).sort((a, b) => Number(a.number) - Number(b.number) || a.id - b.id);
  const read = (c: Chapter) => c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  const queued = (c: Chapter) => ({ id: c.id, seriesId: c.seriesId, seriesTitle, number: c.number });
  return { unread: list.filter((c) => !read(c)).map(queued), all: list.map(queued) };
}
