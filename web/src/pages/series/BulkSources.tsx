import { t } from "../../lib/i18n/core";
import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { api, unwrap, type Catalog, type S } from "../../api/client";
import { followCommand, useCatalogs } from "../../api/queries";
import { Badge, Button, ErrorBox, Input, Modal, Progress } from "../../components/ui";
import { languageMatches, languageName } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { SourceSearchModal } from "./SourceSearch";

type Action = "add" | "replace" | "remove" | "enable" | "disable";
type Result = S["BulkResult"];
type Pick = { url: string; title: string; engineRef?: string };

const key = (c: { moduleId: number; id?: string; sourceId?: string }) => `${c.moduleId}:${c.id ?? c.sourceId}`;
const chunk = 25; // the server previews at most this many series per request
const sure = 0.9; // matches at least this good are ticked by default

/**
 * CatalogChooser is a searchable list of catalogs, with how many of the
 * selected series use each one.
 */
function CatalogChooser({ label, catalogs, usage, value, onChange }: { label: string; catalogs: Catalog[]; usage: Map<string, number>; value: string; onChange: (k: string) => void }) {
  const [filter, setFilter] = useState("");
  const shown = catalogs.filter((c) => !filter || c.displayName.toLowerCase().includes(filter.toLowerCase()) || languageMatches(c.lang, filter));
  return (
    <fieldset className="flex min-w-0 flex-1 flex-col gap-1.5">
      <legend className="mb-1.5 text-sm text-muted">{label}</legend>
      <Input placeholder={t("Filter catalogs…")} aria-label={t("Filter catalogs…")} value={filter} onChange={(e) => setFilter(e.target.value)} />
      <div role="radiogroup" aria-label={label} className="max-h-44 overflow-y-auto rounded-md border border-border">
        {shown.map((c) => {
          const n = usage.get(key(c)) ?? 0;
          const on = value === key(c);
          return (
            <button key={key(c)} type="button" role="radio" aria-checked={on} onClick={() => onChange(key(c))} className={clsx("flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm", on ? "bg-accent/15 text-fg" : "hover:bg-panel-2")}>
              <span className="flex-1 truncate">{c.displayName}</span>
              <Badge>{languageName(c.lang)}</Badge>
              {n > 0 && <span className="text-xs text-muted">{t("used by {count}", { count: n })}</span>}
            </button>
          );
        })}
        {shown.length === 0 && <p className="px-3 py-2 text-sm text-muted">{t("No catalog matches.")}</p>}
      </div>
    </fieldset>
  );
}

/**
 * BulkSourcesModal changes one catalog across many series: add it as a
 * fallback, replace another catalog with it, or remove / switch it off.
 * Every series is previewed first; matches can be unticked or searched by
 * hand, and Apply runs exactly what was reviewed.
 */
export function BulkSourcesModal({ ids, onClose }: { ids: number[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: catalogs } = useCatalogs();
  const { data: usageRows } = useQuery({
    queryKey: ["source-usage", ids],
    queryFn: () => unwrap(api.POST("/api/v1/series/sources/usage", { body: { seriesIds: ids } })),
  });
  const [action, setAction] = useState<Action>("add");
  const [to, setTo] = useState("");
  const [from, setFrom] = useState("");
  const [results, setResults] = useState<Result[]>([]);
  const [checked, setChecked] = useState<Set<number>>(new Set());
  const [picks, setPicks] = useState<Map<number, Pick>>(new Map());
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [searchFor, setSearchFor] = useState<Result | null>(null);
  const actions: { id: Action; label: string }[] = [
    { id: "add", label: t("Add fallback") },
    { id: "replace", label: t("Replace") },
    { id: "remove", label: t("Remove") },
    { id: "enable", label: t("Turn on") },
    { id: "disable", label: t("Turn off") },
  ];

  const all = useMemo(() => [...(catalogs?.items ?? [])].sort((a, b) => a.displayName.localeCompare(b.displayName)), [catalogs]);
  const usage = useMemo(() => new Map((usageRows ?? []).map((u) => [`${u.moduleId}:${u.sourceId}`, u.series])), [usageRows]);
  const used = all.filter((c) => usage.has(key(c)));
  const needsTo = action === "add" || action === "replace";
  const target = action === "replace" || action === "add" ? to : from;
  const ready = needsTo ? !!to && (action !== "replace" || (!!from && from !== to)) : !!from;
  const toCatalog = all.find((c) => key(c) === to);

  // preview every selected series, a chunk at a time, as soon as the choice is complete
  useEffect(() => {
    setResults([]);
    setChecked(new Set());
    setPicks(new Map());
    setError(null);
    if (!ready) return;
    let stop = false;
    const [moduleId, sourceId] = target.split(/:(.*)/s);
    const [fromModuleId, fromSourceId] = from.split(/:(.*)/s);
    (async () => {
      setChecking(true);
      try {
        for (let i = 0; i < ids.length && !stop; i += chunk) {
          const r = await unwrap(
            api.POST("/api/v1/series/sources/bulk", {
              body: { action, moduleId: Number(moduleId), sourceId, fromModuleId: action === "replace" ? Number(fromModuleId) : undefined, fromSourceId: action === "replace" ? fromSourceId : undefined, seriesIds: ids.slice(i, i + chunk), dryRun: true },
            }),
          );
          if (stop) return;
          const rows = r.results ?? [];
          setResults((cur) => [...cur, ...rows]);
          setChecked((cur) => new Set([...cur, ...rows.filter((x) => x.done !== "skipped" && (!x.score || x.score >= sure)).map((x) => x.seriesId)]));
        }
      } catch (e) {
        if (!stop) setError(e);
      } finally {
        if (!stop) setChecking(false);
      }
    })();
    return () => {
      stop = true;
    };
  }, [action, to, from, ready, ids.join(",")]);

  const rowState = (r: Result) => (picks.has(r.seriesId) ? "picked" : r.done === "skipped" ? "skipped" : r.score && r.score < sure ? "unsure" : "ok");
  const counts = results.reduce<Record<string, number>>((acc, r) => ((acc[rowState(r)] = (acc[rowState(r)] ?? 0) + 1), acc), {});
  const willApply = results.filter((r) => checked.has(r.seriesId) && rowState(r) !== "skipped");
  const verb = { add: t("will be added"), replace: t("will be replaced"), remove: t("will be removed"), enable: t("will be turned on"), disable: t("will be turned off") }[action];

  const apply = async () => {
    const [moduleId, sourceId] = target.split(/:(.*)/s);
    const [fromModuleId, fromSourceId] = from.split(/:(.*)/s);
    setApplying(true);
    try {
      const r = await unwrap(
        api.POST("/api/v1/series/sources/bulk", {
          body: {
            action,
            moduleId: Number(moduleId),
            sourceId,
            fromModuleId: action === "replace" ? Number(fromModuleId) : undefined,
            fromSourceId: action === "replace" ? fromSourceId : undefined,
            seriesIds: willApply.map((x) => x.seriesId),
            picks: needsTo
              ? willApply.map((x) => {
                  const p = picks.get(x.seriesId);
                  return { seriesId: x.seriesId, url: p?.url ?? x.url ?? "", title: p?.title ?? x.match, engineRef: p?.engineRef ?? x.engineRef };
                })
              : undefined,
          },
        }),
      );
      const name = (action === "remove" || action === "enable" || action === "disable" ? all.find((c) => key(c) === from) : toCatalog)?.displayName ?? "";
      toast.info(t("{catalog} queued for {count} series", { catalog: name, count: willApply.length }), t("You'll get the result here when it's done."));
      if (r.command) void followCommand(r.command.id, `${name} · ${willApply.length} series`, toast, () => qc.invalidateQueries({ queryKey: ["series"] }));
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setApplying(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      size="xl"
      title={t("Change sources for {count} series", { count: ids.length })}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={applying} disabled={checking || willApply.length === 0} onClick={() => void apply()}>
            {t("Apply to {count} series", { count: willApply.length })}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <div role="tablist" aria-label={t("Action")} className="grid grid-cols-5 gap-0.5 rounded-lg border border-border bg-bg p-0.5">
          {actions.map((a) => (
            <button key={a.id} type="button" role="tab" aria-selected={action === a.id} onClick={() => setAction(a.id)} className={clsx("rounded-md py-1.5 text-sm font-medium", action === a.id ? "bg-panel-2 text-fg" : "text-muted hover:text-fg")}>
              {a.label}
            </button>
          ))}
        </div>
        <div className="flex flex-col gap-3 sm:flex-row">
          {action !== "add" && <CatalogChooser label={action === "replace" ? t("From") : t("Catalog")} catalogs={used} usage={usage} value={from} onChange={setFrom} />}
          {needsTo && <CatalogChooser label={action === "replace" ? t("To") : t("Catalog")} catalogs={action === "replace" ? all.filter((c) => key(c) !== from) : all} usage={usage} value={to} onChange={setTo} />}
        </div>
        <p className="text-xs text-muted">
          {action === "add" && t("Each series is found at the catalog by title and linked last, so downloads keep preferring the sources it already has.")}
          {action === "replace" && t("Each series is found at the new catalog and takes the old link’s place and priority. Downloaded files and read progress stay.")}
          {(action === "remove" || action === "enable" || action === "disable") && t("Only series linked to the catalog are touched. Downloaded files stay.")}
        </p>
        {error ? <ErrorBox error={error} /> : null}
        {ready && (
          <section aria-label={t("Review")} className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="font-medium">{t("Review")}</span>
              {(counts.ok ?? 0) + (counts.picked ?? 0) > 0 && <Badge tone="ok">{(counts.ok ?? 0) + (counts.picked ?? 0)} {verb}</Badge>}
              {counts.unsure ? <Badge tone="warn">{t("{count} need a look", { count: counts.unsure })}</Badge> : null}
              {counts.skipped ? <Badge>{t("{count} skipped", { count: counts.skipped })}</Badge> : null}
              <span className="ml-auto text-xs text-muted">{t("Checked {done} of {total}", { done: results.length, total: ids.length })}</span>
            </div>
            {checking && <Progress value={(100 * results.length) / Math.max(1, ids.length)} />}
            <div className="max-h-80 overflow-y-auto rounded-md border border-border">
              <table className="w-full text-sm">
                <tbody>
                  {results.map((r) => {
                    const st = rowState(r);
                    const p = picks.get(r.seriesId);
                    return (
                      <tr key={r.seriesId} className={clsx("border-b border-border last:border-0", st === "skipped" && "opacity-70")}>
                        <td className="w-8 px-3 py-2">
                          <input
                            type="checkbox"
                            aria-label={t("Include {title}", { title: r.title })}
                            disabled={st === "skipped"}
                            checked={checked.has(r.seriesId) && st !== "skipped"}
                            onChange={() => setChecked((cur) => { const n = new Set(cur); if (n.has(r.seriesId)) n.delete(r.seriesId); else n.add(r.seriesId); return n; })}
                          />
                        </td>
                        <td className="px-2 py-2 font-medium">{r.title}</td>
                        <td className="px-2 py-2">
                          {st === "skipped" ? (
                            <span className="text-muted">{r.reason}</span>
                          ) : (
                            <span className="flex flex-col">
                              <span>{p?.title ?? r.match}</span>
                              {r.current && <span className="text-xs text-muted">{t("instead of “{title}”", { title: r.current })}</span>}
                            </span>
                          )}
                        </td>
                        <td className="w-20 px-2 py-2">
                          {st === "picked" ? <Badge tone="info">{t("picked")}</Badge> : r.score ? <Badge tone={r.score >= sure ? "ok" : r.score >= 0.7 ? "warn" : "err"}>{Math.round(r.score * 100)}%</Badge> : null}
                        </td>
                        <td className="w-24 px-3 py-2 text-right">
                          {needsTo && toCatalog && (st === "skipped" ? /no confident match/.test(r.reason ?? "") : true) && (
                            <Button size="sm" onClick={() => setSearchFor(r)}>{st === "skipped" ? t("Search…") : t("Change…")}</Button>
                          )}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              {results.length === 0 && checking && <p className="p-3 text-sm text-muted">{t("Looking up each series…")}</p>}
            </div>
          </section>
        )}
      </div>
      {searchFor && toCatalog && (
        <SourceSearchModal
          initialQuery={searchFor.title}
          title={t("Find {title} at {catalog}", { title: searchFor.title, catalog: toCatalog.displayName })}
          initialKeys={[key(toCatalog)]}
          excludeLinked={false}
          onClose={() => setSearchFor(null)}
          onPick={(m, g) => {
            // the pick is linked under the catalog being applied, so a match
            // from another catalog (scope widened in the search) can't be used
            if (key(g) !== key(toCatalog)) {
              toast.error(t("Pick a match from {catalog}", { catalog: toCatalog.displayName }));
              return;
            }
            const id = searchFor.seriesId;
            setPicks((cur) => new Map(cur).set(id, { url: m.url, title: m.title, engineRef: m.engineRef }));
            setChecked((cur) => new Set(cur).add(id));
            setResults((cur) => cur.map((x) => (x.seriesId === id ? { ...x, done: action === "replace" ? "replaced" : "added", reason: undefined } : x)));
            setSearchFor(null);
          }}
        />
      )}
    </Modal>
  );
}
