import { t as tr, t } from "../../lib/i18n/core";
import { Fragment, memo } from "react";
import { ArrowDownToLine, ArrowUpToLine, BookOpen, History, Check, ChevronDown, ChevronRight, ExternalLink, HelpCircle, Pause, Play, RotateCcw, Search, Sparkles, Eye, Trash2 } from "lucide-react";
import { Link } from "react-router";
import clsx from "clsx";
import { type Chapter } from "../../api/client";
import { Badge, Button, Progress, Switch, Td } from "../../components/ui";
import { bytes, date, relative } from "../../lib/format";
import { eta } from "../../lib/liveProgress";
import { SaveChapterButton } from "../../components/OnDevice";

const stateTone: Record<string, "ok" | "warn" | "err" | "info" | "default" | "accent"> = {
  imported: "ok",
  missing: "warn",
  failed: "err",
  queued: "info",
  downloading: "info",
  processing: "accent",
  cleaned: "default",
};

/** readable: downloaded, or a source to stream it from. */
export const readable = (c: Chapter) => !!c.file || c.releases.length > 0;

export const deletableFile = (c: Chapter) => !!c.file && (!c.job || ["completed", "failed"].includes(c.job.status));

export type ChapterRowProps = {
  chapter: Chapter;
  manage: boolean;
  selected: boolean;
  open: boolean;
  accountKind?: string;
  onSelect: (id: number) => void;
  onExpand: (id: number) => void;
  onMonitor: (id: number, monitored: boolean) => void;
  onSearch: (id: number) => void;
  onRestore: (chapter: Chapter) => void;
  onDelete?: (id: number) => void;
  onMark: (chapter: Chapter, read: boolean, scope?: "chapter" | "previous") => unknown;
  onQueueAction: (jobID: number, action: "top" | "bottom" | "pause" | "resume") => void;
  onExplain: (chapter: Chapter) => void;
  /** versions: how many earlier versions of the file the recycle bin holds. */
  versions?: number;
  onVersions?: (chapter: Chapter) => void;
  /** queuePaused: the whole download queue is paused, so queued chapters wait. */
  queuePaused?: boolean;
  /** next: this is the chapter to read next. */
  next?: boolean;
  /** found: the chapter just jumped to, outlined so the eye lands on it. */
  found?: boolean;
};

/** A memoized row keeps queue progress updates from rerendering every chapter. */
export const ChapterRow = memo(function ChapterRow({
  chapter: c,
  manage,
  selected,
  open,
  accountKind,
  onSelect,
  onExpand,
  onMonitor,
  onSearch,
  onRestore,
  onDelete,
  onMark,
  onQueueAction,
  onExplain,
  versions = 0,
  onVersions,
  queuePaused,
  next,
  found,
}: ChapterRowProps) {
  // read: everyone who opened it finished it (for a user account, that's you)
  const read = c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  return (
    <Fragment>
      <tr data-chapter-id={c.id} className={clsx(!c.monitored && "opacity-60", next && "bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent)]", found && "shadow-[inset_0_0_0_2px_var(--color-info)]")}>
        <Td>
          {manage && <input type="checkbox" checked={selected} onChange={() => onSelect(c.id)} />}
        </Td>
        <Td>
          {manage && <Switch checked={c.monitored} onChange={(monitored) => onMonitor(c.id, monitored)} />}
        </Td>
        <Td className={clsx("font-mono text-xs", read && "text-muted")}>
          {c.volume && <span className="text-muted">v{c.volume} </span>}
          {c.number}
        </Td>
        <Td>
          <div className="flex items-center gap-1">
            <button className="flex items-center gap-1 text-left hover:text-accent-2" onClick={() => onExpand(c.id)} aria-expanded={open}>
              {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
              <span className={clsx("line-clamp-1", c.title && "min-w-40", read && "text-muted")}>{c.title || <span className="text-muted">—</span>}</span>
            </button>
            {next && <Badge tone="accent">{t("Up next")}</Badge>}
            {read && (
              <span className="inline-flex items-center gap-0.5 text-xs text-ok">
                <Check className="size-3" />
                {t("read")}
              </span>
            )}
            {c.releases.length > 1 && <Badge title={t("Expand to compare releases")}>{t("{count} releases", { count: c.releases.length })}</Badge>}
          </div>
        </Td>
        <Td className="whitespace-nowrap text-muted">{date(c.releaseDate)}</Td>
        <Td>
          <div className="flex flex-col gap-1">
            {queuePaused && c.job?.status === "queued" ? (
              <Badge tone="warn" title={t("Queue paused")}>{c.state} · {t("paused")}</Badge>
            ) : (
              <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
            )}
            {c.job && ["downloading", "processing", "importing"].includes(c.job.status) && (
              <div className="w-20">
                <Progress value={c.job.progress} />
              </div>
            )}
            {manage && c.job && c.job.priority !== 0 && !["completed", "failed"].includes(c.job.status) && (
              <span className="text-[11px] text-muted">{t("Priority")}: {c.job.priority}</span>
            )}
          </div>
        </Td>
        <Td className="text-xs">
          {c.file ? (
            <div className="flex flex-col">
              <span>
                {bytes(c.file.size)} · {c.file.pageCount}{t("p ·") + " "}{c.file.avgWidth}px
              </span>
              <span className="flex items-center gap-1 text-muted">
                {c.file.scanlator || c.file.sourceName}
                {c.file.upscaled && (
                  <Badge tone="accent" title={c.file.upscaleModel}>
                    <Sparkles className="size-3" />{" " + t("upscaled")}</Badge>
                )}
                {(c.file.format === "avif" || c.file.format === "jxl") && (
                  <Badge
                    tone="info"
                    title={[
                      c.file.sizeOriginal > c.file.size ? `was ${bytes(c.file.sizeOriginal)}` : "",
                      c.file.processSeconds ? `processed in ${eta(c.file.processSeconds)} (${((c.file.processPages ?? 0) / c.file.processSeconds).toFixed(1)} pages/s)` : "",
                    ]
                      .filter(Boolean)
                      .join(", ") || undefined}
                  >
                    {c.file.format}
                    {c.file.sizeOriginal > c.file.size && ` −${Math.round(100 - (100 * c.file.size) / c.file.sizeOriginal)}%`}
                  </Badge>
                )}
                {c.file.processState === "failed" && (
                  <Badge tone="err" title={c.file.processError}>{t("processing failed")}</Badge>
                )}
              </span>
              {versions > 0 && onVersions && (
                <button
                  type="button"
                  onClick={() => onVersions(c)}
                  className="mt-1 inline-flex w-fit items-center gap-1 rounded-full border border-border bg-panel-2 px-2 py-0.5 text-xs font-medium hover:border-accent"
                >
                  <History className="size-3" />
                  {versions === 1 ? t("1 earlier version") : t("{count} earlier versions", { count: versions })}
                </button>
              )}
            </div>
          ) : versions > 0 && onVersions ? (
            <button type="button" onClick={() => onVersions(c)} className="inline-flex items-center gap-1 rounded-full border border-border bg-panel-2 px-2 py-0.5 text-xs font-medium hover:border-accent">
              <History className="size-3" />
              {versions === 1 ? t("1 earlier version") : t("{count} earlier versions", { count: versions })}
            </button>
          ) : (
            <span className="text-muted">—</span>
          )}
        </Td>
        <Td className="text-right">
          <div className="flex items-center justify-end gap-1">
            {readable(c) && <SaveChapterButton chapterId={c.id} />}
            {readable(c) && (
              <Link to={`/read/${c.id}`} title={c.file ? tr("Read") : tr("Read (streamed from the source)")}>
                <Button className="w-28" variant={next ? "primary" : read ? "ghost" : "secondary"} icon={<BookOpen className="size-4" />}>
                  {next ? t("Continue") : read ? t("Re-read") : t("Read")}
                </Button>
              </Link>
            )}
          </div>
        </Td>
      </tr>
      {open && (
        <tr>
          <Td colSpan={8} className="bg-bg/60">
            {manage && (
              <ChapterManagementActions
                chapter={c}
                onMonitor={onMonitor}
                onQueueAction={onQueueAction}
                onRestore={onRestore}
                onSearch={onSearch}
                onDelete={onDelete}
                onExplain={onExplain}
              />
            )}
            <ChapterExpandedInfo chapter={c} accountKind={accountKind} onMark={onMark} />
          </Td>
        </tr>
      )}
    </Fragment>
  );
});

/** Phone and tablet rows keep the reading action visible without a wide table. */
export const ChapterMobileRow = memo(function ChapterMobileRow({
  chapter: c,
  manage,
  selected,
  open,
  accountKind,
  onSelect,
  onExpand,
  onMonitor,
  onSearch,
  onRestore,
  onDelete,
  onMark,
  onQueueAction,
  onExplain,
  queuePaused,
  next,
  found,
}: ChapterRowProps) {
  const read = c.readBy.length > 0 && c.readBy.every((r) => r.completed);
  return (
    <article data-chapter-id={c.id} className={clsx("px-4 py-3", !c.monitored && "opacity-60", next && "bg-accent/5 shadow-[inset_3px_0_0_var(--color-accent)]", found && "shadow-[inset_0_0_0_2px_var(--color-info)]")}>
      <div className="flex min-w-0 items-center gap-2">
        {manage && <input className="shrink-0" type="checkbox" checked={selected} onChange={() => onSelect(c.id)} aria-label={`${t("Select")} ${t("Chapter")} ${c.number}`} />}
        <button className="flex min-w-0 flex-1 items-center gap-2 text-left hover:text-accent-2" onClick={() => onExpand(c.id)} aria-expanded={open}>
          {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
          <span className={clsx("shrink-0 font-mono text-xs", read && "text-muted")}>{c.volume ? `v${c.volume} ` : ""}{c.number}</span>
          <span className={clsx("min-w-0 flex-1 truncate text-sm", read && "text-muted")}>{c.title || t("Chapter") + " " + c.number}</span>
        </button>
        {readable(c) && <SaveChapterButton chapterId={c.id} />}
        {readable(c) && (
          <Link className="shrink-0" to={`/read/${c.id}`} title={c.file ? tr("Read") : tr("Read (streamed from the source)")}>
            <Button size="sm" variant={next ? "primary" : read ? "ghost" : "secondary"} icon={<BookOpen className="size-4" />}>
              {next ? t("Continue") : read ? t("Re-read") : t("Read")}
            </Button>
          </Link>
        )}
      </div>
      {(manage || next || read) && <div className={clsx("mt-2 flex flex-wrap items-center gap-1.5 text-xs", manage && "pl-6")}>
        {next && <Badge tone="accent">{t("Up next")}</Badge>}
        {read && <Badge tone="ok"><Check className="size-3" /> {t("read")}</Badge>}
        {manage && (queuePaused && c.job?.status === "queued" ? (
          <Badge tone="warn" title={t("Queue paused")}>{c.state} · {t("paused")}</Badge>
        ) : (
          <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
        ))}
        {manage && c.file && <span className="text-muted">{c.file.pageCount}{t("p ·") + " "}{bytes(c.file.size)}</span>}
        {manage && c.releases.length > 1 && <Badge>{t("{count} releases", { count: c.releases.length })}</Badge>}
      </div>}
      {manage && c.job && ["downloading", "processing", "importing"].includes(c.job.status) && (
        <div className={clsx("mt-2 max-w-48", manage && "ml-6")}><Progress value={c.job.progress} /></div>
      )}
      {open && (
        <div className="mt-3 border-t border-border bg-bg/50 px-1 pt-3">
          {manage && (
            <ChapterManagementActions
              chapter={c}
              showMonitor
              onMonitor={onMonitor}
              onQueueAction={onQueueAction}
              onRestore={onRestore}
              onSearch={onSearch}
              onDelete={onDelete}
              onExplain={onExplain}
            />
          )}
          {!manage && (
            <div className="mb-3 flex flex-wrap items-center gap-2 text-xs">
              <Badge tone={stateTone[c.state] ?? "default"}>{c.state}</Badge>
              {c.file && <span className="text-muted">{c.file.pageCount}{t("p ·") + " "}{bytes(c.file.size)}</span>}
              {c.releases.length > 1 && <Badge>{t("{count} releases", { count: c.releases.length })}</Badge>}
              {c.job && ["downloading", "processing", "importing"].includes(c.job.status) && <div className="w-full max-w-48"><Progress value={c.job.progress} /></div>}
            </div>
          )}
          {c.file && (
            <div className="mb-3 text-xs text-muted">
              {c.file.scanlator || c.file.sourceName} · {c.file.avgWidth}px
              {c.file.upscaled && <> · {t("upscaled")}</>}
              {(c.file.format === "avif" || c.file.format === "jxl") && <> · {c.file.format}</>}
              {c.file.processState === "failed" && <p className="mt-1 text-err">{c.file.processError}</p>}
            </div>
          )}
          <ChapterExpandedInfo chapter={c} accountKind={accountKind} onMark={onMark} />
        </div>
      )}
    </article>
  );
});

function ChapterManagementActions({
  chapter: c,
  showMonitor = false,
  onMonitor,
  onQueueAction,
  onRestore,
  onSearch,
  onDelete,
  onExplain,
}: {
  chapter: Chapter;
  showMonitor?: boolean;
  onMonitor: (id: number, monitored: boolean) => void;
  onQueueAction: (jobID: number, action: "top" | "bottom" | "pause" | "resume") => void;
  onRestore: (chapter: Chapter) => void;
  onSearch: (id: number) => void;
  onDelete?: (id: number) => void;
  onExplain: (chapter: Chapter) => void;
}) {
  return (
    <div className="mb-3 flex flex-wrap items-center gap-2">
      {showMonitor && <span className="flex items-center gap-2 text-xs text-muted"><Switch checked={c.monitored} onChange={(monitored) => onMonitor(c.id, monitored)} /> {t("Monitored")}</span>}
      {c.job && !["completed", "failed"].includes(c.job.status) && (
        <>
          <Button size="sm" icon={<ArrowUpToLine className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "top")}>{t("Top")}</Button>
          <Button size="sm" icon={<ArrowDownToLine className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "bottom")}>{t("Bottom")}</Button>
          {c.job.status === "paused" ? (
            <Button size="sm" icon={<Play className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "resume")}>{t("Resume")}</Button>
          ) : (
            <Button size="sm" icon={<Pause className="size-3.5" />} onClick={() => onQueueAction(c.job!.id, "pause")}>{t("Pause")}</Button>
          )}
        </>
      )}
      {c.state === "cleaned" ? (
        <Button size="sm" icon={<RotateCcw className="size-3.5" />} onClick={() => onRestore(c)}>{t("Restore")}</Button>
      ) : (
        <Button size="sm" icon={<Search className="size-3.5" />} onClick={() => onSearch(c.id)}>{t("Search")}</Button>
      )}
      {onDelete && deletableFile(c) && <Button size="sm" variant="danger" icon={<Trash2 className="size-3.5" />} onClick={() => onDelete(c.id)}>{t("Delete chapter file")}</Button>}
      <Button size="sm" icon={<HelpCircle className="size-3.5" />} onClick={() => onExplain(c)}>{t("Why (not) downloaded?")}</Button>
    </div>
  );
}

function ChapterExpandedInfo({ chapter: c, accountKind, onMark }: { chapter: Chapter; accountKind?: string; onMark: (chapter: Chapter, read: boolean, scope?: "chapter" | "previous") => unknown }) {
  return <>
    {c.releases.length === 0 ? (
      <p className="text-xs text-muted">{t("No releases.")}</p>
    ) : (
      <div className="flex flex-col gap-1">
        {c.releases.map((r) => (
          <div key={r.id} className="flex flex-wrap items-center gap-2 text-xs">
            <Badge>{r.sourceName}</Badge>
            <span className={r.removed ? "line-through text-muted" : ""}>{r.name}</span>
            {r.scanlator && <span className="text-muted">{t("by") + " "}{r.scanlator}</span>}
            <span className="text-muted">{date(r.uploadDate)}</span>
            {r.blocklisted && <Badge tone="err">{t("blocklisted")}</Badge>}
            {c.file?.releaseId === r.id && <Badge tone="ok">{t("current file")}</Badge>}
            {r.webUrl && (
              <a href={r.webUrl} target="_blank" rel="noreferrer" className="text-muted hover:text-accent-2">
                <ExternalLink className="size-3" />
              </a>
            )}
          </div>
        ))}
      </div>
    )}
    <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border pt-3">
      <span className="text-xs font-medium text-muted">{t("Read by")}</span>
      {c.readBy.map((r) => (
        <Badge key={r.userId} tone={r.completed ? "ok" : "info"} title={r.completed ? `read ${relative(r.readAt)}` : `page ${r.page}`}>
          <Eye className="size-3" /> {accountKind === "user" ? (r.completed ? t("Read") : `${t("Page")} ${r.page}`) : r.name}
        </Badge>
      ))}
      {c.readBy.length === 0 && <span className="text-xs text-muted">{t("Nobody yet")}</span>}
      <span className="ml-auto flex flex-wrap gap-1">
        <Button size="sm" onClick={() => onMark(c, true)}>{t("Mark read")}</Button>
        <Button size="sm" onClick={() => onMark(c, false)}>{t("Mark unread")}</Button>
        <Button size="sm" onClick={() => onMark(c, true, "previous")}>{t("Mark previous chapters read")}</Button>
        <Button size="sm" onClick={() => onMark(c, false, "previous")}>{t("Mark previous chapters unread")}</Button>
      </span>
    </div>
    {c.job?.error && <p className="mt-2 text-xs text-err">{t("Last error:") + " "}{c.job.error}</p>}
  </>;
}
