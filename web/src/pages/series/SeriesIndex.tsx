import { useUIMode, useUIPreferences } from "../../lib/uiPreferences";
import { t } from "../../lib/i18n/core";
import { useDocumentTitle } from "../../lib/documentTitle";
import { useEffect, useMemo, useRef, useState } from "react";
import clsx from "clsx";
import { Link } from "react-router";
import { ArrowDown, ArrowUp, Check, CheckSquare, Inbox, LayoutGrid, List, Plus, RefreshCw, Search, SlidersHorizontal, X } from "lucide-react";
import { api, apiUrl, unwrap, type Series } from "../../api/client";
import { usePushCommand, useRootFolders, useSeriesSearch } from "../../api/queries";
import { useReachable } from "../../lib/offline/connection";
import { useSavedChapters } from "../../lib/offline/store";
import { Cover } from "../../components/Cover";
import { Badge, Button, EmptyState, ErrorBox, Input, Loading, Progress, Select, ButtonLink } from "../../components/ui";
import { GenrePicker, type GenreMatch } from "./GenrePicker";
import { bytes, languageName } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useListParam, useQueryParam, useStoredListParam } from "../../lib/urlState";
import { MassEditBar } from "./Organize";
import { ContinueReading } from "./ContinueReading";
import { SetupChecklist } from "./SetupChecklist";
import { useAccount } from "../../lib/account";
import { useToast } from "../../lib/toast";
import { RequestSearch } from "../requests/Requests";
import { ColumnPicker, LibraryTable, progressOf, sortLabel, sortOrder, sorts, useLibraryColumns, type Order, type Sort } from "./LibraryTable";
import { readingListName, readingStatusName, readingStatuses, useReadingLists, type ReadingStatus } from "../../lib/readingLists";
import { useQuery } from "@tanstack/react-query";

type Filter = "all" | "monitored" | "missing" | "ongoing" | "completed" | "unread" | "reading" | "following";
// filter is the old single "Show" choice; links with it open with the matching fields set
const filters: readonly Filter[] = ["all", "following", "monitored", "missing", "ongoing", "completed", "unread", "reading"];
const orders = ["", "asc", "desc"] as const;
const monitoredValues = ["", "yes", "no"] as const;
const statusValues = ["", "ongoing", "completed", "hiatus", "cancelled"] as const;
const downloadValues = ["", "missing", "complete", "none"] as const;
const readingValues = ["", "unstarted", "reading", "finished", "unread"] as const;
const followingValues = ["", "1"] as const;
const pageSizes = ["24", "36", "48", "72"] as const;
const libraryStateKey = (name: string) => `mangarr:library:${name}`;

type Choice = { value: string; label: string };
const choiceLabel = (label: string) => t(label as Parameters<typeof t>[0]);
const monitoredChoices: Choice[] = [{ value: "", label: "Any" }, { value: "yes", label: "Monitored" }, { value: "no", label: "Not monitored" }];
const statusChoices: Choice[] = [{ value: "", label: "Any" }, { value: "ongoing", label: "Ongoing" }, { value: "completed", label: "Completed" }, { value: "hiatus", label: "Hiatus" }, { value: "cancelled", label: "Cancelled" }];
const downloadChoices: Choice[] = [{ value: "", label: "Any" }, { value: "missing", label: "Missing chapters" }, { value: "complete", label: "All downloaded" }, { value: "none", label: "Nothing downloaded yet" }];
const readingChoices: Choice[] = [{ value: "", label: "Any" }, { value: "unstarted", label: "Not started" }, { value: "reading", label: "Started reading" }, { value: "finished", label: "Finished" }, { value: "unread", label: "With unread chapters" }];
const chosen = (list: Choice[], value: string) => choiceLabel(list.find((c) => c.value === value)?.label ?? value);

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
  const [monitored, setMonitored] = useStoredListParam("monitored", "", libraryStateKey("monitored"), monitoredValues);
  const [status, setStatus] = useStoredListParam("status", "", libraryStateKey("status"), statusValues);
  const [downloads, setDownloads] = useStoredListParam("downloads", "", libraryStateKey("downloads"), downloadValues);
  const [reading, setReading] = useStoredListParam("reading", "", libraryStateKey("reading"), readingValues);
  const [followingParam, setFollowing] = useStoredListParam("following", "", libraryStateKey("following"), followingValues);
  const [requestedBy, setRequestedBy] = useStoredListParam("requestedBy", "", libraryStateKey("requestedBy"));
  const [sortParam, setSortOnly] = useStoredListParam("sort", ui.librarySort && (sorts as readonly string[]).includes(ui.librarySort) ? ui.librarySort : "title", libraryStateKey("sort"), sorts);
  const [orderParam, setOrder] = useStoredListParam("order", "", libraryStateKey("order"), orders);
  const setSort = (v: string) => { setSortOnly(v); void saveUI({ options: { librarySort: v } }).catch(() => undefined); };
  // an old "Show" choice (a bookmark, or what this browser remembered) moves to its own field
  useEffect(() => {
    if (filterParam === "all") return;
    const move: Record<string, () => void> = {
      following: () => setFollowing("1"),
      monitored: () => setMonitored("yes"),
      missing: () => setDownloads("missing"),
      ongoing: () => setStatus("ongoing"),
      completed: () => setStatus("completed"),
      unread: () => setReading("unread"),
      reading: () => setReading("reading"),
    };
    move[filterParam]?.();
    setFilter("all");
  }, [filterParam]);
  const requests = account.can("requests.manage");
  const [columns, setColumns] = useLibraryColumns(ui.libraryColumns, (ids) => void saveUI({ options: { libraryColumns: ids } }).catch(() => undefined));
  const [rootParam, setRoot] = useStoredListParam("root", "", libraryStateKey("root"));
  const [language, setLanguage] = useStoredListParam("language", "", libraryStateKey("language"));
  // the picked genres share one parameter in the page address, comma-separated
  const [genreParam, setGenreParam] = useListParam("genre");
  const [matchParam, setMatch] = useListParam("genreMatch", "all");
  const genres = genreParam ? genreParam.split(",").filter(Boolean) : [];
  const genreMatch: GenreMatch = matchParam === "any" ? "any" : "all";
  const setGenres = (list: string[]) => { setGenreParam(list.join(",")); setPage("1"); };
  const genreQuery = { genre: genres.length ? genres : undefined, genreMatch: genres.length > 1 && genreMatch === "any" ? ("any" as const) : undefined };
  // a status or a list (by id) of yours, or with listUser someone else's for people who manage the library
  const [listParam, setListParam] = useListParam("list");
  const [listUserParam, setListUser] = useListParam("listUser");
  const seesOthersLists = account.can("library.manage");
  const listUser = seesOthersLists ? Number(listUserParam) || undefined : undefined;
  const listQuery = { list: listParam || undefined, listUser: listParam ? listUser : undefined };
  const { data: lists } = useReadingLists(!!account.account, listUser);
  const { data: owners } = useQuery({
    queryKey: ["reading-lists", "owners"],
    queryFn: () => unwrap(api.GET("/api/v1/reading-lists/owners")),
    enabled: seesOthersLists,
  });
  const ownerName = (id?: number) => {
    const o = owners?.find((x) => x.id === id);
    return o ? o.displayName || o.username : t("Someone else");
  };
  const listName = (key: string) => {
    if ((readingStatuses as readonly string[]).includes(key)) return readingStatusName(key as ReadingStatus);
    const l = lists?.lists?.find((x) => String(x.id) === key);
    return l ? readingListName(l) : t("Removed list");
  };
  const listOptions = [
    ...(lists?.statuses ?? []).filter((x) => x.count > 0).map((x) => ({ key: x.status, label: readingStatusName(x.status as ReadingStatus), count: x.count })),
    ...(lists?.lists ?? []).filter((x) => x.count > 0).map((x) => ({ key: String(x.id), label: readingListName(x), count: x.count })),
  ];
  const showsLists = listOptions.length > 0 || listParam !== "" || (seesOthersLists && (owners?.length ?? 0) > 0);
  const [pageParam, setPage] = useListParam("page", "1");
  const [pageSizeParam, setPageSizeOnly] = useStoredListParam("pageSize", ui.libraryPageSize && (pageSizes as readonly string[]).includes(ui.libraryPageSize) ? ui.libraryPageSize : "36", libraryStateKey("pageSize"), pageSizes);
  const setPageSize = (v: string) => { setPageSizeOnly(v); void saveUI({ options: { libraryPageSize: v } }).catch(() => undefined); };
  const sort = sortParam as Sort;
  const order = sortOrder(sort, orderParam);
  const following = followingParam === "1";
  const page = Math.max(1, Number(pageParam) || 1);
  const pageSize = Number(pageSizeParam);
  const rootFolderId = Number(rootParam) || undefined;
  const searchQuery = {
    ...listQuery,
    q: q || undefined, sort, order: orderParam ? order : undefined, rootFolderId, language: language || undefined, ...genreQuery,
    monitored: (monitored || undefined) as "yes" | "no" | undefined,
    status: (status || undefined) as "ongoing" | "completed" | "hiatus" | "cancelled" | undefined,
    downloads: (downloads || undefined) as "missing" | "complete" | "none" | undefined,
    reading: (reading || undefined) as "unstarted" | "reading" | "finished" | "unread" | undefined,
    following: following || undefined,
    requestedBy: requests && requestedBy ? requestedBy : undefined,
  };
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
  const { data, isLoading, isPlaceholderData, error } = useSeriesSearch({ ...searchQuery, page, pageSize });
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
  useEffect(() => setLastClicked(null), [q, monitored, status, downloads, reading, following, requestedBy, listParam, listUser, sort, order, rootFolderId, language, genreParam, genreMatch, page, pageSize]);
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
        const r = await unwrap(api.GET("/api/v1/series/search", { params: { query: { ...searchQuery, page: p, pageSize: 100 } } }));
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

  // offline, the library keeps to the series with chapters saved on this device
  const reachable = useReachable();
  const saved = useSavedChapters();
  const savedSeries = useMemo(() => new Set(Object.values(saved).map((c) => c.seriesId)), [saved]);
  const list = !reachable && savedSeries.size > 0 ? (data?.items ?? []).filter((s) => savedSeries.has(s.id)) : data?.items ?? [];
  const reset = (set: (v: string) => void) => (value: string) => { set(value); setPage("1"); };
  const setSortAndReset = (value: string, nextOrder: Order | "" = "") => { setSort(value); setOrder(nextOrder); setPage("1"); };
  const requesterName = (id: string) => id === "none" ? t("Nobody") : data?.requesters?.find((r) => String(r.id) === id)?.name ?? t("Removed user");

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
    monitored && { key: "monitored", label: chosen(monitoredChoices, monitored), clear: () => reset(setMonitored)("") },
    status && { key: "status", label: chosen(statusChoices, status), clear: () => reset(setStatus)("") },
    downloads && { key: "downloads", label: chosen(downloadChoices, downloads), clear: () => reset(setDownloads)("") },
    reading && { key: "reading", label: chosen(readingChoices, reading), clear: () => reset(setReading)("") },
    following && { key: "following", label: t("Following"), clear: () => reset(setFollowing)("") },
    requests && requestedBy && { key: "requestedBy", label: t("Requested by {name}", { name: requesterName(requestedBy) }), clear: () => reset(setRequestedBy)("") },
    rootFolderId && { key: "root", label: roots?.find((root) => root.id === rootFolderId)?.path ?? t("Removed library"), clear: () => { setRoot(""); setPage("1"); } },
    language && { key: "language", label: languageName(language), clear: () => { setLanguage(""); setPage("1"); } },
    listParam && { key: "list", label: listUser ? t("{name}: {list}", { name: ownerName(listUser), list: listName(listParam) }) : t("List: {name}", { name: listName(listParam) }), clear: () => { setListParam(""); setListUser(""); setPage("1"); } },
    ...genres.map((g) => ({ key: `genre:${g}`, label: genreName(g), clear: () => setGenres(genres.filter((x) => x !== g)) })),
  ].filter((chip): chip is { key: string; label: string; clear: () => void } => !!chip);
  const clearFilters = () => {
    setFilter("all");
    setMonitored("");
    setStatus("");
    setDownloads("");
    setReading("");
    setFollowing("");
    setRequestedBy("");
    setRoot("");
    setLanguage("");
    setListParam("");
    setListUser("");
    setGenreParam("");
    setMatch("all");
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
              <div className="grid grid-cols-2 gap-3">
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Monitoring")}
                  <Select value={monitored} onChange={(e) => reset(setMonitored)(e.target.value)}>
                    {monitoredChoices.map((c) => <option key={c.value} value={c.value}>{choiceLabel(c.label)}</option>)}
                  </Select>
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Status")}
                  <Select value={status} onChange={(e) => reset(setStatus)(e.target.value)}>
                    {statusChoices.map((c) => <option key={c.value} value={c.value}>{choiceLabel(c.label)}</option>)}
                  </Select>
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Downloads")}
                  <Select value={downloads} onChange={(e) => reset(setDownloads)(e.target.value)}>
                    {downloadChoices.map((c) => <option key={c.value} value={c.value}>{choiceLabel(c.label)}</option>)}
                  </Select>
                </label>
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Reading")}
                  <Select value={reading} onChange={(e) => reset(setReading)(e.target.value)}>
                    {readingChoices.map((c) => <option key={c.value} value={c.value}>{choiceLabel(c.label)}</option>)}
                  </Select>
                </label>
              </div>
              {requests && ((data?.requesters?.length ?? 0) > 0 || requestedBy !== "") && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Requested by")}
                  <Select value={requestedBy} onChange={(e) => reset(setRequestedBy)(e.target.value)}>
                    <option value="">{t("Anyone")}</option>
                    {(data?.requesters ?? []).map((r) => <option key={r.id} value={String(r.id)}>{r.name}</option>)}
                    {requestedBy !== "" && requestedBy !== "none" && !data?.requesters?.some((r) => String(r.id) === requestedBy) && <option value={requestedBy}>{requesterName(requestedBy)}</option>}
                    <option value="none">{t("Nobody")}</option>
                  </Select>
                </label>
              )}
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={following} onChange={(e) => reset(setFollowing)(e.target.checked ? "1" : "")} />
                {t("Only titles I follow")}
              </label>
              <div className="flex flex-col gap-1 text-xs text-muted">
                <label htmlFor="library-sort">{t("Sort")}</label>
                <div className="flex gap-2">
                  <Select id="library-sort" className="flex-1" value={sort} onChange={(e) => setSortAndReset(e.target.value)}>
                    {sorts.filter((s) => s !== "requested" || requests).map((s) => <option key={s} value={s}>{sortLabel(s)}</option>)}
                  </Select>
                  <div className="inline-flex shrink-0 overflow-hidden rounded-md border border-border" role="group" aria-label={t("Sort direction")}>
                    <button type="button" aria-pressed={order === "asc"} aria-label={t("Ascending")} onClick={() => setSortAndReset(sort, "asc")} className={clsx("inline-flex h-9 w-9 items-center justify-center", order === "asc" ? "bg-border text-fg" : "bg-panel text-muted hover:text-fg")}>
                      <ArrowUp className="size-4" />
                    </button>
                    <button type="button" aria-pressed={order === "desc"} aria-label={t("Descending")} onClick={() => setSortAndReset(sort, "desc")} className={clsx("inline-flex h-9 w-9 items-center justify-center", order === "desc" ? "bg-border text-fg" : "bg-panel text-muted hover:text-fg")}>
                      <ArrowDown className="size-4" />
                    </button>
                  </div>
                </div>
              </div>
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
              {seesOthersLists && showsLists && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("Whose lists")}
                  <Select aria-label={t("Whose lists")} value={listUser ? String(listUser) : ""} onChange={(e) => { setListUser(e.target.value); setListParam(""); setPage("1"); }}>
                    <option value="">{t("Mine")}</option>
                    {listUser && !owners?.some((o) => o.id === listUser) && <option value={String(listUser)}>{ownerName(listUser)}</option>}
                    {owners?.filter((o) => o.id !== account.account?.id).map((o) => <option key={o.id} value={String(o.id)}>{o.displayName || o.username}</option>)}
                  </Select>
                </label>
              )}
              {showsLists && (
                <label className="flex flex-col gap-1 text-xs text-muted">
                  {t("List")}
                  <Select aria-label={t("List")} value={listParam} onChange={(e) => { setListParam(e.target.value); setPage("1"); }}>
                    <option value="">{t("All titles")}</option>
                    {listParam !== "" && !listOptions.some((o) => o.key === listParam) && <option value={listParam}>{listName(listParam)}</option>}
                    {listOptions.map((o) => <option key={o.key} value={o.key}>{o.label} ({o.count})</option>)}
                  </Select>
                </label>
              )}
              {((data?.genres?.length ?? 0) > 0 || genres.length > 0) && (
                <GenrePicker genres={data?.genres ?? []} counts={data?.genreCounts} value={genres} onChange={setGenres} match={genreMatch} onMatch={(m) => { setMatch(m); setPage("1"); }} />
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
          {genres.length > 1 && <span className="px-1 text-xs text-muted">{genreMatch === "all" ? t("has all of these") : t("has any of these")}</span>}
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
      {!q && active.length === 0 && !rootFolderId && !language && !listParam && !genres.length && <ContinueReading />}
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
      {data && data.total === 0 && !q && active.length === 0 && !rootFolderId && !language && !listParam && !genres.length && (
        account.isAdmin ? (
          <SetupChecklist />
        ) : (
          <EmptyState title={t("No series yet")}>
            {account.can(["library.add", "requests.manage"]) ? (
              <ButtonLink to="/add" variant="primary">{t("Add series")}</ButtonLink>
            ) : requester ? (
              <ButtonLink to="/requests?tab=ask" variant="primary" icon={<Inbox className="size-4" />}>{t("Request a title")}</ButtonLink>
            ) : (
              t("The library is empty. Ask an admin to add series.")
            )}
          </EmptyState>
        )
      )}
      {data && data.total === 0 && (q || active.length > 0 || rootFolderId || language || listParam || genres.length > 0) && (
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
                  <Cover src={apiUrl(s.coverUrl)} alt={s.displayTitle || s.title} className="aspect-[2/3] w-full ring-accent/60 transition group-hover:ring-2" />
                  {manage && !s.monitored && <div className={`absolute top-1.5 ${selecting ? "left-9" : "left-1.5"}`}><Badge>{t("unmonitored")}</Badge></div>}
                  {manage && s.stats.missingCount > 0 && (
                    <div className="absolute right-1.5 top-1.5">
                      <Badge tone="warn">{s.stats.missingCount}{" " + t("missing")}</Badge>
                    </div>
                  )}
                </div>
                <Progress value={p.pct} tone={p.tone} />
                {s.stats.readCount > 0 && <ReadBar s={s} />}
                <div className="line-clamp-2 text-sm font-medium leading-tight">{s.displayTitle || s.title}</div>
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
        <>
          <div className="mb-2 flex justify-end">
            <ColumnPicker value={columns} onChange={setColumns} requests={requests} />
          </div>
          <LibraryTable
            list={list}
            columns={columns}
            sort={sort}
            order={order}
            onSort={(by, next) => setSortAndReset(by, next)}
            edit={manage}
            requests={requests}
            roots={new Map((roots ?? []).map((root) => [root.id, root.path]))}
            selecting={selecting}
            selected={selected}
            onToggle={toggle}
          />
        </>
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
      {data && data.total > 0 && account.can("library.edit") && (
        <p className="mt-3 text-center text-sm text-muted">
          {t(q || active.length > 0 || rootFolderId || language || genres.length > 0 ? "{count} matching series · {size} on disk" : "{count} series · {size} on disk", { count: data.total, size: bytes(data.totalSize) })}
        </p>
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
