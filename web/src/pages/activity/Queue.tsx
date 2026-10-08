import { t as tr, t } from "../../lib/i18n/core";
import { useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, ArrowDownToLine, ArrowUpToLine, Ban, ListOrdered, Pause, Play, RotateCw, Trash2 } from "lucide-react";
import { api, ApiError, unwrap, type Job } from "../../api/client";
import { useQueue } from "../../api/queries";
import { Badge, Button, Confirm, EmptyState, ErrorBox, Input, Loading, Menu, PageHeader, Progress, Switch, Table, Td, Th } from "../../components/ui";
import { isPending, isRunning, queueGroups, queuePositions, selectionNeighbor } from "../../lib/queueOrder";
import { relative } from "../../lib/format";
import { useListParam, useQueryParam } from "../../lib/urlState";
import { useToast } from "../../lib/toast";
import { useAccount } from "../../lib/account";
import { describe, useLiveProgress } from "../../lib/liveProgress";

const tone = (s: string) => (s === "completed" ? "ok" : s === "failed" ? "err" : s === "paused" ? "warn" : s === "queued" ? "default" : "info");
// Finished chapters live in History, so the queue shows only what still
// needs something: waiting, running and failed jobs.
const statuses = ["downloading", "processing", "importing", "queued", "paused", "failed"] as const;
const statusLabel = (s: string) => ({ downloading: t("downloading"), processing: t("processing"), importing: t("importing"), queued: t("queued"), paused: t("paused"), failed: t("failed"), completed: t("completed") } as Record<string, string>)[s] ?? s;
type Action = "pause" | "resume" | "retry" | "remove" | "blocklist" | "top" | "bottom" | "before" | "after" | "sort";

const groupKey = "mangarr:queue-group";
const storedGroup = () => {
  try {
    return localStorage.getItem(groupKey) === "flat" ? "flat" : "series";
  } catch {
    return "series";
  }
};

export function QueuePage({ mode }: { mode: "downloads" | "processing" }) {
  const qc = useQueryClient();
  const toast = useToast();
  // watching without library.manage: the queue as it is, nothing to change
  const manage = useAccount().can("library.manage");
  const [status, setStatus] = useListParam("status");
  const [q, setQ] = useQueryParam("q");
  const [pageStr, setPage] = useListParam("page", "1");
  // grouped by series unless you switched it off (remembered per browser)
  const [groupParam, setGroupParam] = useListParam("group", storedGroup());
  const group = groupParam === "series";
  const setGroup = (on: boolean) => {
    const v = on ? "series" : "flat";
    try {
      localStorage.setItem(groupKey, v);
    } catch {
      /* private mode: the URL still carries it */
    }
    setGroupParam(v);
  };
  const page = Number(pageStr) || 1;
  const pageSize = 100;
  const filter = { status: status ? status.split(",") : [...statuses], kind: mode === "processing" ? "reprocess" as const : "download" as const, q: q || undefined, includeDone: true };
  const { data, isLoading, isFetching, isPlaceholderData, error } = useQueue({ ...filter, page, pageSize });
  const liveMap = useLiveProgress();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [allMatching, setAllMatching] = useState(false);
  const [lastClicked, setLastClicked] = useState<number | null>(null);
  const [confirm, setConfirm] = useState<{ action: Action; label: string } | null>(null);
  const [moving, setMoving] = useState(false);
  const moveLock = useRef(false);
  const [announcement, setAnnouncement] = useState("");
  const items = data?.items ?? [];
  const reorderDisabled = moving || isFetching || isPlaceholderData;
  const total = data?.total ?? 0;
  const state = data?.state;

  const refresh = () => Promise.all([
    qc.invalidateQueries({ queryKey: ["queue"] }),
    qc.invalidateQueries({ queryKey: ["series"] }),
  ]);
  const resetSelection = () => (setSelected(new Set()), setAllMatching(false), setLastClicked(null));
  const queueChanged = () => {
    const message = t("Queue changed. Refresh and try again.");
    setAnnouncement(message);
    toast.info(message);
  };
  const run = async (action: Action, ids?: number[], anchorId?: number) => {
    if (moveLock.current) return;
    moveLock.current = true;
    setMoving(true);
    try {
      const body = allMatching && !ids ? { action, filter: { statuses: filter.status, kind: filter.kind, q: filter.q, includeDone: true } } : { action, ids: ids ?? [...selected], anchorId };
      const r = await unwrap(api.POST("/api/v1/queue/bulk", { body }));
      // failed entries whose chapter is already queued stay failed
      const skipped = r.skipped ? t("Skipped, chapter already in the queue: {count}", { count: r.skipped }) : "";
      const message = r.affected ? [t("{count} queue entries updated", { count: r.affected }), skipped].filter(Boolean).join(". ")
        : skipped || t("Queue changed. Refresh and try again.");
      toast.success(message);
      setAnnouncement(message);
      if (!ids) resetSelection();
    } catch (e) {
      toast.fromError(e);
      setAnnouncement(t("Could not reorder queue. Try again."));
    } finally {
      await refresh();
      moveLock.current = false;
      setMoving(false);
    }
  };
  // Moves the selected pending jobs one place past their pending neighbour,
  // keeping their order. At a page edge it looks at the neighbouring page.
  const moveSelected = async (direction: -1 | 1) => {
    if (reorderDisabled || moveLock.current) return;
    let anchor = selectionNeighbor(items, selected, direction);
    if (!anchor) {
      moveLock.current = true;
      setMoving(true);
      try {
        const adjacent = await unwrap(api.GET("/api/v1/queue", { params: { query: {
          ...filter, page: page + direction, pageSize, revision: data?.revision,
        } } }));
        anchor = adjacent.items.filter(isPending).at(direction === -1 ? -1 : 0);
        if (!anchor) queueChanged();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409) {
          queueChanged();
        } else toast.fromError(e);
      } finally {
        if (!anchor) await refresh();
        moveLock.current = false;
        setMoving(false);
      }
    }
    const ids = items.filter((job) => isPending(job) && selected.has(job.id)).map((job) => job.id);
    if (anchor && ids.length) await run(direction === -1 ? "before" : "after", ids, anchor.id);
  };
  const countStatuses = (values: string[]) => values.reduce((sum, value) =>
    sum + (!filter.status || filter.status.includes(value) ? data?.counts?.[value] ?? 0 : 0), 0);
  const runningCount = countStatuses(["importing", "processing", "downloading"]);
  const pendingEnd = runningCount + countStatuses(["queued", "paused"]);
  const selectedJobs = allMatching ? items : items.filter((job) => selected.has(job.id));
  const selectedPending = selectedJobs.filter(isPending);
  const canMove = (direction: -1 | 1) => !allMatching && selectedPending.length > 0 && (!!selectionNeighbor(items, selected, direction)
    || (direction === -1 ? (page - 1) * pageSize > runningCount : page * pageSize < pendingEnd));
  const positions = queuePositions(items, (page - 1) * pageSize, runningCount);
  const pauseAll = async (minutes?: number) => {
    try {
      await unwrap(minutes === undefined ? api.POST("/api/v1/queue/resume") : api.POST("/api/v1/queue/pause", { body: { minutes } }));
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };


  // click selects one row; shift+click selects the range from the last click
  const toggle = (idx: number, shift: boolean) => {
    setAllMatching(false);
    const id = items[idx].id;
    setSelected((cur) => {
      const next = new Set(cur);
      const lastIndex = items.findIndex((job) => job.id === lastClicked);
      if (shift && lastIndex >= 0) {
        const [a, b] = [Math.min(lastIndex, idx), Math.max(lastIndex, idx)];
        const on = !cur.has(id);
        for (let i = a; i <= b; i++) on ? next.add(items[i].id) : next.delete(items[i].id);
      } else if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
    setLastClicked(id);
  };
  const pageAllSelected = items.length > 0 && items.every((j) => selected.has(j.id));
  const count = allMatching ? total : selected.size;
  const morePages = total > items.length;
  // everything matching the filter, across pages; a single page is just its rows
  const selectAll = () => {
    setSelected(new Set(items.map((j) => j.id)));
    setAllMatching(morePages);
  };
  const selectPage = () => (setAllMatching(false), setSelected(new Set(items.map((j) => j.id))));
  // a paused queue shows its waiting jobs as paused, not "queued"
  const shownStatus = (j: Job) => (j.status === "queued" && state?.paused ? "paused" : j.status);
  const sortDisabled = allMatching ? total < 2 : selectedPending.length < 2;
  const handPicked = allMatching ? t("Moving up or down works on a hand-picked selection") : undefined;

  const groups = useMemo(() => group ? queueGroups(items) : null, [items, group]);

  const row = (j: Job, idx: number) => {
    const live = j.status === "completed" || j.status === "failed" ? undefined : (liveMap.get(j.id) ?? j.live);
    return (
    <tr key={j.id} data-queue-job={j.id} className={selected.has(j.id) || allMatching ? "bg-accent/5" : ""}>
      {manage && (
        <Td className="w-8">
          <input type="checkbox" aria-label={t("Select")} checked={allMatching || selected.has(j.id)} onChange={() => undefined} onClick={(e) => toggle(idx, e.shiftKey)} />
        </Td>
      )}
      <Td className="w-16 whitespace-nowrap tabular-nums">
        <QueuePosition job={j} position={positions.get(j.id)} />
      </Td>
      {!group && (
        <Td>
          <Link to={`/series/${j.seriesId}`} className="font-medium hover:text-accent-2">
            {j.seriesTitle}
          </Link>
        </Td>
      )}
      <Td>
        {j.chapter}
        {j.isUpgrade && (
          <span className="ml-1.5">
            <Badge tone="info">{t("upgrade")}</Badge>
          </span>
        )}
        {j.kind === "reprocess" && (
          <span className="ml-1.5">
            <Badge tone="accent">{t("process")}</Badge>
          </span>
        )}
      </Td>
      <Td className="text-muted">
        {j.sourceName}
        {j.scanlator && ` · ${j.scanlator}`}
      </Td>
      <Td>
        <Badge tone={tone(shownStatus(j))}>{statusLabel(shownStatus(j))}</Badge>
        {j.attempt > 0 && j.status !== "completed" && <span className="ml-1 text-xs text-muted">{t("try") + " "}{j.attempt + 1}</span>}
        {j.worker && (
          <Badge tone="info" title={t("Being done on this worker")}>
            {j.worker}
          </Badge>
        )}
        {j.error && (
          <div className="mt-1 max-w-sm truncate text-xs text-err" title={j.error}>
            {j.error}
          </div>
        )}
      </Td>
      <Td className="w-48">
        {live ? (
          <>
            <Progress value={live.total > 0 ? (live.done / live.total) * 100 : 0} tone="accent" />
            <div className="mt-1 text-xs text-muted">{describe(live)}</div>
          </>
        ) : j.status === "queued" || j.status === "paused" ? (
          <span className="text-xs text-muted">{j.status === "paused" || state?.paused ? t("paused") : t("waiting")}</span>
        ) : (
          <>
            <Progress value={j.progress} tone={j.status === "failed" ? "err" : j.status === "completed" ? "ok" : "accent"} />
            <div className="mt-1 text-xs text-muted">
              {j.pagesDone}/{j.pagesTotal}{" " + t("pages")}</div>
          </>
        )}
      </Td>
      <Td className="whitespace-nowrap text-muted">{relative(j.updatedAt)}</Td>
    </tr>
    );
  };

  return (
    <>
      <PageHeader
        title={mode === "processing" ? t("Processing queue") : t("Download queue")}
        docTitle={`${mode === "processing" ? t("Processing queue") : t("Download queue")}${total > 0 ? ` (${total})` : ""}`}
        subtitle={mode === "processing" ? t("Every chapter waiting to be upscaled or encoded, in processing order") : t("Chapters waiting to be downloaded and imported, in download order")}
        actions={
          <>
            {manage && !state?.paused && (
              <Menu label={t("Pause")} icon={<Pause className="size-4" />} align="right" items={[
                { label: t("for 1 hour"), onSelect: () => void pauseAll(60) },
                { label: t("for 6 hours"), onSelect: () => void pauseAll(360) },
                { label: t("for 24 hours"), onSelect: () => void pauseAll(1440) },
                { label: t("until resumed"), onSelect: () => void pauseAll(0) },
              ]} />
            )}
          </>
        }
      />
      {state?.paused && (
        <div role="status" className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-warn/40 bg-warn/10 px-4 py-3 text-sm">
          <Pause className="size-4 shrink-0 text-warn" />
          <span className="flex-1">
            <span className="font-medium">{t("Queue paused")}</span>
            <span className="text-muted">
              {" · "}
              {state.pausedUntil ? t("until {time}", { time: new Date(state.pausedUntil).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) }) : t("until you resume")}
              {" · "}
              {t("{count} waiting", { count: data?.counts?.queued ?? 0 })}
            </span>
          </span>
          {manage && <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} onClick={() => pauseAll()}>{t("Resume queue")}</Button>}
        </div>
      )}
      {state?.quiet?.windows?.length ? (
        <p className="mb-3 text-sm text-warn">{t("Quiet hours (")}{state.quiet.windows.join(", ")}):{" "}
          {[state.quiet.pauseDownloads && "downloads paused", state.quiet.pauseProcessing && "processing paused", state.quiet.throttle && `${state.quiet.throttle} throttling`]
            .filter(Boolean)
            .join(", ")}
        </p>
      ) : null}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <button
          onClick={() => (setStatus(""), setPage("1"), resetSelection())}
          className={`rounded-full border px-3 py-1 text-xs ${!status ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg"}`}
          title={t("Show every status")}
        >{t("all") + " "}{statuses.reduce((sum, s) => sum + (data?.counts?.[s] ?? 0), 0)}
        </button>
        {statuses.map((s) => {
          const n = data?.counts?.[s] ?? 0;
          const on = status.split(",").includes(s);
          if (!n && !on) return null;
          return (
            <button
              key={s}
              onClick={() => (setStatus(on ? status.split(",").filter((x) => x !== s && x).join(",") : [...status.split(",").filter(Boolean), s].join(",")), setPage("1"), resetSelection())}
              className={`rounded-full border px-3 py-1 text-xs ${on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg"}`}
            >
              {statusLabel(s)} {n}
            </button>
          );
        })}
        <Input className="max-w-xs" placeholder={t("Filter by series…")} defaultValue={q} onChange={(e) => (setQ(e.target.value), setPage("1"), resetSelection())} />
        <Switch checked={group} onChange={setGroup} label={t("Group by series")} />
      </div>

      <p role="status" aria-live="polite" className="sr-only">{announcement}</p>
      {manage && items.length > 0 && (
        <div className={`sticky top-0 z-10 mb-3 flex flex-wrap items-center gap-2 rounded-lg border bg-panel p-2 text-sm ${count > 0 ? "border-accent/40 shadow" : "border-border"}`}>
          <span className={count > 0 ? "font-medium" : "text-muted"}>{count > 0 ? t("{count} selected", { count }) : t("Nothing selected")}</span>
          {!allMatching && !(pageAllSelected && !morePages) && (
            <Button size="sm" variant="ghost" onClick={selectAll}>{morePages ? t("Select all {count}", { count: total }) : t("Select all")}</Button>
          )}
          {morePages && !pageAllSelected && (
            <Button size="sm" variant="ghost" onClick={selectPage}>{t("Select page")}</Button>
          )}
          {count > 0 && <Button size="sm" variant="ghost" onClick={resetSelection}>{t("Clear")}</Button>}
          {/* on phones the actions appear once something is selected */}
          <div className={`ml-auto flex-wrap gap-1 ${count > 0 ? "flex" : "hidden md:flex"}`}>
            {count > 0 && (
              <span className="md:hidden">
                <Menu label={t("Move")} icon={<ListOrdered className="size-3.5" />} items={[
                  { label: t("Up"), onSelect: () => void moveSelected(-1), hidden: reorderDisabled || !canMove(-1) },
                  { label: t("Down"), onSelect: () => void moveSelected(1), hidden: reorderDisabled || !canMove(1) },
                  { label: t("Top"), onSelect: () => void run("top"), hidden: reorderDisabled || !selectedPending.length },
                  { label: t("Bottom"), onSelect: () => void run("bottom"), hidden: reorderDisabled || !selectedPending.length },
                  { label: t("Sort by chapter"), onSelect: () => void run("sort"), hidden: reorderDisabled || sortDisabled },
                ]} />
              </span>
            )}
            <span className="hidden flex-wrap gap-1 md:flex">
              <Button size="sm" disabled={reorderDisabled || !canMove(-1)} title={handPicked} icon={<ArrowUp className="size-3.5" />} onClick={() => void moveSelected(-1)}>{t("Up")}</Button>
              <Button size="sm" disabled={reorderDisabled || !canMove(1)} title={handPicked} icon={<ArrowDown className="size-3.5" />} onClick={() => void moveSelected(1)}>{t("Down")}</Button>
              <Button size="sm" disabled={reorderDisabled || !selectedPending.length} icon={<ArrowUpToLine className="size-3.5" />} onClick={() => run("top")}>{t("Top")}</Button>
              <Button size="sm" disabled={reorderDisabled || !selectedPending.length} icon={<ArrowDownToLine className="size-3.5" />} onClick={() => run("bottom")}>{t("Bottom")}</Button>
              <Button size="sm" disabled={reorderDisabled || sortDisabled} title={t("Put the selected chapters in chapter order, in the places they already hold")} icon={<ListOrdered className="size-3.5" />} onClick={() => run("sort")}>{t("Sort by chapter")}</Button>
            </span>
            <span className="mx-1 hidden w-px self-stretch bg-border md:block" />
            <Button size="sm" disabled={!selectedJobs.some((job) => job.status === "queued" || isRunning(job))} icon={<Pause className="size-3.5" />} onClick={() => run("pause")}>{t("Pause")}</Button>
            <Button size="sm" disabled={!selectedJobs.some((job) => job.status === "paused")} icon={<Play className="size-3.5" />} onClick={() => run("resume")}>{t("Resume")}</Button>
            <Button size="sm" disabled={!selectedJobs.some((job) => job.status === "failed")} icon={<RotateCw className="size-3.5" />} onClick={() => run("retry")}>{t("Retry")}</Button>
            <Button size="sm" disabled={!count} icon={<Ban className="size-3.5" />} onClick={() => setConfirm({ action: "blocklist", label: t("Remove and blocklist") })}>{t("Blocklist")}</Button>
            <Button size="sm" variant="danger" disabled={!count} icon={<Trash2 className="size-3.5" />} onClick={() => setConfirm({ action: "remove", label: t("Remove") })}>{t("Remove")}</Button>
          </div>
        </div>
      )}
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && total === 0 && <EmptyState title={t("Queue is empty")}>{t("New chapters are queued automatically when a monitored series gets an update.")}</EmptyState>}
      {items.length > 0 && (
        <ul className="flex flex-col gap-2 md:hidden">
          {items.map((j, i) => <QueueCard key={j.id} manage={manage} job={j} status={shownStatus(j)} position={positions.get(j.id)} selected={allMatching || selected.has(j.id)} onToggle={(shift) => toggle(i, shift)} live={j.status === "completed" || j.status === "failed" ? undefined : (liveMap.get(j.id) ?? j.live)} />)}
        </ul>
      )}
      {items.length > 0 && (
        <div className="hidden overflow-x-auto md:block">
          <Table>
            <thead>
              <tr>
                {manage && (
                  <Th className="w-8">
                    <input
                      type="checkbox"
                      aria-label={t("Select page")}
                      checked={pageAllSelected || allMatching}
                      onChange={() => (pageAllSelected ? resetSelection() : setSelected(new Set(items.map((j) => j.id))))}
                    />
                  </Th>
                )}
                <Th>#</Th>
                {!group && <Th>{t("Series")}</Th>}
                <Th>{t("Chapter")}</Th>
                <Th>{t("Source")}</Th>
                <Th>{t("Status")}</Th>
                <Th>{t("Progress")}</Th>
                <Th>{t("Updated")}</Th>
              </tr>
            </thead>
            <tbody>
              {groups
                ? groups.map((g) => (
                    <SeriesGroup
                      key={g.jobs[0].job.id}
                      manage={manage}
                      seriesId={g.seriesId}
                      title={g.title}
                      count={g.jobs.length}
                      seriesTotal={data?.seriesTotals?.[String(g.seriesId)] ?? g.jobs.length}
                      onPage={items.filter((j) => j.seriesId === g.seriesId).length}
                      selected={g.jobs.every(({ job }) => selected.has(job.id))}
                      onSelect={(on) =>
                        setSelected((cur) => {
                          const next = new Set(cur);
                          g.jobs.forEach(({ job }) => (on ? next.add(job.id) : next.delete(job.id)));
                          return next;
                        })
                      }
                    >
                      {g.jobs.map(({ job, idx }) => row(job, idx))}
                    </SeriesGroup>
                  ))
                : items.map((j, i) => row(j, i))}
            </tbody>
          </Table>
        </div>
      )}
      {total > pageSize && (
        <div className="mt-4 flex items-center justify-center gap-3 text-sm">
          <Button size="sm" disabled={page <= 1} onClick={() => setPage(String(page - 1))}>{t("Previous")}</Button>
          <span className="text-muted">{t("Page") + " "}{page}{" " + t("of") + " "}{Math.ceil(total / pageSize)}
          </span>
          <Button size="sm" disabled={page * pageSize >= total} onClick={() => setPage(String(page + 1))}>{t("Next")}</Button>
        </div>
      )}
      <Confirm
        open={!!confirm}
        title={confirm?.label ?? ""}
        danger
        confirmLabel={confirm?.label}
        message={
          confirm?.action === "blocklist"
            ? t("Remove {count} entries and blocklist their releases? Other sources may be tried.", { count })
            : count === 1 ? t("Remove this entry from the queue?") : t("Remove {count} entries from the queue?", { count })
        }
        onConfirm={async () => {
          if (confirm) await run(confirm.action);
          setConfirm(null);
        }}
        onClose={() => setConfirm(null)}
      />
    </>
  );
}

function SeriesGroup({
  manage,
  seriesId,
  title,
  count,
  seriesTotal,
  onPage,
  selected,
  onSelect,
  children,
}: {
  manage: boolean;
  seriesId: number;
  title: string;
  count: number;
  /** the series' entries in the whole queue, and how many this page shows */
  seriesTotal: number;
  onPage: number;
  selected: boolean;
  onSelect: (on: boolean) => void;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <tr className="bg-panel-2/60">
        {manage && (
          <Td className="w-8">
            <input type="checkbox" aria-label={`Select ${title}`} checked={selected} onChange={(e) => onSelect(e.target.checked)} />
          </Td>
        )}
        <Td colSpan={6}>
          <button className="mr-2 text-muted" onClick={() => setOpen(!open)} aria-label={open ? tr("Collapse") : tr("Expand")}>
            {open ? "▾" : "▸"}
          </button>
          <Link to={`/series/${seriesId}`} className="font-medium hover:text-accent-2">
            {title}
          </Link>
          <span className="ml-2 text-xs text-muted">
            {seriesTotal > onPage ? t("{shown} of {total} chapters", { shown: count, total: seriesTotal }) : t("{count} chapters", { count })}
          </span>
        </Td>
      </tr>
      {open && children}
    </>
  );
}

function QueuePosition({ job, position }: { job: Job; position?: number }) {
  if (isRunning(job)) return <Badge tone="info">{t("now")}</Badge>;
  if (position === undefined) return <span className="text-muted">—</span>;
  return <span className="font-medium">{position}</span>;
}

function QueueCard({ manage, job, position, selected, onToggle, live, status }: {
  manage: boolean; job: Job; position?: number; selected: boolean; onToggle: (shift: boolean) => void; live?: ReturnType<ReturnType<typeof useLiveProgress>["get"]>; status: string;
}) {
  return (
    <li className={`flex gap-3 rounded-lg border p-3 ${selected ? "border-accent/50 bg-accent/5" : "border-border bg-panel"}`}>
      {manage && <input type="checkbox" className="mt-1" aria-label={t("Select")} checked={selected} onChange={() => undefined} onClick={(e) => onToggle(e.shiftKey)} />}
      <div className="min-w-0 flex-1">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <Link to={`/series/${job.seriesId}`} className="block truncate font-medium hover:text-accent-2">{job.seriesTitle}</Link>
            <div className="truncate text-sm text-muted">{job.chapter}{job.sourceName && ` · ${job.sourceName}`}</div>
          </div>
          <QueuePosition job={job} position={position} />
        </div>
        <div className="mt-2 flex items-center gap-2">
          <Badge tone={tone(status)}>{statusLabel(status)}</Badge>
          {live ? <span className="flex-1"><Progress value={live.total > 0 ? (live.done / live.total) * 100 : 0} tone="accent" /></span>
            : job.status === "failed" || job.status === "completed" ? <span className="flex-1"><Progress value={job.progress} tone={job.status === "failed" ? "err" : "ok"} /></span>
            : <span className="text-xs text-muted">{relative(job.updatedAt)}</span>}
        </div>
        {job.error && <div className="mt-1 truncate text-xs text-err" title={job.error}>{job.error}</div>}
      </div>
    </li>
  );
}
