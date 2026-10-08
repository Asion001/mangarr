import { getLocale, t as tr, t } from "../../lib/i18n/core";
import { LanguageChips, useSourcesSettings } from "../../components/LanguageChooser";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, Navigate, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { ArrowDown, ArrowUp, Plus, Search, X } from "lucide-react";
import { api, apiUrl, unwrap, type AddEditionsRequest, type EditionOptions, type LookupResult, type S } from "../../api/client";
import { useProfiles } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, ErrorBox, Field, IconButton, Input, Loading, PageHeader, Select, Spinner, Switch } from "../../components/ui";
import { sessionState, useQueryParam } from "../../lib/urlState";
import { useToast } from "../../lib/toast";
import { useAccount } from "../../lib/account";
import { useSettingsDoc } from "../settings/useSettingsDoc";
import { LanguageSelect, useLanguageFolders, useOfferedLanguages } from "../../components/LanguageSelect";
import { languageName } from "../../lib/format";
import { ReadButton } from "./Preview";
import { SourceSearchModal, pickKey, useCatalogTargets, useDefaultsSearch, useQuickSearch, type Picked, type Scope } from "./SourceSearch";

/** MetadataSearch looks up series across metadata modules. */
export function MetadataSearch({
  initialQuery = "",
  query: controlled,
  setQuery: setControlled,
  onPick,
  action,
  placeholder = "Search by title (AniList and other metadata modules)",
  language = "",
  hideInput = false,
  hideErrors = false,
}: {
  initialQuery?: string;
  query?: string;
  setQuery?: (q: string) => void;
  onPick?: (c: LookupResult) => void;
  /** action replaces the Select button (e.g. Request). */
  action?: (c: LookupResult) => ReactNode;
  placeholder?: string;
  language?: string;
  /** hideInput: the query comes from a search box elsewhere on the page. */
  hideInput?: boolean;
  /** hideErrors: no provider errors or "nothing found" notes. */
  hideErrors?: boolean;
}) {
  const { isAdmin } = useAccount();
  const [local, setLocal] = useState(initialQuery);
  const query = controlled ?? local;
  const setQuery = setControlled ?? setLocal;
  const [draft, setDraft] = useState(query);
  useEffect(() => setDraft(query), [query]);
  const { data, isLoading, error } = useQuery({
    // without a language, results are titled in the interface one
    queryKey: ["lookup", query, language || getLocale()],
    queryFn: () => unwrap(api.GET("/api/v1/series/lookup", { params: { query: { q: query, lang: language || undefined } } })),
    enabled: query.length > 0,
    staleTime: 5 * 60_000,
  });
  return (
    <div>
      {!hideInput && <form
        className="mb-4 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(draft.trim());
        }}
      >
        <Input autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={placeholder} />
        <Button type="submit" variant="primary" icon={<Search className="size-4" />}>{t("Search")}</Button>
      </form>}
      {isLoading && <Loading />}
      {error && !hideErrors && <ErrorBox error={error} />}
      {!hideErrors && data?.errors?.map((e) => (
        <p key={e} className="mb-2 text-xs text-warn">
          {e}
        </p>
      ))}
      <div className="flex flex-col gap-2">
        {data?.results?.map((r) => (
          <div key={r.provider + r.id} className={clsx("flex gap-3 rounded-lg border border-border bg-panel p-3", isNovel(r.format) && "opacity-60")}>
            <Cover src={r.coverUrl} alt={r.title} className="aspect-[2/3] w-16 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{r.title}</span>
                {r.year ? <Badge>{r.year}</Badge> : null}
                {r.format && <Badge tone={isNovel(r.format) ? "warn" : "default"} title={isNovel(r.format) ? tr("Manga sources have no chapters of novels") : undefined}>{r.format}</Badge>}
                {r.status && <Badge tone="info">{r.status}</Badge>}
                <Badge tone="accent">{r.moduleName}</Badge>
                {r.also?.map((a) => (
                  <Badge key={a.provider}>{a.provider}</Badge>
                ))}
              </div>
              {r.altTitles && <p className="mt-0.5 line-clamp-1 text-xs text-muted">{r.altTitles.slice(0, 3).join(" · ")}</p>}
              {r.description && <p className="mt-1 line-clamp-2 text-sm text-fg/80">{r.description}</p>}
            </div>
            <div className="shrink-0 self-center">
              {action ? (
                action(r)
              ) : r.existingSeriesId ? (
                <Link to={`/series/${r.existingSeriesId}`}>
                  <Button size="sm">{t("In library")}</Button>
                </Link>
              ) : (
                <Button size="sm" variant="primary" onClick={() => onPick?.(r)}>{t("Select")}</Button>
              )}
            </div>
          </div>
        ))}
        {!hideErrors && data && data.results.length === 0 && data.providers > 0 && <p className="text-sm text-muted">{t("No metadata found.")}</p>}
        {!hideErrors && data && data.providers === 0 && (
          <p className="text-sm text-warn">
            {t("No metadata provider is set up, so nothing was searched.")}{" "}
            {isAdmin ? <Link to="/settings/metadata" className="text-accent-2 hover:underline">{t("Add one in Settings → Metadata")}</Link> : t("Ask an admin to add one.")}
          </p>
        )}
      </div>
    </div>
  );
}

/** isNovel: formats no manga source has chapters of. */
const isNovel = (format?: string) => /novel/i.test(format ?? "");

/** Step 1 (/add?q=): find the series by its metadata, or add it by title. */
export function AddSearchStep() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [q, setQ] = useQueryParam("q");
  const [lang, setLang] = useQueryParam("lang");
  // the languages set up on this server, not every language a catalog has
  const { defaults: searchLanguages, languages } = useOfferedLanguages();
  const account = useAccount();
  const searchSettings = useSourcesSettings();
  const [title, setTitle] = useState(q);
  const pick = (r: LookupResult) => {
    qc.setQueryData(["lookup-item", r.moduleId, r.id], r);
    nav(`/add/${r.moduleId}/${encodeURIComponent(r.id)}/sources${lang ? `?lang=${encodeURIComponent(lang)}` : ""}`);
  };
  return (
    <>
      <PageHeader title={t("Add series")} subtitle={t("Find it, check where chapters come from, add.")} />
      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
          <div className="mb-3">
            <LanguageChips value={lang} onChange={(v) => setLang(v, { replace: false })} primary={searchLanguages} options={languages} />
            {account.isAdmin && searchSettings.data && !(searchSettings.data.defaultLanguages ?? []).length && !lang && (
              <p className="mt-2 text-sm text-warn">
                {t("No search language is set, so Add… takes the first match in any language. Pick a language above, or")}{" "}
                <Link to="/settings/search" className="text-accent-2 hover:underline">{t("choose the languages you read")}</Link>.
              </p>
            )}
          </div>
          <MetadataSearch
            query={q}
            language={lang}
            setQuery={(v) => setQ(v, { replace: false })}
            onPick={pick}
            action={(r) =>
              r.existingSeriesId ? (
                <Link to={`/series/${r.existingSeriesId}`}><Button size="sm">{t("In library")}</Button></Link>
              ) : (
                <span className="flex gap-2">
                  {!isNovel(r.format) && <ReadButton result={r} language={lang} />}
                  <Button size="sm" variant={isNovel(r.format) ? "secondary" : "primary"} onClick={() => pick(r)}>{t("Add…")}</Button>
                </span>
              )
            }
          />
        </div>
        <aside aria-label={t("Other ways to add")} className="flex w-full shrink-0 flex-col gap-3 lg:w-72">
          <h2 className="text-[11px] font-semibold uppercase tracking-wide text-muted">{t("Can’t find it?")}</h2>
          <form
            className="flex flex-col gap-2 rounded-lg border border-border bg-panel p-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (title.trim()) nav(`/add/manual/-/sources?title=${encodeURIComponent(title.trim())}${lang ? `&lang=${encodeURIComponent(lang)}` : ""}`);
            }}
          >
            <span className="font-medium">{t("Add by title only")}</span>
            <span className="text-sm text-muted">{t("For series no metadata site has. Title, cover and description come from the source.")}</span>
            <div className="flex gap-2">
              <Input aria-label={t("Series title")} value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t("Series title")} />
              <Button type="submit" disabled={!title.trim()}>{t("Next")}</Button>
            </div>
          </form>
          <Link to="/sources" className="flex flex-col gap-1 rounded-lg border border-border bg-panel p-4 hover:border-accent/60">
            <span className="font-medium">{t("Browse a source")}</span>
            <span className="text-sm text-muted">{t("Popular and latest titles on a site; add from there.")}</span>
          </Link>
          <Link to="/import" className="flex flex-col gap-1 rounded-lg border border-border bg-panel p-4 hover:border-accent/60">
            <span className="font-medium">{t("Import a backup")}</span>
            <span className="text-sm text-muted">{t("Your library and read progress from Mihon, Tachiyomi, Suwayomi or Aidoku.")}</span>
          </Link>
        </aside>
      </div>
    </>
  );
}

/** useAddContext resolves the series being added from the route. */
function useAddContext() {
  const { moduleId = "", metaId = "" } = useParams();
  const [params] = useSearchParams();
  const manual = moduleId === "manual";
  const meta = useQuery({
    queryKey: ["lookup-item", Number(moduleId), metaId, params.get("lang") ?? ""],
    queryFn: () => unwrap(api.GET("/api/v1/series/lookup/{moduleId}/{id}", { params: { path: { moduleId: Number(moduleId), id: metaId }, query: { lang: params.get("lang") || undefined } } })),
    enabled: !manual && !!metaId,
    staleTime: 30 * 60_000,
  });
  const title = manual ? (params.get("title") ?? "") : (meta.data?.title ?? "");
  const language = params.get("lang") ?? "";
  const storageKey = `mangarr.add:${moduleId}:${manual ? title : metaId}`;
  // adding for a request (from the Requests page): link it when added
  const fromRequest = Number(params.get("request") ?? 0);
  if (fromRequest > 0) sessionState.set(storageKey + ":request", fromRequest);
  const requestId = fromRequest || sessionState.get<number>(storageKey + ":request", 0);
  const titles = [title, ...(meta.data?.altTitles ?? [])].filter(Boolean);
  return { manual, meta: meta.data ?? null, metaLoading: meta.isLoading, metaError: meta.error, title, titles, language, storageKey, requestId };
}

function usePicked(storageKey: string): [Picked[], (fn: (cur: Picked[]) => Picked[]) => void] {
  const [picked, setPicked] = useState<Picked[]>(() => sessionState.get<Picked[]>(storageKey + ":picked", []));
  useEffect(() => setPicked(sessionState.get<Picked[]>(storageKey + ":picked", [])), [storageKey]);
  const update = (fn: (cur: Picked[]) => Picked[]) =>
    setPicked((cur) => {
      const next = fn(cur);
      sessionState.set(storageKey + ":picked", next);
      return next;
    });
  return [picked, update];
}

type Options = {
  monitor: string;
  latestCount: number;
  fromChapter: number;
  monitorNew: string;
};

/** Per-language settings of an edition; 0 and "__default" follow the language defaults. */
type EditionChoice = { profileId: number; direction: string };

const pickOf = (c: S["QuickCandidate"]): Picked => ({ manga: c.manga, group: { moduleId: c.moduleId, sourceId: c.sourceId, sourceName: c.sourceName, lang: c.lang } });

/** normLang: catalogs spanning several languages have no edition language. */
const normLang = (l?: string) => {
  const v = (l ?? "").trim().toLowerCase();
  return v === "all" || v === "multi" ? "" : v;
};
/** editionLang is the edition a picked source goes to. */
const editionLang = (p: Picked) => normLang(p.lang) || normLang(p.group.lang);

/**
 * Step 2 (/add/:moduleId/:metaId/sources): review and add. The best match at
 * your sources is picked for you; sources in several languages become one
 * edition per language, each in that language's folder.
 */
export function AddReviewStep() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const ctx = useAddContext();
  const [params] = useSearchParams();
  const src = params.get("src");
  const keys = useMemo(() => (src ? src.split(",") : []), [src]);
  const scope: Scope = keys.length ? "custom" : "active";
  const { data: profiles } = useProfiles();
  const sourceSettings = useSettingsDoc<S["Sources"]>("sources");
  const { defaults: useLangsFallback, languages: allLangs } = useOfferedLanguages();
  const [picked, setPicked] = usePicked(ctx.storageKey);
  const [searching, setSearching] = useState<{ mode: "change" | "add"; lang: string } | null>(null);
  const defaults: Options = { monitor: "all", latestCount: 10, fromChapter: 1, monitorNew: "all" };
  const [o, setO] = useState<Options>(() => ({ ...defaults, ...sessionState.get<Partial<Options>>(ctx.storageKey + ":options", {}) }));
  const patch = (p: Partial<Options>) =>
    setO((cur) => {
      const next = { ...cur, ...p };
      sessionState.set(ctx.storageKey + ":options", next);
      return next;
    });
  const [choices, setChoices] = useState<Record<string, EditionChoice>>(() => sessionState.get(ctx.storageKey + ":editions", {}));
  const choose = (lang: string, p: Partial<EditionChoice>) =>
    setChoices((cur) => {
      const next = { ...cur, [lang]: { ...{ profileId: 0, direction: "__default" }, ...cur[lang], ...p } };
      sessionState.set(ctx.storageKey + ":editions", next);
      return next;
    });
  const [adding, setAdding] = useState<"" | "download" | "later">("");

  // editions in the order their languages first appear
  const langs = Array.from(new Set(picked.map(editionLang)));
  const groups = langs.map((lang) => ({ lang, items: picked.filter((p) => editionLang(p) === lang) }));
  const folders = useLanguageFolders(langs.filter(Boolean));
  const langDefault = (lang: string) => sourceSettings.value?.languageDefaults?.find((p) => p.language.toLowerCase() === lang);
  const profileOf = (lang: string) => choices[lang]?.profileId || langDefault(lang)?.profileId || profiles?.find((p) => p.isDefault)?.id || profiles?.[0]?.id || 0;
  const directionOf = (lang: string) => {
    const d = choices[lang]?.direction ?? "__default";
    if (d !== "__default") return d;
    return langDefault(lang)?.readingDirection || (ctx.meta?.format === "manhwa" || ctx.meta?.format === "manhua" ? "webtoon" : "");
  };
  const blocked = langs.some((lang) => !lang || !folders.get(lang) || !!folders.get(lang)?.error);

  const query = params.get("sq") || ctx.title;
  const searchLang = ctx.language;
  const { gen } = useCatalogTargets(scope, searchLang, keys);
  // picked catalogs: the quick search; otherwise every source of each
  // language's default list, for every search language (or the one chosen)
  const custom = scope === "custom";
  const quick = useQuickSearch({ query, titles: ctx.titles, scope, keys, lang: searchLang, enabled: custom && !ctx.metaLoading && !!query, gen });
  const found = useDefaultsSearch({ query, titles: ctx.titles, lang: searchLang, enabled: !custom && !ctx.metaLoading && !!query, gen });
  const searching_ = custom ? quick : found;
  const threshold = (custom ? quick.data?.threshold : found.data?.threshold) ?? 0.88;
  // what was found, prefilled once unless you already chose sources: one
  // edition per language, its default sources in list order (first primary)
  useEffect(() => {
    const auto = ctx.storageKey + ":auto";
    if (sessionState.get(auto, false)) return;
    let best: Picked[] = [];
    if (custom) {
      const m = quick.data?.match;
      if (!m) return;
      best = [pickOf(m)];
      if (ctx.requestId) {
        for (const c of [...(quick.data?.top ?? [])].sort((a, b) => b.score - a.score)) {
          if (c.score >= threshold && !best.some((b) => editionLang(b) === normLang(c.lang))) best.push(pickOf(c));
        }
      }
    } else {
      if (!found.data) return;
      for (const ed of found.data.editions) for (const src of ed.sources) if (src.match) best.push({ ...pickOf(src.match), lang: normLang(src.match.lang) ? undefined : ed.lang });
      if (!best.length) return;
    }
    sessionState.set(auto, true);
    setPicked((cur) => (cur.length ? cur : best));
  }, [quick.data, found.data]);

  const withParam = (ps: URLSearchParams, k: string, v: string) => {
    const next = new URLSearchParams(ps);
    next.set(k, v);
    return `?${next}`;
  };
  if (ctx.metaLoading) return <Loading />;
  if (ctx.metaError) return <ErrorBox error={ctx.metaError} />;
  if (!ctx.title) return <ErrorBox error="Missing series title" />;

  // every confident result we know of, with the edition it would join
  const cands: (S["QuickCandidate"] & { edition?: string; empty?: boolean })[] = custom
    ? [...(quick.data?.match ? [quick.data.match] : []), ...(quick.data?.top ?? [])]
    : (found.data?.editions ?? []).flatMap((ed) =>
        ed.sources.flatMap((src) => [
          ...(src.match ? [{ ...src.match, edition: ed.lang }] : []),
          ...(src.others ?? []).map((c) => ({ ...c, edition: ed.lang })),
          ...(src.empty ? [{ ...src.empty, edition: ed.lang, empty: true }] : []),
        ]),
      );
  // chapter counts we know: the search's candidates, else the result tile's count
  const counts = new Map<string, number>();
  for (const c of cands) if (c.chapters) counts.set(pickKey(pickOf(c)), c.chapters.count);
  const countOf = (p: Picked) => counts.get(pickKey(p)) ?? p.manga.chapterCount ?? undefined;
  const total = picked[0] ? countOf(picked[0]) : undefined;
  const queued = { all: total, future: 0, latest: total === undefined ? undefined : Math.min(o.latestCount, total), from: undefined, none: 0 }[o.monitor as "all"];
  // also found: one chip per site and edition, the rest behind "N more"
  const unpicked = cands.filter((c, i) => c.score >= threshold && !picked.some((p) => pickKey(p) === pickKey(pickOf(c))) && cands.findIndex((x) => pickKey(pickOf(x)) === pickKey(pickOf(c))) === i);
  const siteKey = (c: S["QuickCandidate"] & { edition?: string }) => `${c.moduleId}:${c.sourceId}:${c.edition ?? ""}`;
  const also = unpicked.filter((c, i) => unpicked.findIndex((x) => siteKey(x) === siteKey(c)) === i).slice(0, 6);
  const moreAt = (c: S["QuickCandidate"] & { edition?: string }) => unpicked.filter((x) => siteKey(x) === siteKey(c)).length - 1;
  const matchKey = custom && quick.data?.match ? pickKey(pickOf(quick.data.match)) : "";
  // moving swaps with the neighbour in the same edition
  const move = (p: Picked, dir: -1 | 1) =>
    setPicked((cur) => {
      const same = cur.map((x, i) => [x, i] as const).filter(([x]) => editionLang(x) === editionLang(p)).map(([, i]) => i);
      const at = same.indexOf(cur.findIndex((x) => pickKey(x) === pickKey(p)));
      const to = same[at + dir];
      return to === undefined ? cur : swap(cur, same[at], to);
    });
  const setLang = (p: Picked, lang: string) => setPicked((cur) => cur.map((x) => (pickKey(x) === pickKey(p) ? { ...x, lang } : x)));

  const add = async (searchMissing: boolean) => {
    setAdding(searchMissing ? "download" : "later");
    try {
      const res = await unwrap(
        api.POST("/api/v1/series/editions", {
          body: {
            metadata: ctx.meta ? { moduleId: ctx.meta.moduleId, provider: ctx.meta.provider, id: ctx.meta.id } : undefined,
            title: ctx.meta ? undefined : ctx.title,
            sources: picked.map((p) => ({ moduleId: p.group.moduleId, sourceId: p.group.sourceId, url: p.manga.url, engineRef: p.manga.engineRef, title: p.manga.title, sourceName: p.group.sourceName, lang: editionLang(p) || p.group.lang })),
            editions: langs.map((lang) => ({ language: lang, profileId: profileOf(lang) || undefined, readingDirection: (directionOf(lang) || undefined) as EditionOptions["readingDirection"] })),
            monitor: o.monitor as AddEditionsRequest["monitor"],
            latestCount: o.monitor === "latest" ? o.latestCount : undefined,
            fromChapter: o.monitor === "from" ? o.fromChapter : undefined,
            monitorNew: o.monitorNew as AddEditionsRequest["monitorNew"],
            searchMissing,
            requestId: ctx.requestId || undefined,
          },
        }),
      );
      for (const k of [":picked", ":options", ":editions", ":request", ":auto"]) sessionState.remove(ctx.storageKey + k);
      qc.invalidateQueries({ queryKey: ["series"] });
      qc.invalidateQueries({ queryKey: ["requests"] });
      qc.invalidateQueries({ queryKey: ["rootfolders"] });
      const first = res.editions[0];
      toast.success(res.editions.length > 1 ? tr("{title} added in {count} languages", { title: first.title, count: res.editions.length }) : tr("{title} added", { title: first.title }), tr("Fetching chapters…"));
      nav(`/series/${first.id}`);
    } catch (e) {
      toast.fromError(e, tr("Could not add series"));
    } finally {
      setAdding("");
    }
  };

  const monitorOptions = [
    { id: "all", label: t("All chapters"), sub: t("The whole back catalogue") },
    { id: "future", label: t("Only new chapters"), sub: t("Start from the next release") },
    { id: "latest", label: t("Latest chapters"), sub: t("The most recent ones only") },
    { id: "from", label: t("From a chapter"), sub: t("Where you are in the story") },
    { id: "none", label: t("Nothing, just track it"), sub: t("Read by streaming from the source") },
  ];
  const editions = langs.length;
  const cta = !picked.length
    ? t("Pick a source first")
    : editions > 1
      ? queued === 0 ? t("Add {count} editions", { count: editions }) : t("Add {count} editions and download", { count: editions })
      : queued === 0 ? t("Add series") : queued === undefined ? t("Add and download") : t("Add and download {count} chapters", { count: queued });
  const addButtons = (
    <div className="flex flex-col gap-2">
      <Button variant="primary" className="h-11 text-base" loading={adding === "download"} disabled={!picked.length || blocked || !!adding} onClick={() => void add(queued !== 0)}>{cta}</Button>
      {queued !== 0 && picked.length > 0 && (
        <Button variant="ghost" loading={adding === "later"} disabled={blocked || !!adding} onClick={() => void add(false)}>{t("Add, don’t download yet")}</Button>
      )}
    </div>
  );
  const pickedLangs = new Set(langs);

  return (
    <>
      <nav aria-label={t("Breadcrumb")} className="mb-3 text-sm">
        <Link to={`/add?q=${encodeURIComponent(ctx.title)}`} className="text-accent-2 hover:underline">← {t("Results for “{query}”", { query: ctx.title })}</Link>
      </nav>
      <div className="flex flex-col gap-5 lg:flex-row lg:items-start">
        <div className="flex min-w-0 flex-1 flex-col gap-4">
          <section aria-label={t("Series")} className="flex gap-4 rounded-xl border border-border bg-panel p-4">
            <Cover src={ctx.meta?.coverUrl ?? (picked[0] ? apiUrl(`api/v1/sources/${picked[0].group.moduleId}/${picked[0].group.sourceId}/thumbnail`, { url: picked[0].manga.url, engineRef: picked[0].manga.engineRef }) : undefined)} alt={ctx.title} className="aspect-[2/3] w-20 shrink-0 self-start sm:w-28" />
            <div className="flex min-w-0 flex-col gap-2">
              <h1 className="text-xl font-semibold leading-tight sm:text-2xl">{ctx.title}</h1>
              {ctx.meta?.altTitles?.length ? <p className="line-clamp-1 text-sm text-muted">{ctx.meta.altTitles.slice(0, 3).join(" · ")}</p> : null}
              <div className="flex flex-wrap gap-1.5">
                {ctx.meta?.status && <Badge tone="info">{ctx.meta.status}</Badge>}
                {ctx.meta?.format && <Badge>{ctx.meta.format}</Badge>}
                {ctx.meta?.year ? <Badge>{ctx.meta.year}</Badge> : null}
                {ctx.meta ? <Badge>{ctx.meta.moduleName}</Badge> : <Badge>{t("title only")}</Badge>}
              </div>
              {ctx.meta?.description && <p className="line-clamp-3 text-sm text-fg/80">{ctx.meta.description}</p>}
            </div>
          </section>

          <section aria-labelledby="add-src" className="rounded-xl border border-border bg-panel">
            <header className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3">
              <h2 id="add-src" className="flex-1 font-semibold">{t("Where chapters come from")}</h2>
              <Button size="sm" onClick={() => setSearching({ mode: "add", lang: "" })}>{t("Search all sources")}</Button>
            </header>
            <div className="flex flex-col gap-3 p-3">
              {searching_.isFetching && !picked.length && (
                <p className="flex items-center gap-2 p-2 text-sm text-muted"><Spinner />{custom ? t("Finding it at your sources…") : t("Searching every source in your language defaults…")}</p>
              )}
              {searching_.error && <ErrorBox error={searching_.error} />}
              {!custom && found.data?.noLanguages && (
                <div className="flex flex-col gap-2 rounded-lg border border-warn/40 bg-warn/10 p-3 text-sm">
                  <span className="font-medium">{t("Which language do you want this in?")}</span>
                  <span className="text-muted">{t("No search language is set, so mangarr doesn't guess.")}</span>
                  <LanguageChips value="" onChange={(l) => l && nav({ search: withParam(params, "lang", l) }, { replace: true })} primary={useLangsFallback} options={allLangs} anyLabel={t("Choose…")} />
                </div>
              )}
              {!custom && found.data && found.data.editions.length > 0 && (
                <ul aria-label={t("Sources searched")} className="flex flex-col gap-1 px-1 text-xs text-muted">
                  {found.data.editions.map((ed) => (
                    <li key={ed.lang} className="flex flex-wrap gap-x-3 gap-y-1">
                      <span className="font-semibold text-fg">{languageName(ed.lang)}</span>
                      {ed.sources.map((src) => (
                        <span key={src.key} className={src.match ? "text-ok" : src.error ? "text-err" : ""} title={src.error}>
                          {src.match ? "✓ " : ""}{src.sourceName}{src.match ? "" : src.empty ? ` · ${t("0 chapters")}` : src.error ? ` · ${t("failed")}` : !src.searched ? ` · ${t("not searched")}` : ` · ${t("not found")}`}
                        </span>
                      ))}
                      {!ed.fromDefaults && <span>{t("(no language default: first match)")}</span>}
                    </li>
                  ))}
                </ul>
              )}
              {!searching_.isFetching && !picked.length && !found.data?.noLanguages && (
                <div className="flex flex-wrap items-center gap-3 rounded-lg border border-dashed border-border p-3 text-sm">
                  <span className="flex-1 text-muted">{t("No confident match at your sources. Search them to pick one.")}</span>
                  <Button size="sm" variant="primary" onClick={() => setSearching({ mode: "add", lang: "" })}>{t("Search sources")}</Button>
                </div>
              )}
              {groups.map((g) => {
                const folder = g.lang ? folders.get(g.lang) : undefined;
                return (
                  <div key={g.lang || "?"} role="group" aria-label={g.lang ? tr("{lang} edition", { lang: languageName(g.lang) }) : tr("Language not chosen")} className="flex flex-col gap-2">
                    {(
                      <div className="flex flex-wrap items-baseline gap-2 px-1 text-sm">
                        <span className="font-semibold">{g.lang ? tr("{lang} edition", { lang: languageName(g.lang) }) : tr("Language not chosen")}</span>
                        {folder?.path && <span className="truncate font-mono text-xs text-muted">{folder.path}{folder.exists ? "" : ` · ${tr("new folder")}`}</span>}
                        {folder?.error && <span className="text-xs text-warn">{folder.error}</span>}
                        {!g.lang && <span className="text-xs text-warn">{t("These sources have titles in several languages; choose which one you want.")}</span>}
                      </div>
                    )}
                    {g.items.map((p, i) => {
                      const n = countOf(p);
                      return (
                        <div key={pickKey(p)} className={clsx("flex flex-wrap items-center gap-3 rounded-lg border p-3 sm:flex-nowrap", i === 0 ? "border-accent/40 bg-accent/5" : "border-border")}>
                          <span className="w-4 shrink-0 text-center font-semibold text-muted">{i + 1}</span>
                          <div className="flex min-w-48 flex-1 flex-col gap-0.5">
                            <span className="flex flex-wrap items-center gap-2">
                              <span className="font-semibold">{p.group.sourceName}</span>
                              {i === 0 ? <Badge tone="accent">{t("Primary")}</Badge> : <span className="text-xs text-muted">{t("fallback")}</span>}
                              {pickKey(p) === matchKey && quick.data?.match && <Badge tone="ok">{t("Best match · {score}%", { score: Math.round(quick.data.match.score * 100) })}</Badge>}
                            </span>
                            <span className="truncate text-sm text-muted">
                              “{p.manga.title}”{n !== undefined ? ` · ${t("{count} chapters", { count: n })}` : ""}
                            </span>
                          </div>
                          {!normLang(p.group.lang) && (
                            <LanguageSelect offered aria-label={t("Edition language")} className="w-36" value={normLang(p.lang)} onChange={(l) => setLang(p, l)} placeholder="—" />
                          )}
                          <div className="flex items-center gap-1">
                            {i === 0 && <Button size="sm" onClick={() => setSearching({ mode: "change", lang: g.lang })}>{t("Change")}</Button>}
                            <IconButton title={t("Up")} disabled={i === 0} onClick={() => move(p, -1)}><ArrowUp className="size-3.5" /></IconButton>
                            <IconButton title={t("Down")} disabled={i === g.items.length - 1} onClick={() => move(p, 1)}><ArrowDown className="size-3.5" /></IconButton>
                            <IconButton title={t("Remove")} onClick={() => setPicked((c) => c.filter((x) => pickKey(x) !== pickKey(p)))}><X className="size-3.5" /></IconButton>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                );
              })}
              {picked.length > 0 && (
                <div className="flex flex-wrap items-center gap-2 px-1 pt-1 text-sm">
                  <span className="text-muted">{also.length ? t("Also found, add as fallback:") : ""}</span>
                  {also.map((c) => {
                    const lang = normLang(c.lang) || c.edition || "";
                    const more = moreAt(c);
                    return (
                      <button key={pickKey(pickOf(c))} type="button" title={c.manga.title} onClick={() => setPicked((cur) => [...cur, { ...pickOf(c), lang: normLang(c.lang) ? undefined : c.edition }])} className={clsx("inline-flex max-w-full items-center gap-1 rounded-full border border-dashed px-3 py-1 hover:border-accent/60", c.empty ? "border-warn/50 text-warn" : "border-border")}>
                        <Plus className="size-3 shrink-0" />
                        <span className="truncate">
                          {c.sourceName} · “{c.manga.title}”
                          {lang && !pickedLangs.has(lang) ? ` · ${tr("new {lang} edition", { lang: languageName(lang) })}` : lang ? ` · ${languageName(lang)}` : ""}
                          {c.chapters ? ` · ${t("{count} chapters", { count: c.chapters.count })}` : ""}
                          {more > 0 ? ` · ${t("{n} more here", { n: more })}` : ""}
                        </span>
                      </button>
                    );
                  })}
                  <button type="button" onClick={() => setSearching({ mode: "add", lang: "" })} className="inline-flex items-center gap-1 text-accent-2 hover:underline">
                    <Plus className="size-3" />
                    {t("Add a source")}
                  </button>
                </div>
              )}
              {groups.length > 1 && <p className="px-1 text-xs text-muted">{t("Each language becomes its own edition of this title, in that language's folder.")}</p>}
            </div>
          </section>
        </div>

        <aside aria-labelledby="add-dl" className="flex w-full shrink-0 flex-col rounded-xl border border-border bg-panel lg:sticky lg:top-0 lg:w-96">
          <h2 id="add-dl" className="border-b border-border px-4 py-3 font-semibold">{t("What to download")}</h2>
          <fieldset className="flex flex-col gap-1.5 p-3">
            <legend className="sr-only">{t("Existing chapters")}</legend>
            {monitorOptions.map((m) => {
              const on = o.monitor === m.id;
              const n = { all: total, future: 0, latest: total === undefined ? undefined : Math.min(o.latestCount, total), from: undefined, none: 0 }[m.id as "all"];
              return (
                <label key={m.id} className={clsx("flex cursor-pointer items-center gap-3 rounded-lg border px-3 py-2.5", on ? "border-accent/60 bg-accent/5" : "border-border hover:border-muted/40")}>
                  <input type="radio" name="monitor" checked={on} onChange={() => patch({ monitor: m.id })} className="accent-accent" />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="font-medium">{m.label}</span>
                    <span className="text-xs text-muted">{m.sub}</span>
                  </span>
                  {m.id === "latest" && on && <Input type="number" min={1} aria-label={t("Number of latest chapters")} className="w-16" value={o.latestCount} onChange={(e) => patch({ latestCount: Number(e.target.value) })} />}
                  {m.id === "from" && on && <Input type="number" step="0.1" aria-label={t("First chapter")} className="w-20" value={o.fromChapter} onChange={(e) => patch({ fromChapter: Number(e.target.value) })} />}
                  {n !== undefined && <span className="text-xs tabular-nums text-muted">{n}</span>}
                </label>
              );
            })}
          </fieldset>
          <div className="flex flex-col gap-3 px-4 pb-4">
            <Switch checked={o.monitorNew === "all"} onChange={(v) => patch({ monitorNew: v ? "all" : "none" })} label={t("Download new chapters when they come out")} />
            <details className="text-sm">
              <summary className="cursor-pointer text-muted">{t("Profile and reading direction")}</summary>
              <div className="mt-3 flex flex-col gap-4">
                {(langs.filter(Boolean).length ? langs.filter(Boolean) : [""]).map((lang) => (
                  <div key={lang || "?"} className="flex flex-col gap-3">
                    {langs.filter(Boolean).length > 1 && <span className="text-xs font-semibold text-muted">{languageName(lang)}</span>}
                    <Field label={t("Profile")}>
                      <Select value={profileOf(lang)} onChange={(e) => choose(lang, { profileId: Number(e.target.value) })}>
                        {profiles?.map((p) => <option key={p.id} value={p.id}>{p.name}{p.isDefault ? tr(" (default)") : ""}</option>)}
                      </Select>
                    </Field>
                    <Field label={t("Reading direction")}>
                      <Select value={directionOf(lang)} onChange={(e) => choose(lang, { direction: e.target.value })}>
                        <option value="">{t("Automatic")}</option>
                        <option value="rtl">{t("Right to left (manga)")}</option>
                        <option value="ltr">{t("Left to right")}</option>
                        <option value="webtoon">{t("Webtoon (long strip)")}</option>
                      </Select>
                    </Field>
                  </div>
                ))}
              </div>
            </details>
          </div>
          <div className="hidden flex-col gap-2 border-t border-border bg-panel-2/40 px-4 py-4 lg:flex">
            {queued !== undefined && queued > 0 && <p className="flex justify-between text-sm"><span className="text-muted">{t("Queued now")}</span><strong>{t("{count} chapters", { count: queued })}</strong></p>}
            {addButtons}
          </div>
        </aside>
      </div>
      <div className="sticky bottom-0 -mx-4 mt-4 border-t border-border bg-panel/95 px-4 py-3 backdrop-blur lg:hidden">
        {queued !== undefined && queued > 0 && <p className="mb-2 text-center text-xs text-muted">{t("Queues {count} chapters now", { count: queued })}</p>}
        {addButtons}
      </div>
      {searching && (
        <SourceSearchModal
          initialQuery={query}
          initialLang={searching.lang || searchLang}
          titles={ctx.titles}
          title={searching.mode === "change" ? t("Change the primary source") : t("Add a source")}
          linked={picked.map((p) => ({ moduleId: p.group.moduleId, sourceId: p.group.sourceId, url: p.manga.url }))}
          onClose={() => setSearching(null)}
          onPick={(m, g) => {
            // a catalog in several languages joins the edition it was searched for
            const p: Picked = { manga: m, group: g, lang: normLang(g.lang) ? undefined : searching.lang || undefined };
            setPicked((cur) => {
              if (searching.mode !== "change") return [...cur, p];
              const at = cur.findIndex((x) => editionLang(x) === searching.lang);
              return at < 0 ? [p, ...cur] : cur.map((x, i) => (i === at ? p : x));
            });
            setSearching(null);
          }}
        />
      )}
    </>
  );
}

/** The old Options step: everything is on the review page now. */
export function AddOptionsRedirect() {
  const { moduleId = "", metaId = "" } = useParams();
  const loc = useLocation();
  return <Navigate replace to={`/add/${moduleId}/${encodeURIComponent(metaId)}/sources${loc.search}`} />;
}

function swap<T>(a: T[], i: number, j: number): T[] {
  const c = [...a];
  [c[i], c[j]] = [c[j], c[i]];
  return c;
}
