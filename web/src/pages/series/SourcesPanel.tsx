import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { ArrowDown, ArrowUp, ExternalLink, MoreHorizontal, Plus } from "lucide-react";
import { api, unwrap, type Series, type SeriesSource, type SourceManga } from "../../api/client";
import { Badge, Button, Card, Confirm, IconButton, Menu, Switch } from "../../components/ui";
import { languageName, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { SourceSearchModal, type PickGroup } from "./SourceSearch";

type Searching = { mode: "add" } | { mode: "change"; link: SeriesSource } | { mode: "replace"; link: SeriesSource };

export function SourcesPanel({ series }: { series: Series }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [searching, setSearching] = useState<Searching | null>(null);
  const [removing, setRemoving] = useState<SeriesSource | null>(null);
  const [ordering, setOrdering] = useState(false);
  const inherited = series.sourcePriorityMode === "inherit";
  const sources = [...(series.sources ?? [])].sort((a, b) => (a.effectivePriority ?? a.priority) - (b.effectivePriority ?? b.priority) || a.id - b.id);
  const refresh = () => qc.invalidateQueries({ queryKey: ["series", series.id] });

  const setMode = async (sourcePriorityMode: "inherit" | "custom") => {
    try {
      await unwrap(api.PUT("/api/v1/series/{id}", { params: { path: { id: series.id } }, body: { sourcePriorityMode } }));
      await refresh();
    } catch (error) {
      toast.fromError(error, t("Could not update source priority mode"));
    }
  };

  const update = async (ss: SeriesSource, body: { enabled?: boolean }) => {
    try {
      await unwrap(api.PUT("/api/v1/series/{id}/sources/{linkId}", { params: { path: { id: series.id, linkId: ss.id } }, body }));
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };

  /** move puts link i at position j in one request; the series keeps its own order from then on. */
  const move = async (i: number, j: number) => {
    if (j < 0 || j >= sources.length || i === j) return;
    const order = sources.map((s) => s.id);
    const [id] = order.splice(i, 1);
    order.splice(j, 0, id);
    setOrdering(true);
    try {
      await unwrap(api.PUT("/api/v1/series/{id}/sources/order", { params: { path: { id: series.id } }, body: { linkIds: order } }));
      if (inherited) toast.info(t("This series now keeps its own source order"), t("Switch back to the library default source list any time."));
      await refresh();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setOrdering(false);
    }
  };

  const remove = async () => {
    if (!removing) return;
    try {
      await unwrap(api.DELETE("/api/v1/series/{id}/sources/{linkId}", { params: { path: { id: series.id, linkId: removing.id } } }));
      refresh();
      setRemoving(null);
    } catch (e) {
      toast.fromError(e);
    }
  };

  const pick = async (m: SourceManga, g: PickGroup) => {
    if (!searching) return;
    const body = { moduleId: g.moduleId, sourceId: g.sourceId, url: m.url, engineRef: m.engineRef, title: m.title, sourceName: g.sourceName, lang: g.lang };
    try {
      if (searching.mode === "add") {
        await unwrap(api.POST("/api/v1/series/{id}/sources", { params: { path: { id: series.id } }, body }));
        toast.success(t("Linked {source}", { source: g.sourceName }));
      } else {
        await unwrap(api.POST("/api/v1/series/{id}/sources/{linkId}/replace", { params: { path: { id: series.id, linkId: searching.link.id } }, body }));
        toast.success(t("{source} now points to “{title}”", { source: g.sourceName, title: m.title }), t("Checking it for chapters…"));
      }
      refresh();
      setSearching(null);
    } catch (e) {
      toast.fromError(e);
    }
  };

  const linked = sources.map((s) => ({ moduleId: s.moduleId, sourceId: s.sourceId, url: s.mangaUrl }));
  const status = (ss: SeriesSource) =>
    !ss.enabled ? <Badge>{t("off")}</Badge>
    : ss.consecutiveFailures > 0 ? <Badge tone="err">{t("{count} failures", { count: ss.consecutiveFailures })}</Badge>
    : ss.lastSuccessAt ? <Badge tone="ok">{t("ok")}</Badge>
    : <Badge>{t("pending")}</Badge>;

  return (
    <Card
      title={<span>{t("Sources")} <span className="font-normal text-muted">{sources.length}</span></span>}
      className="mb-6"
      actions={
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-muted">{t("Order")}</span>
          <div role="group" aria-label={t("Source order")} className="flex rounded-md border border-border bg-bg p-0.5">
            {([["inherit", series.language ? t("Follows {lang} defaults", { lang: languageName(series.language) }) : t("Follows the defaults")], ["custom", t("Custom list")]] as const).map(([mode, text]) => {
              const on = (mode === "inherit") === inherited;
              return (
                <button key={mode} type="button" aria-pressed={on} onClick={() => !on && void setMode(mode)} className={clsx("rounded px-2.5 py-1 text-xs font-medium", on ? "bg-panel-2 text-fg" : "text-muted hover:text-fg")}>
                  {text}
                </button>
              );
            })}
          </div>
          <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setSearching({ mode: "add" })}>{t("Add source")}</Button>
        </div>
      }
    >
      {inherited && sources.length > 1 && <p className="mb-3 text-xs text-muted">{t("Order follows this library, then the series language, then global catalog priority. Moving a source gives this series its own order.")}</p>}
      {sources.length === 0 ? (
        <p className="text-sm text-muted">{t("No source is linked. Add one to receive chapters.")}</p>
      ) : (
        <ol className="flex flex-col gap-2">
          {sources.map((ss, i) => (
            <li key={ss.id} className={clsx("flex flex-wrap items-center gap-3 rounded-lg border p-3 sm:flex-nowrap", i === 0 && ss.enabled ? "border-accent/40 bg-accent/5" : "border-border", !ss.enabled && "opacity-70")}>
              <div className="flex shrink-0 flex-col">
                <IconButton title={t("Higher priority")} className="size-6" disabled={ordering || i === 0} onClick={() => void move(i, i - 1)}>
                  <ArrowUp className="size-3.5" />
                </IconButton>
                <IconButton title={t("Lower priority")} className="size-6" disabled={ordering || i === sources.length - 1} onClick={() => void move(i, i + 1)}>
                  <ArrowDown className="size-3.5" />
                </IconButton>
              </div>
              <span className="w-4 shrink-0 text-center font-semibold text-muted">{i + 1}</span>
              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-semibold">{ss.sourceName || ss.sourceId}</span>
                  {i === 0 && ss.enabled && <Badge tone="accent">{t("Primary")}</Badge>}
                  {status(ss)}
                </div>
                {ss.webUrl ? (
                  <a href={ss.webUrl} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 text-sm text-fg/80 hover:text-accent-2">
                    <span className="truncate">“{ss.title || ss.mangaUrl}”</span> <ExternalLink className="size-3 shrink-0" />
                  </a>
                ) : (
                  <span className="truncate text-sm text-fg/80">“{ss.title || ss.mangaUrl}”</span>
                )}
                <span className="text-xs text-muted">
                  {[
                    ss.chapters !== undefined && t("{count} chapters", { count: ss.chapters }),
                    ss.files !== undefined && t("{count} of your files", { count: ss.files }),
                    ss.lastCheckedAt && t("checked {when}", { when: relative(ss.lastCheckedAt) }),
                    ss.enabled && t("next {when}", { when: relative(ss.nextCheckAt) }),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
                {ss.lastError && <p className="break-words text-xs text-err">{ss.lastError}</p>}
              </div>
              <Switch checked={ss.enabled} onChange={(v) => update(ss, { enabled: v })} label={<span className="sr-only">{t("Enabled")}</span>} />
              <Menu
                align="right"
                label={<><MoreHorizontal className="size-4" /><span className="sr-only">{t("More for {source}", { source: ss.sourceName })}</span></>}
                items={[
                  { label: t("Change match…"), onSelect: () => setSearching({ mode: "change", link: ss }) },
                  { label: t("Replace with another source…"), onSelect: () => setSearching({ mode: "replace", link: ss }) },
                  { label: t("Move to top"), onSelect: () => void move(i, 0), hidden: i === 0 },
                  { label: t("Open on site"), onSelect: () => window.open(ss.webUrl, "_blank", "noreferrer"), hidden: !ss.webUrl },
                  { section: "" },
                  { label: t("Unlink…"), danger: true, onSelect: () => setRemoving(ss) },
                ]}
              />
            </li>
          ))}
        </ol>
      )}
      {searching && (
        <SourceSearchModal
          initialQuery={series.title}
          initialLang={series.language}
          rootFolderId={series.rootFolderId}
          titles={[series.title, ...(series.metadata?.altTitles ?? [])]}
          title={searching.mode === "add" ? t("Add a source") : searching.mode === "change" ? t("Change the {source} match", { source: searching.link.sourceName }) : t("Replace {source}", { source: searching.link.sourceName })}
          linked={linked}
          initialKeys={searching.mode === "change" ? [`${searching.link.moduleId}:${searching.link.sourceId}`] : undefined}
          excludeLinked={searching.mode !== "change"}
          onClose={() => setSearching(null)}
          onPick={(m, g) => void pick(m, g)}
        />
      )}
      <Confirm
        open={!!removing}
        title={t("Unlink source")}
        danger
        confirmLabel={t("Unlink")}
        message={`Stop using ${removing?.sourceName} for this series? Downloaded files are kept.`}
        onConfirm={remove}
        onClose={() => setRemoving(null)}
      />
    </Card>
  );
}
