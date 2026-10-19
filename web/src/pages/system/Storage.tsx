import { t } from "../../lib/i18n/core";
import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import clsx from "clsx";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Card, ErrorBox, Loading, PageHeader, Table, Td, Th } from "../../components/ui";
import { bytes } from "../../lib/format";

type Summary = S["StorageSummary"];

// a drive with less than this share free gets a warning
const lowFreeShare = 0.1;

const statusLabel = (s: string) =>
  ({ ongoing: t("Ongoing"), completed: t("Completed"), hiatus: t("Hiatus"), cancelled: t("Cancelled"), unknown: t("Unknown") })[s] ?? s;
const statusColor: Record<string, string> = { ongoing: "bg-ok", completed: "bg-info", hiatus: "bg-warn", cancelled: "bg-err" };

const pct = (part: number, whole: number) => (whole > 0 ? Math.min(100, (part / whole) * 100) : 0);
const count = (n: number) => n.toLocaleString();

export function StoragePage() {
  const { data, isLoading, error } = useQuery({ queryKey: ["system-storage"], queryFn: () => unwrap(api.GET("/api/v1/system/storage")) });
  return (
    <>
      <PageHeader title={t("Storage")} subtitle={t("How much space the library takes, where it lives, and what takes the most.")} />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <div className="flex flex-col gap-6">
          <LibraryCard data={data} />
          <RootsCard data={data} />
          <div className="grid gap-6 lg:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]">
            <LargestCard data={data} />
            <StatusCard data={data} />
          </div>
        </div>
      )}
    </>
  );
}

function Stat({ label, value, hint, good }: { label: string; value: string; hint?: ReactNode; good?: boolean }) {
  return (
    <div className="min-w-0">
      <div className="text-sm text-muted">{label}</div>
      <div className="text-xl font-semibold tabular-nums">{value}</div>
      {hint && <div className={clsx("text-xs", good ? "text-ok" : "text-muted")}>{hint}</div>}
    </div>
  );
}

function LibraryCard({ data }: { data: Summary }) {
  const lib = data.library;
  const downloaded = lib.bytes + lib.saved;
  return (
    <Card title={t("Library")}>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-5">
        <Stat label={t("On disk")} value={bytes(lib.bytes)} hint={t("{series} series · {files} chapters", { series: count(lib.series), files: count(lib.files) })} />
        <Stat label={t("Average chapter")} value={lib.files > 0 ? bytes(lib.bytes / lib.files) : "—"}
          hint={lib.files > 0 ? t("{pages} pages on average", { pages: Math.round(lib.pages / lib.files) }) : undefined} />
        <Stat label={t("Saved by processing")} value={bytes(lib.saved)} good={lib.saved > 0}
          hint={lib.saved > 0 ? t("{percent}% smaller than downloaded", { percent: Math.round(pct(lib.saved, downloaded)) }) : undefined} />
        <Stat label={t("Recycle bin")} value={bytes(data.recycleBytes)}
          hint={<Link to="/system/recycle-bin" className="text-accent-2 hover:underline">{data.recycleDays > 0
            ? t("{count} files, emptied after {days} days", { count: count(data.recycleFiles), days: data.recycleDays })
            : t("{count} files, kept until emptied", { count: count(data.recycleFiles) })}</Link>} />
        <Stat label={t("Image cache")} value={bytes(data.imageBytes)}
          hint={data.imageMaxBytes > 0 ? t("of {size} limit", { size: bytes(data.imageMaxBytes) }) : t("no limit")} />
      </div>
    </Card>
  );
}

function Bar({ parts, className }: { parts: { share: number; className: string }[]; className?: string }) {
  return (
    <div className={clsx("flex overflow-hidden rounded-full bg-panel-2", className ?? "h-2")}>
      {parts.map((p, i) => <span key={i} className={clsx("block h-full", p.className)} style={{ width: `${p.share}%` }} />)}
    </div>
  );
}

function Swatch({ className }: { className: string }) {
  return <i aria-hidden className={clsx("inline-block size-2.5 rounded-sm align-[-1px]", className)} />;
}

function RootsCard({ data }: { data: Summary }) {
  if (data.roots.length === 0) {
    return <Card title={t("Root folders")}><p className="text-sm text-muted">{t("No root folders yet. Add one in Settings › Media.")}</p></Card>;
  }
  const firstOnDrive = new Map<string, string>();
  return (
    <Card title={t("Root folders")}>
      <div className="mb-3 flex flex-wrap gap-4 text-xs text-muted">
        <span><Swatch className="bg-accent" /> {t("Library")}</span>
        <span><Swatch className="bg-muted/50" /> {t("Other files on the drive")}</span>
        <span><Swatch className="border border-border bg-panel-2" /> {t("Free")}</span>
      </div>
      <Table>
        <thead>
          <tr>
            <Th>{t("Folder")}</Th>
            <Th>{t("Language")}</Th>
            <Th className="text-right">{t("Series")}</Th>
            <Th className="text-right">{t("Chapters")}</Th>
            <Th className="text-right">{t("Library size")}</Th>
            <Th>{t("Drive")}</Th>
          </tr>
        </thead>
        <tbody>
          {data.roots.map((r) => {
            const shared = r.drive ? firstOnDrive.get(r.drive) : undefined;
            if (r.drive && !shared) firstOnDrive.set(r.drive, r.path);
            const used = r.driveTotal - r.driveFree;
            return (
              <tr key={r.id}>
                <Td className="font-mono text-xs">{r.path}</Td>
                <Td>{r.language || "—"}</Td>
                <Td className="text-right tabular-nums">{count(r.series)}</Td>
                <Td className="text-right tabular-nums">{count(r.files)}</Td>
                <Td className="text-right tabular-nums">{bytes(r.bytes)}</Td>
                <Td>
                  {r.error ? <Badge tone="err">{r.error}</Badge> : (
                    <div className="flex min-w-44 flex-col gap-1">
                      <Bar parts={[{ share: pct(r.bytes, r.driveTotal), className: "bg-accent" }, { share: pct(Math.max(0, used - r.bytes), r.driveTotal), className: "bg-muted/50" }]} />
                      <span className="text-xs text-muted">
                        {t("{free} free of {total}", { free: bytes(r.driveFree), total: bytes(r.driveTotal) })}
                        {shared && <span title={shared}>{" · " + t("same drive as {path}", { path: shared.split("/").filter(Boolean).pop() ?? shared })}</span>}
                        {r.driveFree < r.driveTotal * lowFreeShare && <> <Badge tone="warn">{t("low space")}</Badge></>}
                      </span>
                    </div>
                  )}
                </Td>
              </tr>
            );
          })}
        </tbody>
      </Table>
    </Card>
  );
}

function LargestCard({ data }: { data: Summary }) {
  const top = data.largest;
  const max = top[0]?.bytes ?? 0;
  const sum = top.reduce((n, s) => n + s.bytes, 0);
  return (
    <Card title={t("Largest series")} actions={<Link to="/?sort=size" className="text-sm text-accent-2 hover:underline">{t("See all, sorted by size")}</Link>}>
      {top.length === 0 ? <p className="text-sm text-muted">{t("Nothing downloaded yet.")}</p> : (
        <div className="flex flex-col gap-3">
          <ol className="flex flex-col gap-2.5">
            {top.map((s, i) => (
              <li key={s.id} className="grid grid-cols-[1.25rem_minmax(0,1fr)_auto] items-center gap-3">
                <span className="text-right text-xs tabular-nums text-muted">{i + 1}</span>
                <div className="flex min-w-0 flex-col gap-1">
                  <Link to={`/series/${s.id}`} className="truncate hover:underline">{s.title}</Link>
                  <Bar className="h-1.5" parts={[{ share: pct(s.bytes, max), className: "bg-accent" }]} />
                </div>
                <div className="text-right tabular-nums">
                  {bytes(s.bytes)}
                  <div className="text-xs text-muted">{t("{count} ch.", { count: count(s.files) })}</div>
                </div>
              </li>
            ))}
          </ol>
          <p className="text-sm text-muted">
            {t("The top {count} take {size}, {percent}% of the library.", { count: top.length, size: bytes(sum), percent: Math.round(pct(sum, data.library.bytes)) })}
          </p>
        </div>
      )}
    </Card>
  );
}

function StatusCard({ data }: { data: Summary }) {
  const total = data.library.bytes;
  return (
    <Card title={t("By status")}>
      <div className="flex flex-col gap-3">
        <Bar className="h-3" parts={data.byStatus.map((s) => ({ share: pct(s.bytes, total), className: statusColor[s.status] ?? "bg-muted/50" }))} />
        <dl className="flex flex-col gap-2 text-sm">
          {data.byStatus.map((s) => (
            <div key={s.status} className="flex justify-between gap-3">
              <dt className="text-muted"><Swatch className={statusColor[s.status] ?? "bg-muted/50"} /> {statusLabel(s.status)}</dt>
              <dd className="tabular-nums">{t("{size} · {count} series", { size: bytes(s.bytes), count: count(s.series) })}</dd>
            </div>
          ))}
        </dl>
        <h3 className="mt-2 text-sm font-semibold">{t("Already read")}</h3>
        <div className="flex justify-between gap-3 text-sm">
          <span className="text-muted">{t("Chapters read")}</span>
          <span className="tabular-nums">{t("{count} · {size}", { count: count(data.readFiles), size: bytes(data.readBytes) })}</span>
        </div>
        <p className="text-sm text-muted">
          <Link to="/cleanup" className="text-accent-2 hover:underline">{t("Cleanup rules")}</Link>{" " + t("can remove read chapters for you.")}
        </p>
      </div>
    </Card>
  );
}
