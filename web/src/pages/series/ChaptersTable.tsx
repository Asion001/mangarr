import { t as tr, t } from "../../lib/i18n/core";
import { useUIOption } from "../../lib/uiPreferences";
import { OtherLanguageChapters } from "./OtherLanguageChapters";
import { Fragment, memo, useCallback, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDownToLine, ArrowUpToLine, BookOpen, History, Check, ChevronDown, ChevronRight, ExternalLink, HelpCircle, Pause, Play, RotateCcw, RotateCw, Search, Sparkles, Eye, Trash2 } from "lucide-react";
import { Link } from "react-router";
import clsx from "clsx";
import { api, unwrap, type Chapter, type S } from "../../api/client";
import { useChapters, useQueue } from "../../api/queries";
import { Badge, Button, Card, Confirm, ErrorBox, Loading, Modal, Progress, Switch, Table, Td, Th } from "../../components/ui";
import { bytes, date, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { eta } from "../../lib/liveProgress";
import { useListParam, useStoredListParam } from "../../lib/urlState";
import { useAccount } from "../../lib/account";
import { RecycledDrawer, ReprocessModal, VersionsModal } from "../system/RecycleDialogs";
import type { Recycled } from "../system/recycle";

const stateTone: Record<string, "ok" | "warn" | "err" | "info" | "default" | "accent"> = {
  imported: "ok",
  missing: "warn",
  failed: "err",
  queued: "info",
  downloading: "info",
  processing: "accent",
  cleaned: "default",
};

const chapterPageSizes = ["25", "50", "100"] as const;

/** readable: downloaded, or a source to stream it from. */
export const readable = (c: Chapter) => !!c.file || c.releases.length > 0;

const deletableFile = (c: Chapter) => !!c.file && (!c.job || ["completed", "failed"].includes(c.job.status));

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
  const qc = useQueryClient();
  const toast = useToast();
  const { account, isAdmin } = useAccount();
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
  const [page, setPage] = useState(1);
  const [filterParam, setFilter] = useListParam("chapters", "all");
  const [pageSizeParam, setPageSize] = useStoredListParam("chapterPageSize", "25", "mangarr:chapters:page-size", chapterPageSizes);
  const filter = filterParam as "all" | "missing" | "downloaded";
  const chaptersPerPage = Number(pageSizeParam);

  const list = useMemo(() => {
    const l = data ?? [];
    if (filter === "missing") return l.filter((c) => !c.file && c.state !== "cleaned");
    if (filter === "downloaded") return l.filter((c) => c.file);
    return l;
  }, [data, filter]);
  const pages = Math.max(1, Math.ceil(list.length / chaptersPerPage));
  const currentPage = Math.min(page, pages);
  const pageStart = (currentPage - 1) * chaptersPerPage;
  const visible = useMemo(() => list.slice(pageStart, pageStart + chaptersPerPage), [list, pageStart, chaptersPerPage]);

  const refresh = useCallback(() => qc.invalidateQueries({ queryKey: ["series", seriesId] }), [qc, seriesId]);

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
    } catch (e) {
      toast.fromError(e);
    }
  }, [refresh, toast]);
  const queueAction = useCallback(async (jobID: number, action: "top" | "bottom" | "pause" | "resume") => {
    try {
      await unwrap(api.POST("/api/v1/queue/bulk", { body: { action, ids: [jobID] } }));
      refresh();
      qc.invalidateQueries({ queryKey: ["queue"] });
    } catch (e) {
      toast.fromError(e);
    }
  }, [qc, refresh, toast]);
  const queueBulk = async (action: "top" | "bottom" | "pause" | "resume" | "retry" | "remove") => {
    const ids = list.filter((chapter) => selected.has(chapter.id) && chapter.job).map((chapter) => chapter.job!.id);
    if (ids.length === 0) return;
    try {
      const result = await unwrap(api.POST("/api/v1/queue/bulk", { body: { action, ids } }));
      toast.success(`${result.affected} ${result.affected === 1 ? "queue entry" : "queue entries"} updated`);
      refresh();
      qc.invalidateQueries({ queryKey: ["queue"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const processSelected = async () => {
    try {
      await unwrap(api.POST("/api/v1/commands", { body: { name: "ProcessExisting", body: { seriesId, chapterIds: sel } } }));
      toast.info(`Queued processing for ${sel.length} chapter${sel.length === 1 ? "" : "s"}`);
      refresh();
      qc.invalidateQueries({ queryKey: ["queue"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const deleteFiles = useCallback(async () => {
    setDeleting(true);
    try {
      const result = await unwrap(api.POST("/api/v1/chapters/delete", { body: { chapterIds: deleteIds } }));
      toast.success(t("Deleted chapter files: {count}", { count: result.removed }));
      setSelected((current) => {
        const next = new Set(current);
        deleteIds.forEach((id) => next.delete(id));
        return next;
      });
      setDeleteIds([]);
      refresh();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setDeleting(false);
    }
  }, [deleteIds, refresh, toast]);
  const toggleSelected = useCallback((id: number) => setSelected((current) => toggle(current, id)), []);
  const toggleExpanded = useCallback((id: number) => setExpanded((current) => toggle(current, id)), []);
  const monitorOne = useCallback((id: number, monitored: boolean) => monitor([id], monitored), [monitor]);
  const searchOne = useCallback((id: number) => search([id]), [search]);
  const goToPage = useCallback((next: number) => {
    setPage(next);
    requestAnimationFrame(() => tableTop.current?.scrollIntoView({ block: "start" }));
  }, []);

  const sel = [...selected];
  const selectedJobs = list.filter((chapter) => selected.has(chapter.id) && chapter.job).length;
  const selectedFileIDs = list.filter((chapter) => selected.has(chapter.id) && deletableFile(chapter)).map((chapter) => chapter.id);
  return (
    <Card
      title={`Chapters (${data?.length ?? 0})`}
      actions={
        <div className="flex flex-wrap items-center gap-2">
          {sel.length > 0 && (
            <>
              <span className="text-xs text-muted">{sel.length}{" " + t("selected")}</span>
              <Button size="sm" onClick={() => monitor(sel, true)}>{t("Monitor")}</Button>
              <Button size="sm" onClick={() => monitor(sel, false)}>{t("Unmonitor")}</Button>
              <Button size="sm" icon={<Search className="size-3.5" />} onClick={() => search(sel)}>{t("Search")}</Button>
              <Button size="sm" icon={<Sparkles className="size-3.5" />} onClick={processSelected}>{t("Process")}</Button>
              {selectedFileIDs.length > 0 && (
                <Button size="sm" variant="danger" icon={<Trash2 className="size-3.5" />} onClick={() => setDeleteIds(selectedFileIDs)}>{t("Delete files")}</Button>
              )}
              {selectedJobs > 0 && (
                <>
                  <Button size="sm" icon={<ArrowUpToLine className="size-3.5" />} onClick={() => queueBulk("top")}>{t("Top")}</Button>
                  <Button size="sm" icon={<ArrowDownToLine className="size-3.5" />} onClick={() => queueBulk("bottom")}>{t("Bottom")}</Button>
                  <Button size="sm" icon={<Pause className="size-3.5" />} onClick={() => queueBulk("pause")}>{t("Pause")}</Button>
                  <Button size="sm" icon={<Play className="size-3.5" />} onClick={() => queueBulk("resume")}>{t("Resume")}</Button>
                  <Button size="sm" icon={<RotateCw className="size-3.5" />} onClick={() => queueBulk("retry")}>{t("Retry")}</Button>
                  <Button size="sm" variant="danger" icon={<Trash2 className="size-3.5" />} onClick={() => queueBulk("remove")}>{t("Remove")}</Button>
                </>
              )}
              <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>{t("Clear")}</Button>
            </>
          )}
          {manage && list.length > 0 && sel.length < list.length && (
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set(list.map((chapter) => chapter.id)))}>{t("Select all {count}", { count: list.length })}</Button>
          )}
          <select
            className="rounded-md border border-border bg-bg px-2 py-1 text-xs"
            value={filter}
            onChange={(e) => {
              setFilter(e.target.value);
              setPage(1);
            }}
          >
            <option value="all">{t("All")}</option>
            <option value="missing">{t("Missing")}</option>
            <option value="downloaded">{t("Downloaded")}</option>
          </select>
        </div>
      }
    >
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {borrow && data && filter !== "missing" && <OtherLanguageChapters seriesId={seriesId} editions={editions} own={data} />}
      {data && data.length === 0 && <p className="text-sm text-muted">{t("No chapters yet. Refresh the series to fetch the chapter list.")}</p>}
      {list.length > 0 && (
        <div ref={tableTop}>
          <div className="divide-y divide-border xl:hidden">
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
                onDelete={(id) => setDeleteIds([id])}
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
                  <input
                    type="checkbox"
                    checked={sel.length === list.length && list.length > 0}
                    onChange={(e) => setSelected(e.target.checked ? new Set(list.map((c) => c.id)) : new Set())}
                  />
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
                  onDelete={(id) => setDeleteIds([id])}
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
          <Button size="sm" disabled={currentPage <= 1} onClick={() => goToPage(currentPage - 1)}>{t("Previous")}</Button>
          <Button size="sm" disabled={currentPage >= pages} onClick={() => goToPage(currentPage + 1)}>{t("Next")}</Button>
          <select
            className="rounded-md border border-border bg-bg px-2 py-1 text-xs"
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

type ChapterRowProps = {
  chapter: Chapter;
  manage: boolean;
  selected: boolean;
  open: boolean;
  accountKind?: string;
  onSelect: (id: number) => void;
  onExpand: (id: number) => void;
  onMonitor: (id: number, monitored: boolean) => void;
  onSearch: (id: number) => void;
  onRestore: (chapter: Chapter) => void;
  onDelete: (id: number) => void;
  onMark: (chapter: Chapter, read: boolean, scope?: "chapter" | "previous") => void;
  onQueueAction: (jobID: number, action: "top" | "bottom" | "pause" | "resume") => void;
  onExplain: (chapter: Chapter) => void;
  /** versions: how many earlier versions of the file the recycle bin holds. */
  versions?: number;
  onVersions?: (chapter: Chapter) => void;
  /** queuePaused: the whole download queue is paused, so queued chapters wait. */
  queuePaused?: boolean;
  /** next: this is the chapter to read next. */
  next?: boolean;
};

/** A memoized row keeps queue progress updates from rerendering every chapter. */
const ChapterRow = memo(function ChapterRow({
  chapter: c,
  manage,
  selected,
  open,
  accountKind,
  onSelect,
  onExpand,
  onMonitor,
  onSearch,
  onRestore,
  onDelete,
  onMark,
  onQueueAction,
  onExplain,
  versions = 0,
  onVersions,
  queuePaused,
  next,
}: ChapterRowProps) {
  // read: everyone who opened it finished it (for a user account, that's you)
  const read = c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  return (
    <Fragment>
      <tr className={clsx(!c.monitored && "opacity-60", next && "bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent)]")}>
        <Td>
          {manage && <input type="checkbox" checked={selected} onChange={() => onSelect(c.id)} />}
        </Td>
        <Td>
          {manage && <Switch checked={c.monitored} onChange={(monitored) => onMonitor(c.id, monitored)} />}
        </Td>
        <Td className={clsx("font-mono text-xs", read && "text-muted")}>
          {c.volume && <span className="text-muted">v{c.volume} </span>}
          {c.number}
        </Td>
        <Td>
          <div className="flex items-center gap-1">
            <button className="flex items-center gap-1 text-left hover:text-accent-2" onClick={() => onExpand(c.id)} aria-expanded={open}>
              {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
              <span className={clsx("line-clamp-1", c.title && "min-w-40", read && "text-muted")}>{c.title || <span className="text-muted">—</span>}</span>
            </button>
            {next && <Badge tone="accent">{t("Up next")}</Badge>}
            {read && (
              <span className="inline-flex items-center gap-0.5 text-xs text-ok">
                <Check className="size-3" />
                {t("read")}
              </span>
            )}
            {c.releases.length > 1 && <Badge title={t("Expand to compare releases")}>{t("{count} releases", { count: c.releases.length })}</Badge>}
          </div>
        </Td>
        <Td className="whitespace-nowrap text-muted">{date(c.releaseDate)}</Td>
        <Td>
          <div className="flex flex-col gap-1">
            {queuePaused && c.job?.status === "queued" ? (
              <Badge tone="warn" title={t("Queue paused")}>{c.state} · {t("paused")}</Badge>
            ) : (
              <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
            )}
            {c.job && ["downloading", "processing", "importing"].includes(c.job.status) && (
              <div className="w-20">
                <Progress value={c.job.progress} />
              </div>
            )}
            {manage && c.job && c.job.priority !== 0 && !["completed", "failed"].includes(c.job.status) && (
              <span className="text-[11px] text-muted">{t("Priority")}: {c.job.priority}</span>
            )}
          </div>
        </Td>
        <Td className="text-xs">
          {c.file ? (
            <div className="flex flex-col">
              <span>
                {bytes(c.file.size)} · {c.file.pageCount}{t("p ·") + " "}{c.file.avgWidth}px
              </span>
              <span className="flex items-center gap-1 text-muted">
                {c.file.scanlator || c.file.sourceName}
                {c.file.upscaled && (
                  <Badge tone="accent" title={c.file.upscaleModel}>
                    <Sparkles className="size-3" />{" " + t("upscaled")}</Badge>
                )}
                {(c.file.format === "avif" || c.file.format === "jxl") && (
                  <Badge
                    tone="info"
                    title={[
                      c.file.sizeOriginal > c.file.size ? `was ${bytes(c.file.sizeOriginal)}` : "",
                      c.file.processSeconds ? `processed in ${eta(c.file.processSeconds)} (${((c.file.processPages ?? 0) / c.file.processSeconds).toFixed(1)} pages/s)` : "",
                    ]
                      .filter(Boolean)
                      .join(", ") || undefined}
                  >
                    {c.file.format}
                    {c.file.sizeOriginal > c.file.size && ` −${Math.round(100 - (100 * c.file.size) / c.file.sizeOriginal)}%`}
                  </Badge>
                )}
                {c.file.processState === "failed" && (
                  <Badge tone="err" title={c.file.processError}>{t("processing failed")}</Badge>
                )}
              </span>
              {versions > 0 && onVersions && (
                <button
                  type="button"
                  onClick={() => onVersions(c)}
                  className="mt-1 inline-flex w-fit items-center gap-1 rounded-full border border-border bg-panel-2 px-2 py-0.5 text-xs font-medium hover:border-accent"
                >
                  <History className="size-3" />
                  {versions === 1 ? t("1 earlier version") : t("{count} earlier versions", { count: versions })}
                </button>
              )}
            </div>
          ) : versions > 0 && onVersions ? (
            <button type="button" onClick={() => onVersions(c)} className="inline-flex items-center gap-1 rounded-full border border-border bg-panel-2 px-2 py-0.5 text-xs font-medium hover:border-accent">
              <History className="size-3" />
              {versions === 1 ? t("1 earlier version") : t("{count} earlier versions", { count: versions })}
            </button>
          ) : (
            <span className="text-muted">—</span>
          )}
        </Td>
        <Td className="text-right">
          <div className="flex items-center justify-end gap-1">
            {readable(c) && (
              <Link to={`/read/${c.id}`} title={c.file ? tr("Read") : tr("Read (streamed from the source)")}>
                <Button className="w-28" variant={next ? "primary" : read ? "ghost" : "secondary"} icon={<BookOpen className="size-4" />}>
                  {next ? t("Continue") : read ? t("Re-read") : t("Read")}
                </Button>
              </Link>
            )}
          </div>
        </Td>
      </tr>
      {open && (
        <tr>
          <Td colSpan={8} className="bg-bg/60">
            {manage && (
              <ChapterManagementActions
                chapter={c}
                onMonitor={onMonitor}
                onQueueAction={onQueueAction}
                onRestore={onRestore}
                onSearch={onSearch}
                onDelete={onDelete}
                onExplain={onExplain}
              />
            )}
            <ChapterExpandedInfo chapter={c} accountKind={accountKind} onMark={onMark} />
          </Td>
        </tr>
      )}
    </Fragment>
  );
});

/** Phone and tablet rows keep the reading action visible without a wide table. */
const ChapterMobileRow = memo(function ChapterMobileRow({
  chapter: c,
  manage,
  selected,
  open,
  accountKind,
  onSelect,
  onExpand,
  onMonitor,
  onSearch,
  onRestore,
  onDelete,
  onMark,
  onQueueAction,
  onExplain,
  queuePaused,
  next,
}: ChapterRowProps) {
  const read = c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  return (
    <article className={clsx("py-3", !c.monitored && "opacity-60", next && "bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent)]")}>
      <div className="flex min-w-0 items-center gap-2">
        {manage && <input className="shrink-0" type="checkbox" checked={selected} onChange={() => onSelect(c.id)} aria-label={`${t("Select")} ${t("Chapter")} ${c.number}`} />}
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left hover:text-accent-2" onClick={() => onExpand(c.id)} aria-expanded={open}>
          {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
          <span className={clsx("shrink-0 font-mono text-xs", read && "text-muted")}>{c.volume ? `v${c.volume} ` : ""}{c.number}</span>
          <span className={clsx("min-w-0 flex-1 truncate text-sm", read && "text-muted")}>{c.title || t("Chapter") + " " + c.number}</span>
        </button>
        {readable(c) && (
          <Link className="shrink-0" to={`/read/${c.id}`} title={c.file ? tr("Read") : tr("Read (streamed from the source)")}>
            <Button size="sm" variant={next ? "primary" : read ? "ghost" : "secondary"} icon={<BookOpen className="size-4" />}>
              {next ? t("Continue") : read ? t("Re-read") : t("Read")}
            </Button>
          </Link>
        )}
      </div>
      {(manage || next || read) && <div className={clsx("mt-2 flex flex-wrap items-center gap-1.5 text-xs", manage && "pl-6")}>
        {next && <Badge tone="accent">{t("Up next")}</Badge>}
        {read && <Badge tone="ok"><Check className="size-3" /> {t("read")}</Badge>}
        {manage && (queuePaused && c.job?.status === "queued" ? (
          <Badge tone="warn" title={t("Queue paused")}>{c.state} · {t("paused")}</Badge>
        ) : (
          <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
        ))}
        {manage && c.file && <span className="text-muted">{c.file.pageCount}{t("p ·") + " "}{bytes(c.file.size)}</span>}
        {manage && c.releases.length > 1 && <Badge>{t("{count} releases", { count: c.releases.length })}</Badge>}
      </div>}
      {manage && c.job && ["downloading", "processing", "importing"].includes(c.job.status) && (
        <div className={clsx("mt-2 max-w-48", manage && "ml-6")}><Progress value={c.job.progress} /></div>
      )}
      {open && (
        <div className="mt-3 border-t border-border bg-bg/50 px-1 pt-3">
          {manage && (
            <ChapterManagementActions
              chapter={c}
              showMonitor
              onMonitor={onMonitor}
              onQueueAction={onQueueAction}
              onRestore={onRestore}
              onSearch={onSearch}
              onDelete={onDelete}
              onExplain={onExplain}
            />
          )}
          {!manage && (
            <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
              <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
              {c.file && <span className="text-muted">{c.file.pageCount}{t("p ·") + " "}{bytes(c.file.size)}</span>}
              {c.releases.length > 1 && <Badge>{t("{count} releases", { count: c.releases.length })}</Badge>}
              {c.job && ["downloading", "processing", "importing"].includes(c.job.status) && <div className="w-full max-w-48"><Progress value={c.job.progress} /></div>}
            </div>
          )}
          {c.file && (
            <div className="mb-3 text-xs text-muted">
              {c.file.scanlator || c.file.sourceName} · {c.file.avgWidth}px
              {c.file.upscaled && <> · {t("upscaled")}</>}
              {(c.file.format === "avif" || c.file.format === "jxl") && <> · {c.file.format}</>}
              {c.file.processState === "failed" && <p className="mt-1 text-err">{c.file.processError}</p>}
            </div>
          )}
          <ChapterExpandedInfo chapter={c} accountKind={accountKind} onMark={onMark} />
        </div>
      )}
    </article>
  );
});

function ChapterManagementActions({
  chapter: c,
  showMonitor = false,
  onMonitor,
  onQueueAction,
  onRestore,
  onSearch,
  onDelete,
  onExplain,
}: {
  chapter: Chapter;
  showMonitor?: boolean;
  onMonitor: (id: number, monitored: boolean) => void;
  onQueueAction: (jobID: number, action: "top" | "bottom" | "pause" | "resume") => void;
  onRestore: (chapter: Chapter) => void;
  onSearch: (id: number) => void;
  onDelete: (id: number) => void;
  onExplain: (chapter: Chapter) => void;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      {showMonitor && <span className="flex items-center gap-2 text-xs text-muted"><Switch checked={c.monitored} onChange={(monitored) => onMonitor(c.id, monitored)} /> {t("Monitored")}</span>}
      {c.job && !["completed", "failed"].includes(c.job.status) && (
        <>
          <Button size="sm" icon={<ArrowUpToLine className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "top")}>{t("Top")}</Button>
          <Button size="sm" icon={<ArrowDownToLine className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "bottom")}>{t("Bottom")}</Button>
          {c.job.status === "paused" ? (
            <Button size="sm" icon={<Play className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "resume")}>{t("Resume")}</Button>
          ) : (
            <Button size="sm" icon={<Pause className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "pause")}>{t("Pause")}</Button>
          )}
        </>
      )}
      {c.state === "cleaned" ? (
        <Button size="sm" icon={<RotateCcw className="size-3.5" />} onClick={() => onRestore(c)}>{t("Restore")}</Button>
      ) : (
        <Button size="sm" icon={<Search className="size-3.5" />} onClick={() => onSearch(c.id)}>{t("Search")}</Button>
      )}
      {deletableFile(c) && <Button size="sm" variant="danger" icon={<Trash2 className="size-3.5" />} onClick={() => onDelete(c.id)}>{t("Delete chapter file")}</Button>}
      <Button size="sm" icon={<HelpCircle className="size-3.5" />} onClick={() => onExplain(c)}>{t("Why (not) downloaded?")}</Button>
    </div>
  );
}

function ChapterExpandedInfo({ chapter: c, accountKind, onMark }: { chapter: Chapter; accountKind?: string; onMark: (chapter: Chapter, read: boolean, scope?: "chapter" | "previous") => void }) {
  return <>
    {c.releases.length === 0 ? (
      <p className="text-xs text-muted">{t("No releases.")}</p>
    ) : (
      <div className="flex flex-col gap-1">
        {c.releases.map((r) => (
          <div key={r.id} className="flex flex-wrap items-center gap-2 text-xs">
            <Badge>{r.sourceName}</Badge>
            <span className={r.removed ? "line-through text-muted" : ""}>{r.name}</span>
            {r.scanlator && <span className="text-muted">{t("by") + " "}{r.scanlator}</span>}
            <span className="text-muted">{date(r.uploadDate)}</span>
            {r.blocklisted && <Badge tone="err">{t("blocklisted")}</Badge>}
            {c.file?.releaseId === r.id && <Badge tone="ok">{t("current file")}</Badge>}
            {r.webUrl && (
              <a href={r.webUrl} target="_blank" rel="noreferrer" className="text-muted hover:text-accent-2">
                <ExternalLink className="size-3" />
              </a>
            )}
          </div>
        ))}
      </div>
    )}
    <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border pt-3">
      <span className="text-xs font-medium text-muted">{t("Read by")}</span>
      {c.readBy.map((r) => (
        <Badge key={r.readerId} tone={r.completed ? "ok" : "info"} title={r.completed ? `read ${relative(r.readAt)}` : `page ${r.page}`}>
          <Eye className="size-3" /> {accountKind === "user" ? (r.completed ? t("Read") : `${t("Page")} ${r.page}`) : r.reader}
        </Badge>
      ))}
      {c.readBy.length === 0 && <span className="text-xs text-muted">{t("Nobody yet")}</span>}
      <span className="ml-auto flex flex-wrap gap-1">
        <Button size="sm" onClick={() => onMark(c, true)}>{t("Mark read")}</Button>
        <Button size="sm" onClick={() => onMark(c, false)}>{t("Mark unread")}</Button>
        <Button size="sm" onClick={() => onMark(c, true, "previous")}>{t("Mark previous chapters read")}</Button>
        <Button size="sm" onClick={() => onMark(c, false, "previous")}>{t("Mark previous chapters unread")}</Button>
      </span>
    </div>
    {c.job?.error && <p className="mt-2 text-xs text-err">{t("Last error:") + " "}{c.job.error}</p>}
  </>;
}

function DecisionModal({ seriesId, chapter, onClose }: { seriesId: number; chapter: Chapter; onClose: () => void }) {
  const { data, error } = useQuery({
    queryKey: ["decision", seriesId, chapter.id],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/chapters/{chapterId}/decision", { params: { path: { id: seriesId, chapterId: chapter.id } } })),
  });
  const relName = (id?: number) => chapter.releases.find((r) => r.id === id);
  return (
    <Modal open onClose={onClose} title={`Chapter ${chapter.number}: download decision`}>
      {error && <ErrorBox error={error} />}
      {!data && !error && <Loading />}
      {data && (
        <div className="flex flex-col gap-3 text-sm">
          {data.approved ? (
            <p>{t("Would download") + " "}<b>{data.approved.name}</b>{" " + t("from") + " "}<b>{data.approved.sourceName}</b>
              {data.approved.scanlator ? ` (${data.approved.scanlator})` : ""}
              {data.decision.isUpgrade ? tr(" as an upgrade") : ""}.
            </p>
          ) : (
            <p className="text-muted">{t("Nothing would be downloaded right now.")}</p>
          )}
          {data.decision.rejections.length > 0 && (
            <ul className="flex flex-col gap-1">
              {data.decision.rejections.map((r, i) => (
                <li key={i} className="flex items-start gap-2">
                  <Badge tone={r.temporary ? "warn" : "err"}>{r.temporary ? tr("temporary") : tr("rejected")}</Badge>
                  <span>
                    {r.releaseId ? <span className="text-muted">{relName(r.releaseId)?.sourceName ?? tr("release")}: </span> : null}
                    {r.reason}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Modal>
  );
}
