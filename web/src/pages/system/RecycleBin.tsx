import { t } from "../../lib/i18n/core";
import { Fragment, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight, FolderSearch, RotateCcw, Search, Sparkles, Trash2 } from "lucide-react";
import clsx from "clsx";
import { api, apiUrl, unwrap } from "../../api/client";
import { useTasks } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, Confirm, EmptyState, EnvLock, ErrorBox, Input, Loading, Modal, PageHeader, Select, Td, Th } from "../../components/ui";
import { bytes, date } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "../settings/useSettingsDoc";
import { RecycledDrawer, ReprocessModal } from "./RecycleDialogs";
import { REASONS, chapterText, daysLeft, fileName, processingText, reasonLabel, reasonTone, useRecycleActions, type Recycled } from "./recycle";

type Media = { recycleBinDays: number } & Record<string, unknown>;

const PAGE_SIZE = 50;
const keepChoices = [1, 3, 7, 14, 30, 90];

/** KeepFor edits Settings › Media › recycle bin retention in place. */
function KeepFor() {
  const doc = useSettingsDoc<Media>("media");
  const [custom, setCustom] = useState<number | null>(null);
  const days = doc.value?.recycleBinDays ?? 0;
  const env = doc.lock("recycleBinDays");
  const choose = (v: string) => {
    if (!doc.value) return;
    if (v === "custom") return setCustom(days || 7);
    void doc.save({ ...doc.value, recycleBinDays: Number(v) });
  };
  const known = days === 0 || keepChoices.includes(days);
  return (
    <label className="flex items-center gap-2 text-sm text-muted">
      {t("Keep files for")}
      <Select aria-label={t("Keep recycled files for")} className="w-32" value={known ? String(days) : "current"} disabled={!doc.value || !!env || doc.saving} onChange={(e) => choose(e.target.value)}>
        {keepChoices.map((d) => (
          <option key={d} value={d}>{t("{days} days", { days: d })}</option>
        ))}
        {!known && <option value="current">{t("{days} days", { days })}</option>}
        <option value="0">{t("Forever")}</option>
        <option value="custom">{t("Custom…")}</option>
      </Select>
      <EnvLock env={env} />
      <Modal
        open={custom !== null}
        onClose={() => setCustom(null)}
        title={t("Keep recycled files for")}
        size="sm"
        footer={
          <>
            <Button onClick={() => setCustom(null)}>{t("Cancel")}</Button>
            <Button variant="primary" disabled={!custom || custom < 1} onClick={() => (doc.value && void doc.save({ ...doc.value, recycleBinDays: custom! }), setCustom(null))}>{t("Save")}</Button>
          </>
        }
      >
        <div className="flex items-center gap-2 text-sm">
          <Input type="number" min={1} className="w-28" aria-label={t("Days")} value={custom ?? ""} onChange={(e) => setCustom(e.target.valueAsNumber || 0)} />
          {t("days")}
        </div>
      </Modal>
    </label>
  );
}

function PurgeIn({ item, paused }: { item: Recycled; paused: boolean }) {
  if (paused) return <span className="text-warn">{t("Paused")}</span>;
  const left = daysLeft(item.purgeAt);
  if (left === null) return <span className="text-muted">{t("Never")}</span>;
  return <span className={left <= 2 ? "text-warn" : "text-muted"} title={date(item.purgeAt)}>{left === 0 ? t("Today") : t("{days} days", { days: left })}</span>;
}

function Pages({ item }: { item: Recycled }) {
  if (item.kind === "folder") return <span className="text-muted">—</span>;
  return (
    <span className="whitespace-nowrap">
      {item.current ? t("{from} → {to} now", { from: item.pageCount, to: item.current.pageCount }) : t("{count} · not in library", { count: item.pageCount })}
      {item.countChanged && <span className="ml-1.5"><Badge tone="warn">{t("Count changed")}</Badge></span>}
    </span>
  );
}

export function RecycleBinPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [reason, setReason] = useState("");
  const [page, setPage] = useState(1);
  const [selected, setSelected] = useState<Map<number, Recycled>>(new Map());
  const [open, setOpen] = useState<Recycled | null>(null);
  const [reprocess, setReprocess] = useState<Recycled[] | null>(null);
  const [confirm, setConfirm] = useState<"delete" | "empty" | null>(null);
  const { restore, remove } = useRecycleActions();
  const { data: tasks } = useTasks();
  const housekeepingPaused = !!tasks?.find((x) => x.name === "Housekeeping")?.paused;

  useEffect(() => {
    const id = setTimeout(() => (setSearch(q.trim()), setPage(1)), 300);
    return () => clearTimeout(id);
  }, [q]);

  const { data, isLoading, error } = useQuery({
    queryKey: ["recycle-bin", "list", { search, reason, page }],
    queryFn: () => unwrap(api.GET("/api/v1/recycle-bin", { params: { query: { q: search || undefined, reason: reason ? (reason as (typeof REASONS)[number]) : undefined, page, pageSize: PAGE_SIZE } } })),
    placeholderData: (prev) => prev,
  });
  const rescan = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/recycle-bin/rescan")),
    onSuccess: (r) => toast.success(r.indexed ? t("Found {count} new files", { count: r.indexed }) : t("No new files found")),
    onError: (e) => toast.fromError(e, t("Could not rescan")),
    onSettled: () => qc.invalidateQueries({ queryKey: ["recycle-bin"] }),
  });

  const items = data?.items ?? [];
  const groups = useMemo(() => {
    const out: { key: string; seriesId?: number; title: string; rows: Recycled[] }[] = [];
    for (const r of items) {
      const key = `${r.seriesId ?? 0}:${r.seriesTitle}`;
      let g = out.find((x) => x.key === key);
      if (!g) out.push((g = { key, seriesId: r.seriesId, title: r.seriesTitle || t("Unknown series"), rows: [] }));
      g.rows.push(r);
    }
    return out;
  }, [items]);
  const groupMeta = (seriesId?: number, title?: string) => data?.groups.find((g) => (g.seriesId ?? 0) === (seriesId ?? 0) && g.seriesTitle === title);
  const pages = Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE));
  const reasonTotal = Object.values(data?.reasons ?? {}).reduce((a, b) => a + b, 0);

  const toggle = (rows: Recycled[], on: boolean) =>
    setSelected((s) => {
      const next = new Map(s);
      for (const r of rows) on ? next.set(r.id, r) : next.delete(r.id);
      return next;
    });
  const chosen = [...selected.values()];
  const chosenSeries = [...new Set(chosen.map((r) => r.seriesTitle || t("Unknown series")))];
  const reprocessable = chosen.filter((r) => r.kind === "file" && r.chapterId);
  const clear = () => setSelected(new Map());
  const allOnPage = items.length > 0 && items.every((r) => selected.has(r.id));

  return (
    <>
      <PageHeader
        title={t("Recycle bin")}
        subtitle={
          data
            ? `${t("{count} files · {size}", { count: data.total, size: bytes(data.size) })} · ${housekeepingPaused ? t("Housekeeping is paused, so nothing is purged.") : t("older files are purged by Housekeeping.")}`
            : undefined
        }
        actions={
          <>
            <KeepFor />
            <span className="mx-1 hidden h-5 w-px bg-border sm:block" />
            <Button icon={<FolderSearch className="size-4" />} loading={rescan.isPending} onClick={() => rescan.mutate()}>{t("Rescan folder")}</Button>
            <Button variant="ghost" className="text-err" icon={<Trash2 className="size-4" />} disabled={!data?.total} onClick={() => setConfirm("empty")}>{t("Empty recycle bin")}</Button>
          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <label className="relative w-full sm:w-72">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted" />
          <Input type="search" aria-label={t("Search")} placeholder={t("Search series or chapter")} className="pl-8" value={q} onChange={(e) => setQ(e.target.value)} />
        </label>
        <div className="flex flex-wrap gap-1.5">
          {[{ value: "", label: t("All reasons"), count: reasonTotal }, ...REASONS.filter((r) => data?.reasons[r]).map((r) => ({ value: r, label: reasonLabel(r), count: data!.reasons[r] }))].map((c) => (
            <button
              key={c.value}
              type="button"
              aria-pressed={reason === c.value}
              onClick={() => (setReason(c.value), setPage(1))}
              className={clsx("h-8 rounded-full border px-3 text-sm", reason === c.value ? "border-border bg-panel-2 text-fg" : "border-border text-muted hover:text-fg")}
            >
              {c.value ? `${c.label} · ${c.count}` : c.label}
            </button>
          ))}
        </div>
      </div>

      {chosen.length > 0 && (
        <div className="sticky top-0 z-10 mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-primary/40 bg-panel px-3 py-2">
          <span className="text-sm font-medium">{t("{count} selected", { count: chosen.length })}</span>
          <span className="text-sm text-muted">
            {bytes(chosen.reduce((n, r) => n + r.size, 0))} · {chosenSeries.length === 1 ? t("all from {series}", { series: chosenSeries[0] }) : t("{count} series", { count: chosenSeries.length })}
          </span>
          <span className="flex-1" />
          <Button size="sm" variant="ghost" onClick={clear}>{t("Clear")}</Button>
          <Button size="sm" icon={<RotateCcw className="size-3.5" />} loading={restore.isPending} onClick={() => restore.mutate(chosen.map((r) => r.id), { onSuccess: clear })}>{t("Restore")}</Button>
          <Button size="sm" variant="primary" icon={<Sparkles className="size-3.5" />} disabled={reprocessable.length === 0} onClick={() => setReprocess(reprocessable)}>{t("Reprocess from these…")}</Button>
          <Button size="sm" variant="ghost" className="text-err" icon={<Trash2 className="size-3.5" />} onClick={() => setConfirm("delete")}>{t("Delete now")}</Button>
        </div>
      )}

      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && data.total === 0 && (
        <EmptyState title={search || reason ? t("Nothing matches") : t("The recycle bin is empty")}>
          {t("Files replaced by upgrades or reprocessing, cleaned up or deleted land here, so you can put them back.")}
        </EmptyState>
      )}
      {items.length > 0 && (
        <div className="overflow-x-auto rounded-lg border border-border">
          <table className="w-full text-left text-sm">
            <thead>
              <tr>
                <Th className="w-8"><input type="checkbox" aria-label={t("Select all")} checked={allOnPage} onChange={(e) => toggle(items, e.target.checked)} /></Th>
                <Th>{t("Chapter")}</Th>
                <Th>{t("Why it is here")}</Th>
                <Th>{t("Pages")}</Th>
                <Th>{t("Processing")}</Th>
                <Th>{t("Size")}</Th>
                <Th>{t("Recycled")}</Th>
                <Th>{t("Purged in")}</Th>
                <Th className="w-10" />
              </tr>
            </thead>
            <tbody>
              {groups.map((g) => {
                const meta = groupMeta(g.seriesId, g.rows[0].seriesTitle);
                return (
                  <Fragment key={g.key}>
                    <tr>
                      <td colSpan={9} className="border-b border-border/60 bg-panel px-3 py-2">
                        <div className="flex items-center gap-2.5">
                          <input type="checkbox" aria-label={t("Select {series}", { series: g.title })} checked={g.rows.every((r) => selected.has(r.id))} onChange={(e) => toggle(g.rows, e.target.checked)} />
                          <Cover src={g.seriesId ? apiUrl(`/api/v1/series/${g.seriesId}/cover`) : undefined} alt="" className="aspect-[2/3] w-6 shrink-0 rounded-sm" />
                          <span className="font-semibold">{g.title}</span>
                          {meta && <span className="text-xs text-muted">{t("{count} files · {size}", { count: meta.count, size: bytes(meta.size) })}</span>}
                        </div>
                      </td>
                    </tr>
                    {g.rows.map((r) => (
                      <tr key={r.id} className={clsx("cursor-pointer hover:bg-panel-2/40", selected.has(r.id) && "bg-accent/5")} onClick={() => setOpen(r)}>
                        <Td>
                          <input type="checkbox" aria-label={t("Select {chapter}", { chapter: chapterText(r) })} checked={selected.has(r.id)} onClick={(e) => e.stopPropagation()} onChange={(e) => toggle([r], e.target.checked)} />
                        </Td>
                        <Td>
                          <div className="font-medium">{chapterText(r)}</div>
                          <div className="max-w-72 truncate text-xs text-muted" title={r.originalRelativePath}>{fileName(r)}</div>
                        </Td>
                        <Td><Badge tone={reasonTone(r.reason)}>{reasonLabel(r.reason)}</Badge></Td>
                        <Td><Pages item={r} /></Td>
                        <Td className="text-muted">{r.kind === "folder" ? "—" : processingText(r.file)}</Td>
                        <Td className="whitespace-nowrap text-muted">{bytes(r.size)}</Td>
                        <Td className="whitespace-nowrap text-muted"><span title={r.recycledAt}>{date(r.recycledAt)}</span></Td>
                        <Td className="whitespace-nowrap"><PurgeIn item={r} paused={housekeepingPaused} /></Td>
                        <Td>
                          <button type="button" aria-label={t("Open")} className="inline-flex size-7 items-center justify-center rounded-md text-muted hover:bg-panel-2 hover:text-fg" onClick={(e) => (e.stopPropagation(), setOpen(r))}>
                            <ChevronRight className="size-4" />
                          </button>
                        </Td>
                      </tr>
                    ))}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {data && data.total > 0 && (
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
          <span>{t("Showing {shown} of {count} files", { shown: items.length, count: data.total })}</span>
          {pages > 1 && (
            <span className="flex items-center gap-2">
              <Button size="sm" variant="ghost" icon={<ChevronLeft className="size-3.5" />} disabled={page <= 1} onClick={() => setPage(page - 1)}>{t("Previous")}</Button>
              {t("Page {page} of {pages}", { page, pages })}
              <Button size="sm" variant="ghost" disabled={page >= pages} onClick={() => setPage(page + 1)}>{t("Next")}<ChevronRight className="size-3.5" /></Button>
            </span>
          )}
        </div>
      )}

      {open && <RecycledDrawer item={open} housekeepingPaused={housekeepingPaused} onClose={() => setOpen(null)} onReprocess={(r) => (setOpen(null), setReprocess([r]))} />}
      {reprocess && <ReprocessModal items={reprocess} onClose={() => (setReprocess(null), clear())} />}
      <Confirm
        open={confirm !== null}
        title={confirm === "empty" ? t("Empty the recycle bin?") : t("Delete {count} files now?", { count: chosen.length })}
        message={confirm === "empty" ? t("Every file in the recycle bin is removed from disk. This can't be undone.") : t("They are removed from disk and can't be restored.")}
        confirmLabel={confirm === "empty" ? t("Empty recycle bin") : t("Delete now")}
        danger
        loading={remove.isPending}
        onClose={() => setConfirm(null)}
        onConfirm={() =>
          remove.mutate(confirm === "empty" ? { all: true } : { ids: chosen.map((r) => r.id) }, {
            onSuccess: () => (setConfirm(null), clear()),
          })
        }
      />
    </>
  );
}
