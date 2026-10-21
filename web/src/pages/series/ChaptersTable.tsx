import { t } from "../../lib/i18n/core";
import { useUIOption } from "../../lib/uiPreferences";
import { OtherLanguageChapters } from "./OtherLanguageChapters";
import { useCallback, useMemo, useRef, useState } from "react";
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

const toggle = (set: Set<number>, id: number) => {
  const next = new Set(set);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  return next;
};

/** nextChapterId is the chapter to read next: the one row with a primary Continue. */
export function ChaptersTable({ seriesId, manage = true, nextChapterId, editions = [] }: { seriesId: number; manage?: boolean; nextChapterId?: number; editions?: S["EditionSummary"][] }) {
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
    if (filter === "missing") return l.filter((c) => !c.file && c.state !== "cleaned");
    if (filter === "downloaded") return l.filter((c) => c.file);
    return l;
  }, [data, filter, offlineOnly, saved]);
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
    setPage(next);
    requestAnimationFrame(() => tableTop.current && scrollToTop(tableTop.current));
  }, [setPage]);

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
          {sel.length === 0 && data && <SaveSeriesMenu {...saveable(data)} />}
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
          {nextIndex >= 0 && currentPage !== nextPage && (
            <Button size="sm" variant="ghost" onClick={() => goToPage(nextPage)}>{t("Up next: chapter {number}", { number: list[nextIndex].number })}</Button>
          )}
          <Button size="sm" disabled={currentPage <= 1} onClick={() => goToPage(currentPage - 1)}>{t("Previous")}</Button>
          <Button size="sm" disabled={currentPage >= pages} onClick={() => goToPage(currentPage + 1)}>{t("Next")}</Button>
          {data && <SaveSeriesMenu {...saveable(data)} />}
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
function saveable(chapters: Chapter[]) {
  const list = chapters.filter(readable).sort((a, b) => Number(a.number) - Number(b.number) || a.id - b.id);
  const read = (c: Chapter) => c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  return { unread: list.filter((c) => !read(c)).map((c) => c.id), all: list.map((c) => c.id) };
}
