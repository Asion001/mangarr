import { t as tr, t } from "../../lib/i18n/core";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { Check, ChevronRight, GripVertical, MoreHorizontal } from "lucide-react";
import { api, apiUrl, unwrap, type Catalog, type ModuleResource, type S } from "../../api/client";
import { useCatalogs, useRootFolders, useSeriesList } from "../../api/queries";
import { Badge, Button, EmptyState, ErrorBox, Input, Loading, Menu, Modal, Select, Switch } from "../../components/ui";
import { languageMatches, languageName, relative, sortLanguages } from "../../lib/format";
import { LanguageMenu, useSearchLanguages } from "../../components/LanguageChooser";
import { useToast } from "../../lib/toast";
import { SourceSettings } from "./SourceSettings";

type Patch = { enabled?: boolean; priority?: number; throttle?: S["ThrottleConfig"]; clearCooldown?: boolean };
type Health = S["CatalogHealth"];
const key = (c: Catalog) => `${c.moduleId}:${c.id}`;
const speeds = [
  { value: "", label: "Default" },
  { value: "gentle", label: "Gentle" },
  { value: "normal", label: "Normal" },
  { value: "fast", label: "Fast" },
] as const;

/**
 * Catalogs is "My catalogs": the catalogs that are on, in the order searches
 * go through them (globally, or for one library or language), with how each
 * is doing; the ones that are off are folded away underneath.
 */
export function Catalogs({ module }: { module: ModuleResource }) {
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const { data, isLoading, error } = useCatalogs();
  const roots = useRootFolders();
  const lists = useQuery({ queryKey: ["source-priorities"], queryFn: () => unwrap(api.GET("/api/v1/source-priorities")) });
  const health = useQuery({ queryKey: ["catalogs-health"], queryFn: () => unwrap(api.GET("/api/v1/catalogs/health")), staleTime: 30_000 });
  const { data: series } = useSeriesList();
  const [scope, setScope] = useState("global");
  const [settings, setSettings] = useState<Catalog | null>(null);
  const [showOff, setShowOff] = useState(false);
  const [offFilter, setOffFilter] = useState("");
  const [dragging, setDragging] = useState<number | null>(null);
  const [reviewing, setReviewing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [onFilter, setOnFilter] = useState("");
  const [onLang, setOnLang] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());

  const mine = useMemo(() => (data?.items ?? []).filter((c) => c.moduleId === module.id && !c.hidden), [data, module.id]);
  const byGlobal = useMemo(() => [...mine].sort((a, b) => a.priority - b.priority || a.displayName.localeCompare(b.displayName)), [mine]);
  const healthOf = useMemo(() => new Map((health.data ?? []).map((h) => [`${h.moduleId}:${h.sourceId}`, h])), [health.data]);

  // only languages you use: a folder, a series, a search language or a saved order
  const searchLanguages = useSearchLanguages();
  const languages = useMemo(
    () => sortLanguages([
      ...(roots.data ?? []).map((r) => r.language),
      ...(series ?? []).map((x) => x.language),
      ...searchLanguages,
      ...(lists.data ?? []).filter((l) => l.scope.startsWith("language:")).map((l) => l.scope.slice("language:".length)),
    ].filter((l) => l && l !== "*" && l !== "all" && l !== "multi")),
    [roots.data, series, searchLanguages, lists.data],
  );
  const scopes = [
    { value: "global", label: tr("Every library"), language: "" },
    ...(roots.data ?? []).map((r) => ({ value: `library:${r.id}`, label: `${r.path.split("/").filter(Boolean).pop() || r.path}${r.language ? ` (${languageName(r.language)})` : ""}`, language: r.language })),
    ...languages.map((l) => ({ value: `language:${l}`, label: tr("{lang} series", { lang: languageName(l) }), language: l })),
  ];
  const current = scopes.find((s) => s.value === scope) ?? scopes[0];
  useEffect(() => {
    if (!scopes.some((s) => s.value === scope)) setScope("global");
  }, [scopes.length]);

  // catalogs searched for this scope, in order
  const saved = scope === "global" ? [] : (lists.data?.find((l) => l.scope === scope)?.sources ?? []);
  const eligible = byGlobal.filter((c) => c.enabled && (!current.language || c.lang === current.language || c.lang === "all" || c.lang === "multi"));
  const ordered = useMemo(() => {
    const index = new Map(eligible.map((c) => [key(c), c]));
    const out = saved.map((k) => index.get(k)).filter((c): c is Catalog => !!c);
    for (const c of eligible) if (!out.includes(c)) out.push(c);
    return out;
  }, [eligible, saved]);
  // the "on" list can be narrowed; dragging only works on the whole list
  const filtering = !!onFilter || !!onLang;
  const shownOn = ordered
    .map((c, i) => ({ c, i }))
    .filter(({ c }) => (!onLang || c.lang === onLang) && (!onFilter || c.displayName.toLowerCase().includes(onFilter.toLowerCase()) || languageMatches(c.lang, onFilter)));
  const onLangs = sortLanguages(ordered.map((c) => c.lang));
  // catalogs outside your search languages (multi-language ones stay)
  const foreign = searchLanguages.length ? ordered.filter((c) => c.lang !== "all" && c.lang !== "multi" && !searchLanguages.includes(c.lang)) : [];
  const turnOff = async (list: Catalog[]) => {
    await update(Object.fromEntries(list.map((c) => [key(c), { enabled: false }])));
    setPicked(new Set());
    toast.success(tr("{n} catalogs turned off", { n: list.length }));
  };
  const off = byGlobal.filter((c) => !c.enabled);
  const shownOff = offFilter ? off.filter((c) => c.displayName.toLowerCase().includes(offFilter.toLowerCase()) || languageMatches(c.lang, offFilter)) : off;
  const customSeries = (series ?? []).filter((s) => s.sourcePriorityMode === "custom");

  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: ["catalogs"] }), qc.invalidateQueries({ queryKey: ["sources"] }), qc.invalidateQueries({ queryKey: ["source-priorities"] })]);
  const update = async (patches: Record<string, Patch>) => {
    if (!Object.keys(patches).length) return;
    try {
      await unwrap(api.PUT("/api/v1/catalogs", { body: patches }));
      await refresh();
    } catch (e) {
      toast.fromError(e, tr("Could not update catalogs"));
    }
  };

  /** move puts the catalog at i to position j and saves the scope's order. */
  const move = async (i: number, j: number) => {
    if (i === j || j < 0 || j >= ordered.length) return;
    const order = [...ordered];
    const [c] = order.splice(i, 1);
    order.splice(j, 0, c);
    setBusy(true);
    try {
      if (scope === "global") {
        // renumber every catalog of the engine, the ones that are off after
        // the rest, so no two share a priority whatever is shown
        const all = [...order, ...byGlobal.filter((x) => !order.includes(x))];
        const patches: Record<string, Patch> = {};
        all.forEach((x, n) => {
          if (x.priority !== (n + 1) * 10) patches[key(x)] = { priority: (n + 1) * 10 };
        });
        await update(patches);
      } else {
        await unwrap(api.PUT("/api/v1/source-priorities", { body: { scope, sources: order.map(key) } }));
        await refresh();
      }
      toast.success(tr("Order saved"), tr("{name} is now number {n}", { name: c.displayName, n: j + 1 }));
    } catch (e) {
      toast.fromError(e, tr("Could not save source priority"));
    } finally {
      setBusy(false);
    }
  };
  const useGlobal = async () => {
    try {
      await unwrap(api.PUT("/api/v1/source-priorities", { body: { scope, sources: [] } }));
      await refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };

  const status = (c: Catalog) => {
    const h: Health | undefined = healthOf.get(key(c));
    if (c.cooldownUntil)
      return {
        tone: "warn" as const,
        text: t("Paused until {time}", { time: new Date(c.cooldownUntil).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) }) + (c.cooldownReason ? ` · ${c.cooldownReason}` : ""),
      };
    if (!h || !h.series) return { tone: "muted" as const, text: t("Not used yet") };
    if (h.failing)
      return {
        tone: "err" as const,
        text: t("{failing} of {n} links failing", { failing: h.failing, n: h.series }) + (h.lastSuccessAt ? ` · ${t("last success {when}", { when: relative(h.lastSuccessAt) })}` : ""),
      };
    return { tone: "ok" as const, text: t("All links fine") + (h.lastCheckedAt ? ` · ${t("checked {when}", { when: relative(h.lastCheckedAt) })}` : "") };
  };

  if (isLoading || roots.isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  if (!mine.length) return <EmptyState title={t("No catalogs")}>{t("Add catalogs from an extension first.")}</EmptyState>;

  return (
    <div className="flex flex-col gap-4">
      {customSeries.length > 0 && (
        <div role="status" className="flex flex-wrap items-center gap-3 rounded-lg border border-info/30 bg-info/8 px-3.5 py-2.5 text-sm">
          <span className="flex-1">{t("Series with their own source order: {n}. They ignore this list.", { n: customSeries.length })}</span>
          <button type="button" className="font-semibold text-info hover:underline" onClick={() => setReviewing(true)}>{t("Review…")}</button>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <label className="flex items-center gap-2 text-sm font-medium">
          {t("Order for")}
          <Select className="w-auto" value={scope} onChange={(e) => setScope(e.target.value)}>
            {scopes.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </Select>
        </label>
        {scope !== "global" && saved.length > 0 && (
          <>
            <Badge>{t("own order")}</Badge>
            <button type="button" className="text-sm font-semibold text-accent-2 hover:underline" onClick={useGlobal}>{t("Use the global order")}</button>
          </>
        )}
        <span className="flex-1" />
        <span className="text-xs text-muted">{t("Searched top to bottom. The first hit becomes a new series' primary source.")}</span>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Input className="max-w-64" aria-label={t("Filter catalogs")} placeholder={t("Filter catalogs…")} value={onFilter} onChange={(e) => setOnFilter(e.target.value)} />
        <LanguageMenu
          label={<>{onLang ? languageName(onLang) : t("All languages")} ▾</>}
          options={onLangs}
          top={[{ label: t("All languages"), value: "" }]}
          onPick={setOnLang}
          className="h-9 rounded-md border border-border bg-panel-2 px-3 text-sm"
        />
        <span className="text-sm text-muted">{t("{n} on", { n: ordered.length })}</span>
        <span className="flex-1" />
        {picked.size > 0 ? (
          <>
            <span className="text-sm font-medium">{t("{count} selected", { count: picked.size })}</span>
            <Button size="sm" onClick={() => void turnOff(ordered.filter((c) => picked.has(key(c))))}>{t("Turn off")}</Button>
            <Button size="sm" variant="ghost" onClick={() => setPicked(new Set())}>{t("Clear")}</Button>
          </>
        ) : (
          shownOn.length > 0 && <Button size="sm" variant="ghost" onClick={() => setPicked(new Set(shownOn.map(({ c }) => key(c))))}>{t("Select all {count}", { count: shownOn.length })}</Button>
        )}
        {foreign.length > 0 && (
          <Button size="sm" title={t("Keeps catalogs in {langs} and multi-language ones", { langs: searchLanguages.map(languageName).join(", ") })} onClick={() => void turnOff(foreign)}>
            {t("Turn off {n} outside my languages", { n: foreign.length })}
          </Button>
        )}
      </div>

      <section aria-label={t("Catalogs that are on")} className="rounded-xl border border-border bg-panel">
        {ordered.length === 0 && <p className="p-4 text-sm text-muted">{t("No catalog is on for this scope. Turn one on below.")}</p>}
        <ol className="flex flex-col p-1.5">
          {filtering && shownOn.length === 0 && <li className="p-3 text-sm text-muted">{t("No catalog matches.")}</li>}
          {shownOn.map(({ c, i }, n) => {
            const st = status(c);
            const h = healthOf.get(key(c));
            return (
              <li
                key={key(c)}
                draggable={!busy && !filtering}
                onDragStart={() => setDragging(i)}
                onDragEnd={() => setDragging(null)}
                onDragOver={(e) => e.preventDefault()}
                onDrop={() => dragging !== null && void move(dragging, i)}
                className={clsx("flex flex-wrap items-center gap-3 rounded-lg px-2 py-2.5 sm:flex-nowrap", n > 0 && "border-t border-border", dragging === i && "opacity-50")}
              >
                <input
                  type="checkbox"
                  aria-label={t("Select {name}", { name: c.displayName })}
                  checked={picked.has(key(c))}
                  onChange={(e) => setPicked((cur) => {
                    const next = new Set(cur);
                    if (e.target.checked) next.add(key(c));
                    else next.delete(key(c));
                    return next;
                  })}
                />
                <span className="hidden cursor-grab text-muted sm:block" aria-hidden="true">
                  <GripVertical className="size-4" />
                </span>
                <span className="w-5 shrink-0 text-center font-semibold text-muted">{i + 1}</span>
                <CatalogIcon moduleId={module.id} iconUrl={c.iconUrl} name={c.name || c.displayName} />
                <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold">{c.name || c.displayName}</span>
                    <Badge>{languageName(c.lang)}</Badge>
                    {c.nsfw && <Badge tone="warn">18+</Badge>}
                    {c.throttle?.preset && <Badge>{tr(c.throttle.preset)}</Badge>}
                  </div>
                  <span className="text-xs text-muted">{[t("{n} series", { n: h?.series ?? 0 }), c.moduleName, c.extension].filter(Boolean).join(" · ")}</span>
                </div>
                <div className="flex w-full items-center gap-2 text-xs sm:w-72">
                  <span aria-hidden="true" className={clsx("size-2 shrink-0 rounded-full", { ok: "bg-ok", warn: "bg-warn", err: "bg-err", muted: "bg-border" }[st.tone])} />
                  <span className={clsx("min-w-0 flex-1", { ok: "text-fg/80", warn: "text-warn", err: "text-err", muted: "text-muted" }[st.tone])}>{st.text}</span>
                  {c.cooldownUntil && <Button size="sm" onClick={() => update({ [key(c)]: { clearCooldown: true } })}>{t("Resume now")}</Button>}
                </div>
                <Switch checked={c.enabled} onChange={(v) => update({ [key(c)]: { enabled: v } })} label={<span className="sr-only">{t("On")}</span>} />
                <Menu
                  align="right"
                  label={<><MoreHorizontal className="size-4" /><span className="sr-only">{t("More for {source}", { source: c.displayName })}</span></>}
                  items={[
                    { label: tr("Browse this catalog"), onSelect: () => nav(`/sources/browse?catalog=${encodeURIComponent(c.id)}`) },
                    { label: tr("Catalog settings…"), onSelect: () => setSettings(c), hidden: !module.capabilities.includes("preferences") },
                    { section: tr("Request speed") },
                    ...speeds.map((s) => ({
                      label: tr(s.label),
                      icon: <Check className={clsx("size-3.5", (c.throttle?.preset ?? "") === s.value ? "opacity-100" : "opacity-0")} />,
                      onSelect: () => update({ [key(c)]: { throttle: { ...c.throttle, preset: s.value as S["ThrottleConfig"]["preset"] } } }),
                    })),
                    { section: "" },
                    { label: tr("Move to top"), onSelect: () => void move(i, 0), hidden: i === 0 },
                    { label: tr("Move up"), onSelect: () => void move(i, i - 1), hidden: i === 0 },
                    { label: tr("Move down"), onSelect: () => void move(i, i + 1), hidden: i === ordered.length - 1 },
                  ]}
                />
              </li>
            );
          })}
        </ol>
        {off.length > 0 && (
          <div className="border-t border-border px-4 py-3">
            <div className="flex flex-wrap items-center gap-3">
              <button type="button" aria-expanded={showOff} onClick={() => setShowOff(!showOff)} className="flex items-center gap-2 text-sm font-semibold">
                <ChevronRight className={clsx("size-4 transition-transform", showOff && "rotate-90")} />
                {t("Off")} <span className="font-normal text-muted">{t("{n} installed catalogs, never searched", { n: off.length })}</span>
              </button>
              <span className="flex-1" />
              <Input
                className="max-w-64"
                aria-label={t("Find a catalog to turn on")}
                placeholder={t("Find a catalog to turn on…")}
                value={offFilter}
                onChange={(e) => (setOffFilter(e.target.value), setShowOff(true))}
              />
            </div>
            {showOff && offFilter && shownOff.length > 1 && shownOff.length <= 200 && (
              <Button size="sm" className="mt-2" onClick={() => void update(Object.fromEntries(shownOff.map((c) => [key(c), { enabled: true }])))}>{t("Turn on {n} shown", { n: shownOff.length })}</Button>
            )}
            {showOff && (
              <ul className="mt-2 grid gap-1 sm:grid-cols-2">
                {shownOff.slice(0, 200).map((c) => (
                  <li key={key(c)} className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-panel-2">
                    <span className="min-w-0 flex-1 truncate text-sm">{c.displayName}</span>
                    <Badge>{languageName(c.lang)}</Badge>
                    <Switch checked={false} onChange={() => update({ [key(c)]: { enabled: true } })} label={<span className="sr-only">{t("Turn on {name}", { name: c.displayName })}</span>} />
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </section>
      {settings && <SourceSettings moduleId={settings.moduleId} sourceId={settings.id} title={settings.displayName} onClose={() => setSettings(null)} />}
      {reviewing && <CustomOrderReview seriesIds={customSeries.map((s) => s.id)} onClose={() => setReviewing(false)} />}
    </div>
  );
}

/** CatalogIcon is a catalog's icon, or its first letter when there is none or it doesn't load. */
export function CatalogIcon({ moduleId, iconUrl, name, small }: { moduleId: number; iconUrl?: string; name: string; small?: boolean }) {
  const [failed, setFailed] = useState(false);
  const size = small ? "size-5 rounded text-[11px]" : "size-8 rounded-md text-sm";
  if (!iconUrl || failed)
    return (
      <span aria-hidden="true" className={clsx("flex shrink-0 items-center justify-center bg-panel-2 font-bold text-muted", size)}>
        {name.slice(0, 1)}
      </span>
    );
  return <img src={apiUrl(`api/v1/modules/${moduleId}/asset`, { path: iconUrl })} alt="" onError={() => setFailed(true)} className={clsx("shrink-0 bg-panel-2", size)} loading="lazy" />;
}

/** CustomOrderReview previews moving series with their own source order back to the shared lists. */
function CustomOrderReview({ seriesIds, onClose }: { seriesIds: number[]; onClose: () => void }) {
  const toast = useToast();
  const qc = useQueryClient();
  const ids = seriesIds.slice(0, 200);
  const preview = useQuery({
    queryKey: ["priority-preview", ids.join(",")],
    queryFn: () => unwrap(api.POST("/api/v1/source-priorities/inherit", { body: { seriesIds: ids, dryRun: true } })),
  });
  const [applying, setApplying] = useState(false);
  const apply = async () => {
    setApplying(true);
    try {
      await unwrap(api.POST("/api/v1/source-priorities/inherit", { body: { seriesIds: ids, dryRun: false } }));
      await qc.invalidateQueries({ queryKey: ["series"] });
      toast.success(tr("Series now inherit source priorities"));
      onClose();
    } catch (e) {
      toast.fromError(e, tr("Could not update series priorities"));
    } finally {
      setApplying(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Series with their own source order")}
      size="lg"
      footer={
        <>
          <Button onClick={onClose}>{t("Keep their order")}</Button>
          <Button variant="primary" loading={applying} disabled={!preview.data} onClick={apply}>{t("Use the shared order for {n}", { n: ids.length })}</Button>
        </>
      }
    >
      <p className="mb-3 text-sm text-muted">
        {t("Switching them links no sources and starts no downloads; it only changes which source is asked first.")}
        {seriesIds.length > ids.length && ` ${t("The first 200 series are shown per migration.")}`}
      </p>
      {preview.isLoading && <Loading />}
      {preview.error && <ErrorBox error={preview.error} />}
      {preview.data && (
        <div className="max-h-80 overflow-y-auto rounded-md border border-border">
          {preview.data.map((item) => (
            <div key={item.seriesId} className="flex items-center gap-3 border-b border-border px-3 py-2 text-sm last:border-b-0">
              <span className="min-w-0 flex-1 truncate">{item.title}</span>
              <span className="text-xs text-muted">{item.sources.map((s) => s.sourceName || s.sourceId).join(" → ") || t("No linked sources")}</span>
            </div>
          ))}
        </div>
      )}
    </Modal>
  );
}
