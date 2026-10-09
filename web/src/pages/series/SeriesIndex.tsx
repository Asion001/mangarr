import { useUIMode, useUIPreferences } from "../../lib/uiPreferences";
import { t } from "../../lib/i18n/core";
import { useDocumentTitle } from "../../lib/documentTitle";
import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { Link } from "react-router";
import { Check, CheckSquare, Inbox, LayoutGrid, List, Plus, RefreshCw, Search, SlidersHorizontal, X } from "lucide-react";
import { api, apiUrl, unwrap, type Series } from "../../api/client";
import { usePushCommand, useRootFolders, useSeriesSearch } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, EmptyState, ErrorBox, Input, Loading, Progress, Select, Table, Td, Th } from "../../components/ui";
import { bytes, date, languageName } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useListParam, useQueryParam, useStoredListParam } from "../../lib/urlState";
import { MassEditBar } from "./Organize";
import { ContinueReading } from "./ContinueReading";
import { SetupChecklist } from "./SetupChecklist";
import { useAccount } from "../../lib/account";
import { useToast } from "../../lib/toast";
import { RequestSearch } from "../requests/Requests";

type Filter = "all" | "monitored" | "missing" | "ongoing" | "completed" | "unread" | "reading" | "following";
type Sort = "title" | "added" | "latest" | "missing" | "size" | "read";
const filters: readonly Filter[] = ["all", "following", "monitored", "missing", "ongoing", "completed", "unread", "reading"];
const sorts: readonly Sort[] = ["title", "added", "latest", "missing", "size", "read"];
const pageSizes = ["24", "36", "48", "72"] as const;
const libraryStateKey = (name: string) => `mangarr:library:${name}`;

const filterLabels: Record<Filter, string> = {
  all: "All",
  following: "Following",
  monitored: "Monitored",
  missing: "Missing chapters",
  ongoing: "Ongoing",
  completed: "Completed",
  unread: "With unread chapters",
  reading: "Started reading",
};
const filterLabel = (f: Filter) => t(filterLabels[f] as Parameters<typeof t>[0]);

export function statusTone(s: string) {
  return s === "ongoing" ? "ok" : s === "completed" ? "info" : s === "hiatus" ? "warn" : s === "cancelled" ? "err" : "default";
}

function progressOf(s: Series) {
  const { fileCount, monitoredCount, missingCount } = s.stats;
  if (monitoredCount === 0) return { pct: 0, tone: "accent" as const };
  const pct = ((monitoredCount - missingCount) / monitoredCount) * 100;
  return { pct, tone: missingCount === 0 ? ("ok" as const) : fileCount === 0 ? ("err" as const) : ("warn" as const) };
}

/** ReadBar is a thin bar of chapters read. */
function ReadBar({ s }: { s: Series }) {
  const pct = s.stats.chapterCount > 0 ? Math.min(100, (s.stats.readCount / s.stats.chapterCount) * 100) : 0;
  return (
    <div className="-mt-1 h-1 overflow-hidden rounded-full bg-panel-2" title={`${s.stats.readCount} of ${s.stats.chapterCount} read`}>
      <div className="h-full rounded-full bg-info" style={{ width: `${pct}%` }} />
    </div>
  );
}

export function SeriesIndex() {
  const { data: roots } = useRootFolders();
  const { editing } = useUIMode();
  const account = useAccount();
  const toast = useToast();
  const manage = account.can("library.edit") && editing;
  // adding is offered in both modes: it's how a title gets into the library
  const adds = account.can("library.add");
  // people who ask for titles instead of adding them
  const requester = !account.can("library.add") && account.can("requests.create");
  const push = usePushCommand();
  const [q, setQ] = useQueryParam("q");
  // the account's library defaults for a browser that has none yet
  const { preferences: uiPrefs, save: saveUI } = useUIPreferences();
  const ui = uiPrefs.options ?? {};
  const [filterParam, setFilter] = useStoredListParam("filter", "all", libraryStateKey("filter"), filters);
  const [sortParam, setSortOnly] = useStoredListParam("sort", ui.librarySort && (sorts as readonly string[]).includes(ui.librarySort) ? ui.librarySort : "title", libraryStateKey("sort"), sorts);
  const setSort = (v: string) => { setSortOnly(v); void saveUI({ options: { librarySort: v } }).catch(() => undefined); };
  const [rootParam, setRoot] = useStoredListParam("root", "", libraryStateKey("root"));
  const [language, setLanguage] = useStoredListParam("language", "", libraryStateKey("language"));
  const [genre, setGenre] = useListParam("genre");
  const [pageParam, setPage] = useListParam("page", "1");
  const [pageSizeParam, setPageSizeOnly] = useStoredListParam("pageSize", ui.libraryPageSize && (pageSizes as readonly string[]).includes(ui.libraryPageSize) ? ui.libraryPageSize : "36", libraryStateKey("pageSize"), pageSizes);
  const setPageSize = (v: string) => { setPageSizeOnly(v); void saveUI({ options: { libraryPageSize: v } }).catch(() => undefined); };
  const filter = filterParam as Filter;
  const sort = sortParam as Sort;
  const page = Math.max(1, Number(pageParam) || 1);
  const pageSize = Number(pageSizeParam);
  const rootFolderId = Number(rootParam) || undefined;
  const [searchDraft, setSearchDraft] = useState(q);
  useEffect(() => setSearchDraft(q), [q]);
  useEffect(() => {
    if (searchDraft === q) return;
    const timeout = window.setTimeout(() => {
      setQ(searchDraft);
      setPage("1");
    }, 250);
    return () => window.clearTimeout(timeout);
  }, [searchDraft, q]);
  const { data, isLoading, isPlaceholderData, error } = useSeriesSearch({ q: q || undefined, filter, sort, rootFolderId, language: language || undefined, genre: genre || undefined, page, pageSize });
  useEffect(() => {
    if (!data?.total) return;
    const last = Math.max(1, Math.ceil(data.total / pageSize));
    if (page > last) setPage(String(last));
  }, [data?.total, page, pageSize, setPage]);
  const [selecting, setSelecting] = useState(false);
  // selected maps id to title, so the bar can list series from other pages
  const [selected, setSelected] = useState<Map<number, string>>(new Map());
  const [lastClicked, setLastClicked] = useState<number | null>(null);
  const [selectingAll, setSelectingAll] = useState(false);
  useEffect(()=>{if(!manage){setSelecting(false);setSelected(new Map());}},[manage]);
  // a shift-click range starts on this page, not where the last one was clicked
  useEffect(() => setLastClicked(null), [q, filter, sort, rootFolderId, language, genre, page, pageSize]);
  /** toggle flips one series; shift+click sets the whole range from the last click the same way. */
  const toggle = (idx: number, shift = false) => {
    const s = list[idx];
    setSelected((cur) => {
      const n = new Map(cur);
      const on = !cur.has(s.id);
      const from = lastClicked !== null && lastClicked < list.length ? lastClicked : null;
      const [a, b] = shift && from !== null ? [Math.min(from, idx), Math.max(from, idx)] : [idx, idx];
      for (let i = a; i <= b; i++) on ? n.set(list[i].id, list[i].title) : n.delete(list[i].id);
      return n;
    });
    setLastClicked(idx);
  };
  const selectAllMatching = async () => {
    setSelectingAll(true);
    try {
      const all = new Map<number, string>();
      for (let p = 1; ; p++) {
        const r = await unwrap(api.GET("/api/v1/series/search", { params: { query: { q: q || undefined, filter, sort, rootFolderId, language: language || undefined, genre: genre || undefined, page: p, pageSize: 100 } } }));
        r.items.forEach((s) => all.set(s.id, s.title));
        if (r.items.length < 100 || all.size >= r.total) break;
      }
      setSelected(all);
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSelectingAll(false);
    }
  };
  const [view, setView] = useState<"posters" | "table">(() => (localStorage.getItem("seriesView") as "posters" | "table") || ui.libraryView || "posters");

  const list = data?.items ?? [];
  const setFilterAndReset = (value: string) => { setFilter(value); setPage("1"); };
  const setSortAndReset = (value: string) => { setSort(value); setPage("1"); };

  useDocumentTitle(t("Series"));
  const [filtersOpen, setFiltersOpen] = useState(false);
  const toolsRef = useRef<HTMLDivElement>(null);
  // the filters close on a click outside them or Escape
  useEffect(() => {
    if (!filtersOpen) return;
    const outside = (e: MouseEvent) => {
      if (!toolsRef.current?.contains(e.target as Node)) setFiltersOpen(false);
    };
    const escape = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFiltersOpen(false);
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [filtersOpen]);
  // what narrows the list, as chips that clear one at a time
  const active = [
    filter !== "all" && { key: "filter", label: filterLabel(filter), clear: () => setFilterAndReset("all") },
    rootFolderId && { key: "root", label: roots?.find((root) => root.id === rootFolderId)?.path ?? t("Removed library"), clear: () => { setRoot(""); setPage("1"); } },
    language && { key: "language", label: languageName(language), clear: () => { setLanguage(""); setPage("1"); } },
    genre && { key: "genre", label: genreName(genre), clear: () => { setGenre(""); setPage("1"); } },
  ].filter((chip): chip is { key: string; label: string; clear: () => void } => !!chip);
  const clearFilters = () => {
    setFilter("all");
    setRoot("");
    setLanguage("");
    setGenre("");
    setPage("1");
  };

  const setViewPersist = (v: "posters" | "table") => {
    setView(v);
    localStorage.setItem("seriesView", v);
    void saveUI({ options: { libraryView: v } }).catch(() => undefined);
  };

  return (
    <>
      <div className="mb-5 flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="mr-auto flex items-baseline gap-2.5 sm:mr-0">
          <h1 className="text-2xl font-semibold">{t("Series")}</h1>
          {data && <span className="text-sm text-muted">{data.total}</span>}
        </div>
        {/* on a phone the search takes its own row under the title and the add button */}
        <div ref={toolsRef} className="relative order-last min-w-0 basis-full sm:order-none sm:flex-[1_1_20rem] sm:basis-auto">
          <label className="block">
            <span className="sr-only">{t("Search the library")}</span>
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" />
            <Input type="search" className="h-11 rounded-lg bg-panel pl-10 pr-12 text-[15px]" placeholder={t("Search titles, alternative titles and genres…")} value={searchDraft} onChange={(e) => setSearchDraft(e.target.value)} />
          </label>
          <button
            type="button"
            aria-label={t("Filters")}
            title={t("Filters, sort and view")}
            aria-expanded={filtersOpen}
            aria-controls="library-filters"
            onClick={() => setFiltersOpen(!filtersOpen)}
            className={clsx("absolute right-1.5 top-1/2 inline-flex size-8 -translate-y-1/2 items-center justify-center rounded-md hover:bg-panel-2 hover:text-fg", filtersOpen || active.length > 0 ? "text-accent-2" : "text-muted")}
          >
            <SlidersHorizontal className="size-4" />
            {active.length > 0 && <span aria-hidden className="absolute right-1 top-1 size-2 rounded-full bg-accent" />}
          </button>
          {filtersOpen && (
            <div id="library-filters" className="absolute right-0 top-full z-30 mt-2 flex w-[min(22rem,calc(100vw-2rem))] flex-col gap-3 rounded-xl border border-border bg-panel-2 p-4 shadow-xl">
              <label className="flex flex-col gap-1 text-xs text-muted">
                {t("Show")}
                <Select value={filter} onChange={(e) => setFilterAndReset(e.target.value)}>
                  {filters.map((f) => <option key={f} value={f}>{filterLabel(f)}</option>)}
                </Select>
              </label>
              <label className="flex flex-col gap-1 text-xs text-muted">
                {t("Sort")}
                <Select value={sort} onChange={(e) => setSortAndReset(e.target.value)}>
                  <option value="title">{t("Sort: title")}</option>
                  <option value="added">{t("Sort: recently added")}</option>
                  <option value="latest">{t("Sort: latest chapter")}</option>
                  <option value="missing">{t("Sort: missing")}</option>
                  <option value="size">{t("Sort: size")}</option>
                  <option value="read">{t("Sort: recently read")}</option>
                </Select>
              </label>
              {/* a remembered filter stays visible, so it can be cleared even when it no longer applies */}
              {((roots?.length ?? 0) > 1 || rootParam !== "") && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Library")}
                  <Select value={rootParam} onChange={(e) => { setRoot(e.target.value); setPage("1"); }}>
                    <option value="">{t("All libraries")}</option>
                    {rootParam !== "" && !roots?.some((root) => String(root.id) === rootParam) && <option value={rootParam}>{t("Removed library")}</option>}
                    {roots?.map((root) => <option key={root.id} value={root.id}>{root.path}</option>)}
                  </Select>
                </label>
              )}
              {((data?.languages?.length ?? 0) > 1 || language !== "") && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Language")}
                  <Select value={language} onChange={(e) => { setLanguage(e.target.value); setPage("1"); }}>
                    <option value="">{t("All languages")}</option>
                    {language !== "" && !data?.languages?.includes(language) && <option value={language}>{languageName(language)}</option>}
                    {data?.languages?.map((item) => <option key={item} value={item}>{languageName(item)}</option>)}
                  </Select>
                </label>
              )}
              {((data?.genres?.length ?? 0) > 0 || genre !== "") && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Genres and tags")}
                  <Select value={genre} onChange={(e) => { setGenre(e.target.value); setPage("1"); }}>
                    <option value="">{t("All genres and tags")}</option>
                    {genre !== "" && !data?.genres?.includes(genre) && <option value={genre}>{genreName(genre)}</option>}
                    {[...(data?.genres ?? [])].sort((a, b) => genreName(a).localeCompare(genreName(b))).map((item) => <option key={item} value={item}>{genreName(item)}</option>)}
                  </Select>
                </label>
              )}
              <div className="flex items-center justify-between gap-3 text-xs text-muted">
                {t("View")}
                <div className="inline-flex overflow-hidden rounded-md border border-border" role="group" aria-label={t("View")}>
                  <button type="button" aria-pressed={view === "posters"} aria-label={t("Posters")} onClick={() => setViewPersist("posters")} className={clsx("inline-flex h-8 w-9 items-center justify-center", view === "posters" ? "bg-border text-fg" : "bg-panel text-muted hover:text-fg")}>
                    <LayoutGrid className="size-4" />
                  </button>
                  <button type="button" aria-pressed={view === "table"} aria-label={t("Table")} onClick={() => setViewPersist("table")} className={clsx("inline-flex h-8 w-9 items-center justify-center", view === "table" ? "bg-border text-fg" : "bg-panel text-muted hover:text-fg")}>
                    <List className="size-4" />
                  </button>
                </div>
              </div>
              {(manage || active.length > 0) && (
                <div className="flex flex-wrap items-center gap-2 border-t border-border pt-3">
                  {manage && <Button size="sm" icon={<CheckSquare className="size-3.5" />} onClick={() => { setSelecting(true); setSelected(new Map()); setFiltersOpen(false); }}>{t("Select")}</Button>}
                  {manage && <Button size="sm" icon={<RefreshCw className="size-3.5" />} title={t("Check sources for new chapters now")} onClick={() => push.mutate({ name: "RefreshSources", label: "Checking sources for new chapters" })}>{t("Check now")}</Button>}
                  {active.length > 0 && <Button size="sm" variant="ghost" className="ml-auto" onClick={clearFilters}>{t("Clear all")}</Button>}
                </div>
              )}
            </div>
          )}
        </div>
        {adds ? (
          <Link to="/add" className="inline-flex h-11 items-center gap-2 rounded-lg bg-primary px-4 text-sm font-semibold text-white hover:bg-primary-hover">
            <Plus className="size-4" />{t("Add series")}
          </Link>
        ) : requester ? (
          <Link to={q ? `/requests?tab=ask&q=${encodeURIComponent(q)}` : "/requests?tab=ask"} className="inline-flex h-11 items-center gap-2 rounded-lg bg-primary px-4 text-sm font-semibold text-white hover:bg-primary-hover">
            <Inbox className="size-4" />{t("Request a title")}
          </Link>
        ) : null}
      </div>
      {(active.length > 0 || selecting) && (
        <div className="-mt-2 mb-4 flex flex-wrap items-center gap-1.5">
          {active.map((chip) => (
            <span key={chip.key} className="inline-flex h-7 items-center gap-1 rounded-full bg-panel-2 pl-3 pr-1 text-xs">
              {chip.label}
              <button type="button" onClick={chip.clear} aria-label={t("Remove filter {name}", { name: chip.label })} className="inline-flex size-5 items-center justify-center rounded-full text-muted hover:bg-border hover:text-fg">
                <X className="size-3" />
              </button>
            </span>
          ))}
          {active.length > 1 && (
            <button type="button" onClick={clearFilters} className="px-1.5 text-xs text-muted hover:text-fg">{t("Clear all")}</button>
          )}
          {selecting && (
            <span className="ml-auto flex items-center gap-2">
              <Button size="sm" onClick={() => setSelected((cur) => new Map([...cur, ...list.map((s) => [s.id, s.title] as [number, string])]))}>{t("All shown")}</Button>
              <Button size="sm" variant="primary" onClick={() => { setSelecting(false); setSelected(new Map()); }}>{t("Done")}</Button>
            </span>
          )}
        </div>
      )}
      {!q && filter === "all" && !rootFolderId && !language && !genre && <ContinueReading />}
      {/* the bar's row is always there so showing it never moves the list; it only shows while a new filter or page loads, not on live refreshes */}
      <div className={clsx("mb-2 h-0.5 overflow-hidden rounded", isPlaceholderData ? "bg-panel-2" : "invisible")} aria-hidden={!isPlaceholderData}><div className="h-full w-1/3 animate-pulse rounded bg-accent" /></div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {selecting && data && list.length > 0 && list.every((s) => selected.has(s.id)) && data.total > selected.size && (
        <div role="status" className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border border-border bg-panel px-4 py-2 text-sm">
          <span>{t("All {count} on this page are selected.", { count: list.length })}</span>
          <Button size="sm" variant="ghost" loading={selectingAll} onClick={() => void selectAllMatching()}>{t("Select all {count} matching", { count: data.total })}</Button>
        </div>
      )}
      {data && data.total === 0 && !q && filter === "all" && !rootFolderId && !language && !genre && (
        account.isAdmin ? (
          <SetupChecklist />
        ) : (
          <EmptyState title={t("No series yet")}>
            {account.can(["library.add", "requests.manage"]) ? (
              <Link to="/add"><Button variant="primary">{t("Add series")}</Button></Link>
            ) : requester ? (
              <Link to="/requests?tab=ask"><Button variant="primary" icon={<Inbox className="size-4" />}>{t("Request a title")}</Button></Link>
            ) : (
              t("The library is empty. Ask an admin to add series.")
            )}
          </EmptyState>
        )
      )}
      {data && data.total === 0 && (q || filter !== "all" || rootFolderId || language || genre) && (
        <EmptyState title={t("No series match these filters")}>{t("Try a shorter title or clear one of the filters.")}</EmptyState>
      )}
      {view === "posters" ? (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(150px,1fr))] gap-4">
          {list.map((s, idx) => {
            const p = progressOf(s);
            const on = selecting && selected.has(s.id);
            return (
              <Link
                key={s.id}
                to={`/series/${s.id}`}
                role={selecting ? "checkbox" : undefined}
                aria-checked={selecting ? on : undefined}
                onClick={(e) => {
                  if (selecting) {
                    e.preventDefault();
                    toggle(idx, e.shiftKey);
                  }
                }}
                className={`group flex flex-col gap-2 ${on ? "rounded-md ring-2 ring-accent ring-offset-2 ring-offset-bg" : ""}`}
              >
                <div className="relative">
                  {selecting && (
                    <span aria-hidden className={`absolute left-1.5 top-1.5 z-10 flex size-6 items-center justify-center rounded-md border-2 ${on ? "border-primary bg-primary text-white" : "border-white/80 bg-black/50"}`}>
                      {on && <Check className="size-4" />}
                    </span>
                  )}
                  <Cover src={apiUrl(s.coverUrl)} alt={s.title} className="aspect-[2/3] w-full ring-accent/60 transition group-hover:ring-2" />
                  {manage && !s.monitored && <div className={`absolute top-1.5 ${selecting ? "left-9" : "left-1.5"}`}><Badge>{t("unmonitored")}</Badge></div>}
                  {manage && s.stats.missingCount > 0 && (
                    <div className="absolute right-1.5 top-1.5">
                      <Badge tone="warn">{s.stats.missingCount}{" " + t("missing")}</Badge>
                    </div>
                  )}
                </div>
                <Progress value={p.pct} tone={p.tone} />
                {s.stats.readCount > 0 && <ReadBar s={s} />}
                <div className="line-clamp-2 text-sm font-medium leading-tight">{s.title}</div>
                {(s.editions?.length ?? 0) > 0 && (
                  <div className="flex flex-wrap gap-1">
                    {(s.editions ?? []).map((edition) => <Badge key={edition.id}>{edition.language ? languageName(edition.language) : "—"}</Badge>)}
                  </div>
                )}
                <div className="-mt-1 text-xs text-muted">
                  {s.stats.fileCount}/{s.stats.chapterCount}{" " + t("chapters")}{s.stats.readCount > 0 && ` · ${s.stats.readCount} read`}
                </div>
              </Link>
            );
          })}
        </div>
      ) : (
        <Table>
          <thead>
            <tr>
              {selecting && <Th className="w-8" />}
              <Th>{t("Title")}</Th>
              <Th>{t("Status")}</Th>
              <Th>{t("Chapters")}</Th>
              <Th>{t("Latest")}</Th>
              <Th>{t("Size")}</Th>
              <Th>{t("Added")}</Th>
            </tr>
          </thead>
          <tbody>
            {list.map((s, idx) => (
              <tr key={s.id} className="hover:bg-panel-2/60">
                {selecting && (
                  <Td className="w-8">
                    <input type="checkbox" aria-label={`Select ${s.title}`} checked={selected.has(s.id)} onChange={() => undefined} onClick={(e) => toggle(idx, e.shiftKey)} />
                  </Td>
                )}
                <Td>
                  <Link to={`/series/${s.id}`} className="font-medium hover:text-accent-2">
                    {s.title}
                  </Link>
                  {(s.editions?.length ?? 0) > 0 && (
                    <span className="ml-2 inline-flex gap-1">
                      {(s.editions ?? []).map((edition) => <Badge key={edition.id}>{edition.language ? languageName(edition.language) : "—"}</Badge>)}
                    </span>
                  )}
                  {manage && !s.monitored && <span className="ml-2"><Badge>{t("unmonitored")}</Badge></span>}
                </Td>
                <Td>
                  <Badge tone={statusTone(s.status)}>{s.status}</Badge>
                </Td>
                <Td className="w-48">
                  <div className="flex items-center gap-2">
                    <div className="w-24"><Progress value={progressOf(s).pct} tone={progressOf(s).tone} /></div>
                    <span className="text-xs text-muted">
                      {s.stats.fileCount}/{s.stats.chapterCount}
                      {s.stats.readCount > 0 && ` · ${s.stats.readCount} read`}
                    </span>
                  </div>
                </Td>
                <Td>{s.stats.lastChapter || "—"}</Td>
                <Td>{bytes(s.stats.sizeOnDisk)}</Td>
                <Td>{date(s.addedAt)}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {data && data.total > 0 && (
        <div className="mt-5 flex flex-wrap items-center justify-center gap-2">
          <Button size="sm" disabled={page <= 1} onClick={() => setPage(String(page - 1))}>{t("Previous")}</Button>
          <span className="px-2 text-sm text-muted">{t("Page")} {page} {t("of")} {Math.max(1, Math.ceil(data.total / pageSize))}</span>
          <Button size="sm" disabled={page * pageSize >= data.total} onClick={() => setPage(String(page + 1))}>{t("Next")}</Button>
          <Select className="ml-2 w-auto" value={pageSize} onChange={(e) => { setPageSize(e.target.value); setPage("1"); }}>
            {[24, 36, 48, 72].map((size) => <option key={size} value={size}>{size} {t("per page")}</option>)}
          </Select>
        </div>
      )}
      {(adds || requester) && q.trim().length > 1 && page === 1 && (
        <section aria-labelledby="request-results" className="mt-8">
          <h2 id="request-results" className="mb-1 text-lg font-semibold">{t("Not in the library?")}</h2>
          <p className="mb-3 text-sm text-muted">
            {adds ? t("Titles matching “{query}” you can add.", { query: q.trim() }) : t("Titles matching “{query}” you can ask for.", { query: q.trim() })}
          </p>
          <RequestSearch query={q.trim()} hideInput add={adds} />
        </section>
      )}
      {selecting && selected.size > 0 && (
        <>
          <div className="h-20" />
          <MassEditBar
            selected={selected}
            onRemove={(id) => setSelected((cur) => { const n = new Map(cur); n.delete(id); return n; })}
            onClear={() => setSelected(new Map())}
          />
        </>
      )}
    </>
  );
}
