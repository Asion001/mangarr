import { t as tr, t } from "../../lib/i18n/core";
import { LanguageMenu } from "../../components/LanguageChooser";
import { useMemo, useState } from "react";
import { useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ChevronDown, Search, SlidersHorizontal } from "lucide-react";
import { api, apiUrl, unwrap, type Catalog, type S, type SearchGroup, type SourceManga } from "../../api/client";
import { useCatalogs } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, ErrorBox, Input, Modal, Spinner } from "../../components/ui";
import { languageMatches, languageName, relative } from "../../lib/format";
import { useSettingsDoc } from "../settings/useSettingsDoc";

export type PickGroup = Pick<SearchGroup, "moduleId" | "sourceId" | "sourceName" | "lang">;
/** Picked is a chosen source; lang is the edition a catalog in several languages goes to. */
export type Picked = { manga: SourceManga; group: PickGroup; lang?: string };
export type Scope = "active" | "all" | "custom";

/** Linked is a link a series already has (catalog and manga address). */
export type Linked = { moduleId: number; sourceId: string; url: string };
export const pickKey = (p: { group: PickGroup; manga: SourceManga }) => `${p.group.moduleId}:${p.group.sourceId}:${p.manga.url}`;
const catKey = (c: { moduleId: number; id: string }) => `${c.moduleId}:${c.id}`;

/** selectCatalogs mirrors the server's catalog selection (catalogs.Select). */
export function selectCatalogs(items: Catalog[], settings: S["Sources"] | null, scope: Scope, lang: string, keys: string[]): Catalog[] {
  let out = items.filter((c) => !c.hidden);
  if (scope === "custom") {
    out = out.filter((c) => keys.includes(catKey(c)));
  } else {
    const preset = scope === "active" && lang ? settings?.languageDefaults?.find((p) => p.language.toLowerCase() === lang.toLowerCase()) : undefined;
    if (preset?.sources.length) {
      const byKey = new Map(out.map((c) => [catKey(c), c]));
      return preset.sources.map((key) => byKey.get(key)).filter((c): c is Catalog => !!c);
    }
    const langs = lang ? [lang] : scope === "active" ? (settings?.defaultLanguages ?? []) : [];
    if (langs.length) out = out.filter((c) => c.lang === "all" || c.lang === "multi" || langs.includes(c.lang));
    if (scope === "active") out = out.filter((c) => c.enabled);
  }
  return [...out].sort((a, b) => a.priority - b.priority || a.lang.localeCompare(b.lang) || a.name.localeCompare(b.name));
}

/** useCatalogTargets returns the catalogs a search will query. */
export function useCatalogTargets(scope: Scope, lang: string, keys: string[]) {
  const { data } = useCatalogs();
  const src = useSettingsDoc<S["Sources"]>("sources");
  const items = data?.items ?? [];
  const targets = useMemo(() => selectCatalogs(items, src.value, scope, lang, keys), [items, src.value, scope, lang, keys]);
  const counts = useMemo(
    () => ({ active: selectCatalogs(items, src.value, "active", lang, []).length, all: selectCatalogs(items, src.value, "all", lang, []).length }),
    [items, src.value, lang],
  );
  const langs = useMemo(() => Array.from(new Set(items.filter((c) => !c.hidden).map((c) => c.lang))).sort(), [items]);
  return { targets, counts, langs, items, gen: data?.generation, settings: src.value };
}

/** ScopeBar picks which catalogs are searched: active, all, or a custom set. */
export function ScopeBar({
  scope,
  setScope,
  lang,
  setLang,
  keys,
  setKeys,
}: {
  scope: Scope;
  setScope: (s: Scope) => void;
  lang: string;
  setLang: (l: string) => void;
  keys: string[];
  setKeys: (k: string[]) => void;
}) {
  const { counts, langs, items } = useCatalogTargets(scope, lang, keys);
  const [picking, setPicking] = useState(false);
  const chip = (active: boolean) =>
    `rounded-full border px-3 py-1 text-xs font-medium ${active ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg"}`;
  return (
    <div className="mb-4 flex flex-wrap items-center gap-2">
      <button type="button" className={chip(scope === "active")} onClick={() => setScope("active")} title={t("Enabled catalogs in your default languages")}>{t("Active sources (")}{counts.active})
      </button>
      <button type="button" className={chip(scope === "all")} onClick={() => setScope("all")}>{t("All sources (")}{counts.all})
      </button>
      <button type="button" className={chip(scope === "custom")} onClick={() => setPicking(true)}>
        <SlidersHorizontal className="mr-1 inline size-3" />
        {scope === "custom" ? `${keys.length} picked` : tr("Pick…")}
      </button>
      <span className="ml-auto">
        <LanguageMenu
          align="right"
          label={<>{lang ? languageName(lang) : scope === "active" ? t("Your languages") : t("All languages")} ▾</>}
          options={langs.filter((l) => l !== "all" && l !== "multi")}
          top={[{ label: scope === "active" ? t("Your languages") : t("All languages"), value: "" }]}
          onPick={setLang}
          className="h-8 rounded-md border border-border bg-bg px-2.5 text-xs"
        />
      </span>
      {picking && (
        <CatalogPicker
          items={items.filter((c) => !c.hidden)}
          selected={keys}
          onClose={() => setPicking(false)}
          onSave={(k) => {
            setKeys(k);
            setScope(k.length ? "custom" : "active");
            setPicking(false);
          }}
        />
      )}
    </div>
  );
}

function CatalogPicker({ items, selected, onSave, onClose }: { items: Catalog[]; selected: string[]; onSave: (k: string[]) => void; onClose: () => void }) {
  const [sel, setSel] = useState<string[]>(selected);
  const [q, setQ] = useState("");
  const list = items
    .filter((c) => !q || c.displayName.toLowerCase().includes(q.toLowerCase()) || languageMatches(c.lang, q))
    .sort((a, b) => a.priority - b.priority || a.displayName.localeCompare(b.displayName));
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Search these catalogs")}
      footer={
        <>
          <Button onClick={() => setSel([])}>{t("Clear")}</Button>
          <Button variant="primary" onClick={() => onSave(sel)}>{t("Search") + " "}{sel.length}{" " + t("catalogs")}</Button>
        </>
      }
    >
      <Input autoFocus className="mb-3" placeholder={t("Filter…")} value={q} onChange={(e) => setQ(e.target.value)} />
      <div className="flex max-h-[50vh] flex-col gap-1 overflow-y-auto">
        {list.map((c) => {
          const k = catKey(c);
          return (
            <label key={k} className="flex items-center gap-2 rounded px-2 py-1 text-sm hover:bg-panel-2">
              <input type="checkbox" checked={sel.includes(k)} onChange={(e) => setSel(e.target.checked ? [...sel, k] : sel.filter((x) => x !== k))} />
              <span className="flex-1">{c.displayName}</span>
              <Badge>{languageName(c.lang)}</Badge>
              {!c.enabled && <Badge>{t("disabled")}</Badge>}
            </label>
          );
        })}
      </div>
    </Modal>
  );
}

/** SearchInput edits the query submitted to catalogs. */
export function SearchInput({ query, setQuery, placeholder }: { query: string; setQuery: (q: string) => void; placeholder?: string }) {
  const [draft, setDraft] = useState(query);
  return (
    <form
      className="mb-3 flex gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        setQuery(draft.trim());
      }}
    >
      <Input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={placeholder ?? tr("Title to search at sources")} />
      <Button type="submit" variant="primary" icon={<Search className="size-4" />}>{t("Search")}</Button>
    </form>
  );
}

function ResultTile({ m, g, selected, linked, onPick }: { m: SourceManga; g: PickGroup; selected: boolean; linked?: boolean; onPick: () => void }) {
  return (
    <button
      type="button"
      onClick={onPick}
      disabled={linked}
      title={linked ? tr("Already linked to this series") : undefined}
      className={`group relative flex flex-col gap-1 rounded-md p-1 text-left disabled:cursor-not-allowed disabled:opacity-50 ${selected ? "bg-accent/15 ring-2 ring-accent" : "hover:bg-panel-2"}`}
    >
      <Cover src={apiUrl(`api/v1/sources/${g.moduleId}/${g.sourceId}/thumbnail`, { url: m.url, engineRef: m.engineRef })} alt={m.title} className="aspect-[2/3] w-full" />
      {selected && (
        <span className="absolute right-2 top-2 rounded-full bg-primary p-0.5 text-white">
          <Check className="size-3.5" />
        </span>
      )}
      {linked && <span className="absolute right-2 top-2 rounded bg-black/70 px-1.5 text-[10px] text-white">{t("Linked")}</span>}
      {m.chapterCount != null && <span className="absolute left-2 top-2 rounded bg-black/70 px-1.5 text-[10px] text-white">{m.chapterCount}{" " + t("ch")}</span>}
      <span className="line-clamp-2 text-xs">{m.title}</span>
    </button>
  );
}

/**
 * CatalogResults searches catalogs in priority order, a few at a time, and
 * renders each catalog as soon as it answers (not waiting for the slowest).
 * Results are cached by the server, so catalogs searched before are instant.
 */
export function CatalogResults({
  query,
  targets,
  gen,
  selected,
  linked,
  onPick,
  parallel = 4,
}: {
  query: string;
  targets: Catalog[];
  gen?: number;
  selected?: Picked[];
  /** linked are pickKeys of links the series already has: shown, not pickable. */
  linked?: Set<string>;
  onPick: (m: SourceManga, g: PickGroup) => void;
  parallel?: number;
}) {
  const sel = new Set((selected ?? []).map(pickKey));
  const qc = useQueryClient();
  const keyOf = (c: Catalog) => ["catalog-search", gen, catKey(c), query];
  // enable the next catalogs as earlier ones finish, keeping `parallel` in flight
  const done = targets.filter((c) => {
    const st = qc.getQueryState(keyOf(c));
    return st?.status === "success" || st?.status === "error";
  }).length;
  const active = useQueries({
    queries: targets.map((c, i) => ({
      queryKey: keyOf(c),
      queryFn: () =>
        unwrap(api.GET("/api/v1/sources/{moduleId}/{sourceId}/browse", { params: { path: { moduleId: c.moduleId, sourceId: c.id }, query: { type: "search", q: query } } })),
      enabled: !!query && gen !== undefined && i < done + parallel,
      staleTime: 10 * 60_000,
      retry: 0,
    })),
  });
  if (!query) return null;
  if (!targets.length) return <p className="text-sm text-muted">{t("No catalogs match this selection.")}</p>;
  const pending = active.filter((r) => r.isPending).length;
  return (
    <div className="flex flex-col gap-4">
      {pending > 0 && (
        <p className="flex items-center gap-2 text-xs text-muted">
          <Spinner />{" " + t("Searched") + " "}{targets.length - pending}{" " + t("of") + " "}{targets.length}{" " + t("catalogs…")}</p>
      )}
      {targets.map((c, i) => {
        const r = active[i];
        const g: PickGroup = { moduleId: c.moduleId, sourceId: c.id, sourceName: c.displayName, lang: c.lang };
        if (r.isPending) return null;
        if (r.isError)
          return (
            <div key={catKey(c)} className="text-sm">
              <span className="font-medium">{c.displayName}</span>{" "}
              <span className="text-xs text-err" title={String(r.error)}>
                {String(r.error)}
              </span>
            </div>
          );
        const mangas = r.data?.mangas ?? [];
        if (!mangas.length) return null;
        return (
          <div key={catKey(c)}>
            <div className="mb-2 flex items-center gap-2 text-sm font-medium">
              {c.displayName} <Badge>{languageName(c.lang)}</Badge>
              <span className="text-xs font-normal text-muted">{mangas.length}{" " + t("results")}</span>
            </div>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(110px,1fr))] gap-3">
              {mangas.slice(0, 18).map((m) => (
                <ResultTile key={m.url} m={m} g={g} selected={sel.has(pickKey({ group: g, manga: m }))} linked={linked?.has(pickKey({ group: g, manga: m }))} onPick={() => onPick(m, g)} />
              ))}
            </div>
          </div>
        );
      })}
      {pending === 0 && active.every((r) => (r.data?.mangas ?? []).length === 0 && !r.isError) && <p className="text-sm text-muted">{t("No results.")}</p>}
    </div>
  );
}

type QuickCandidate = S["QuickCandidate"];

/** HeroMatch shows the confident match of a quick search. */
export function HeroMatch({ c, selected, linked, onUse }: { c: QuickCandidate; selected: boolean; linked?: boolean; onUse: () => void }) {
  const g: PickGroup = { moduleId: c.moduleId, sourceId: c.sourceId, sourceName: c.sourceName, lang: c.lang };
  const ch = c.chapters;
  return (
    <div className="mb-4 flex flex-col gap-4 rounded-lg border border-accent/40 bg-accent/5 p-4 sm:flex-row">
      <Cover
        src={apiUrl(`api/v1/sources/${g.moduleId}/${g.sourceId}/thumbnail`, { url: c.manga.url, engineRef: c.manga.engineRef })}
        alt={c.manga.title}
        className="aspect-[2/3] w-40 shrink-0 self-center sm:self-start"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="text-xs uppercase tracking-wide text-accent-2">{t("Best match ·") + " "}{Math.round(c.score * 100)}{t("% title match")}</div>
        <h3 className="text-lg font-semibold">{c.manga.title}</h3>
        <div className="flex flex-wrap gap-1.5">
          <Badge tone="accent">{c.sourceName}</Badge>
          {ch && <Badge tone="info">{ch.count}{" " + t("chapters")}</Badge>}
          {ch?.status && <Badge>{ch.status}</Badge>}
          {ch?.latestName && <Badge>{t("latest:") + " "}{ch.latestName}</Badge>}
          {ch?.latestUpload && <Badge>{t("updated") + " "}{relative(ch.latestUpload)}</Badge>}
          {(ch?.scanlators ?? []).map((s) => (
            <Badge key={s}>{s}</Badge>
          ))}
        </div>
        {ch?.description && <p className="line-clamp-3 text-sm text-fg/80">{ch.description}</p>}
        <div className="mt-auto flex flex-wrap gap-2 pt-2">
          <Button variant="primary" icon={<Check className="size-4" />} onClick={onUse} disabled={selected || linked}>
            {linked ? tr("Already linked to this series") : selected ? tr("Selected") : tr("Use this")}
          </Button>
        </div>
      </div>
    </div>
  );
}

/** useQuickSearch runs the one-by-one search on the server. */
export function useQuickSearch(opts: { query: string; titles: string[]; scope: Scope; keys: string[]; lang: string; rootFolderId?: number; exclude?: string[]; enabled: boolean; gen?: number }) {
  return useQuery({
    queryKey: ["quick-search", opts.gen, opts.query, opts.titles, opts.scope, opts.keys, opts.lang, opts.rootFolderId, opts.exclude],
    queryFn: () =>
      unwrap(
        api.POST("/api/v1/sources/quick-search", {
          body: {
            query: opts.query,
            titles: opts.titles,
            scope: opts.scope === "custom" ? undefined : opts.scope,
            sources: opts.scope === "custom" ? opts.keys : undefined,
            lang: opts.lang || undefined,
            rootFolderId: opts.rootFolderId || undefined,
            exclude: opts.exclude?.length ? opts.exclude : undefined,
          },
        }),
      ),
    enabled: opts.enabled && !!opts.query && opts.gen !== undefined,
    staleTime: 10 * 60_000,
    retry: 0,
  });
}

/** SourceSearch is the full source search: quick match first, then every catalog on demand. */
export function SourceSearch({
  query,
  setQuery,
  titles,
  scope,
  setScope,
  lang,
  setLang,
  keys,
  setKeys,
  more,
  setMore,
  selected,
  linked,
  excludeLinked = true,
  rootFolderId,
  onPick,
}: {
  query: string;
  setQuery: (q: string) => void;
  titles: string[];
  scope: Scope;
  setScope: (s: Scope) => void;
  lang: string;
  setLang: (l: string) => void;
  keys: string[];
  setKeys: (k: string[]) => void;
  more: boolean;
  setMore: (v: boolean) => void;
  selected: Picked[];
  /** linked are the series' current links: marked in the results, and the quick match skips their catalogs. */
  linked?: Linked[];
  /** excludeLinked false still searches the linked catalogs (changing a match at the same site). */
  excludeLinked?: boolean;
  rootFolderId?: number;
  onPick: (m: SourceManga, g: PickGroup, only?: boolean) => void;
}) {
  const { targets, gen, settings } = useCatalogTargets(scope, lang, keys);
  const quickEnabled = (settings?.quickSearch.enabled ?? true) && !more;
  const exclude = excludeLinked ? [...new Set((linked ?? []).map((l) => catKey({ moduleId: l.moduleId, id: l.sourceId })))] : [];
  const linkedKeys = new Set((linked ?? []).map((l) => `${l.moduleId}:${l.sourceId}:${l.url}`));
  const quick = useQuickSearch({ query, titles, scope, keys, lang, rootFolderId, exclude, enabled: quickEnabled, gen });
  const sel = new Set(selected.map(pickKey));
  const match = quick.data?.match;
  const showGrid = more || !quickEnabled || (quick.isSuccess && !match);
  return (
    <>
      <SearchInput key={query} query={query} setQuery={setQuery} />
      <ScopeBar scope={scope} setScope={setScope} lang={lang} setLang={setLang} keys={keys} setKeys={setKeys} />
      {quickEnabled && quick.isFetching && (
        <p className="mb-4 flex items-center gap-2 text-sm text-muted">
          <Spinner />{" " + t("Searching catalogs one by one, best first…")}</p>
      )}
      {quick.error && <ErrorBox error={quick.error} />}
      {!more && match && (
        <>
          <HeroMatch
            c={match}
            selected={sel.has(pickKey({ group: { ...match }, manga: match.manga }))}
            linked={linkedKeys.has(pickKey({ group: { ...match }, manga: match.manga }))}
            onUse={() => onPick(match.manga, { moduleId: match.moduleId, sourceId: match.sourceId, sourceName: match.sourceName, lang: match.lang }, true)}
          />
          <button type="button" className="mb-2 inline-flex items-center gap-1 text-sm text-accent-2 hover:underline" onClick={() => setMore(true)}>
            <ChevronDown className="size-4" />{" " + t("Not it? Search other sources (")}{targets.length})
          </button>
          <p className="text-xs text-muted">{t("Searched") + " "}{quick.data?.searched.map((s) => s.sourceName).join(", ")}
            {quick.data?.remaining.length ? `; ${quick.data.remaining.length} more not searched yet` : ""}{t(". You can pick more sources as fallbacks.")}</p>
        </>
      )}
      {!more && quick.isSuccess && !match && quickEnabled && (
        <p className="mb-3 text-sm text-muted">{t("No confident match; showing every catalog.")}</p>
      )}
      {showGrid && <CatalogResults query={query} targets={targets} gen={gen} selected={selected} linked={linkedKeys} onPick={(m, g) => onPick(m, g)} />}
    </>
  );
}

/** SourceSearchModal links a source to an existing series. */
export function SourceSearchModal({
  initialQuery,
  initialLang = "",
  rootFolderId,
  title,
  titles,
  linked,
  initialKeys,
  excludeLinked,
  onPick,
  onClose,
}: {
  initialQuery: string;
  initialLang?: string;
  rootFolderId?: number;
  title: string;
  titles?: string[];
  linked?: Linked[];
  /** initialKeys limits the search to these catalogs (moduleId:sourceId). */
  initialKeys?: string[];
  excludeLinked?: boolean;
  onPick: (m: SourceManga, g: PickGroup) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState(initialQuery);
  const [scope, setScope] = useState<Scope>(initialKeys?.length ? "custom" : "active");
  const [lang, setLang] = useState(initialLang);
  const [keys, setKeys] = useState<string[]>(initialKeys ?? []);
  const [more, setMore] = useState(false);
  return (
    <Modal open onClose={onClose} title={title} size="xl">
      <SourceSearch
        query={query}
        setQuery={setQuery}
        titles={titles ?? [initialQuery]}
        scope={scope}
        setScope={setScope}
        lang={lang}
        setLang={setLang}
        keys={keys}
        setKeys={setKeys}
        more={more}
        setMore={setMore}
        selected={[]}
        linked={linked}
        excludeLinked={excludeLinked}
        rootFolderId={rootFolderId}
        onPick={(m, g) => onPick(m, g)}
      />
    </Modal>
  );
}
