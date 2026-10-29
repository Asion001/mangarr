import { t } from "../../lib/i18n/core";
import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import clsx from "clsx";
import { Link } from "react-router";
import { ArrowDown, ArrowUp, Columns3, Star } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Series } from "../../api/client";
import { Badge, Button, Progress, Table, Td, Th } from "../../components/ui";
import { bytes, date, languageName, relative } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useToast } from "../../lib/toast";
import { outOf10 } from "./Ratings";

export type Sort = "title" | "added" | "latest" | "missing" | "size" | "read" | "progress" | "chapters" | "year" | "status" | "requested" | "rating";
export type Order = "asc" | "desc";
export const sorts: readonly Sort[] = ["title", "added", "latest", "missing", "progress", "chapters", "size", "read", "year", "rating", "status", "requested"];

/** descendingSorts list the biggest or newest first unless flipped (as the server does). */
const descendingSorts = new Set<Sort>(["added", "latest", "missing", "size", "read", "progress", "chapters", "year", "requested", "rating"]);
export const sortOrder = (sort: Sort, order: string): Order => (order === "asc" || order === "desc" ? order : descendingSorts.has(sort) ? "desc" : "asc");

export const sortLabels: Record<Sort, string> = {
  title: "Title",
  added: "Recently added",
  latest: "Latest chapter",
  missing: "Missing chapters",
  progress: "Downloaded %",
  chapters: "Chapters",
  size: "Size",
  read: "Recently read",
  year: "Year",
  status: "Status",
  requested: "Requested on",
  rating: "Rating",
};
export const sortLabel = (s: Sort) => t(sortLabels[s] as Parameters<typeof t>[0]);

export function statusTone(s: string) {
  return s === "ongoing" ? "ok" : s === "completed" ? "info" : s === "hiatus" ? "warn" : s === "cancelled" ? "err" : "default";
}

export function progressOf(s: Series) {
  const { fileCount, monitoredCount, missingCount } = s.stats;
  if (monitoredCount === 0) return { pct: 0, tone: "accent" as const };
  const pct = ((monitoredCount - missingCount) / monitoredCount) * 100;
  return { pct, tone: missingCount === 0 ? ("ok" as const) : fileCount === 0 ? ("err" as const) : ("warn" as const) };
}

/** editionIds are the series a library row stands for: its editions, or itself. */
const editionIds = (s: Series) => ((s.editions?.length ?? 0) > 0 ? (s.editions ?? []).map((e) => e.id) : [s.id]);

type Column = {
  id: string;
  label: string;
  /** sort is what clicking the header sorts by */
  sort?: Sort;
  /** requests: only for people who manage requests */
  requests?: boolean;
  className?: string;
};

const columns: Column[] = [
  { id: "monitored", label: "Monitored", className: "w-10" },
  { id: "status", label: "Status", sort: "status" },
  { id: "chapters", label: "Chapters", sort: "progress", className: "w-48" },
  { id: "missing", label: "Missing", sort: "missing" },
  { id: "read", label: "Read" },
  { id: "latest", label: "Latest", sort: "latest" },
  { id: "size", label: "Size", sort: "size" },
  { id: "lastRead", label: "Last read", sort: "read" },
  { id: "added", label: "Added", sort: "added" },
  { id: "requestedBy", label: "Requested by", sort: "requested", requests: true },
  { id: "languages", label: "Languages" },
  { id: "root", label: "Library folder" },
  { id: "year", label: "Year", sort: "year" },
  { id: "rating", label: "Rating", sort: "rating" },
  { id: "genres", label: "Genres" },
  { id: "sources", label: "Sources" },
  { id: "following", label: "Following" },
];
const columnLabel = (c: Column) => t(c.label as Parameters<typeof t>[0]);
export const defaultColumns = ["monitored", "status", "chapters", "latest", "size", "added"];
const columnsKey = "mangarr:library:columns";

/** useLibraryColumns is the table's columns in order: this browser's, else the account's, else the default. */
export function useLibraryColumns(saved: string[] | undefined, save: (ids: string[]) => void): [string[], (ids: string[]) => void] {
  const known = (ids: unknown) => (Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string" && columns.some((c) => c.id === id)) : []);
  const [local, setLocal] = useState<string[] | null>(() => {
    try {
      const raw = localStorage.getItem(columnsKey);
      return raw ? known(JSON.parse(raw)) : null;
    } catch {
      return null;
    }
  });
  const ids = local ?? (saved && saved.length ? known(saved) : defaultColumns);
  const set = (next: string[]) => {
    setLocal(next);
    try {
      localStorage.setItem(columnsKey, JSON.stringify(next));
    } catch {
      /* private mode */
    }
    save(next);
  };
  return [ids, set];
}

/** ColumnPicker turns the table's columns on and off and moves them. */
export function ColumnPicker({ value, onChange, requests }: { value: string[]; onChange: (ids: string[]) => void; requests: boolean }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const outside = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const escape = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  const offered = columns.filter((c) => !c.requests || requests);
  const shown = value.map((id) => offered.find((c) => c.id === id)).filter((c): c is Column => !!c);
  const more = offered.filter((c) => !value.includes(c.id));
  const move = (id: string, by: number) => {
    const i = value.indexOf(id);
    const j = i + by;
    if (i < 0 || j < 0 || j >= value.length) return;
    const next = [...value];
    [next[i], next[j]] = [next[j], next[i]];
    onChange(next);
  };
  const row = "flex h-8 items-center gap-2 rounded px-1.5 text-sm hover:bg-panel";
  return (
    <div ref={ref} className="relative">
      <Button size="sm" icon={<Columns3 className="size-3.5" />} aria-expanded={open} onClick={() => setOpen(!open)}>{t("Columns")}</Button>
      {open && (
        <div className="absolute right-0 top-full z-30 mt-2 max-h-[70vh] w-64 overflow-y-auto rounded-xl border border-border bg-panel-2 p-2 shadow-xl">
          <div className="px-1.5 pb-1 pt-1.5 text-[11px] uppercase tracking-wide text-muted">{t("Shown")}</div>
          <div className={clsx(row, "text-muted")}><input type="checkbox" checked disabled aria-label={t("Title")} />{t("Title")}</div>
          {shown.map((c, i) => (
            <div key={c.id} className={row}>
              <label className="flex flex-1 items-center gap-2">
                <input type="checkbox" checked onChange={() => onChange(value.filter((id) => id !== c.id))} />
                {columnLabel(c)}
              </label>
              <button type="button" aria-label={t("Move up")} disabled={i === 0} onClick={() => move(c.id, -1)} className="inline-flex size-6 items-center justify-center rounded text-muted hover:text-fg disabled:opacity-30"><ArrowUp className="size-3.5" /></button>
              <button type="button" aria-label={t("Move down")} disabled={i === shown.length - 1} onClick={() => move(c.id, 1)} className="inline-flex size-6 items-center justify-center rounded text-muted hover:text-fg disabled:opacity-30"><ArrowDown className="size-3.5" /></button>
            </div>
          ))}
          {more.length > 0 && <div className="px-1.5 pb-1 pt-2.5 text-[11px] uppercase tracking-wide text-muted">{t("More")}</div>}
          {more.map((c) => (
            <label key={c.id} className={row}>
              <input type="checkbox" checked={false} onChange={() => onChange([...value, c.id])} />
              {columnLabel(c)}
            </label>
          ))}
          <div className="mt-1.5 border-t border-border px-1.5 pt-2">
            <button type="button" className="text-xs text-accent-2 hover:underline" onClick={() => onChange(defaultColumns)}>{t("Reset to default")}</button>
          </div>
        </div>
      )}
    </div>
  );
}

/** MonitorStar shows whether a title is monitored; people who edit the library click it to change that. */
function MonitorStar({ s, edit }: { s: Series; edit: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const label = s.monitored ? t("Monitored") : t("Not monitored");
  const icon = <Star className={clsx("size-4", s.monitored ? "fill-current text-accent-2" : "text-muted")} />;
  if (!edit) return <span title={label} aria-label={label} className="inline-flex size-6 items-center justify-center">{icon}</span>;
  const flip = async () => {
    setBusy(true);
    try {
      await unwrap(api.POST("/api/v1/series/editor", { body: { seriesIds: editionIds(s), monitored: !s.monitored } }));
      await qc.invalidateQueries({ queryKey: ["series"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <button type="button" disabled={busy} aria-pressed={s.monitored} title={s.monitored ? t("Stop monitoring") : t("Monitor")} aria-label={s.monitored ? t("Stop monitoring") : t("Monitor")} onClick={() => void flip()} className="inline-flex size-6 items-center justify-center rounded hover:bg-panel-2 disabled:opacity-50">
      {icon}
    </button>
  );
}

function cell(id: string, s: Series, edit: boolean, roots: Map<number, string>): ReactNode {
  switch (id) {
    case "monitored":
      return <MonitorStar s={s} edit={edit} />;
    case "status":
      return <Badge tone={statusTone(s.status)}>{s.status}</Badge>;
    case "chapters": {
      const p = progressOf(s);
      return (
        <div className="flex items-center gap-2">
          <div className="w-24"><Progress value={p.pct} tone={p.tone} /></div>
          <span className="text-xs text-muted">{s.stats.fileCount}/{s.stats.chapterCount}</span>
        </div>
      );
    }
    case "missing":
      return s.stats.missingCount > 0 ? <Badge tone="warn">{s.stats.missingCount}</Badge> : <span className="text-muted">0</span>;
    case "read":
      return `${s.stats.readCount}/${s.stats.chapterCount}`;
    case "latest":
      return s.stats.lastChapter || "—";
    case "size":
      return bytes(s.stats.sizeOnDisk);
    case "lastRead":
      return <span className="text-muted">{s.stats.lastReadAt ? relative(s.stats.lastReadAt) : "—"}</span>;
    case "added":
      return <span className="text-muted">{date(s.addedAt)}</span>;
    case "requestedBy":
      return (s.requestedBy?.length ?? 0) > 0 ? <span title={s.requestedAt ? date(s.requestedAt) : undefined}>{(s.requestedBy ?? []).map((r) => r.name).join(", ")}</span> : <span className="text-muted">—</span>;
    case "languages":
      return (
        <span className="inline-flex gap-1">
          {(s.editions?.length ?? 0) > 0 ? (s.editions ?? []).map((e) => <Badge key={e.id}>{e.language ? languageName(e.language) : "—"}</Badge>) : <Badge>{s.language ? languageName(s.language) : "—"}</Badge>}
        </span>
      );
    case "root":
      return <span className="text-muted">{roots.get(s.rootFolderId) ?? "—"}</span>;
    case "year":
      return s.metadata.year || "—";
    case "rating":
      return s.score != null ? <b className="font-semibold tabular-nums">{outOf10(s.score)}</b> : <span className="text-muted">—</span>;
    case "genres":
      return <span className="block max-w-[16rem] truncate text-muted" title={(s.metadata.genres ?? []).map(genreName).join(", ")}>{(s.metadata.genres ?? []).map(genreName).join(", ") || "—"}</span>;
    case "sources":
      return s.sourceCount ?? 0;
    case "following":
      return s.following ? t("Yes") : <span className="text-muted">—</span>;
  }
  return null;
}

/** LibraryTable is the library's table view; clicking a sortable header sorts by it, again flips the direction. */
export function LibraryTable({ list, columns: ids, sort, order, onSort, edit, requests, roots, selecting, selected, onToggle }: {
  list: Series[];
  columns: string[];
  sort: Sort;
  order: Order;
  onSort: (sort: Sort, order: Order | "") => void;
  edit: boolean;
  requests: boolean;
  roots: Map<number, string>;
  selecting: boolean;
  selected: Map<number, string>;
  onToggle: (idx: number, shift: boolean) => void;
}) {
  const shown = ids.map((id) => columns.find((c) => c.id === id)).filter((c): c is Column => !!c && (!c.requests || requests));
  const header = (label: ReactNode, by: Sort | undefined, className?: string) => {
    if (!by) return <Th className={className}>{label}</Th>;
    const on = sort === by;
    const dir = sortOrder(by, on ? order : "");
    return (
      <Th className={className}>
        <button type="button" onClick={() => onSort(by, on ? (dir === "asc" ? "desc" : "asc") : "")} className={clsx("inline-flex items-center gap-1 uppercase hover:text-fg", on && "text-fg")} aria-label={t("Sort by {name}", { name: typeof label === "string" ? label : by })}>
          {label}
          {on && (dir === "asc" ? <ArrowUp className="size-3 text-accent-2" /> : <ArrowDown className="size-3 text-accent-2" />)}
        </button>
      </Th>
    );
  };
  return (
    <Table>
      <thead>
        <tr>
          {selecting && <Th className="w-8" />}
          {header(t("Title"), "title", "sticky left-0 z-10")}
          {shown.map((c) => <Fragment key={c.id}>{header(columnLabel(c), c.sort, c.className)}</Fragment>)}
        </tr>
      </thead>
      <tbody>
        {list.map((s, idx) => (
          <tr key={s.id} className="group hover:bg-panel-2/60">
            {selecting && (
              <Td className="w-8">
                <input type="checkbox" aria-label={t("Select {name}", { name: s.title })} checked={selected.has(s.id)} onChange={() => undefined} onClick={(e) => onToggle(idx, e.shiftKey)} />
              </Td>
            )}
            <Td className="sticky left-0 z-10 max-w-[18rem] bg-bg group-hover:bg-panel-2 sm:max-w-none">
              <Link to={`/series/${s.id}`} className="font-medium hover:text-accent-2">
                {s.displayTitle || s.title}
              </Link>
              {!ids.includes("languages") && (s.editions?.length ?? 0) > 0 && (
                <span className="ml-2 inline-flex gap-1">
                  {(s.editions ?? []).map((edition) => <Badge key={edition.id}>{edition.language ? languageName(edition.language) : "—"}</Badge>)}
                </span>
              )}
              {edit && !s.monitored && !ids.includes("monitored") && <span className="ml-2"><Badge>{t("unmonitored")}</Badge></span>}
            </Td>
            {shown.map((c) => <Td key={c.id} className={clsx("whitespace-nowrap", c.className)}>{cell(c.id, s, edit, roots)}</Td>)}
          </tr>
        ))}
      </tbody>
    </Table>
  );
}

