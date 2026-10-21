import { t } from "../../lib/i18n/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import clsx from "clsx";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, EmptyState, ErrorBox, Loading, Segmented, Table, Td, Th } from "../../components/ui";
import { useQueryParam } from "../../lib/urlState";
import { languageName, relative } from "../../lib/format";

type Row = S["Reliability"];
type State = "cooling" | "failing" | "healthy" | "idle";

const key = (r: Row) => `${r.moduleId}:${r.sourceId}`;

/** state is what the table says about a catalog: cooling down, failing (under 80% of 5+ requests worked), healthy, or not used. */
export function sourceState(r: Row): State {
  if (r.cooldownUntil && new Date(r.cooldownUntil) > new Date()) return "cooling";
  if (r.requests >= 5 && r.requests - r.failures < 0.8 * r.requests) return "failing";
  return r.requests > 0 ? "healthy" : "idle";
}

const stateLabel = (s: State) => ({ cooling: t("Cooling down"), failing: t("Failing"), healthy: t("Healthy"), idle: t("Not used") })[s];
const stateTone = (s: State) => ({ cooling: "err", failing: "warn", healthy: "ok", idle: "default" } as const)[s];
const rateTone = (pct: number) => (pct >= 95 ? "bg-ok" : pct >= 80 ? "bg-warn" : "bg-err");
const seconds = (ms: number) => (ms ? (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`) : "—");
const pctOf = (r: Row) => (r.requests ? Math.round(((r.requests - r.failures) / r.requests) * 100) : null);

/** Reliability shows how each catalog answered over the last day or week, with one catalog opened in detail. */
export function Reliability() {
  const [days, setDays] = useQueryParam("days", "1");
  const [picked, setPicked] = useQueryParam("source", "");
  const { data, isLoading, error } = useQuery({
    queryKey: ["catalogs-reliability", days],
    queryFn: () => unwrap(api.GET("/api/v1/catalogs/reliability", { params: { query: { days: days === "7" ? 7 : 1 } } })),
    refetchInterval: 60_000,
  });
  const rows = data ?? [];
  const open = rows.find((r) => key(r) === picked);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <p className="flex-1 text-sm text-muted">{t("How each source answered requests. Pick a source to see its cooldown and recent failures.")}</p>
        <Segmented label={t("Period")} value={days === "7" ? "7" : "1"} onChange={(v) => setDays(v)} options={[{ value: "1", label: t("24 hours") }, { value: "7", label: t("7 days") }]} />
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && rows.length === 0 && <EmptyState title={t("No requests yet")}>{t("Sources show here once series use them.")}</EmptyState>}
      {open && <Detail row={open} days={days === "7" ? 7 : 1} onClose={() => setPicked("")} />}
      {rows.length > 0 && (
        <Table>
          <thead>
            <tr>
              <Th>{t("Source")}</Th>
              <Th>{t("Success")}</Th>
              <Th className="text-right">{t("Requests")}</Th>
              <Th className="text-right">{t("Median time")}</Th>
              <Th className="text-right">{t("Fallback used")}</Th>
              <Th>{t("State")}</Th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => {
              const st = sourceState(r);
              const pct = pctOf(r);
              const on = key(r) === picked;
              return (
                <tr key={key(r)} className={clsx(on && "bg-panel-2")}>
                  <Td>
                    <button type="button" aria-expanded={on} onClick={() => setPicked(on ? "" : key(r))} className="text-left font-medium hover:text-accent-2">
                      {r.name}
                    </button>
                    <div className="text-xs text-muted">{[r.lang && languageName(r.lang), t("{n} series", { n: r.series }), !r.enabled && t("off")].filter(Boolean).join(" · ")}</div>
                  </Td>
                  <Td className="min-w-40">
                    <div className="flex items-center gap-2">
                      <span className="w-10 tabular-nums">{pct === null ? "—" : `${pct}%`}</span>
                      <span className="block h-1.5 flex-1 overflow-hidden rounded-full bg-border">
                        <span className={clsx("block h-full", pct !== null && rateTone(pct))} style={{ width: `${pct ?? 0}%` }} />
                      </span>
                    </div>
                  </Td>
                  <Td className="text-right tabular-nums">{r.requests.toLocaleString()}</Td>
                  <Td className="text-right tabular-nums">{seconds(r.medianMs)}</Td>
                  <Td className="text-right tabular-nums">{r.fallbacks || "—"}</Td>
                  <Td><Badge tone={stateTone(st)}>{stateLabel(st)}</Badge></Td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}
    </div>
  );
}

function Detail({ row: r, days, onClose }: { row: Row; days: number; onClose: () => void }) {
  const qc = useQueryClient();
  const st = sourceState(r);
  const pct = pctOf(r);
  const resume = async () => {
    await unwrap(api.PUT("/api/v1/catalogs", { body: { [key(r)]: { clearCooldown: true } } }));
    await Promise.all([qc.invalidateQueries({ queryKey: ["catalogs-reliability"] }), qc.invalidateQueries({ queryKey: ["catalogs"] })]);
  };
  const peak = Math.max(1, ...r.points.map((p) => p.requests));
  return (
    <Card
      title={<>{r.name} <span className="text-sm font-normal text-muted">{r.moduleName}</span></>}
      className={clsx(st === "cooling" && "border-err/50", st === "failing" && "border-warn/50")}
      actions={
        <div className="flex flex-wrap gap-2">
          {st === "cooling" && <Button size="sm" onClick={resume}>{t("End cooldown now")}</Button>}
          <Link to={`/activity/downloads?status=failed&source=${encodeURIComponent(key(r))}`}><Button size="sm">{t("Show its failed downloads")}</Button></Link>
          <Button size="sm" onClick={onClose}>{t("Close")}</Button>
        </div>
      }
    >
      <div className="grid grid-cols-[repeat(auto-fit,minmax(150px,1fr))] gap-4 text-sm">
        <Stat label={st === "cooling" ? t("Cooling down") : t("State")} value={st === "cooling" && r.cooldownUntil ? t("until {time}", { time: new Date(r.cooldownUntil).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }) }) : stateLabel(st)} hint={r.cooldownReason} />
        <Stat label={days === 7 ? t("Success, 7 days") : t("Success, 24 hours")} value={pct === null ? "—" : `${pct}%`} hint={t("{failed} of {n} requests failed", { failed: r.failures, n: r.requests })} />
        <Stat label={t("Median time")} value={seconds(r.medianMs)} hint={r.p90Ms ? t("slowest 10%: {time}", { time: seconds(r.p90Ms) }) : undefined} />
        <Stat label={t("Fallback used")} value={String(r.fallbacks)} hint={t("downloads that moved to another source")} />
        <Stat label={t("Series")} value={String(r.series)} hint={r.failingSeries ? t("{n} failing their checks", { n: r.failingSeries }) : undefined} />
      </div>
      <div className="mt-4">
        <div role="img" aria-label={days === 7 ? t("Requests and failures over 7 days") : t("Requests and failures over 24 hours")} className="flex h-14 items-end gap-0.5">
          {r.points.map((p) => {
            const ok = p.requests ? (p.requests - p.failures) / p.requests : 1;
            return (
              <span
                key={p.at}
                title={`${new Date(p.at).toLocaleString()}: ${p.requests - p.failures}/${p.requests}`}
                className={clsx("block flex-1 rounded-t-sm", !p.requests ? "bg-border" : ok >= 0.95 ? "bg-ok" : ok >= 0.8 ? "bg-warn" : "bg-err")}
                style={{ height: p.requests ? `${Math.max(8, (p.requests / peak) * 100)}%` : "3px" }}
              />
            );
          })}
        </div>
        <div className="mt-1 flex justify-between text-[11px] text-muted"><span>{days === 7 ? t("7 days ago") : t("24 hours ago")}</span><span>{t("now")}</span></div>
      </div>
      <h3 className="mb-1 mt-4 text-xs font-semibold uppercase tracking-wide text-muted">{t("Recent failures")}</h3>
      {r.recentFailures.length === 0 ? (
        <p className="text-sm text-muted">{t("None since the server started.")}</p>
      ) : (
        <ul className="divide-y divide-border/60 text-sm">
          {r.recentFailures.slice(0, 8).map((f, i) => (
            <li key={i} className="flex flex-wrap gap-x-3 py-1.5">
              <span className="w-24 shrink-0 text-muted">{relative(f.at)}</span>
              <span className="min-w-0 flex-1">{f.what || t("Request")}</span>
              <span className="break-all font-mono text-xs text-err">{f.error}</span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <div className="text-muted">{label}</div>
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      {hint && <div className="text-xs text-muted">{hint}</div>}
    </div>
  );
}
