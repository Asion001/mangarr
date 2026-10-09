import { sizeChange, sizeChangeLabel, throughput, signedBytes } from "../../lib/processingMetrics";
import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { AlertTriangle, CheckCircle2, Info, LifeBuoy, RefreshCw, XCircle } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { useCommands, useHealth } from "../../api/queries";
import { Badge, Button, Card, Confirm, Loading, PageHeader, Progress, Table, Td, Th } from "../../components/ui";
import { bytes, dateTime, duration, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { describe, eta, useLiveProgress } from "../../lib/liveProgress";

export function StatusPage() {
  const qc = useQueryClient();
  const { data: health, isLoading } = useHealth();
  const { data: status } = useQuery({ queryKey: ["system-status"], queryFn: () => unwrap(api.GET("/api/v1/system/status")) });
  const { data: commands } = useCommands();
  const run = async () => {
    const r = await unwrap(api.POST("/api/v1/health/check"));
    qc.setQueryData(["health"], r);
  };
  const icon = (t: string) =>
    t === "error" ? <XCircle className="size-4 text-err" /> : t === "warning" ? <AlertTriangle className="size-4 text-warn" /> : <Info className="size-4 text-info" />;
  return (
    <>
      <PageHeader
        title={t("Status")}
        actions={
          <a href={apiUrl("api/v1/system/diagnostics")} download title={t("Status, health, modules, settings, queue and logs, with secrets removed")}>
            <Button icon={<LifeBuoy className="size-4" />}>{t("Download diagnostics")}</Button>
          </a>
        }
      />
      <Card
        title={t("Health")}
        className="mb-6"
        actions={
          <Button size="sm" icon={<RefreshCw className="size-3.5" />} onClick={run}>{t("Check now")}</Button>
        }
      >
        {isLoading && <Loading />}
        {health && health.checks.length === 0 && (
          <p className="flex items-center gap-2 text-sm text-ok">
            <CheckCircle2 className="size-4" />{" " + t("Everything looks good.")}</p>
        )}
        <div className="flex flex-col gap-2">
          {health?.checks.map((c, i) => (
            <div key={i} className="flex items-start gap-2 text-sm">
              {icon(c.type)}
              <div className="min-w-0">
                <span className="font-medium">{c.source}:</span>{" "}
                {c.link ? (
                  <Link to={c.link} className="text-fg/85 underline decoration-muted decoration-dotted underline-offset-2 hover:text-accent-2">
                    {c.message}
                  </Link>
                ) : (
                  <span className="text-fg/85">{c.message}</span>
                )}
                {c.items && c.items.length > 0 && <CheckItems items={c.items} />}
              </div>
            </div>
          ))}
        </div>
        {health && <p className="mt-3 text-xs text-muted">{t("Checked") + " "}{relative(health.checkedAt)}</p>}
      </Card>
      {status && (
        <Card title={t("About")} className="mb-6">
          <dl className="grid grid-cols-[140px_1fr] gap-y-1.5 text-sm">
            <dt className="text-muted">{t("Version")}</dt>
            <dd>{status.version}</dd>
            <dt className="text-muted">{t("Build")}</dt>
            <dd>
              {status.build} <span className="text-muted">({status.commit})</span>
            </dd>
            <dt className="text-muted">{t("Runtime")}</dt>
            <dd>
              {status.goVersion} · {status.os}/{status.arch}
            </dd>
            <dt className="text-muted">{t("Database")}</dt>
            <dd>{status.database}</dd>
            <dt className="text-muted">{t("Data folder")}</dt>
            <dd className="font-mono text-xs">{status.dataDir}</dd>
            <dt className="text-muted">{t("Started")}</dt>
            <dd>{dateTime(status.startedAt)}</dd>
          </dl>
        </Card>
      )}
      <ProcessingCard />
      <CacheCard />
      <Card title={t("Recent commands")}>
        <Table className="border-0">
          <thead>
            <tr>
              <Th>{t("Command")}</Th>
              <Th>{t("Status")}</Th>
              <Th>{t("Message")}</Th>
              <Th>{t("Trigger")}</Th>
              <Th>{t("Queued")}</Th>
              <Th>{t("Duration")}</Th>
            </tr>
          </thead>
          <tbody>
            {commands?.map((c) => (
              <tr key={c.id}>
                <Td className="font-medium">{c.name}</Td>
                <Td>
                  <Badge tone={c.status === "completed" ? "ok" : c.status === "failed" ? "err" : c.status === "started" ? "info" : "default"}>{c.status}</Badge>
                </Td>
                <Td className="max-w-md truncate text-xs text-muted" >{c.error || c.message}</Td>
                <Td className="text-xs text-muted">{c.trigger}</Td>
                <Td className="whitespace-nowrap text-xs text-muted">{relative(c.queuedAt)}</Td>
                <Td className="text-xs text-muted">{duration(c.durationMs)}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </Card>
    </>
  );
}

function CacheCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data } = useQuery({ queryKey: ["cache"], queryFn: () => unwrap(api.GET("/api/v1/system/cache")) });
  const clear = async (body: { catalogs?: boolean; images?: string[] }) => {
    try {
      qc.setQueryData(["cache"], await unwrap(api.POST("/api/v1/system/cache/clear", { body: { catalogs: false, ...body } })));
      toast.success(tr("Cache cleared"));
    } catch (e) {
      toast.fromError(e);
    }
  };
  const compact = async () => {
    try {
      await unwrap(api.POST("/api/v1/commands", { body: { name: "CompactImageCache" } }));
      toast.info(tr("Resizing cached images"));
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (!data) return null;
  const pct = data.imageMaxBytes > 0 ? Math.min(100, (data.imageBytes / data.imageMaxBytes) * 100) : 0;
  return (
    <Card
      title={t("Caches")}
      className="mb-6"
      actions={
        <Button size="sm" onClick={compact} title={t("Resize cached thumbnails and covers to small JPEGs")}>{t("Compact images")}</Button>
      }
    >
      <div className="mb-3 flex flex-col gap-1 text-sm">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span>{t("Images on disk:") + " "}<b>{bytes(data.imageBytes)}</b>
            {data.imageMaxBytes > 0 ? ` of ${bytes(data.imageMaxBytes)} limit` : tr(" (no limit)")}
          </span>
          <span className="text-xs text-muted">{t("oldest images are removed above the limit (Settings → General)")}</span>
        </div>
        {data.imageMaxBytes > 0 && <Progress value={pct} tone={pct > 95 ? "warn" : "accent"} />}
        {data.needsCompact && <p className="text-xs text-warn">{t("Images cached by an older version are being resized; sizes drop once that finishes.")}</p>}
      </div>
      <div className="overflow-x-auto">
        <Table>
          <thead>
            <tr>
              <Th>{t("Cache")}</Th>
              <Th>{t("Entries")}</Th>
              <Th>{t("Size")}</Th>
              <Th>{t("Average")}</Th>
              <Th></Th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <Td>{t("Search results & manga details (memory)")}</Td>
              <Td>{data.entries}</Td>
              <Td>
                {bytes(data.bytes)} / {bytes(data.maxBytes)}
              </Td>
              <Td>—</Td>
              <Td className="text-right">
                <Button size="sm" onClick={() => clear({ catalogs: true })}>{t("Clear")}</Button>
              </Td>
            </tr>
            {data.images.map((b) => (
              <tr key={b.name}>
                <Td>{imageLabels[b.name] ?? b.name}</Td>
                <Td>{b.files}</Td>
                <Td>{bytes(b.bytes)}</Td>
                <Td>{b.files > 0 ? bytes(b.bytes / b.files) : "—"}</Td>
                <Td className="text-right">
                  <Button size="sm" onClick={() => clear({ images: [b.name] })}>{t("Clear")}</Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      </div>
    </Card>
  );
}

const imageLabels: Record<string, string> = { thumbs: "Search thumbnails (disk)", assets: "Extension icons (disk)", covers: "Series covers (disk)", pages: "Streamed pages (disk)" };

function ProcessingCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const liveMap = useLiveProgress();
  const { data } = useQuery({
    queryKey: ["processing"],
    queryFn: () => unwrap(api.GET("/api/v1/processing")),
    refetchInterval: (q) => ((q.state.data?.active.length ?? 0) > 0 || (q.state.data?.pending ?? 0) > 0 ? 10_000 : 60_000),
  });
  const [clearing, setClearing] = useState<"ask" | "busy" | null>(null);
  if (!data) return null;
  const clearBacklog = async () => {
    setClearing("busy");
    try {
      const r = await unwrap(api.POST("/api/v1/processing/clear-backlog"));
      toast.success(t("Backlog cleared: {count} chapters left as they are", { count: r.chapters }));
      qc.invalidateQueries({ queryKey: ["processing"] });
      qc.invalidateQueries({ queryKey: ["queue"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setClearing(null);
    }
  };
  const resume = async () => {
    try {
      await unwrap(api.POST("/api/v1/processing/resume"));
      qc.invalidateQueries({ queryKey: ["processing"] });
      toast.success(tr("Re-encoding resumed"));
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <Card
      title={t("Processing")}
      className="mb-6"
      actions={data.pending > 0 && (
        <Button size="sm" onClick={() => setClearing("ask")}>{t("Clear backlog")}</Button>
      )}
    >
      <Confirm
        open={clearing !== null}
        title={t("Clear backlog")}
        danger
        confirmLabel={t("Clear backlog")}
        loading={clearing === "busy"}
        message={t("Stop waiting on {count} chapters? Their queued processing is removed and they stay as they are; chapters being processed now still finish. Changing the profile's processing settings, or Process downloaded chapters on a series, queues them again.", { count: data.pending })}
        onConfirm={clearBacklog}
        onClose={() => setClearing(null)}
      />
      {data.state.encodeBlocked && (
        <div className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-err/40 bg-err/10 p-2 text-sm">
          <span className="flex-1">{t("Re-encoding is paused:") + " "}{data.state.reason}</span>
          <Button size="sm" onClick={resume}>{t("Resume")}</Button>
        </div>
      )}
      <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3 lg:grid-cols-7">
        <Stat label={t("Space saved")} value={bytes(data.spaceSaved)} />
        <Stat label={t("Space added")} value={bytes(data.spaceAdded)} />
        <Stat label={t("Net space saved")} value={signedBytes(data.netSpaceSaved)} />
        <Stat label={t("Processed")} value={String(data.processed)} />
        <Stat
          label={t("Waiting")}
          value={data.pending > 0 ? `${data.pending} ch · ${data.pendingPages.toLocaleString()} p` : "0"}
          hint={data.failed > 0 ? `${data.failed} gave up` : undefined}
        />
        <Stat label={t("Speed (last day)")} value={data.pagesPerMinute > 0 ? throughput(data.pagesPerMinute,60) : "—"} />
        <Stat label={t("Backlog done in")} value={data.etaSeconds > 0 ? `~${eta(data.etaSeconds)}` : "—"} />
      </div>

      {data.active.length > 0 && (
        <div className="mt-4 flex flex-col gap-2">
          {data.active.map((j) => {
            const live = liveMap.get(j.id) ?? j.live;
            return (
              <div key={j.id} className="rounded-md border border-border p-2 text-sm">
                <div className="mb-1 flex flex-wrap justify-between gap-2">
                  <Link to={`/series/${j.seriesId}`} className="font-medium hover:text-accent-2">
                    {j.seriesTitle}{" " + t("· ch.") + " "}{j.chapter}
                  </Link>
                  <span className="text-xs text-muted">{live ? describe(live) : j.status}</span>
                </div>
                <Progress value={live && live.total > 0 ? (live.done / live.total) * 100 : 0} />
              </div>
            );
          })}
        </div>
      )}

      <SavedChart />

      {data.recent.length > 0 && (
        <div className="mt-4">
          <div className="hidden md:block">
          <Table>
            <thead>
              <tr>
                <Th>{t("Recently processed")}</Th>
                <Th>{t("Size")}</Th>
                <Th>{t("Pages")}</Th>
                <Th>{t("Time")}</Th>
                <Th>{t("When")}</Th>
              </tr>
            </thead>
            <tbody>
              {data.recent.map((f, i) => (
                <tr key={i}>
                  <Td>
                    <Link to={`/series/${f.seriesId}`} className="hover:text-accent-2">
                      {f.seriesTitle}{" " + t("· ch.") + " "}{f.chapter}
                    </Link>
                  </Td>
                  <Td className="whitespace-nowrap">
                    {bytes(f.sizeOriginal)} → {bytes(f.size)}
                    <SizeDelta before={f.sizeOriginal} after={f.size} />
                  </Td>
                  <Td>{f.pages}</Td>
                  <Td className="whitespace-nowrap">
                    {eta(f.seconds)}
                    <span className="ml-1 text-xs text-muted">{throughput(f.pages,f.seconds)}</span>
                  </Td>
                  <Td className="whitespace-nowrap text-muted">{relative(f.processedAt)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
          </div>
          <div className="space-y-3 md:hidden">
            <h3 className="text-sm font-medium">{t("Recently processed")}</h3>
            {data.recent.map((f,i)=><article key={i} className="rounded border border-border p-3 text-sm">
              <Link to={`/series/${f.seriesId}`} className="font-medium hover:text-accent-2">{f.seriesTitle} {t("· ch.")} {f.chapter}</Link>
              <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
                <dt className="text-muted">{t("Size")}</dt><dd>{bytes(f.sizeOriginal)} → {bytes(f.size)}<SizeDelta before={f.sizeOriginal} after={f.size}/></dd>
                <dt className="text-muted">{t("Pages")}</dt><dd>{f.pages}</dd>
                <dt className="text-muted">{t("Time")}</dt><dd>{eta(f.seconds)} · {throughput(f.pages,f.seconds)}</dd>
                <dt className="text-muted">{t("When")}</dt><dd>{relative(f.processedAt)}</dd>
              </dl>
            </article>)}
          </div>
        </div>
      )}
      <p className="mt-3 text-xs text-muted">{t("Encoders:") + " "}{data.engines.map((e) => `${e.name} (${e.format}${e.slow ? ", slow" : ""})`).join(", ") || tr("none")}
      </p>
    </Card>
  );
}

/** CheckItems lists what a health check is about, linked (first 10, then "show all"). */
function CheckItems({ items }: { items: NonNullable<S["HealthCheck"]["items"]> }) {
  const [all, setAll] = useState(false);
  const shown = all ? items : items.slice(0, 10);
  return (
    <div className="mt-1 flex flex-wrap gap-1.5">
      {shown.map((it, i) =>
        it.link ? (
          <Link key={i} to={it.link} title={it.detail} className="rounded border border-border bg-panel-2 px-1.5 py-0.5 text-xs hover:border-accent hover:text-accent-2">
            {it.label}
          </Link>
        ) : (
          <span key={i} title={it.detail} className="rounded border border-border bg-panel-2 px-1.5 py-0.5 text-xs">
            {it.label}
          </span>
        ),
      )}
      {items.length > shown.length && (
        <button type="button" className="text-xs text-accent-2 hover:underline" onClick={() => setAll(true)}>
          +{items.length - shown.length}{" " + t("more")}</button>
      )}
    </div>
  );
}

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <div className="text-muted">{label}</div>
      <div className="font-medium">{value}</div>
      {hint && <div className="text-xs text-warn">{hint}</div>}
    </div>
  );
}

/** SavedChart shows MB saved (bars) and pages processed per day for 30 days. */
function SavedChart() {
  const { data } = useQuery({ queryKey: ["processing", "history"], queryFn: () => unwrap(api.GET("/api/v1/processing/history", { params: { query: { days: 30 } } })) });
  if (!data || !data.some((d) => d.files > 0)) return null;
  const saved = data.map((d) => d.bytesBefore - d.bytesAfter);
  const maxSaved = Math.max(...saved.map(Math.abs), 1);
  const total = saved.reduce((a, b) => a + b, 0);
  const pages = data.reduce((a, d) => a + d.pages, 0);
  const W = 600,
    H = 120,
    gap = 2,
    bw = W / data.length - gap;
  return (
    <div className="mt-4">
      <div className="mb-1 flex flex-wrap justify-between gap-2 text-xs text-muted">
        <span>{t("Net saved per day, last 30 days")}</span>
        <span>
          {t("{size} net saved · {pages} pages",{size:signedBytes(total),pages})}</span>
      </div>
      <svg viewBox={`0 0 ${W} ${H + 14}`} className="h-auto w-full" role="img" aria-label={t("Net space saved per day")}>
        <line x1={0} x2={W} y1={H/2} y2={H/2} className="stroke-muted/50"/>
        {data.map((d, i) => {
          const h = (Math.abs(saved[i]) / maxSaved) * (H/2);
          const x = i * (bw + gap);
          return (
            <g key={d.day}>
              <rect x={x} y={saved[i]>=0?H/2-h:H/2} width={bw} height={Math.max(h, d.files > 0 ? 1 : 0)} rx={1.5} className={saved[i]>=0?"fill-ok/80":"fill-warn/80"}>
                <title>{`${d.day}: ${signedBytes(saved[i])} · ${d.files} · ${d.pages} · ${eta(d.seconds)}`}</title>
              </rect>
              {(i === data.length - 1 || (i % 7 === 0 && i < data.length - 4)) && (
                <text
                  x={i === 0 ? x : i === data.length - 1 ? x + bw : x + bw / 2}
                  y={H + 12}
                  textAnchor={i === 0 ? "start" : i === data.length - 1 ? "end" : "middle"}
                  className="fill-muted text-[9px]"
                >
                  {d.day.slice(5)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
    </div>
  );
}

function SizeDelta({before,after}:{before:number;after:number}) {
  const delta=sizeChange(before,after);
  return <span title={delta===null?t("Unknown original size"):undefined} className={`ml-1 text-xs ${delta===null||delta===0?'text-muted':delta>0?'text-warn':'text-ok'}`}>{sizeChangeLabel(before,after)}</span>;
}
