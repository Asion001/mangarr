import { useUIMode } from "../../lib/uiPreferences";
import { useDocumentTitle } from "../../lib/documentTitle";
import { label, t as tr, t } from "../../lib/i18n/core";
import { useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Bell, BellOff, Plus, BookOpen, ChevronDown, ExternalLink, Eye, EyeOff, FilePen, Languages, HardDrive, MoreHorizontal, Pencil, RefreshCw, Search, Sparkles, Trash2, FileSearch, BookText, Link2, Unlink } from "lucide-react";
import { api, apiUrl, unwrap, type Chapter, type S, type Series } from "../../api/client";
import { useChapters, usePushCommand, useSeries, useSeriesList } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, Confirm, ErrorBox, Loading, Menu, Modal, Select, Switch, type MenuItem } from "../../components/ui";
import { bytes, languageName, relative } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useToast } from "../../lib/toast";
import { SourcesPanel } from "./SourcesPanel";
import { ChaptersTable, readable } from "./ChaptersTable";
import { EditSeriesModal } from "./EditSeriesModal";
import { RenameModal } from "./Organize";
import { useAccount } from "../../lib/account";
import { AddLanguageModal, RequestLanguageModal } from "./AddLanguage";
import { PreviewBanner } from "./Preview";
import { AdaptationsChip } from "./Adaptations";
import { ReleaseLine } from "./ReleaseSchedule";
import { RequestDownloadButton, waitingChapters } from "./RequestDownload";
import { Recommendations } from "./Recommendations";

export function SeriesDetail() {
  const id = Number(useParams().id);
  const { data: s, isLoading, error } = useSeries(id);
  const { data: library } = useSeriesList();
  const { data: chapters } = useChapters(id);
  useDocumentTitle(s?.title);
  const push = usePushCommand();
  const { can, account } = useAccount();
  const { editing } = useUIMode();
  // a preview is read-only until it is added
  const manage = can("library.edit") && editing && !s?.preview;
  const adds = can("library.add") && editing && !s?.preview;
  const deletes = can("library.delete");
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const [edit, setEdit] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [del, setDel] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showDesc, setShowDesc] = useState(false);
  const [showDetails, setShowDetails] = useState(false);
  const [grouping, setGrouping] = useState(false);
  const [groupTarget, setGroupTarget] = useState("");
  const [groupBusy, setGroupBusy] = useState(false);
  const [addingLang, setAddingLang] = useState(false);
  // a request for another language, opened from Requests: ?addLanguage=xx&request=id
  const [params, setParams] = useSearchParams();
  const requestedLang = params.get("addLanguage") ?? "";
  const fulfils = Number(params.get("request") ?? 0);
  const handlesRequests = can(["library.add", "requests.manage"]) && !s?.preview;
  const closeRequested = () => {
    const next = new URLSearchParams(params);
    next.delete("addLanguage");
    next.delete("request");
    setParams(next, { replace: true });
  };
  // people who can't add a language ask for one
  const asksLang = !adds && !s?.preview && can("requests.create") && account?.kind === "user";

  if (isLoading) return <Loading />;
  if (error || !s) return <ErrorBox error={error ?? "Series not found"} />;
  const readTarget = readTargetOf(chapters, s.reading?.nextUnread);
  const waiting = asksLang ? waitingChapters(chapters) : 0;

  const setMonitored = async (v: boolean) => {
    try {
      await unwrap(api.PUT("/api/v1/series/{id}", { params: { path: { id } }, body: { monitored: v } }));
      qc.invalidateQueries({ queryKey: ["series"] });
    } catch (e) {
      toast.fromError(e);
    }
  };

  const remove = async () => {
    setDeleting(true);
    try {
      await unwrap(api.DELETE("/api/v1/series/{id}", { params: { path: { id }, query: { deleteFiles } } }));
      toast.success(`${s.title} deleted`);
      qc.invalidateQueries({ queryKey: ["series"] });
      nav("/");
    } catch (e) {
      toast.fromError(e);
    } finally {
      setDeleting(false);
    }
  };

  const setWork = async (workId: number) => {
    setGroupBusy(true);
    try {
      await unwrap(api.PUT("/api/v1/series/{id}/work", { params: { path: { id } }, body: { workId } }));
      toast.success(workId ? t("Language edition grouped") : t("Language edition separated"));
      await qc.invalidateQueries({ queryKey: ["series"] });
      setGrouping(false);
      setGroupTarget("");
    } catch (e) {
      toast.fromError(e);
    } finally {
      setGroupBusy(false);
    }
  };

  const md = s.metadata;
  const tags = titleTags(md);
  const follows = account?.kind === "user" && !s.preview;
  const manageItems: MenuItem[] = [
    { label: t("Edit…"), icon: <Pencil className="size-4" />, onSelect: () => setEdit(true) },
    s.monitored
      ? { label: t("Stop monitoring"), icon: <EyeOff className="size-4" />, onSelect: () => void setMonitored(false) }
      : { label: t("Monitor"), icon: <Eye className="size-4" />, onSelect: () => void setMonitored(true) },
    { section: t("Find") },
    { label: t("Refresh sources"), icon: <RefreshCw className="size-4" />, onSelect: () => push.mutate({ name: "RefreshSeries", body: { seriesId: id }, label: "Refreshing sources" }) },
    { label: t("Search missing chapters"), icon: <Search className="size-4" />, onSelect: () => push.mutate({ name: "SearchMissing", body: { seriesId: id }, label: "Searching missing chapters" }) },
    { label: t("Refresh metadata"), icon: <BookText className="size-4" />, onSelect: () => push.mutate({ name: "RefreshMetadata", body: { seriesId: id }, label: "Refreshing metadata" }) },
    { section: t("Files") },
    { label: t("Rescan disk"), icon: <FileSearch className="size-4" />, onSelect: () => push.mutate({ name: "DiskScan", body: { seriesId: id }, label: "Scanning files" }) },
    { label: t("Rename files…"), icon: <FilePen className="size-4" />, onSelect: () => setRenaming(true) },
    { label: t("Process downloaded chapters"), icon: <Sparkles className="size-4" />, onSelect: () => push.mutate({ name: "ProcessExisting", body: { seriesId: id }, label: "Downloaded chapters will be processed in the background" }) },
    { label: t("Copy folder path"), icon: <HardDrive className="size-4" />, onSelect: () => void navigator.clipboard.writeText(s.fullPath ?? "").then(() => toast.success(t("Folder path copied"), s.fullPath), () => toast.info(s.fullPath ?? "")), hidden: !s.fullPath },
    { section: t("Editions") },
    { label: t("Group with another language…"), icon: <Link2 className="size-4" />, onSelect: () => setGrouping(true) },
    { label: t("Separate edition"), icon: <Unlink className="size-4" />, onSelect: () => void setWork(0), hidden: (s.editions?.length ?? 0) <= 1 },
    { section: "" },
    { label: t("Delete series…"), icon: <Trash2 className="size-4" />, onSelect: () => setDel(true), danger: true, hidden: !deletes },
  ];
  const editions = s.editions ?? [];
  const current = editions.find((edition) => edition.id === id);
  const langName = current?.language ? languageName(current.language) : s.language ? languageName(s.language) : "";
  const credits = [...new Set([...(md.authors ?? []), ...(md.artists ?? [])])];
  const showsRead = account?.kind === "user" || s.stats.readCount > 0;
  return (
    <>
      {s.preview && <PreviewBanner series={s} />}
      {/* on a phone the cover sits beside the title and everything else runs full width under them */}
      <div className="mb-6 grid grid-cols-[6.5rem_minmax(0,1fr)] gap-4 sm:flex sm:items-start sm:gap-9">
        <Cover src={apiUrl(s.coverUrl)} alt={s.title} className="aspect-[2/3] w-full rounded-lg sm:w-50 sm:flex-none" />
        <div className="contents sm:flex sm:min-w-0 sm:flex-1 sm:flex-col sm:gap-4 sm:pt-1">
          <div className="flex min-w-0 flex-col gap-2 self-end sm:self-auto">
            <h1 className="text-xl font-bold leading-tight sm:text-3xl">{s.title}</h1>
            {s.workTitle && s.workTitle !== s.title && <p className="text-sm text-muted">{s.workTitle}</p>}
            <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 text-sm text-muted">
              {s.status && (
                <span className={`inline-flex items-center gap-1.5 ${statusClass[s.status] ?? ""}`}>
                  <span className="size-1.5 rounded-full bg-current" aria-hidden="true" />{label(capital(s.status))}
                </span>
              )}
              {(md.format || md.year) && <Dot>{[md.format ? label(capital(md.format)) : "", md.year ? String(md.year) : ""].filter(Boolean).join(", ")}</Dot>}
              {credits.length > 0 && <Dot>{credits.slice(0, 2).join(", ")}</Dot>}
              {editions.length > 1 || adds || asksLang ? (
                <Menu
                  size="sm"
                  label={langName || t("Language")}
                  icon={<Languages className="size-3.5" />}
                  items={[
                    { section: t("Language editions") },
                    ...editions.map((edition) => ({
                      label: (edition.language ? languageName(edition.language) : "?") + (edition.title !== s.title ? ` · ${edition.title}` : "") + (edition.id === id ? " ✓" : ""),
                      onSelect: () => nav(`/series/${edition.id}`),
                    })),
                    { label: adds ? t("Add language") : t("Ask for a language"), icon: <Plus className="size-4" />, onSelect: () => setAddingLang(true), hidden: !(adds || asksLang) },
                  ]}
                />
              ) : (
                langName && <Dot>{langName}</Dot>
              )}
              {manage && !s.monitored && <Badge tone="warn">{tr("Unmonitored")}</Badge>}
              <AdaptationsChip adaptations={s.adaptations ?? []} />
            </div>
          </div>
          {(readTarget || waiting > 0 || follows || manage) && (
            <div className="col-span-2 flex flex-wrap items-center gap-2">
              {readTarget && (
                <Link to={`/read/${readTarget.id}`} className="inline-flex h-11 flex-1 items-center justify-center gap-2 rounded-md bg-primary px-5 text-sm font-semibold text-white hover:bg-primary-hover sm:flex-none">
                  <BookOpen className="size-4" /> {readTarget.label}
                </Link>
              )}
              {waiting > 0 && <RequestDownloadButton seriesId={id} waiting={waiting} />}
              {follows && <FollowButton seriesId={id} following={s.following} />}
              {manage && <Menu className="h-11 px-3" label={<span className="sr-only">{t("Manage")}</span>} icon={<MoreHorizontal className="size-4" />} align="right" items={manageItems} />}
            </div>
          )}
          {md.description && (
            <p className={`col-span-2 max-w-[70ch] cursor-pointer whitespace-pre-line text-sm leading-relaxed text-fg/85 ${showDesc ? "" : "line-clamp-3"}`} onClick={() => setShowDesc(!showDesc)}>
              {md.description}
            </p>
          )}
          <div className="col-span-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm text-muted">
            {showsRead && (
              <>
                <span className="block h-1 w-28 overflow-hidden rounded-full bg-border" aria-hidden="true">
                  <span className="block h-full rounded-full bg-accent" style={{ width: `${s.stats.chapterCount ? Math.min(100, (100 * s.stats.readCount) / s.stats.chapterCount) : 0}%` }} />
                </span>
                <span className="text-fg/85">{t("{read} of {total} read", { read: s.stats.readCount, total: s.stats.chapterCount })}</span>
              </>
            )}
            {s.stats.missingCount > 0 && <Dot className="text-warn">{t("{count} not downloaded", { count: s.stats.missingCount })}</Dot>}
            {s.reading?.nextUnread && !readTarget && (
              <Dot>
                {t("Continue: ch.") + " " + s.reading.nextUnread.number}
                {!s.reading.nextUnread.available && <span className="ml-1.5"><Badge tone="warn">{t("not downloaded")}</Badge></span>}
              </Dot>
            )}
            <ReleaseLine chapters={chapters} status={s.status} />
          </div>
          <div className="col-span-2">
            <button
              type="button"
              aria-expanded={showDetails}
              onClick={() => setShowDetails(!showDetails)}
              className="inline-flex items-center gap-1 text-sm font-medium text-muted hover:text-fg"
            >
              {showDetails ? t("Hide details") : t("Details, tags and links")}
              <ChevronDown className={`size-3.5 transition-transform ${showDetails ? "rotate-180" : ""}`} />
            </button>
            {showDetails && (
              <div className="mt-3 grid gap-x-10 gap-y-5 border-t border-border pt-4 md:grid-cols-2">
                <Facts series={s} manage={manage} />
                {tags.length > 0 && (
                  <div className="flex flex-wrap content-start gap-1.5">
                    {tags.map((g) => (
                      <Link key={g} to={{ pathname: "/", search: `?genre=${encodeURIComponent(g)}` }} title={t("Series with this genre")} className="inline-flex h-7 items-center rounded-full bg-panel-2 px-2.5 text-xs font-medium text-fg/80 hover:text-fg">
                        {genreName(g)}
                      </Link>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>

      {manage && <SourcesPanel series={s} />}
      <ChaptersTable key={String(manage)} seriesId={id} manage={manage} nextChapterId={readTarget?.id} editions={s.editions ?? []} />
      {!s.preview && <Recommendations seriesId={id} />}

      {manage && edit && <EditSeriesModal series={s} onClose={() => setEdit(false)} />}
      {manage && renaming && <RenameModal seriesIds={[id]} onClose={() => setRenaming(false)} />}
      {adds && addingLang && <AddLanguageModal series={s} onClose={() => setAddingLang(false)} />}
      {asksLang && addingLang && <RequestLanguageModal series={s} onClose={() => setAddingLang(false)} />}
      {handlesRequests && !!requestedLang && fulfils > 0 && <AddLanguageModal series={s} initialLang={requestedLang} requestId={fulfils} onClose={closeRequested} />}
      {manage && grouping && (
        <Modal open onClose={() => setGrouping(false)} title={t("Group language edition")}>
          <p className="mb-4 text-sm text-muted">{t("Choose the title this edition belongs to. Files, sources, and settings stay separate; reading progress, the cover and the title's info are shared.")}</p>
          <Select value={groupTarget} onChange={(event) => setGroupTarget(event.target.value)}>
            <option value="">{t("Choose a title…")}</option>
            {(library ?? []).filter((candidate) => candidate.workId !== s.workId).map((candidate) => (
              <option key={candidate.workId} value={candidate.workId}>
                {candidate.title}{candidate.editions?.length ? ` (${candidate.editions.map((edition) => (edition.language ? languageName(edition.language) : "—")).join(", ")})` : ""}
              </option>
            ))}
          </Select>
          <div className="mt-5 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setGrouping(false)}>{t("Cancel")}</Button>
            <Button variant="primary" loading={groupBusy} disabled={!groupTarget} onClick={() => setWork(Number(groupTarget))}>{t("Group edition")}</Button>
          </div>
        </Modal>
      )}
      <Confirm
        open={manage && del}
        title={t("Delete series")}
        danger
        confirmLabel={t("Delete")}
        loading={deleting}
        message={
          <>{t("Remove") + " "}<b>{s.title}</b>{" " + t("from mangarr?")}</>
        }
        onConfirm={remove}
        onClose={() => setDel(false)}
      >
        <div className="mt-3">
          <Switch checked={deleteFiles} onChange={setDeleteFiles} label={t("Also move its folder to the recycle bin")} />
        </div>
      </Confirm>
      {manage && <div className="mt-6 text-xs text-muted">
        <Link to={`/activity/history?series=${s.id}`} className="hover:text-fg">{t("View history →")}</Link>
      </div>}
    </>
  );
}

/** titleTags merges a title's genres and tags into one list, without repeats. */
export function titleTags(md: S["SeriesMetadata"]): string[] {
  const seen = new Set<string>();
  return [...(md.genres ?? []), ...(md.tags ?? [])].filter((g) => {
    const key = g.trim().toLowerCase();
    if (!key || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

const statusClass: Record<string, string> = { ongoing: "text-ok", completed: "text-info", hiatus: "text-warn", cancelled: "text-err" };
const directionNames: Record<string, string> = { rtl: "Right to left", ltr: "Left to right", vertical: "Vertical" };
const capital = (v: string) => v.charAt(0).toUpperCase() + v.slice(1);

/** Dot is one item of a quiet "a · b · c" line. */
function Dot({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2.5 ${className ?? ""}`}>
      <span className="text-muted/60" aria-hidden="true">·</span>
      <span className="inline-flex items-center">{children}</span>
    </span>
  );
}

/** Facts is what the title page keeps behind "Details": the other names,
 * reading direction, credits, files, who reads it and links. */
function Facts({ series: s, manage }: { series: Series; manage: boolean }) {
  const { account } = useAccount();
  const md = s.metadata;
  const links = Object.entries(md.links ?? {});
  const authors = md.authors ?? [];
  const artists = md.artists ?? [];
  const sameCredits = authors.length > 0 && authors.join() === artists.join();
  const row = (name: string, value: ReactNode, cls?: string) => (
    <>
      <dt className="text-muted">{name}</dt>
      <dd className={cls ?? "text-fg"}>{value}</dd>
    </>
  );
  const readers = account?.kind !== "user" ? (s.reading?.readers ?? []) : [];
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] content-start gap-x-4 gap-y-1.5 text-sm">
      {md.altTitles && md.altTitles.length > 0 && row(t("Also known as"), md.altTitles.slice(0, 6).join(" · "))}
      {s.readingDirection && row(t("Direction"), label(directionNames[s.readingDirection] ?? s.readingDirection))}
      {md.ageRating && row(t("Age rating"), md.ageRating, "text-warn")}
      {sameCredits ? row(t("Story & art"), authors.join(", ")) : (
        <>
          {authors.length > 0 && row(t("Story"), authors.join(", "))}
          {artists.length > 0 && row(t("Art"), artists.join(", "))}
        </>
      )}
      {md.publisher && row(t("Publisher"), md.publisher)}
      {row(t("Downloaded"), `${s.stats.fileCount} / ${s.stats.chapterCount}` + (manage && s.stats.cleanedCount > 0 ? ` · ${t("{count} cleaned", { count: s.stats.cleanedCount })}` : ""))}
      {manage && row(t("On disk"), bytes(s.stats.sizeOnDisk) + (s.stats.spaceSaved > 0 ? ` · ${t("saved {size}", { size: bytes(s.stats.spaceSaved) })}` : ""))}
      {readers.length > 0 && row(t("Readers"), (
        <span className="flex flex-col gap-0.5">
          {readers.map((r) => (
            <span key={r.readerId} title={r.lastReadAt ? `last read ${relative(r.lastReadAt)}` : undefined}>
              {`${r.reader}: `}{r.read}/{s.stats.chapterCount}{" " + t("read")}{r.inProgress > 0 && `, ${r.inProgress} started`}
            </span>
          ))}
        </span>
      ))}
      {(links.length > 0 || s.reading?.webUrl) && row(t("Links"), (
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
          {links.map(([k, v]) => (
            <a key={k} href={v} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent-2 hover:underline">
              {k}<ExternalLink className="size-3" />
            </a>
          ))}
          {s.reading?.webUrl && (
            <a href={s.reading.webUrl} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent-2 hover:underline">
              {t("Open in") + " "}{s.reading.webName || tr("library")}<ExternalLink className="size-3" />
            </a>
          )}
        </span>
      ))}
    </dl>
  );
}

/** readTargetOf picks what the Read button opens: where you left off, or
 * the first chapter when you haven't started. Nothing when that chapter
 * can't be read (not downloaded and no source). */
function readTargetOf(chapters: Chapter[] | undefined, next?: S["NextChapter"]) {
  if (!chapters) return null;
  if (next) {
    const c = chapters.find((c) => c.id === next.chapterId);
    return c && readable(c) ? { id: c.id, label: t("Continue ch. {number}", { number: c.number }) } : null;
  }
  const byNumber = chapters.filter(readable).sort((a, b) => a.numberSort - b.numberSort);
  // opened but not finished (nothing finished yet, so there's no "next unread")
  const started = byNumber.find((c) => c.readBy.some((r) => !r.completed && r.page > 0));
  if (started) return { id: started.id, label: t("Continue ch. {number}", { number: started.number }) };
  const first = byNumber[0];
  return first ? { id: first.id, label: t("Start reading ch. {number}", { number: first.number }) } : null;
}

/** FollowButton: follow a series to get its new chapters on your notification targets. */
function FollowButton({ seriesId, following }: { seriesId: number; following: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const toggle = async () => {
    setBusy(true);
    try {
      const path = { params: { path: { id: seriesId } } };
      await unwrap(following ? api.DELETE("/api/v1/series/{id}/follow", path) : api.PUT("/api/v1/series/{id}/follow", path));
      qc.invalidateQueries({ queryKey: ["series"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button
      className="h-11 px-4"
      variant={following ? "secondary" : undefined}
      loading={busy}
      icon={following ? <BellOff className="size-4" /> : <Bell className="size-4" />}
      onClick={toggle}
      title={following ? tr("Stop getting its new chapters") : tr("Get its new chapters on your notifications (set them up under My account)")}
    >
      {following ? tr("Following") : tr("Follow")}
    </Button>
  );
}
