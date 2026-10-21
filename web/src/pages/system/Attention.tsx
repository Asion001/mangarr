import { t } from "../../lib/i18n/core";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import clsx from "clsx";
import { api, unwrap, type S } from "../../api/client";
import { bytes, relative } from "../../lib/format";

type Level = "act" | "check" | "ok";
type Tile = { label: string; value: string; detail: string; to: string; cta: string; level: Level };

const time = (at: string) => new Date(at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

/** tiles turns the attention summary into one tile per area, worst first within the fixed order. */
export function attentionTiles(a: S["Attention"]): Tile[] {
  const fd = a.failedDownloads;
  const bad = a.sources.unhealthy;
  const b = a.backups;
  const p = a.processing;
  const tiles: Tile[] = [
    {
      label: t("Failed downloads"),
      value: String(fd.count),
      detail: fd.count
        ? [fd.sources.slice(0, 2).map((s) => t("{n} from {source}", { n: s.count, source: s.name })).join(", "), t("last 24 hours")].filter(Boolean).join(" · ")
        : t("None in the last 24 hours"),
      to: fd.sources.length === 1 ? `/activity/downloads?status=failed&source=${encodeURIComponent(fd.sources[0].source)}` : "/activity/downloads?status=failed",
      cta: t("Show failed downloads"),
      level: fd.count ? "act" : "ok",
    },
    {
      label: t("Unhealthy sources"),
      value: String(bad.length),
      detail: bad.length
        ? bad
            .slice(0, 2)
            .map((s) =>
              s.cooldownUntil
                ? t("{source} cooling down until {time}", { source: s.name, time: time(s.cooldownUntil) })
                : t("{source} failing {pct}%", { source: s.name, pct: Math.round((s.failures / Math.max(1, s.requests)) * 100) }),
            )
            .join(" · ")
        : t("Every source answers"),
      to: bad.length ? `/sources/reliability?source=${encodeURIComponent(bad[0].source)}` : "/sources/reliability",
      cta: t("Open reliability"),
      level: bad.length ? "act" : "ok",
    },
    {
      label: t("Backups"),
      value: !b.count ? t("None yet") : b.latestVerified ? t("Verified") : t("Not verified"),
      detail: b.lastVerifiedAt ? t("Last verified backup {when}", { when: relative(b.lastVerifiedAt) }) : b.latestAt ? t("Latest made {when}", { when: relative(b.latestAt) }) : t("No backup has been made"),
      to: "/system/backups",
      cta: t("Open backups"),
      level: b.count && b.latestVerified ? "ok" : "check",
    },
    {
      label: t("Workers"),
      value: a.workers.stale.length ? t("{n} stopped", { n: a.workers.stale.length }) : String(a.workers.enabled),
      detail: a.workers.stale.length
        ? a.workers.stale.slice(0, 2).map((w) => t("{name} last checked in {when}", { name: w.name, when: w.lastSeenAt ? relative(w.lastSeenAt) : t("never") })).join(" · ")
        : a.workers.enabled ? t("All checking in") : t("No workers; this server does the work"),
      to: "/system/workers",
      cta: t("Open workers"),
      level: a.workers.stale.length ? "check" : "ok",
    },
    {
      label: t("Processing"),
      value: t("{n} failed", { n: p.failed }),
      detail: t("{running} running, {waiting} waiting", { running: p.running, waiting: p.waiting }),
      to: p.failed ? "/activity/processing?status=failed" : "/activity/processing",
      cta: t("Open processing"),
      level: p.failed ? "check" : "ok",
    },
    {
      label: t("Disk space"),
      value: a.disk.folders ? t("{size} free", { size: bytes(a.disk.free) }) : "—",
      detail: a.disk.path ? t("Lowest folder: {path}", { path: a.disk.path }) : t("No root folder yet"),
      to: "/system/storage",
      cta: t("Open storage"),
      level: a.disk.low ? "check" : "ok",
    },
  ];
  return tiles;
}

const levelLabel = (l: Level) => ({ act: t("Action"), check: t("Check"), ok: t("OK") })[l];

/** NeedsAttention is the row of tiles at the top of System → Status. */
export function NeedsAttention() {
  const { data } = useQuery({ queryKey: ["system-attention"], queryFn: () => unwrap(api.GET("/api/v1/system/attention")), refetchInterval: 60_000 });
  if (!data?.failedDownloads) return null; // not loaded (or an older server)
  const tiles = attentionTiles(data);
  const open = tiles.filter((x) => x.level !== "ok").length;
  return (
    <section aria-labelledby="attention" className="mb-6">
      <h2 id="attention" className="mb-2 text-base font-semibold">
        {t("Needs attention")} <span className="text-sm font-normal text-muted">{open ? t("{n} of {total} areas", { n: open, total: tiles.length }) : t("nothing right now")}</span>
      </h2>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-3">
        {tiles.map((x) => (
          <Link
            key={x.label}
            to={x.to}
            className={clsx(
              "flex flex-col gap-1 rounded-lg border bg-panel p-4 hover:bg-panel-2",
              x.level === "act" ? "border-err/50" : x.level === "check" ? "border-warn/50" : "border-border",
            )}
          >
            <span className="flex items-center justify-between gap-2 text-sm text-muted">
              {x.label}
              <span className={clsx("rounded-full px-2 py-0.5 text-[11px] font-semibold", x.level === "act" ? "bg-err/15 text-err" : x.level === "check" ? "bg-warn/15 text-warn" : "bg-ok/15 text-ok")}>
                {levelLabel(x.level)}
              </span>
            </span>
            <span className="text-2xl font-semibold tabular-nums">{x.value}</span>
            <span className="text-xs text-muted [overflow-wrap:anywhere]">{x.detail}</span>
            <span className="text-sm text-accent-2">{x.cta} →</span>
          </Link>
        ))}
      </div>
    </section>
  );
}
