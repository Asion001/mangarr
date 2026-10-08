import { useQuery } from "@tanstack/react-query";
import { ArrowRight, BookOpen, Clock3, Compass, RefreshCw, Sparkles, TriangleAlert } from "lucide-react";
import { Link } from "react-router";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, EmptyState, ErrorBox, PageHeader } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { relative } from "../../lib/format";
import { t } from "../../lib/i18n/core";
import { languageName } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useUIMode } from "../../lib/uiPreferences";
import { ContinueReading, useReadingShelf } from "../series/ContinueReading";

type LibraryItem = S["DiscoverLibraryItem"];
type SourceItem = S["DiscoverSourceItem"];

export function useDiscover() {
  return useQuery({
    queryKey: ["discover"],
    queryFn: () => unwrap(api.GET("/api/v1/discover", { params: { query: { limit: 24 } } })),
    staleTime: 5 * 60_000,
  });
}

function recommendationReason(item: LibraryItem) {
  switch (item.reason) {
    case "matches-genres":
      return t("Based on your {genres} reading", { genres: item.matchingGenres?.map(genreName).join(" · ") || t("library") });
    case "followed":
      return t("You follow this series");
    case "recently-added":
      return t("Recently added to your library");
    default:
      return "";
  }
}

function Shelf({ title, subtitle, icon, href, children }: { href: string; title: string; subtitle?: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="mb-8" aria-label={title}>
      <div className="mb-3 flex items-end justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-base font-semibold">{icon}{title}</h2>
          {subtitle && <p className="mt-0.5 text-sm text-muted">{subtitle}</p>}
        </div>
        <Link to={href} className="flex shrink-0 items-center gap-1 rounded-md py-2 text-sm font-medium text-accent-2 hover:underline">{t("See all")}<ArrowRight className="size-4" /></Link>
      </div>
      <div className="flex snap-x snap-mandatory gap-4 overflow-x-auto pb-3">{children}</div>
    </section>
  );
}

export function LibraryCard({ item, recommendation = false, grid = false }: { item: LibraryItem; recommendation?: boolean; grid?: boolean }) {
  const detail = recommendation ? recommendationReason(item) : item.latestChapter ? `${t("Chapter")} ${item.latestChapter}` : t("Library updated");
  return (
    <Link to={`/series/${item.seriesId}`} className={grid ? "group min-w-0" : "group w-36 shrink-0 snap-start sm:w-40"} aria-label={`${t("Open series")}: ${item.title}`}>
      <div className="relative">
        <Cover src={apiUrl(item.coverUrl)} alt={item.title} className="aspect-[2/3] w-full ring-accent/60 transition duration-200 group-hover:-translate-y-1 group-hover:ring-2" />
        {item.unread > 0 && <span className="absolute right-1.5 top-1.5"><Badge tone="accent">{item.unread} {t("unread")}</Badge></span>}
      </div>
      <div className="mt-2 line-clamp-2 text-sm font-medium leading-tight group-hover:text-accent-2">{item.title}</div>
      <div className="mt-1 line-clamp-2 min-h-8 text-xs leading-4 text-muted">{detail}</div>
    </Link>
  );
}

function sourceDestination(item: SourceItem, manage: boolean, request: boolean) {
  if (item.existingSeriesId) return `/series/${item.existingSeriesId}`;
  if (manage) {
    const title = encodeURIComponent(item.title);
    const source = encodeURIComponent(`${item.moduleId}:${item.sourceId}`);
    return `/add/manual/-/sources?title=${title}&sq=${title}&src=${source}`;
  }
  if (request) return `/requests?tab=ask&q=${encodeURIComponent(item.title)}`;
  return "";
}

export function SourceCard({ item, manage, request, grid = false }: { item: SourceItem; manage: boolean; request: boolean; grid?: boolean }) {
  const destination = sourceDestination(item, manage, request);
  const card = (
    <>
      <div className="relative">
        <Cover src={item.thumbnailUrl ? apiUrl(item.thumbnailUrl) : undefined} alt={item.title} className="aspect-[2/3] w-full ring-accent/60 transition duration-200 group-hover:-translate-y-1 group-hover:ring-2" />
        <span className="absolute left-1.5 top-1.5"><Badge tone={item.existingSeriesId ? "ok" : "default"}>{item.existingSeriesId ? t("In library") : languageName(item.language)}</Badge></span>
        {destination && !item.existingSeriesId && (
          <span className="absolute bottom-2 right-2 rounded-full bg-black/75 p-2 text-white opacity-0 transition group-hover:opacity-100 group-focus-visible:opacity-100">
            <ArrowRight className="size-4" />
          </span>
        )}
      </div>
      <div className="mt-2 line-clamp-2 text-sm font-medium leading-tight group-hover:text-accent-2">{item.title}</div>
      <div className="mt-1 line-clamp-1 text-xs text-muted">{item.sourceName}</div>
    </>
  );
  const className = grid ? "group min-w-0" : "group w-36 shrink-0 snap-start sm:w-40";
  return destination ? <Link to={destination} className={className} aria-label={`${item.existingSeriesId ? t("Open series") : manage ? t("Add series") : t("Request")}: ${item.title}`}>{card}</Link> : <article className={className}>{card}</article>;
}

function Spotlight({ item }: { item: LibraryItem }) {
  return (
    <section className="relative mb-8 min-h-72 overflow-hidden rounded-xl border border-border bg-panel">
      <img src={apiUrl(item.coverUrl)} alt="" aria-hidden className="absolute inset-0 h-full w-full scale-110 object-cover opacity-25 blur-xl" />
      <div className="absolute inset-0 bg-gradient-to-r from-bg via-bg/90 to-bg/25" />
      <div className="relative flex min-h-72 items-center gap-6 p-6 sm:p-8">
        <Cover src={apiUrl(item.coverUrl)} alt={item.title} className="hidden aspect-[2/3] w-36 shrink-0 shadow-2xl sm:block" />
        <div className="max-w-2xl">
          <div className="mb-3 flex flex-wrap gap-2">
            <Badge tone="accent">{item.reason === "recent-update" ? t("Recently updated") : t("Recommended for you")}</Badge>
            {item.genres.slice(0, 3).map((genre) => <Badge key={genre}>{genreName(genre)}</Badge>)}
          </div>
          <h2 className="text-2xl font-semibold sm:text-3xl">{item.title}</h2>
          {recommendationReason(item) && <p className="mt-2 text-sm text-accent-2">{recommendationReason(item)}</p>}
          {item.description && <p className="mt-3 line-clamp-3 max-w-xl text-sm leading-6 text-fg/80">{item.description}</p>}
          <div className="mt-5 flex flex-wrap items-center gap-3">
            <Link to={`/series/${item.seriesId}`}><Button variant="primary" icon={<BookOpen className="size-4" />}>{t("Open series")}</Button></Link>
            {item.unread > 0 && <span className="text-sm text-muted">{item.unread} {t("unread chapters")}</span>}
          </div>
        </div>
      </div>
    </section>
  );
}

function DiscoverSkeleton() {
  return (
    <div aria-label={t("Loading discovery")} className="animate-pulse">
      <div className="mb-8 h-72 rounded-xl bg-panel" />
      <div className="mb-8 flex gap-4 overflow-hidden">{Array.from({ length: 7 }, (_, i) => <div key={i} className="h-64 w-36 shrink-0 rounded-md bg-panel sm:w-40" />)}</div>
    </div>
  );
}

export function DiscoverPage() {
  const query = useDiscover();
  const { can } = useAccount();
  const { editing } = useUIMode();
  const manage = editing && can(["library.add", "requests.manage"]);
  const request = can("requests.create") && !can(["library.add", "requests.manage"]);
  const data = query.data;
  // the spotlight is for something new: never a series already on the Continue reading shelf
  const shelf = useReadingShelf();
  const reading = new Set((shelf.data?.items ?? []).map((i) => i.seriesId));
  const spotlight = shelf.isPending ? undefined : (data?.recommendations.find((i) => !reading.has(i.seriesId)) ?? data?.updates.find((i) => !reading.has(i.seriesId)));
  const empty = data && data.recommendations.length === 0 && data.updates.length === 0 && data.popular.length === 0;
  return (
    <>
      <PageHeader
        title={t("Discover")}
        subtitle={data ? t("Updated {when}", { when: relative(data.generatedAt) }) : t("Recommendations, library updates and popular titles in one place.")}
        actions={<Button size="sm" loading={query.isFetching} icon={<RefreshCw className="size-4" />} onClick={() => void query.refetch()}>{t("Refresh")}</Button>}
      />
      {query.isLoading && <DiscoverSkeleton />}
      {query.error && <ErrorBox error={query.error} />}
      {data && (
        <>
          {spotlight && <Spotlight item={spotlight} />}
          <ContinueReading />
          {data.recommendations.length > 0 && (
            <Shelf href="/discover/recommendations" title={t("Recommendations")} subtitle={t("Picked from your unread library using what you read and follow.")} icon={<Sparkles className="size-4 text-accent-2" />}>
              {data.recommendations.map((item) => <LibraryCard key={item.seriesId} item={item} recommendation />)}
            </Shelf>
          )}
          {data.updates.length > 0 && (
            <Shelf href="/discover/recently-updated" title={t("Recently updated")} subtitle={t("The latest changes across your library.")} icon={<Clock3 className="size-4 text-info" />}>
              {data.updates.map((item) => <LibraryCard key={item.seriesId} item={item} />)}
            </Shelf>
          )}
          {data.popular.length > 0 && (
            <Shelf href="/discover/popular" title={t("Popular from your sources")} subtitle={t("Prioritized using your library and language source order.")} icon={<Compass className="size-4 text-ok" />}>
              {data.popular.map((item) => <SourceCard key={`${item.moduleId}:${item.sourceId}:${item.url}`} item={item} manage={manage} request={request} />)}
            </Shelf>
          )}
          {empty && <EmptyState title={t("Nothing to discover yet")} icon={<Sparkles className="size-8" />}>{t("Add series or enable source catalogs to fill this page.")}</EmptyState>}
          {data.sourceErrors.length > 0 && (
            <details className="rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm">
              <summary className="flex cursor-pointer items-center gap-2 font-medium text-warn"><TriangleAlert className="size-4" />{t("Some sources are unavailable")} · {data.sourceErrors.length}</summary>
              <ul className="mt-2 space-y-1 pl-6 text-xs text-muted">{data.sourceErrors.map((error) => <li key={error.source}><span className="text-fg">{error.name}:</span> {error.error}</li>)}</ul>
            </details>
          )}
        </>
      )}
    </>
  );
}
