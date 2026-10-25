import { useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import clsx from "clsx";
import { BookOpen, BookPlus, CheckCheck, Eye, Settings, SlidersHorizontal, Sparkles } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { useSeriesList } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { TitlePicker } from "../../components/TitlePicker";
import { Badge, Button, ButtonLink, Card, EmptyState, ErrorBox, Loading, Modal, PageHeader, Segmented, Select } from "../../components/ui";
import { languageName, relative } from "../../lib/format";
import { useLiveUpdateStatus } from "../../lib/events";
import { t } from "../../lib/i18n/core";
import { useAccount } from "../../lib/account";
import { useListParam } from "../../lib/urlState";
import { useToast } from "../../lib/toast";

type Update = S["UpdateItem"];
type Kind = "all" | "chapter" | "series";
type Avail = "" | "downloaded" | "streamable";

/** isNew: an update you haven't seen (an older server sends no seen flag: nothing is new then). */
const isNew = (item: Update) => item.seen === false;
const ref = (item: Update) => ({ kind: item.kind, id: item.kind === "chapter" ? item.chapterId! : item.seriesId, at: item.at });

export function UpdatesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [daysParam, setDays] = useListParam("days", "30");
  const [kindParam, setKind] = useListParam("kind", "all");
  const [show, setShow] = useListParam("show", "all");
  const [availParam, setAvail] = useListParam("availability", "");
  const [language, setLanguage] = useListParam("language", "");
  const [followedParam, setFollowed] = useListParam("followed", "");
  const [seriesParam, setSeries] = useListParam("series", "");
  const days = Number(daysParam) || 30;
  const kind = (["all", "chapter", "series"].includes(kindParam) ? kindParam : "all") as Kind;
  const avail = (["downloaded", "streamable"].includes(availParam) ? availParam : "") as Avail;
  const followed = followedParam === "1";
  const seriesId = Number(seriesParam) || 0;
  const unseenOnly = show === "new";
  const [cursor, setCursor] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [sheet, setSheet] = useState(false);
  const page = cursorHistory.length + 1;
  const pageSize = 50;
  const liveStatus = useLiveUpdateStatus();
  const { account, isAdmin } = useAccount();
  // your own Telegram/Discord messages; with logins off only the server's notifications exist
  const settingsTo = account?.kind === "user" ? "/account/notifications" : isAdmin ? "/settings/notifications" : "";
  const resetPage = () => {
    setCursor("");
    setCursorHistory([]);
  };
  const filters = { kind, availability: avail || undefined, language: language || undefined, followed: followed || undefined, seriesId: seriesId || undefined };
  const { data, isLoading, error } = useQuery({
    queryKey: ["updates", days, filters, unseenOnly, cursor],
    queryFn: () => unwrap(api.GET("/api/v1/updates", { params: { query: { days, ...filters, unseen: unseenOnly || undefined, pageSize, cursor: cursor || undefined } } })),
    staleTime: 60_000,
  });
  const unseen = data?.unseen ?? 0;
  const narrowed = !!(avail || language || followed || seriesId);
  const activeFilters = [kind !== "all", avail, language, followed, seriesId].filter(Boolean).length;

  const markSeen = async (body: S["UpdatesSeenInput"]) => {
    try {
      await unwrap(api.POST("/api/v1/updates/seen", { body }));
      await qc.invalidateQueries({ queryKey: ["updates"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const markAll = () => markSeen({ all: true, days, ...filters, kind: kind === "all" ? undefined : kind });
  const markOne = (item: Update) => markSeen({ items: [ref(item)] });
  const set = (fn: () => void) => () => (fn(), resetPage());

  const items = data?.items ?? [];
  // the line where you stopped last time: before the first update at or
  // before the seen cursor, when something newer is above it (single
  // updates marked seen above the line stay where they are, dimmed)
  const through = data?.seenThrough ? new Date(data.seenThrough).getTime() : null;
  const split = through === null ? -1 : items.findIndex((item, i) => i > 0 && new Date(item.at).getTime() <= through && new Date(items[i - 1].at).getTime() > through);
  const before = groupByDay(split < 0 ? items : items.slice(0, split));
  const after = split < 0 ? [] : groupByDay(items.slice(split));

  const controls = (
    <FilterControls
      kind={kind} avail={avail} language={language} followed={followed} seriesId={seriesId} canFollow={account?.kind === "user"}
      onKind={(v) => set(() => setKind(v))()} onAvail={(v) => set(() => setAvail(v))()} onLanguage={(v) => set(() => setLanguage(v))()}
      onFollowed={(v) => set(() => setFollowed(v ? "1" : ""))()} onSeries={(v) => set(() => setSeries(v ? String(v) : ""))()}
    />
  );
  const clear = set(() => (setKind("all"), setAvail(""), setLanguage(""), setFollowed(""), setSeries("")));

  return (
    <>
      <PageHeader
        title={t("Updates")}
        subtitle={unseen > 0 ? t("{n} new since you last looked.", { n: unseen }) : t("New chapters discovered and new titles added to your library.")}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {liveStatus !== "connected" && (
              <Badge tone="warn">
                <span role="status" className="capitalize">{t(liveStatus)}</span>
              </Badge>
            )}
            <Select className="w-40" aria-label={t("Period")} value={days} onChange={(event) => set(() => setDays(event.target.value))()}>
              <option value={7}>{t("Last 7 days")}</option>
              <option value={30}>{t("Last 30 days")}</option>
              <option value={90}>{t("Last 90 days")}</option>
            </Select>
            {unseen > 0 && (
              <Button variant="primary" icon={<CheckCheck className="size-4" />} onClick={markAll}>
                {narrowed || kind !== "all" ? t("Mark these {n} seen", { n: unseen }) : t("Mark all {n} seen", { n: unseen })}
              </Button>
            )}
            {settingsTo && (
              <Link to={settingsTo} title={t("Notification settings")} aria-label={t("Notification settings")} className="inline-flex size-9 items-center justify-center rounded-md border border-border bg-panel-2 text-muted hover:bg-border hover:text-fg">
                <Settings className="size-4" />
              </Link>
            )}
          </div>
        }
      />
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Segmented
          label={t("Show")}
          value={unseenOnly ? "new" : "all"}
          onChange={(v) => set(() => setShow(v))()}
          options={[
            { value: "new", label: <>{t("New")} {unseen > 0 && <span className="font-semibold text-accent-2">{unseen}</span>}</> },
            { value: "all", label: t("All") },
          ]}
        />
        <div className="hidden flex-wrap items-center gap-2 sm:flex">{controls}</div>
        <Button size="sm" className="sm:hidden" icon={<SlidersHorizontal className="size-4" />} onClick={() => setSheet(true)}>
          {activeFilters ? t("Filters · {n}", { n: activeFilters }) : t("Filters")}
        </Button>
      </div>
      <div className="mb-4 flex flex-wrap items-center gap-2 text-xs text-muted">
        {activeFilters > 0 ? (
          <>
            {t("Some updates are hidden by the filters.")}
            <button type="button" className="text-accent-2 hover:underline" onClick={clear}>{t("Clear filters")}</button>
          </>
        ) : (
          t("Initial chapter catalogs are grouped into the new-title event.")
        )}
      </div>
      <Modal open={sheet} onClose={() => setSheet(false)} title={t("Filters")} footer={<Button variant="primary" onClick={() => setSheet(false)}>{t("Done")}</Button>}>
        <div className="flex flex-col items-stretch gap-3">{controls}</div>
      </Modal>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {!isLoading && !error && items.length === 0 && (unseenOnly ? (
        <Card>
          <p className="flex flex-wrap items-center gap-2 text-sm text-muted">
            {t("You're up to date.")}
            <button type="button" className="text-accent-2 hover:underline" onClick={set(() => setShow("all"))}>{t("Show everything")}</button>
          </p>
        </Card>
      ) : (
        <EmptyState title={t("No updates in this period.")}>
          {t("New chapters of the titles you follow show up here.") + " "}
          <Link to="/discover" className="text-accent-2 hover:underline">{t("Find something to read")}</Link>
        </EmptyState>
      ))}
      <div className="flex flex-col gap-6">
        <Days groups={before} onSeen={markOne} />
        {after.length > 0 && (
          <>
            <div role="separator" className="flex items-center gap-3 text-xs text-muted">
              <span className="h-px flex-1 bg-border" />
              {data?.seenThrough ? t("Seen up to here on {when}", { when: new Date(data.seenThrough).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }) }) : t("Seen")}
              <span className="h-px flex-1 bg-border" />
            </div>
            <Days groups={after} onSeen={markOne} />
          </>
        )}
      </div>
      {!unseenOnly && items.some((item) => !isNew(item)) && items.some(isNew) && (
        <p className="mt-3 text-xs text-muted">{t("Marking updates seen never changes reading progress.")}</p>
      )}
      {data && data.total > pageSize && (
        <div className="mt-5 flex items-center justify-center gap-3 text-sm">
          <Button
            size="sm"
            disabled={cursorHistory.length === 0}
            onClick={() => {
              setCursor(cursorHistory[cursorHistory.length - 1] ?? "");
              setCursorHistory(cursorHistory.slice(0, -1));
            }}
          >{t("Previous")}</Button>
          <span className="text-muted">{t("Page") + " "}{page}{" " + t("of") + " "}{Math.ceil(data.total / pageSize)}</span>
          <Button
            size="sm"
            disabled={!data.nextCursor}
            onClick={() => {
              if (!data.nextCursor) return;
              setCursorHistory([...cursorHistory, cursor]);
              setCursor(data.nextCursor);
            }}
          >{t("Next")}</Button>
        </div>
      )}
    </>
  );
}

function FilterControls(p: {
  kind: Kind; avail: Avail; language: string; followed: boolean; seriesId: number; canFollow: boolean;
  onKind: (v: Kind) => void; onAvail: (v: Avail) => void; onLanguage: (v: string) => void; onFollowed: (v: boolean) => void; onSeries: (v: number) => void;
}) {
  const { data } = useSeriesList();
  const library = Array.isArray(data) ? data : [];
  const languages = useMemo(() => [...new Set(library.map((s) => s.language).filter(Boolean) as string[])].sort(), [library]);
  return (
    <>
      <Segmented
        label={t("Kind")}
        value={p.kind}
        onChange={p.onKind}
        options={[{ value: "all", label: t("All kinds") }, { value: "chapter", label: t("Chapters") }, { value: "series", label: t("Titles") }]}
      />
      <Select className="w-auto" aria-label={t("Availability")} value={p.avail} onChange={(e) => p.onAvail(e.target.value as Avail)}>
        <option value="">{t("Downloaded or not")}</option>
        <option value="downloaded">{t("Downloaded")}</option>
        <option value="streamable">{t("Readable now")}</option>
      </Select>
      {languages.length > 1 && (
        <Select className="w-auto" aria-label={t("Language")} value={p.language} onChange={(e) => p.onLanguage(e.target.value)}>
          <option value="">{t("Any language")}</option>
          {languages.map((l) => <option key={l} value={l}>{languageName(l)}</option>)}
        </Select>
      )}
      {p.canFollow && (
        <label className="flex min-h-9 items-center gap-2 text-sm">
          <input type="checkbox" className="size-4 accent-accent" checked={p.followed} onChange={(e) => p.onFollowed(e.target.checked)} />
          {t("Titles I follow")}
        </label>
      )}
      <TitlePicker className="w-full sm:w-56" placeholder={t("Filter by title…")} value={p.seriesId} onChange={p.onSeries} />
    </>
  );
}

function Days({ groups, onSeen }: { groups: [string, Update[]][]; onSeen: (item: Update) => void }) {
  return (
    <>
      {groups.map(([day, items]) => (
        <section key={day}>
          <h2 className="mb-2 text-sm font-semibold text-muted">{day}</h2>
          <Card className="divide-y divide-border !p-0">
            {items.map((item) => <UpdateRow key={`${item.kind}-${item.chapterId || item.seriesId}`} item={item} onSeen={() => onSeen(item)} />)}
          </Card>
        </section>
      ))}
    </>
  );
}

function UpdateRow({ item, onSeen }: { item: Update; onSeen: () => void }) {
  const chapter = item.kind === "chapter";
  const fresh = isNew(item);
  // on a phone a swipe to the left marks the update seen
  const start = useRef<number | null>(null);
  const swipe = {
    onTouchStart: (e: React.TouchEvent) => (start.current = e.touches[0].clientX),
    onTouchEnd: (e: React.TouchEvent) => {
      if (fresh && start.current !== null && start.current - e.changedTouches[0].clientX > 70) onSeen();
      start.current = null;
    },
  };
  // on a phone the action sits small in the row under the title, on a wider
  // screen at full size on the right
  const action = (size: "sm" | "md"): ReactNode => {
    if (chapter && item.readable) {
      return <ButtonLink to={`/read/${item.chapterId}`} size={size} variant={fresh ? "primary" : "secondary"} icon={<BookOpen className="size-4" />}>{item.readState === "in_progress" ? t("Continue") : t("Read")}</ButtonLink>;
    }
    if (!chapter) return <ButtonLink to={`/series/${item.seriesId}`} size={size} icon={<BookPlus className="size-4" />}>{t("View")}</ButtonLink>;
    return null;
  };
  const seenButton = (className: string) => (
    <button
      type="button"
      onClick={onSeen}
      title={t("Mark seen")}
      aria-label={chapter ? t("Mark {title} chapter {n} seen", { title: item.seriesTitle, n: item.number ?? "" }) : t("Mark {title} seen", { title: item.seriesTitle })}
      className={clsx("shrink-0 items-center justify-center rounded-md text-muted hover:bg-border hover:text-fg", className)}
    >
      <Eye className="size-4" />
    </button>
  );
  const languages = (
    <>
      {item.language && <Badge>{languageName(item.language)}</Badge>}
      {item.languages?.map((language) => <Badge key={language}>{languageName(language)}</Badge>)}
    </>
  );
  const state = chapter && (
    <>
      <Badge tone={item.downloaded ? "ok" : item.readable ? "info" : "default"}>{item.downloaded ? t("Downloaded") : item.readable ? t("Streamable") : t("Unavailable")}</Badge>
      <Badge tone={item.readState === "read" ? "ok" : item.readState === "in_progress" ? "accent" : "default"}>
        {item.readState === "read" ? t("Read") : item.readState === "in_progress" ? `${t("In progress")} · ${item.readPage}` : t("Unread")}
      </Badge>
    </>
  );
  return (
    <div className={clsx("flex items-start gap-3 p-3 sm:items-center sm:p-4", !fresh && item.seen && "opacity-70")} {...swipe}>
      <span aria-hidden="true" className={clsx("mt-1.5 size-2 shrink-0 rounded-full sm:mt-0", fresh ? "bg-accent" : "bg-transparent")} />
      <Link to={`/series/${item.seriesId}`} className="shrink-0">
        <Cover src={apiUrl(item.coverUrl)} alt={item.seriesTitle} className="aspect-[2/3] w-12 sm:w-14" />
      </Link>
      <div className="min-w-0 flex-1">
        {fresh && <span className="sr-only">{t("New")}</span>}
        <div className="mb-1 hidden flex-wrap items-center gap-1.5 sm:flex">
          <Badge tone={chapter ? "info" : "accent"}>
            {chapter ? <BookOpen className="size-3" /> : <Sparkles className="size-3" />}
            {chapter ? t("New chapter") : t("New title")}
          </Badge>
          {languages}
          {state}
          <span className="text-xs text-muted">{relative(item.at)}</span>
        </div>
        <div className="flex items-start gap-2">
          <Link to={`/series/${item.seriesId}`} className={clsx("line-clamp-2 min-w-0 flex-1 hover:text-accent-2 sm:line-clamp-none", fresh ? "font-semibold" : "font-medium")}>{item.seriesTitle}</Link>
          {fresh && seenButton("-mr-1 -mt-1.5 inline-flex size-9 sm:hidden")}
        </div>
        <p className="truncate text-sm text-muted">
          {chapter ? <>{t("Chapter") + " "}{item.number}{item.title ? ` · ${item.title}` : ""}</> : <span className="sm:hidden">{t("New title")}</span>}
          <span className="sm:hidden">{" · " + relative(item.at)}</span>
        </p>
        <div className="mt-1.5 flex flex-wrap items-center gap-1 sm:hidden">
          {languages}
          {state}
          <span className="ml-auto pl-1">{action("sm")}</span>
        </div>
      </div>
      {fresh && seenButton("hidden size-9 border border-border bg-panel-2 sm:inline-flex")}
      <div className="hidden shrink-0 sm:block">{action("md")}</div>
    </div>
  );
}

function groupByDay(items: Update[]): [string, Update[]][] {
  const groups = new Map<string, Update[]>();
  for (const item of items) {
    const day = new Date(item.at).toLocaleDateString(undefined, { weekday: "long", year: "numeric", month: "long", day: "numeric" });
    groups.set(day, [...(groups.get(day) ?? []), item]);
  }
  return [...groups.entries()];
}
