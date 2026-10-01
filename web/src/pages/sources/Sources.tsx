import { t as tr, t } from "../../lib/i18n/core";
import { useMemo, useState, type ReactNode } from "react";
import { Link, Navigate, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Settings2 } from "lucide-react";
import { api, apiUrl, unwrap, type Catalog, type ModuleResource, type S } from "../../api/client";
import { useCatalogs, useModules, useSeriesList } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Button, EmptyState, ErrorBox, Input, Loading, PageHeader, Segmented, Select } from "../../components/ui";
import { useListParam } from "../../lib/urlState";
import { languageMatches, languageName } from "../../lib/format";
import { useSettingsDoc } from "../settings/useSettingsDoc";
import { AddCatalogs, useExtensions } from "./AddCatalogs";
import { CatalogIcon, Catalogs } from "./Catalogs";
import { SourceSettings } from "./SourceSettings";
import { SwitchEngine } from "./SwitchEngine";

type Tab = "catalogs" | "add" | "browse";
// older addresses of the tabs this page replaced
const moved: Record<string, Tab> = { extensions: "add", stores: "add", priorities: "catalogs" };

export function SourcesPage() {
  const { data: modules, isLoading } = useModules("source");
  const { tab: tabParam } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const mods = modules ?? [];
  const moduleId = Number(params.get("module") ?? 0);
  const current = mods.find((m) => m.id === moduleId) ?? mods[0];
  const [switching, setSwitching] = useState(false);
  const { data: catalogs } = useCatalogs();
  const src = useSettingsDoc<S["Sources"]>("sources").value;
  const hasExtensions = !!current?.capabilities.includes("extensions");
  const { data: extensions } = useExtensions(current ?? ({ id: 0, capabilities: [] } as unknown as ModuleResource));

  if (tabParam && moved[tabParam]) return <Navigate replace to={{ pathname: `/sources/${moved[tabParam]}`, search: params.toString() ? `?${params}` : "" }} />;
  if (isLoading) return <Loading />;
  if (!mods.length)
    return (
      <>
        <PageHeader title={t("Sources")} />
        <EmptyState title={t("No source module configured")}>{t("Add a source module (for example Suwayomi with Keiyoushi extensions) in") + " "}<Link to="/settings/sources" className="text-accent-2">{t("Settings → Source modules")}</Link>.
        </EmptyState>
      </>
    );
  const on = (catalogs?.items ?? []).filter((c) => c.moduleId === current?.id && c.enabled && !c.hidden).length;
  const updates = (extensions ?? []).filter((e) => e.installed && e.hasUpdate).length;
  const tabs: { value: Tab; label: ReactNode }[] = [
    { value: "catalogs", label: <>{t("My catalogs")} <span className="font-normal text-muted">{on}</span></> },
    ...(hasExtensions
      ? [{ value: "add" as const, label: <>{t("Add catalogs")} {updates > 0 && <span className="ml-1 rounded-full bg-accent/15 px-1.5 py-0.5 text-[11px] font-semibold text-accent-2">{t("{n} updates", { n: updates })}</span>}</> }]
      : []),
    { value: "browse", label: t("Browse") },
  ];
  const tab: Tab = tabs.some((x) => x.value === tabParam) ? (tabParam as Tab) : "catalogs";
  const go = (x: Tab) => nav({ pathname: `/sources/${x}`, search: current && mods.length > 1 ? `?module=${current.id}` : "" });
  const langs = src?.defaultLanguages ?? [];
  return (
    <>
      <PageHeader
        title={t("Sources")}
        subtitle={
          src && (
            <>
              {[langs.length ? t("Searching {langs}", { langs: langs.map(languageName).join(", ") }) : t("Searching every language"), src.hideNsfw ? t("NSFW hidden") : t("NSFW shown")].join(" · ")} ·{" "}
              <Link to="/settings/search" className="text-accent-2 hover:underline">{t("change in Settings → Search")}</Link>
            </>
          )
        }
        actions={
          mods.length > 1 && (
            <>
              <label className="flex items-center gap-2 text-sm text-muted">
                {t("Engine")}
                <Select className="w-auto" value={current?.id} onChange={(e) => setParams({ module: e.target.value })}>
                  {mods.map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))}
                </Select>
              </label>
              <Button onClick={() => setSwitching(true)}>{t("Switch engine…")}</Button>
            </>
          )
        }
      />
      <nav aria-label={t("Sources sections")} className="mb-4 flex gap-1 overflow-x-auto border-b border-border">
        {tabs.map((x) => (
          <button
            key={x.value}
            type="button"
            aria-current={tab === x.value ? "page" : undefined}
            onClick={() => go(x.value)}
            className={clsx("-mb-px shrink-0 border-b-2 px-3 py-2 text-sm font-medium", tab === x.value ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg")}
          >
            {x.label}
          </button>
        ))}
      </nav>
      {current && tab === "catalogs" && <Catalogs module={current} />}
      {current && tab === "add" && <AddCatalogs module={current} />}
      {current && tab === "browse" && <Browse module={current} />}
      {switching && current && <SwitchEngine modules={mods} from={current} onClose={() => setSwitching(false)} />}
    </>
  );
}

/** Browse lists a catalog's popular or latest series; a cover opens Add series with that catalog picked. */
function Browse({ module }: { module: ModuleResource }) {
  const { data: catalogs } = useCatalogs();
  const { data: library } = useSeriesList();
  const nav = useNavigate();
  // the catalogs that are on, in your order
  const mine = (catalogs?.items ?? []).filter((c) => c.moduleId === module.id && c.enabled && !c.hidden).sort((a, b) => a.priority - b.priority);
  const [sourceId, setSourceId] = useListParam("catalog", "");
  const [typeParam, setType] = useListParam("show", "popular");
  const type = typeParam as "popular" | "latest" | "search";
  const [q, setQ] = useListParam("q", "");
  const [pageParam, setPage] = useListParam("page", "1");
  const page = Math.max(1, Number(pageParam) || 1);
  const [filter, setFilter] = useState("");
  const [prefs, setPrefs] = useState<Catalog | null>(null);
  const src = mine.find((s) => s.id === sourceId) ?? mine[0];
  const { data, isFetching, error } = useQuery({
    queryKey: ["browse", module.id, src?.id, type, q, page],
    queryFn: () => unwrap(api.GET("/api/v1/sources/{moduleId}/{sourceId}/browse", { params: { path: { moduleId: module.id, sourceId: src!.id }, query: { type, q, page } } })),
    enabled: !!src && (type !== "search" || q.length > 0),
  });
  // series already in the library, by the source entry they link
  const have = useMemo(() => new Set((library ?? []).flatMap((s) => (s.sources ?? []).map((l) => `${l.moduleId}:${l.sourceId}:${l.mangaUrl}`))), [library]);
  if (!mine.length) return <EmptyState title={t("No catalogs")}>{t("Turn a catalog on under My catalogs first.")}</EmptyState>;
  const rail = filter ? mine.filter((s) => s.displayName.toLowerCase().includes(filter.toLowerCase()) || languageMatches(s.lang, filter)) : mine;
  const choose = (id: string) => (setSourceId(id), setPage("1"));
  return (
    <div className="flex flex-col gap-4 md:flex-row md:gap-6">
      <nav aria-label={t("Catalogs")} className="flex shrink-0 flex-col gap-1 md:w-56">
        <Input aria-label={t("Filter catalogs")} placeholder={t("Filter catalogs…")} value={filter} onChange={(e) => setFilter(e.target.value)} className="mb-1" />
        <Select className="md:hidden" aria-label={t("Catalog")} value={src?.id} onChange={(e) => choose(e.target.value)}>
          {rail.map((s) => (
            <option key={s.id} value={s.id}>
              {s.displayName} · {languageName(s.lang)}
            </option>
          ))}
        </Select>
        <div className="hidden max-h-[70vh] flex-col gap-0.5 overflow-y-auto md:flex">
          {rail.map((s) => (
            <button
              key={s.id}
              type="button"
              aria-current={s.id === src?.id ? "page" : undefined}
              onClick={() => choose(s.id)}
              className={clsx("flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm", s.id === src?.id ? "bg-panel-2 font-semibold text-fg" : "text-fg/80 hover:bg-panel-2")}
            >
              <CatalogIcon moduleId={module.id} iconUrl={s.iconUrl} name={s.name} small />
              <span className="min-w-0 flex-1 truncate">{s.name}</span>
              <span className="text-[11px] text-muted">{languageName(s.lang)}</span>
            </button>
          ))}
        </div>
      </nav>
      <section aria-label={src?.displayName} className="min-w-0 flex-1">
        <div className="mb-4 flex flex-wrap items-center gap-2">
          <h2 className="mr-1 text-base font-semibold">
            {src?.name} <span className="text-sm font-normal text-muted">{src?.lang ? languageName(src.lang) : ""}</span>
          </h2>
          <Segmented
            label={t("List")}
            value={type === "search" ? "popular" : type}
            onChange={(v) => (setType(v), setQ(""), setPage("1"))}
            options={[
              { value: "popular", label: t("Popular") },
              ...(src?.supportsLatest ? [{ value: "latest" as const, label: t("Latest") }] : []),
            ]}
          />
          <form
            className="min-w-48 max-w-96 flex-1"
            onSubmit={(e) => {
              e.preventDefault();
              const v = (new FormData(e.currentTarget).get("q") as string).trim();
              setQ(v);
              setType(v ? "search" : "popular");
              setPage("1");
            }}
          >
            <Input name="q" key={`${src?.id}:${q}`} aria-label={t("Search {name}", { name: src?.name ?? "" })} placeholder={t("Search {name}…", { name: src?.name ?? "" })} defaultValue={q} />
          </form>
          <span className="flex-1" />
          {module.capabilities.includes("preferences") && src && (
            <Button icon={<Settings2 className="size-4" />} onClick={() => setPrefs(src)}>{t("Catalog settings…")}</Button>
          )}
        </div>
        {isFetching && <Loading />}
        {error && <ErrorBox error={error} />}
        {data && (
          <>
            <div className="grid grid-cols-[repeat(auto-fill,minmax(130px,1fr))] gap-4">
              {data.mangas.map((m) => {
                const inLibrary = have.has(`${module.id}:${src!.id}:${m.url}`);
                const title = encodeURIComponent(m.title);
                return (
                  <button
                    key={m.url}
                    type="button"
                    className="group flex flex-col gap-1.5 text-left"
                    // Add series, searching only this catalog, so this entry is the pick
                    onClick={() => nav(`/add/manual/-/sources?title=${title}&sq=${title}&src=${encodeURIComponent(`${module.id}:${src!.id}`)}${src!.lang && src!.lang !== "all" ? `&lang=${src!.lang}` : ""}`)}
                  >
                    <span className="relative">
                      <Cover
                        src={apiUrl(`api/v1/sources/${module.id}/${src!.id}/thumbnail`, { url: m.url, engineRef: m.engineRef })}
                        alt={m.title}
                        className="aspect-[2/3] w-full ring-accent/60 group-hover:ring-2"
                      />
                      {inLibrary && <span className="absolute left-1.5 top-1.5 rounded-full bg-bg/85 px-2 py-0.5 text-[11px] font-semibold text-ok">{tr("In library")}</span>}
                    </span>
                    <span className="line-clamp-2 text-xs">{m.title}</span>
                  </button>
                );
              })}
            </div>
            {!data.mangas.length && <p className="text-sm text-muted">{t("Nothing found.")}</p>}
            <div className="mt-4 flex justify-center gap-2">
              <Button size="sm" disabled={page <= 1} onClick={() => setPage(String(page - 1))}>{t("Previous")}</Button>
              <Button size="sm" disabled={!data.hasNext} onClick={() => setPage(String(page + 1))}>{t("Next")}</Button>
            </div>
          </>
        )}
      </section>
      {prefs && <SourceSettings moduleId={module.id} sourceId={prefs.id} title={prefs.displayName} onClose={() => setPrefs(null)} />}
    </div>
  );
}
