import { useEffect, useRef, useState } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, RefreshCw, SlidersHorizontal, Sparkles, TriangleAlert } from "lucide-react";
import { Link, Navigate, useParams, useSearchParams } from "react-router";
import { api, ApiError, unwrap } from "../../api/client";
import { Button, EmptyState, ErrorBox, Input, Modal, PageHeader, Select, Spinner } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { languageName } from "../../lib/format";
import { t, type MessageKey } from "../../lib/i18n/core";
import { useUIMode } from "../../lib/uiPreferences";
import { LibraryCard, SourceCard, useDiscover } from "./Discover";
import { shelfQueryOptions } from "./shelfQuery";
import { parseShelfFilters, restoreShelfFilters, shelfParams, shelves, shelfSorts, shouldLoadShelfPage, type Shelf, type ShelfFilters } from "./shelfState";

const titles: Record<Shelf, MessageKey> = { recommendations: "Recommendations", "recently-updated": "Recently updated", popular: "Popular from your sources" };
const subtitles: Record<Shelf, MessageKey> = {
  recommendations: "Picked from your unread library using what you read and follow.",
  "recently-updated": "The latest changes across your library.",
  popular: "Prioritized using your library and language source order.",
};
const sortLabels: Record<string, MessageKey> = { recommended: "Recommended", popularity: "Popularity", "recently-updated": "Recently updated", newest: "Newest", title: "Title" };
const statusLabels: Record<string, MessageKey> = { unknown: "Unknown", ongoing: "Ongoing", completed: "Completed", hiatus: "Hiatus", cancelled: "Cancelled" };
const formatLabels: Record<string, MessageKey> = { manga: "Manga", manhwa: "Manhwa", manhua: "Manhua" };

export function DiscoverShelfPage() {
  const { shelf } = useParams();
  const { account } = useAccount();
  const viewer = `${account?.kind}:${account?.id}`;
  if (!shelves.includes(shelf as Shelf)) return <Navigate to="/discover" replace />;
  return <ShelfPage key={`${viewer}:${shelf}`} shelf={shelf as Shelf} viewer={viewer} />;
}

function ShelfPage({ shelf, viewer }: { shelf: Shelf; viewer: string }) {
  const [params, setParams] = useSearchParams();
  const storageKey = `mangarr:discover:${viewer}:${shelf}`;
  let saved: string | null = null;
  try { saved = localStorage.getItem(storageKey); } catch { /* URL state works without storage. */ }
  const filters = restoreShelfFilters(shelf, params.toString(), saved);
  const canonical = shelfParams(filters).toString();
  useEffect(() => {
    if (params.toString() !== canonical) setParams(canonical, { replace: true });
    try { localStorage.setItem(storageKey, canonical); } catch { /* Storage full or disabled. */ }
  }, [canonical, params, setParams, storageKey]);
  const apply = (next: ShelfFilters) => setParams(shelfParams(parseShelfFilters(shelf, shelfParams(next))));
  const { can } = useAccount();
  const { editing } = useUIMode();
  const manage = can(["library.add", "requests.manage"]);
  const discover = useDiscover();
  const catalogs = useQuery({ queryKey: ["catalogs"], enabled: manage, queryFn: () => unwrap(api.GET("/api/v1/catalogs")), staleTime: 30_000 });
  const roots = useQuery({ queryKey: ["rootfolders"], enabled: manage, queryFn: () => unwrap(api.GET("/api/v1/rootfolders")) });
  const tags = useQuery({ queryKey: ["tags"], enabled: shelf !== "popular", queryFn: () => unwrap(api.GET("/api/v1/tags")) });
  const options = shelfQueryOptions(shelf, filters, viewer);
  const query = useInfiniteQuery(options);
  const qc = useQueryClient();
  const restart = () => void qc.resetQueries({ queryKey: options.queryKey, exact: true });
  const pages = query.data?.pages ?? [];
  const library = pages.flatMap((page) => page.library);
  const popular = pages.flatMap((page) => page.popular);
  const errors = [...new Map(pages.flatMap((page) => page.sourceErrors).map((error) => [error.source, error])).values()];
  const count = shelf === "popular" ? popular.length : library.length;
  const sourceOptions = new Map<string, string>();
  for (const item of [...(discover.data?.popular ?? []), ...popular]) sourceOptions.set(`${item.moduleId}:${item.sourceId}`, `${item.sourceName} · ${languageName(item.language)}`);
  for (const catalog of catalogs.data?.items ?? []) {
    if (catalog.hidden || (shelf === "popular" && !catalog.enabled)) continue;
    sourceOptions.set(`${catalog.moduleId}:${catalog.id}`, `${catalog.displayName} · ${languageName(catalog.lang)}`);
  }
  const languages = [...new Set(["en", "ru", "uk", ...library.map((item) => item.language), ...popular.map((item) => item.language), ...(catalogs.data?.items ?? []).map((item) => item.lang), ...(roots.data ?? []).map((item) => item.language)])].filter((lang) => lang && !["*", "multi", "all"].includes(lang)).sort();
  const sentinel = useRef<HTMLDivElement>(null);
  const loadMore = query.fetchNextPage;
  const canLoad = shouldLoadShelfPage(query.hasNextPage, query.isFetching, query.isError);
  useEffect(() => {
    const element = sentinel.current;
    if (!element || !canLoad) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMore({ cancelRefetch: false });
    }, { root: element.closest("main"), rootMargin: "400px" });
    observer.observe(element);
    return () => observer.disconnect();
  }, [canLoad, loadMore, pages.length]);
  const expired = query.error instanceof ApiError && query.error.status === 410;

  return (
    <>
      <Link to="/discover" className="mb-4 inline-flex items-center gap-1.5 rounded-md py-1 text-sm text-muted hover:text-fg"><ArrowLeft className="size-4" />{t("Back to Discover")}</Link>
      <PageHeader title={t(titles[shelf])} subtitle={t(subtitles[shelf])} actions={<Button size="sm" loading={query.isFetching} icon={<RefreshCw className="size-4" />} onClick={restart}>{t("Refresh")}</Button>} />
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <span role="status" className="text-sm text-muted">{t("{count} titles loaded", { count })}</span>
        <div className="flex flex-wrap items-center gap-2">
        <ShelfFiltersButton key={canonical} shelf={shelf} filters={filters} apply={apply} sources={sourceOptions} languages={languages} roots={roots.data ?? []} tags={tags.data ?? []} />
        <label className="flex items-center gap-2 text-sm text-muted">{t("Sort by")}
          <Select className="w-auto max-w-52" value={filters.sort} onChange={(event) => apply({ ...filters, sort: event.target.value as ShelfFilters["sort"] })}>
            {shelfSorts[shelf].map((sort) => <option key={sort} value={sort}>{t(sortLabels[sort])}</option>)}
          </Select>
        </label>
        </div>
      </div>
      {errors.length > 0 && <details className="mb-4 rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm">
        <summary className="cursor-pointer font-medium text-warn"><TriangleAlert className="mr-2 inline size-4" />{t("Some sources are unavailable")} · {errors.length}</summary>
        <ul className="mt-2 space-y-1 text-xs text-muted">{errors.map((error) => <li key={error.source}><span className="text-fg">{error.name}:</span> {error.error}</li>)}</ul>
        <Button size="sm" className="mt-3" onClick={restart} loading={query.isFetching}>{t("Retry sources")}</Button>
      </details>}
      <div className="grid grid-cols-2 gap-x-4 gap-y-6 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-6" aria-label={t(titles[shelf])} aria-busy={query.isFetching}>
        {library.map((item) => <LibraryCard key={item.seriesId} item={item} recommendation={shelf === "recommendations"} grid />)}
        {popular.map((item) => <SourceCard key={`${item.moduleId}:${item.sourceId}:${item.url}`} item={item} manage={editing && manage} request={can("requests.create") && !manage} grid />)}
        {query.isPending && Array.from({ length: 12 }, (_, i) => <div key={i} aria-hidden className="animate-pulse"><div className="aspect-[2/3] rounded-md bg-panel" /><div className="mt-2 h-4 w-3/4 rounded bg-panel" /><div className="mt-2 h-3 w-1/2 rounded bg-panel" /></div>)}
      </div>
      {query.isError && <div role="alert" className="mt-5 space-y-3">
        <ErrorBox error={expired ? t("This browsing session expired. Restart to load fresh results.") : query.error} />
        <div className="flex gap-2">
          {!expired && <Button onClick={() => void (query.isFetchNextPageError ? query.fetchNextPage() : query.refetch())}>{t("Try again")}</Button>}
          {(expired || pages.length > 0) && <Button onClick={restart}>{t("Restart browsing")}</Button>}
        </div>
      </div>}
      {!query.isPending && !query.isError && !query.hasNextPage && count === 0 && <EmptyState title={t("No matching titles")} icon={<Sparkles className="size-8" />}>
        <p>{t("Try changing your filters or check back later.")}</p>
        <Button className="mt-3" onClick={() => apply({ sort: filters.sort })}>{t("Clear filters")}</Button>
      </EmptyState>}
      <div ref={sentinel} className="flex min-h-24 items-center justify-center py-6">
        {query.isFetching ? <div role="status" className="flex items-center gap-2 text-sm text-muted"><Spinner />{t("Loading titles…")}</div>
          : canLoad ? <Button onClick={() => void loadMore({ cancelRefetch: false })}>{t("Load more")}</Button>
          : !query.isError && count > 0 && <p role="status" className="text-sm text-muted">{t("You’ve reached the end of this shelf.")}</p>}
      </div>
    </>
  );
}

function ShelfFiltersButton({ shelf, filters, apply, sources, languages, roots, tags }: {
  shelf: Shelf; filters: ShelfFilters; apply: (filters: ShelfFilters) => void; sources: Map<string, string>; languages: string[];
  roots: { id: number; path: string }[]; tags: { id: number; label: string }[];
}) {
  const [draft, setDraft] = useState(filters);
  const [open, setOpen] = useState(false);
  const set = (key: keyof ShelfFilters, value: string) => setDraft({ ...draft, [key]: value });
  const active = Object.entries(filters).filter(([key, value]) => key !== "sort" && value !== undefined && value !== "").length;
  return (
    <>
    <Button size="sm" icon={<SlidersHorizontal className="size-4" />} aria-haspopup="dialog" onClick={() => (setDraft(filters), setOpen(true))}>
      {t("Filters")}{active > 0 && <span className="ml-1.5 rounded bg-accent/15 px-1.5 text-xs text-accent-2">{active}</span>}
    </Button>
    <Modal open={open} onClose={() => setOpen(false)} title={t("Filters")} footer={<>
      <Button type="button" onClick={() => { const next = { sort: filters.sort }; setDraft(next); apply(next); setOpen(false); }}>{t("Clear filters")}</Button>
      <Button type="submit" form="shelf-filters" variant="primary">{t("Apply filters")}</Button>
    </>}>
      <form id="shelf-filters" onSubmit={(event) => { event.preventDefault(); apply(draft); setOpen(false); }}>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <label className="space-y-1.5 text-sm font-medium">{t("Language")}
            <Select value={draft.lang ?? ""} onChange={(event) => set("lang", event.target.value)}>
              <option value="">{t("All languages")}</option>
              {draft.lang && !languages.includes(draft.lang) && <option value={draft.lang}>{languageName(draft.lang)}</option>}
              {languages.map((lang) => <option key={lang} value={lang}>{languageName(lang)}</option>)}
            </Select>
          </label>
          <label className="min-w-0 space-y-1.5 text-sm font-medium">{t("Source")}
            <Select value={draft.source ?? ""} onChange={(event) => set("source", event.target.value)}>
              <option value="">{t("All sources")}</option>
              {draft.source && !sources.has(draft.source) && <option value={draft.source}>{t("Selected source")}</option>}
              {[...sources].map(([key, name]) => <option key={key} value={key}>{name}</option>)}
            </Select>
          </label>
          <label className="space-y-1.5 text-sm font-medium">{t("Library membership")}
            <Select value={draft.inLibrary ?? ""} onChange={(event) => set("inLibrary", event.target.value)}>
              <option value="">{t("All")}</option><option value="true">{t("In library")}</option><option value="false">{t("Not in library")}</option>
            </Select>
          </label>
          {shelf !== "popular" && <>
            <label className="space-y-1.5 text-sm font-medium">{t("Genre")}<Input value={draft.genre ?? ""} placeholder={t("Any genre")} onChange={(event) => set("genre", event.target.value)} /></label>
            <label className="space-y-1.5 text-sm font-medium">{t("Metadata tag")}<Input value={draft.tag ?? ""} placeholder={t("Any metadata tag")} onChange={(event) => set("tag", event.target.value)} /></label>
            <label className="space-y-1.5 text-sm font-medium">{t("Library tag")}<Select value={draft.tagId ?? ""} onChange={(event) => set("tagId", event.target.value)}>
              <option value="">{t("All tags")}</option>
              {draft.tagId && !tags.some((tag) => String(tag.id) === String(draft.tagId)) && <option value={draft.tagId}>{t("Selected tag")}</option>}
              {tags.map((tag) => <option key={tag.id} value={tag.id}>{tag.label}</option>)}
            </Select></label>
            <label className="space-y-1.5 text-sm font-medium">{t("Format")}<Select value={draft.format ?? ""} onChange={(event) => set("format", event.target.value)}>
              <option value="">{t("All formats")}</option>{Object.entries(formatLabels).map(([value, text]) => <option key={value} value={value}>{t(text)}</option>)}
            </Select></label>
            <label className="space-y-1.5 text-sm font-medium">{t("Status")}<Select value={draft.status ?? ""} onChange={(event) => set("status", event.target.value)}>
              <option value="">{t("All statuses")}</option>{Object.entries(statusLabels).map(([value, text]) => <option key={value} value={value}>{t(text)}</option>)}
            </Select></label>
          </>}
          {(roots.length > 0 || draft.rootFolderId) && <label className="min-w-0 space-y-1.5 text-sm font-medium">{t("Library")}<Select value={draft.rootFolderId ?? ""} onChange={(event) => set("rootFolderId", event.target.value)}>
            <option value="">{t("All libraries")}</option>
            {draft.rootFolderId && !roots.some((root) => String(root.id) === String(draft.rootFolderId)) && <option value={draft.rootFolderId}>{t("Selected library")}</option>}
            {roots.map((root) => <option key={root.id} value={root.id}>{root.path}</option>)}
          </Select></label>}
        </div>
        {shelf === "popular" && <p className="mt-4 text-xs text-muted">{t("Your source content settings apply.")}</p>}
      </form>
    </Modal>
    </>
  );
}
