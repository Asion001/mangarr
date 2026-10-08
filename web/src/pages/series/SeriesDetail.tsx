import { useUIMode } from "../../lib/uiPreferences";
import { useDocumentTitle } from "../../lib/documentTitle";
import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Bell, BellOff, Plus, BookOpen, ExternalLink, Eye, FilePen, HardDrive, Pencil, RefreshCw, Search, Sparkles, Trash2, FileSearch, BookText, Link2, Unlink } from "lucide-react";
import { api, apiUrl, unwrap, type Chapter, type S } from "../../api/client";
import { useChapters, usePushCommand, useSeries, useSeriesList } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, Confirm, ErrorBox, Loading, Menu, Modal, Select, Switch } from "../../components/ui";
import { bytes, languageName, relative } from "../../lib/format";
import { genreName } from "../../lib/genres";
import { useToast } from "../../lib/toast";
import { statusTone } from "./SeriesIndex";
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
  const links = Object.entries(md.links ?? {});
  return (
    <>
      {s.preview && <PreviewBanner series={s} />}
      <div className="mb-6 grid grid-cols-[6rem_minmax(0,1fr)] gap-x-4 gap-y-3 md:grid-cols-[12rem_minmax(0,1fr)] md:gap-x-6">
        <Cover src={apiUrl(s.coverUrl)} alt={s.title} className="aspect-[2/3] w-full self-start md:row-span-2" />
        <div className="min-w-0">
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <h1 className="text-2xl font-semibold leading-tight">{s.title}</h1>
              {s.workTitle && s.workTitle !== s.title && <p className="mt-1 text-sm text-muted">{s.workTitle}</p>}
              {md.altTitles && md.altTitles.length > 0 && <p className="mt-1 line-clamp-1 text-sm text-muted">{md.altTitles.slice(0, 4).join(" · ")}</p>}
            </div>
            <div className="flex items-center gap-3">
              {account?.kind === "user" && !s.preview && <FollowButton seriesId={id} following={s.following} />}
              {manage && <Switch checked={s.monitored} onChange={setMonitored} label={s.monitored ? tr("Monitored") : tr("Unmonitored")} />}
            </div>
          </div>
          <div className="mt-3 flex flex-wrap gap-1.5">
            <Badge tone={statusTone(s.status)}>{s.status}</Badge>
            {md.format && <Badge>{md.format}</Badge>}
            {md.year ? <Badge>{md.year}</Badge> : null}
            <Badge>{s.readingDirection}</Badge>
            {s.language && <Badge>{languageName(s.language)}</Badge>}
            {md.ageRating && <Badge tone="warn">{md.ageRating}</Badge>}
            {(md.genres ?? []).slice(0, 8).map((g) => (
              <Link key={g} to={{ pathname: "/", search: `?genre=${encodeURIComponent(g)}` }} title={t("Series with this genre")}>
                <Badge tone="info">{genreName(g)}</Badge>
              </Link>
            ))}
          </div>
        </div>
        <div className="col-span-2 min-w-0 md:col-span-1 md:col-start-2">
          {((s.editions?.length ?? 0) > 1 || adds || asksLang) && (
            <nav className="mt-3 flex flex-wrap gap-2" aria-label={t("Language editions")}>
              {(s.editions ?? []).map((edition) => (
                <Link
                  key={edition.id}
                  to={`/series/${edition.id}`}
                  aria-current={edition.id === id ? "page" : undefined}
                  className={`rounded-md border px-3 py-1.5 text-sm ${edition.id === id ? "border-accent bg-accent/15 text-accent-2" : "border-border bg-panel hover:border-accent/60"}`}
                >
                  <span className="font-medium">{edition.language ? languageName(edition.language) : "?"}</span>
                  {edition.title !== s.title && <span className="ml-2 text-muted">{edition.title}</span>}
                </Link>
              ))}
              {(adds || asksLang) && (
                <button
                  type="button"
                  onClick={() => setAddingLang(true)}
                  title={adds ? undefined : t("Ask for this title in another language")}
                  className="inline-flex items-center gap-1 rounded-md border border-dashed border-border px-3 py-1.5 text-sm text-accent-2 hover:border-accent/60"
                >
                  <Plus className="size-4" />{t("Add language")}
                </button>
              )}
            </nav>
          )}
          <div className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-sm sm:grid-cols-4">
            <Stat label={t("Chapters")} value={`${s.stats.fileCount} / ${s.stats.chapterCount}`} />
            <Stat label={t("Missing")} value={String(s.stats.missingCount)} />
            <Stat label={t("Cleaned")} value={String(s.stats.cleanedCount)} />
            <Stat label={t("On disk")} value={s.stats.spaceSaved > 0 ? `${bytes(s.stats.sizeOnDisk)} (saved ${bytes(s.stats.spaceSaved)})` : bytes(s.stats.sizeOnDisk)} />
          </div>
          <ReleaseLine chapters={chapters} status={s.status} />
          <div className="mt-4 flex flex-wrap items-center gap-2">
            {readTarget && (
              <Link to={`/read/${readTarget.id}`} className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-semibold text-white hover:bg-primary-hover">
                <BookOpen className="size-4" /> {readTarget.label}
              </Link>
            )}
            {waiting > 0 && <RequestDownloadButton seriesId={id} waiting={waiting} />}
            {manage && <Button icon={<Pencil className="size-4" />} onClick={() => setEdit(true)}>{t("Edit")}</Button>}
            {manage && (
              <Menu
                label={t("Manage")}
                items={[
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
                ]}
              />
            )}
          </div>
          {s.reading && (s.reading.readers.length > 0 || s.reading.webUrl) && (
            <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
              {s.reading.nextUnread && !readTarget && (
                <span className="flex items-center gap-1">
                  <BookOpen className="size-4 text-info" />{t("Continue: ch.") + " "}{s.reading.nextUnread.number}
                  {s.reading.nextUnread.title && s.reading.nextUnread.title !== s.reading.nextUnread.number && (
                    <span className="text-muted">{s.reading.nextUnread.title}</span>
                  )}
                  {!s.reading.nextUnread.available && <Badge tone="warn">{t("not downloaded")}</Badge>}
                </span>
              )}
              {s.reading.readers.map((r) => (
                <span key={r.readerId} className="text-muted" title={r.lastReadAt ? `last read ${relative(r.lastReadAt)}` : undefined}>
                  <Eye className="mr-1 inline size-3.5" />
                  {account?.kind === "user" ? "" : `${r.reader}: `}{r.read}/{s.stats.chapterCount}{" " + t("read")}{r.inProgress > 0 && `, ${r.inProgress} started`}
                </span>
              ))}
              {s.reading.webUrl && (
                <a href={s.reading.webUrl} target="_blank" rel="noreferrer" className="flex items-center gap-1 text-accent-2 hover:underline">
                  <ExternalLink className="size-3.5" />{" " + t("Open in") + " "}{s.reading.webName || tr("library")}
                </a>
              )}
            </div>
          )}
          {(md.authors?.length || md.artists?.length) && (
            <p className="mt-3 text-sm text-muted">
              {md.authors?.length ? <>{t("Story:") + " "}{md.authors.join(", ")}</> : null}
              {md.artists?.length ? <>{" " + t("· Art:") + " "}{md.artists.join(", ")}</> : null}
            </p>
          )}
          {md.description && (
            <p className={`mt-3 whitespace-pre-line text-sm text-fg/85 ${showDesc ? "" : "line-clamp-3"} cursor-pointer`} onClick={() => setShowDesc(!showDesc)}>
              {md.description}
            </p>
          )}
          {(links.length > 0 || (s.adaptations?.length ?? 0) > 0) && (
          <div className="mt-3 flex flex-wrap items-center gap-3 text-xs text-muted">
            {links.map(([k, v]) => (
              <a key={k} href={v} target="_blank" rel="noreferrer" className="flex items-center gap-1 hover:text-accent-2">
                <ExternalLink className="size-3.5" /> {k}
              </a>
            ))}
            <AdaptationsChip adaptations={s.adaptations ?? []} />
          </div>
          )}
        </div>
      </div>

      {manage && <SourcesPanel series={s} />}
      <ChaptersTable key={String(manage)} seriesId={id} manage={manage} nextChapterId={readTarget?.id} editions={s.editions ?? []} />

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

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-muted">{label}</div>
      <div className="font-medium">{value}</div>
    </div>
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
      size="sm"
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
