import { t, type MessageKey } from "../../lib/i18n/core";
import { useState, type ReactNode } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Card, ErrorBox, Loading, PageHeader, Segmented, Table, Td, Th } from "../../components/ui";
import { bytes } from "../../lib/format";

type Range = "1h" | "24h" | "7d";
type Report = S["PerformanceReport"];

/** Friendly names of the Server-Timing phases. */
const phaseNames: Record<string, MessageKey> = {
  db: "Database",
  source: "Source requests",
  file: "Reading archives",
  variant: "Resizing pages",
  decode: "Decoding images",
};

const num = (n: number, digits = 0) => new Intl.NumberFormat(undefined, { maximumFractionDigits: digits }).format(n);
const pct = (share: number) => new Intl.NumberFormat(undefined, { style: "percent", maximumFractionDigits: share > 0 && share < 0.1 ? 1 : 0 }).format(share);
/** ms shows a latency: 0.4 ms, 38 ms, 1.9 s. */
export function ms(v: number): string {
  if (v <= 0) return "—";
  if (v < 1) return `${num(v, 1)} ms`;
  if (v < 1000) return `${num(v)} ms`;
  return `${num(v / 1000, 1)} s`;
}
function uptime(seconds: number): string {
  const d = Math.floor(seconds / 86400), h = Math.floor((seconds % 86400) / 3600), m = Math.floor((seconds % 3600) / 60);
  return d > 0 ? `${d} d ${h} h` : h > 0 ? `${h} h ${m} min` : `${m} min`;
}

export function PerformancePage() {
  const [range, setRange] = useState<Range>("24h");
  const { data, error, isLoading } = useQuery({
    queryKey: ["system-metrics", range],
    queryFn: () => unwrap(api.GET("/api/v1/system/metrics", { params: { query: { range } } })),
    refetchInterval: 30_000,
    placeholderData: keepPreviousData, // a new range doesn't blank the page while it loads
  });
  return (
    <>
      <PageHeader
        title={t("Performance")}
        subtitle={t("How fast the server answers and where the time goes. Counted in memory since the last restart.")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Segmented label={t("Time range")} value={range} onChange={setRange} options={[{ value: "1h", label: t("1 hour") }, { value: "24h", label: t("24 hours") }, { value: "7d", label: t("7 days") }]} />
            <a className="text-sm text-accent-2 hover:underline" href={apiUrl("api/v1/system/metrics/prometheus")} title={t("The same numbers for Prometheus; scrape with the API key as a bearer token")}>
              {t("Prometheus")}
            </a>
          </div>
        }
      />
      {error ? <ErrorBox error={error} /> : isLoading || !data ? <Loading /> : <Body r={data} range={range} />}
    </>
  );
}

function Body({ r, range }: { r: Report; range: Range }) {
  const s = r.summary;
  const delta = s.prevP95Ms > 0 && s.p95Ms > 0 ? (s.p95Ms - s.prevP95Ms) / s.prevP95Ms : null;
  return (
    <div className="flex flex-col gap-4">
      <section aria-label={t("Summary")} className="grid grid-cols-[repeat(auto-fit,minmax(13rem,1fr))] gap-3">
        <Tile label={t("Requests per minute")} value={num(s.perMinute, s.perMinute < 10 ? 1 : 0)} note={t("peak {n}", { n: num(s.peakPerMinute, 1) })} />
        <Tile
          label={t("Response time, p95")}
          value={ms(s.p95Ms)}
          note={delta === null ? t("no earlier range to compare") : t("{change} on the range before", { change: (delta > 0 ? "+" : "") + pct(delta) })}
          tone={delta === null ? undefined : delta <= 0 ? "ok" : delta > 0.2 ? "warn" : undefined}
        />
        <Tile label={t("Answered with 304")} value={pct(s.cachedShare)} note={t("the browser already had it")} />
        <Tile
          label={t("Server errors")}
          value={pct(s.errorShare)}
          note={s.errors === 0 ? t("none in this range") : t("{n} in this range", { n: num(s.errors) })}
          tone={s.errors === 0 ? "ok" : "warn"}
        />
      </section>

      <div className="flex flex-wrap gap-4">
        <Card title={t("Response time")} className="min-w-0 flex-[999_1_36rem]" actions={<Legend />}>
          <div className="p-4">
            <Chart points={r.points} step={r.stepSeconds} range={range} />
          </div>
        </Card>
        <Card title={t("Where request time goes")} className="min-w-0 flex-[1_1_20rem]">
          <Phases r={r} />
        </Card>
      </div>

      <Card title={t("Slowest endpoints")} actions={<span className="text-xs text-muted">{t("By total time spent")}</span>}>
        {r.endpoints.length === 0 ? (
          <p className="p-4 text-sm text-muted">{t("No requests in this range yet.")}</p>
        ) : (
          <Table className="rounded-none border-0">
            <thead>
              <tr>
                <Th>{t("Endpoint")}</Th>
                <Th className="text-right">{t("Calls")}</Th>
                <Th className="text-right">{"p50"}</Th>
                <Th className="text-right">{"p95"}</Th>
                <Th className="text-right">{"304"}</Th>
                <Th className="text-right">{t("Sent")}</Th>
                <Th className="text-right">{t("Errors")}</Th>
              </tr>
            </thead>
            <tbody>
              {r.endpoints.map((e) => (
                <tr key={e.route}>
                  <Td className="font-mono text-xs">{e.route}</Td>
                  <Td className="text-right tabular-nums">{num(e.calls)}</Td>
                  <Td className="text-right tabular-nums">{ms(e.p50Ms)}</Td>
                  <Td className={`text-right tabular-nums ${e.p95Ms >= 1000 ? "text-warn" : ""}`}>{ms(e.p95Ms)}</Td>
                  <Td className="text-right tabular-nums">{e.calls > 0 && e.notModified > 0 ? pct(e.notModified / e.calls) : "—"}</Td>
                  <Td className="text-right tabular-nums">{bytes(e.bytes)}</Td>
                  <Td className={`text-right tabular-nums ${e.errors > 0 ? "text-err" : ""}`}>{num(e.errors)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      <div className="grid grid-cols-[repeat(auto-fit,minmax(17rem,1fr))] gap-4">
        <Card title={t("Caches")}>
          <Rows
            note={t("Hit rate since restart")}
            rows={r.caches.map((c) => [t(c.name as MessageKey), c.hits + c.misses === 0 ? "—" : `${pct(c.hits / (c.hits + c.misses))} · ${num(c.hits + c.misses)}`])}
          />
        </Card>
        <Card title={t("Database")}>
          <Rows
            note={t("Connection pool and queries in this range")}
            rows={[
              [t("Connections in use"), r.gauges.dbMaxOpen > 0 ? t("{n} of {max}", { n: r.gauges.dbInUse, max: r.gauges.dbMaxOpen }) : num(r.gauges.dbInUse)],
              [t("Waited for a connection"), num(r.gauges.dbWaitCount)],
              [t("Query time, p95"), ms(r.queries.p95Ms)],
              [t("Slow queries"), num(r.queries.slow)],
            ]}
          />
        </Card>
        <Card title={t("Server process")}>
          <Rows
            note={r.goVersion}
            rows={[
              [t("Memory in use"), bytes(r.gauges.heapBytes)],
              [t("Goroutines"), num(r.gauges.goroutines)],
              [t("GC pause, p99"), ms(r.gauges.gcPauseP99Ms)],
              [t("Uptime"), uptime(r.uptimeSeconds)],
              [t("Profiler"), r.profiling ? <a key="p" className="text-accent-2 hover:underline" href={apiUrl("api/v1/system/pprof/")}>{t("on")}</a> : t("off")],
            ]}
          />
        </Card>
        <Card title={t("Background work")}>
          <Rows
            note={t("Download queue now")}
            rows={[
              [t("Waiting"), num(r.gauges.queueWaiting)],
              [t("Running"), num(r.gauges.queueRunning)],
            ]}
          />
        </Card>
      </div>
    </div>
  );
}

function Tile({ label, value, note, tone }: { label: string; value: string; note: string; tone?: "ok" | "warn" }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border border-border bg-panel p-4">
      <span className="text-sm text-muted">{label}</span>
      <span className="font-mono text-2xl tabular-nums">{value}</span>
      <span className={`text-xs ${tone === "ok" ? "text-ok" : tone === "warn" ? "text-warn" : "text-muted"}`}>{note}</span>
    </div>
  );
}

function Legend() {
  return (
    <span className="flex gap-4 text-xs text-muted">
      <span className="inline-flex items-center gap-1.5">
        <span className="h-0.5 w-3.5 rounded bg-accent" />
        {"p95"}
      </span>
      <span className="inline-flex items-center gap-1.5">
        <span className="h-0.5 w-3.5 rounded bg-info" />
        {"p50"}
      </span>
    </span>
  );
}

/** niceMax rounds a chart's top up to 1, 2 or 5 times a power of ten. */
function niceMax(v: number): number {
  if (v <= 0) return 10;
  const p = 10 ** Math.floor(Math.log10(v));
  return [1, 2, 5, 10].map((m) => m * p).find((m) => m >= v) ?? 10 * p;
}

function Chart({ points, step, range }: { points: Report["points"]; step: number; range: Range }) {
  const W = 600, H = 180;
  const max = niceMax(Math.max(0, ...points.map((p) => p.p95Ms)));
  const x = (i: number) => (points.length > 1 ? (i * W) / (points.length - 1) : 0);
  const y = (v: number) => H - 2 - (v / max) * (H - 4);
  const line = (key: "p50Ms" | "p95Ms") => points.map((p, i) => `${x(i).toFixed(1)},${y(p[key]).toFixed(1)}`).join(" ");
  const fmtTime = (iso: string) =>
    new Intl.DateTimeFormat(undefined, range === "7d" ? { weekday: "short", hour: "2-digit" } : { hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((f) => Math.min(points.length - 1, Math.round(f * (points.length - 1))));
  if (!points.some((p) => p.requests > 0)) return <p className="py-10 text-center text-sm text-muted">{t("No requests in this range yet.")}</p>;
  return (
    <div className="flex gap-2">
      <div className="flex flex-col justify-between pb-5 text-right font-mono text-[11px] text-muted">
        <span>{ms(max)}</span>
        <span>{ms(max / 2)}</span>
        <span>0</span>
      </div>
      <div className="min-w-0 flex-1">
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={t("p50 and p95 response time over the range")} className="block h-52 w-full">
          {[1, H / 2, H - 1].map((v) => (
            <line key={v} x1={0} x2={W} y1={v} y2={v} className="stroke-border" strokeWidth={1} vectorEffect="non-scaling-stroke" />
          ))}
          <polyline points={line("p95Ms")} fill="none" className="stroke-accent" strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
          <polyline points={line("p50Ms")} fill="none" className="stroke-info" strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
          {points.map((p, i) => (
            <rect key={p.at} x={x(i) - W / points.length / 2} y={0} width={W / points.length} height={H} fill="transparent">
              <title>{`${fmtTime(p.at)}: ${num(p.requests)} ${t("requests")}, p50 ${ms(p.p50Ms)}, p95 ${ms(p.p95Ms)}`}</title>
            </rect>
          ))}
        </svg>
        <div className="mt-1 flex justify-between font-mono text-[11px] text-muted">
          {ticks.map((i, k) => (
            <span key={k}>{points[i] ? fmtTime(points[i].at) : ""}</span>
          ))}
        </div>
        <p className="sr-only">{t("Each point covers {n} minutes.", { n: num(step / 60) })}</p>
      </div>
    </div>
  );
}

function Phases({ r }: { r: Report }) {
  const known = r.phases.filter((p) => p.share > 0.001);
  const rest = Math.max(0, 1 - known.reduce((a, p) => a + p.share, 0));
  const rows = [...known.map((p) => ({ name: phaseNames[p.name] ? t(phaseNames[p.name]) : p.name, share: Math.min(1, p.share) })), { name: t("Everything else"), share: rest }];
  if (r.summary.requests === 0) return <p className="p-4 text-sm text-muted">{t("No requests in this range yet.")}</p>;
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="text-xs text-muted">{t("Share of the time handlers took, from their Server-Timing phases")}</p>
      {rows.map((p) => (
        <div key={p.name} className="flex flex-col gap-1">
          <div className="flex justify-between text-sm">
            <span>{p.name}</span>
            <span className="font-mono text-muted">{pct(p.share)}</span>
          </div>
          <div className="h-2 overflow-hidden rounded bg-panel-2">
            <div className="h-2 rounded bg-accent" style={{ width: `${(p.share * 100).toFixed(1)}%` }} />
          </div>
        </div>
      ))}
    </div>
  );
}

function Rows({ note, rows }: { note?: string; rows: [string, ReactNode][] }) {
  return (
    <div className="flex flex-col gap-2 p-4 text-sm">
      {note && <p className="text-xs text-muted">{note}</p>}
      <dl className="flex flex-col">
        {rows.map(([k, v]) => (
          <div key={k} className="flex justify-between gap-3 border-t border-border/60 py-1.5 first:border-t-0">
            <dt className="text-muted">{k}</dt>
            <dd className="font-mono tabular-nums">{v}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
