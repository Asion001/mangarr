import { t } from "../../lib/i18n/core";
import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { BookOpen, RotateCcw, Sparkles, Trash2, X } from "lucide-react";
import { Link } from "react-router";
import clsx from "clsx";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { useProfiles } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, Confirm, ErrorBox, IconButton, Input, Modal, Select, Spinner, Switch } from "../../components/ui";
import { bytes, date, dateTime } from "../../lib/format";
import { chapterText, daysLeft, processingText, reasonLabel, reasonTone, sourceText, useRecycleActions, type ChapterFile, type Recycled } from "./recycle";

type Config = S["ProfileConfig"];
type StartFrom = "recycled" | "current" | "download";

const coverOf = (seriesId?: number) => (seriesId ? apiUrl(`/api/v1/series/${seriesId}/cover`) : undefined);

function Fact({ label, children, wide }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <div className={clsx("min-w-0", wide && "col-span-2")}>
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-0.5 text-sm break-words">{children}</div>
    </div>
  );
}

function VersionCard({ title, note, file, pages, size, highlight, cover, readTo, when, whenLabel }: {
  title: string;
  note: string;
  file?: ChapterFile | null;
  pages: number;
  size: number;
  highlight?: boolean;
  cover?: string;
  readTo?: string;
  when?: string | null;
  whenLabel: string;
}) {
  return (
    <section className={clsx("flex flex-col rounded-lg border bg-bg", highlight ? "border-primary" : "border-border")}>
      <header className="flex items-center justify-between border-b border-border px-3.5 py-2.5">
        <span className="text-sm font-semibold">{title}</span>
        <span className="text-xs text-muted">{note}</span>
      </header>
      <div className="flex gap-3 p-3.5">
        <Cover src={cover} alt="" className="aspect-[2/3] w-20 shrink-0" />
        <dl className="grid min-w-0 flex-1 grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-sm">
          <dt className="text-muted">{t("Pages")}</dt>
          <dd>{pages}</dd>
          <dt className="text-muted">{t("Size")}</dt>
          <dd>{bytes(size)}</dd>
          <dt className="text-muted">{t("Processing")}</dt>
          <dd>{processingText(file)}</dd>
          <dt className="text-muted">{t("Source")}</dt>
          <dd className="break-words">{sourceText(file)}</dd>
          <dt className="text-muted">{whenLabel}</dt>
          <dd>{date(when)}</dd>
        </dl>
      </div>
      {readTo && (
        <div className="mt-auto px-3.5 pb-3.5">
          <Link to={readTo} className="flex h-9 items-center justify-center gap-1.5 rounded-md border border-border bg-panel-2 text-sm font-medium hover:bg-border">
            <BookOpen className="size-4" />
            {t("Open in reader")}
          </Link>
        </div>
      )}
    </section>
  );
}

/** RecycledDrawer shows one recycled file next to what is in the library now. */
export function RecycledDrawer({ item, housekeepingPaused, onClose, onReprocess }: { item: Recycled; housekeepingPaused?: boolean; onClose: () => void; onReprocess: (item: Recycled) => void }) {
  const titleId = useId();
  const { restore, remove } = useRecycleActions();
  const [confirmDelete, setConfirmDelete] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const cur = item.current;
  const left = daysLeft(item.purgeAt);
  const isFile = item.kind === "file";
  const replacedBy = [item.jobKind === "reprocess" ? t("Reprocess job") : item.jobKind === "download" ? t("Download job") : "", item.profileName].filter(Boolean).join(" · ");
  return (
    <div className="fixed inset-0 z-50 bg-black/55" onMouseDown={onClose}>
      <aside
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(e) => e.stopPropagation()}
        className="absolute inset-y-0 right-0 flex w-full max-w-3xl flex-col border-l border-border bg-panel shadow-2xl"
      >
        <header className="flex items-start gap-3.5 border-b border-border px-5 py-4">
          <Cover src={coverOf(item.seriesId)} alt="" className="aspect-[2/3] w-11 shrink-0" />
          <div className="min-w-0 flex-1">
            <div className="text-xs text-muted">{item.seriesTitle || t("Unknown series")}</div>
            <h2 id={titleId} className="mt-0.5 mb-1.5 text-lg font-semibold">{chapterText(item)}</h2>
            <Badge tone={reasonTone(item.reason)}>{reasonLabel(item.reason)}</Badge>
          </div>
          <IconButton title={t("Close")} onClick={onClose}>
            <X className="size-4" />
          </IconButton>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <div className="grid grid-cols-2 gap-3.5 border-b border-border px-5 py-4 sm:grid-cols-4">
            <Fact label={t("Recycled")}>{dateTime(item.recycledAt)}</Fact>
            <Fact label={t("Replaced by")}>{replacedBy || "—"}</Fact>
            <Fact label={t("This file")}>{isFile ? t("{pages} pages · {size}", { pages: item.pageCount, size: bytes(item.size) }) : bytes(item.size)}</Fact>
            <Fact label={t("Processing in this file")}>{processingText(item.file)}</Fact>
            <Fact label={t("Source")}>{sourceText(item.file)}</Fact>
            <Fact label={t("Purged in")}>
              {housekeepingPaused ? <span className="text-warn">{t("Paused")}</span> : left === null ? t("Never") : `${t("{days} days", { days: left })} (${date(item.purgeAt)})`}
            </Fact>
            <Fact label={t("Stored at")} wide>
              <span className="font-mono text-xs text-muted">{item.recycledPath}</span>
            </Fact>
          </div>
          {isFile && (
            <>
              <div className="px-5 pt-4 text-sm font-semibold">{t("Compare versions")}</div>
              <div className="grid gap-4 px-5 py-3 sm:grid-cols-2">
                <VersionCard
                  title={t("In recycle bin")}
                  note={t("this version")}
                  highlight
                  file={item.file}
                  pages={item.pageCount}
                  size={item.size}
                  cover={apiUrl(`/api/v1/recycle-bin/${item.id}/pages/1`)}
                  readTo={`/recycle-bin/${item.id}/read`}
                  when={item.file?.importedAt}
                  whenLabel={t("Downloaded")}
                />
                {cur ? (
                  <VersionCard
                    title={t("In library now")}
                    note={t("current")}
                    file={cur}
                    pages={cur.pageCount}
                    size={cur.size}
                    cover={coverOf(item.seriesId)}
                    readTo={item.chapterId ? `/read/${item.chapterId}` : undefined}
                    when={cur.processedAt ?? cur.importedAt}
                    whenLabel={t("Written")}
                  />
                ) : (
                  <section className="flex items-center justify-center rounded-lg border border-dashed border-border p-6 text-sm text-muted">{t("This chapter has no file in the library now.")}</section>
                )}
              </div>
              {item.countChanged && cur && (
                <div className="mx-5 mb-4 rounded-md bg-warn/10 px-3 py-2.5 text-xs text-warn">
                  {t("Page count changed from {from} to {to}. Restoring remaps read progress to the restored pages.", { from: item.pageCount, to: cur.pageCount })}
                </div>
              )}
            </>
          )}
        </div>
        <footer className="flex flex-wrap items-center gap-2.5 border-t border-border px-5 py-3.5">
          <Button variant="ghost" className="text-err" icon={<Trash2 className="size-4" />} onClick={() => setConfirmDelete(true)}>{t("Delete now")}</Button>
          <span className="min-w-40 flex-1 text-xs text-muted">{cur ? t("Restoring puts the library file in the recycle bin, so you can switch back.") : ""}</span>
          {isFile && item.chapterId && (
            <Button icon={<Sparkles className="size-4" />} onClick={() => onReprocess(item)}>{t("Reprocess from this…")}</Button>
          )}
          <Button
            variant="primary"
            icon={<RotateCcw className="size-4" />}
            loading={restore.isPending}
            disabled={isFile && !item.chapterId}
            title={isFile && !item.chapterId ? t("This file isn't matched to a chapter, so it can't be put back.") : undefined} onClick={() => restore.mutate([item.id], { onSuccess: (r) => !r[0]?.error && onClose() })}>
            {t("Restore this version")}
          </Button>
        </footer>
      </aside>
      <Confirm
        open={confirmDelete}
        title={t("Delete this file now?")}
        message={t("It is removed from disk and can't be restored.")}
        confirmLabel={t("Delete now")}
        danger
        loading={remove.isPending}
        onClose={() => setConfirmDelete(false)}
        onConfirm={() => remove.mutate({ ids: [item.id] }, { onSuccess: () => onClose() })}
      />
    </div>
  );
}

/** ReprocessModal queues one job per chapter, starting from the recycled file, the library file or a new download. */
export function ReprocessModal({ items, onClose }: { items: Recycled[]; onClose: () => void }) {
  const { reprocess } = useRecycleActions();
  const { data: profiles } = useProfiles();
  const { data: seriesProfile } = useQuery({
    queryKey: ["series", items[0]?.seriesId],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}", { params: { path: { id: items[0].seriesId! } } })),
    enabled: !!items[0]?.seriesId,
  });
  const [startFrom, setStartFrom] = useState<StartFrom>("recycled");
  const [release, setRelease] = useState<"same" | "best">("same");
  const [profileId, setProfileId] = useState(0);
  const [custom, setCustom] = useState(false);
  const [override, setOverride] = useState<Config | null>(null);
  const pid = profileId || seriesProfile?.profileId || 0;
  const profile = profiles?.find((p) => p.id === pid);
  const base = useMemo(() => (profile ? (JSON.parse(JSON.stringify(profile.config)) as Config) : null), [profile]);
  const cfg = override ?? base;
  const { data: models } = useQuery({
    queryKey: ["upscaler-models"],
    queryFn: () => unwrap(api.GET("/api/v1/upscalers/models")),
    enabled: custom && !!cfg?.upscale.enabled,
    retry: false,
  });
  const set = (fn: (c: Config) => void) => {
    if (!cfg) return;
    const next = JSON.parse(JSON.stringify(cfg)) as Config;
    fn(next);
    setOverride(next);
  };
  const series = [...new Set(items.map((i) => i.seriesTitle))];
  const chapters = items.filter((i) => i.chapter).map((i) => chapterText(i));
  const pages = items.reduce((n, i) => n + i.pageCount, 0);
  const currentPages = items.reduce((n, i) => n + (i.current?.pageCount ?? 0), 0);
  const noCurrent = items.some((i) => !i.current);
  const submit = () =>
    reprocess.mutate(
      { ids: items.map((i) => i.id), startFrom, release: startFrom === "download" ? release : undefined, profileId: pid || undefined, config: custom && cfg ? cfg : undefined },
      { onSuccess: (r) => r.every((x) => !x.error) && onClose() },
    );
  const option = (value: StartFrom, title: string, help: string, extra?: ReactNode, disabled?: boolean) => (
    <label className={clsx("flex gap-3 rounded-lg border p-3", startFrom === value ? "border-primary bg-accent/5" : "border-border", disabled && "opacity-50")}>
      <input type="radio" name="start-from" className="mt-0.5 size-4 accent-accent" checked={startFrom === value} disabled={disabled} onChange={() => setStartFrom(value)} />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{title}</div>
        <div className="text-xs text-muted">{help}</div>
        {extra}
      </div>
    </label>
  );
  return (
    <Modal
      open
      onClose={onClose}
      title={items.length === 1 ? t("Reprocess {chapter}", { chapter: chapterText(items[0]) }) : t("Reprocess {count} chapters", { count: items.length })}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={reprocess.isPending} disabled={!cfg} onClick={submit}>
            {items.length === 1 ? t("Queue job") : t("Queue {count} jobs", { count: items.length })}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <p className="-mt-1 text-sm text-muted">{[series.join(", "), chapters.join(", ")].filter(Boolean).join(" · ")}</p>
        <div className="flex flex-col gap-2">
          <div className="text-sm font-medium">{t("Start from")}</div>
          {option("recycled", t("Recycled files"), t("{pages} pages, as they were before being replaced. Best when a split or upscale went wrong.", { pages }))}
          {option("current", t("Current library files"), t("{pages} pages, with the processing they already have. Processing runs on top of that.", { pages: currentPages }), undefined, noCurrent)}
          {option(
            "download",
            t("Download again"),
            t("Fetch fresh pages from a source, then process them with the settings below."),
            startFrom === "download" && (
              <Select aria-label={t("Download from")} className="mt-2" value={release} onChange={(e) => setRelease(e.target.value as "same" | "best")}>
                <option value="same">{t("Same release as this file")}</option>
                <option value="best">{t("Best available release (profile ranking)")}</option>
              </Select>
            ),
          )}
        </div>

        <div className="flex flex-col gap-2">
          <div className="text-sm font-medium">{t("Settings")}</div>
          <Select aria-label={t("Profile")} value={pid} onChange={(e) => (setProfileId(Number(e.target.value)), setOverride(null))}>
            {profiles?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.id === seriesProfile?.profileId ? t("Series profile: {name}", { name: p.name }) : p.name}
              </option>
            ))}
          </Select>
          <Switch checked={custom} onChange={setCustom} label={t("Change settings for this run only")} />
          {custom && cfg && (
            <div className="grid gap-3 rounded-lg border border-border bg-bg p-3.5 sm:grid-cols-2">
              <label className="flex flex-col gap-1.5 text-sm font-medium">
                {t("Upscaler model")}
                <Select value={cfg.upscale.enabled ? cfg.upscale.model : ""} onChange={(e) => set((c) => (e.target.value ? ((c.upscale.enabled = true), (c.upscale.model = e.target.value)) : (c.upscale.enabled = false)))}>
                  <option value="">{t("No upscaling")}</option>
                  {[...new Set([...(models?.models.map((m) => m.name) ?? []), cfg.upscale.model].filter(Boolean))].map((m) => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </Select>
              </label>
              <label className="flex flex-col gap-1.5 text-sm font-medium">
                {t("Encode as")}
                <Select value={cfg.encode.format === "keep" ? "keep" : `${cfg.encode.format}:${cfg.encode.preset}`} onChange={(e) => set((c) => {
                  const [format, preset] = e.target.value.split(":");
                  c.encode.format = format as Config["encode"]["format"];
                  if (preset) c.encode.preset = preset as Config["encode"]["preset"];
                })}>
                  <option value="keep">{t("Keep the page format")}</option>
                  <option value="avif:fast">{`AVIF · ${t("Fast")}`}</option>
                  <option value="avif:balanced">{`AVIF · ${t("Balanced")}`}</option>
                  <option value="avif:max">{`AVIF · ${t("Smallest")}`}</option>
                  <option value="jxl:balanced">{`JPEG XL · ${t("lossless")}`}</option>
                </Select>
              </label>
              <div className="flex flex-col gap-1.5 text-sm font-medium">
                {t("Split tall pages")}
                <Switch checked={cfg.pages.splitTall} onChange={(v) => set((c) => (c.pages.splitTall = v))} label={<span className="font-normal text-muted">{cfg.pages.splitTall ? t("On") : t("Off")}</span>} />
              </div>
              <label className="flex flex-col gap-1.5 text-sm font-medium">
                {t("Max page width")}
                <Input type="number" min={0} placeholder={t("no limit")} value={cfg.pages.maxWidth || ""} onChange={(e) => set((c) => (c.pages.maxWidth = Number(e.target.value) || 0))} />
              </label>
              <p className="text-xs text-muted sm:col-span-2">{t("Only these chapters use these settings. The profile stays as it is.")}</p>
            </div>
          )}
        </div>
        <p className="rounded-lg bg-panel-2 px-3 py-2.5 text-xs text-muted">{t("Each run replaces the library file and puts the replaced one in the recycle bin.")}</p>
        {reprocess.error && <ErrorBox error={reprocess.error} />}
      </div>
    </Modal>
  );
}

/** VersionsModal lists a chapter's current file and its recycled versions. */
export function VersionsModal({ chapterId, label, onClose, onCompare }: { chapterId: number; label: string; onClose: () => void; onCompare: (item: Recycled) => void }) {
  const { restore } = useRecycleActions();
  const { data, isLoading, error } = useQuery({
    queryKey: ["recycle-bin", "versions", chapterId],
    queryFn: () => unwrap(api.GET("/api/v1/chapters/{id}/versions", { params: { path: { id: chapterId } } })),
  });
  const line = (f: { pageCount: number; size: number }, file?: ChapterFile | null) =>
    [t("{count} pages", { count: f.pageCount }), processingText(file), sourceText(file), bytes(f.size)].filter((x) => x && x !== "—").join(" · ");
  return (
    <Modal open onClose={onClose} title={t("Versions of {chapter}", { chapter: label })} size="md">
      {isLoading && <Spinner />}
      {error && <ErrorBox error={error} />}
      {data && (
        <div className="flex flex-col gap-1">
          {data.current && (
            <div className="flex items-center gap-3 rounded-md bg-panel-2 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{t("In library now")}</div>
                <div className="text-xs text-muted">{date(data.current.processedAt ?? data.current.importedAt)} · {line(data.current, data.current)}</div>
              </div>
              <span className="text-xs text-muted">{t("Current")}</span>
            </div>
          )}
          {data.recycled.map((r) => (
            <div key={r.id} className="flex flex-wrap items-center gap-2 rounded-md px-3 py-2.5 hover:bg-panel-2/60">
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{reasonLabel(r.reason)}</div>
                <div className="text-xs text-muted">{t("Recycled {when}", { when: date(r.recycledAt) })} · {line(r, r.file)}</div>
              </div>
              <Button size="sm" onClick={() => onCompare(r)}>{t("Compare")}</Button>
              <Button size="sm" variant="primary" loading={restore.isPending && restore.variables?.[0] === r.id} onClick={() => restore.mutate([r.id], { onSuccess: (x) => !x[0]?.error && onClose() })}>{t("Restore")}</Button>
            </div>
          ))}
          <div className="mt-1.5 border-t border-border px-3 pt-2.5 text-xs">
            <Link to="/system/recycle-bin" className="text-accent-2 hover:underline">{t("Open recycle bin")}</Link>
          </div>
        </div>
      )}
    </Modal>
  );
}
