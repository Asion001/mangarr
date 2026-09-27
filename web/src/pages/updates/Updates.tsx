import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { BookOpen, BookPlus, Sparkles } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, Card, ErrorBox, Loading, PageHeader, Select } from "../../components/ui";
import { languageName, relative } from "../../lib/format";
import { useLiveUpdateStatus } from "../../lib/events";
import { t } from "../../lib/i18n/core";

type Update = S["UpdateItem"];

export function UpdatesPage() {
  const [days, setDays] = useState(30);
  const [kind, setKind] = useState<"all" | "chapter" | "series">("all");
  const [cursor, setCursor] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const page = cursorHistory.length + 1;
  const pageSize = 50;
  const liveStatus = useLiveUpdateStatus();
  const resetPage = () => {
    setCursor("");
    setCursorHistory([]);
  };
  const { data, isLoading, error } = useQuery({
    queryKey: ["updates", days, kind, cursor],
    queryFn: () => unwrap(api.GET("/api/v1/updates", { params: { query: { days, kind, pageSize, cursor: cursor || undefined } } })),
    staleTime: 60_000,
  });
  const groups = groupByDay(data?.items ?? []);
  return (
    <>
      <PageHeader
        title={t("Updates")}
        subtitle={t("New chapters discovered and new titles added to your library.")}
        actions={
          <div className="flex items-center gap-2">
            {liveStatus !== "connected" && (
              <Badge tone="warn">
                <span role="status" className="capitalize">{t(liveStatus)}</span>
              </Badge>
            )}
            <Select className="w-40" value={days} onChange={(event) => (setDays(Number(event.target.value)), resetPage())}>
              <option value={7}>{t("Last 7 days")}</option>
              <option value={30}>{t("Last 30 days")}</option>
              <option value={90}>{t("Last 90 days")}</option>
            </Select>
          </div>
        }
      />
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {(["all", "chapter", "series"] as const).map((value) => (
          <Button key={value} size="sm" variant={kind === value ? "primary" : "secondary"} onClick={() => (setKind(value), resetPage())}>
            {value === "all" ? t("All") : value === "chapter" ? t("Chapters") : t("Titles")}
          </Button>
        ))}
        <span className="ml-auto text-xs text-muted">{t("Initial chapter catalogs are grouped into the new-title event.")}</span>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {!isLoading && !error && groups.length === 0 && (
        <Card><p className="text-sm text-muted">{t("No updates in this period.")}</p></Card>
      )}
      <div className="flex flex-col gap-6">
        {groups.map(([day, items]) => (
          <section key={day}>
            <h2 className="mb-2 text-sm font-semibold text-muted">{day}</h2>
            <Card className="divide-y divide-border !p-0">
              {items.map((item) => <UpdateRow key={`${item.kind}-${item.chapterId || item.seriesId}`} item={item} />)}
            </Card>
          </section>
        ))}
      </div>
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

function UpdateRow({ item }: { item: Update }) {
  const chapter = item.kind === "chapter";
  return (
    <div className="flex items-center gap-3 p-3 sm:p-4">
      <Link to={`/series/${item.seriesId}`} className="shrink-0">
        <Cover src={apiUrl(item.coverUrl)} alt={item.seriesTitle} className="aspect-[2/3] w-12 sm:w-14" />
      </Link>
      <div className="min-w-0 flex-1">
        <div className="mb-1 flex flex-wrap items-center gap-1.5">
          <Badge tone={chapter ? "info" : "accent"}>
            {chapter ? <BookOpen className="size-3" /> : <Sparkles className="size-3" />}
            {chapter ? t("New chapter") : t("New title")}
          </Badge>
          {item.language && <Badge>{languageName(item.language)}</Badge>}
          {item.languages?.map((language) => <Badge key={language}>{languageName(language)}</Badge>)}
          {chapter && <Badge tone={item.downloaded ? "ok" : item.readable ? "info" : "default"}>{item.downloaded ? t("Downloaded") : item.readable ? t("Streamable") : t("Unavailable")}</Badge>}
          {chapter && <Badge tone={item.readState === "read" ? "ok" : item.readState === "in_progress" ? "accent" : "default"}>
            {item.readState === "read" ? t("Read") : item.readState === "in_progress" ? `${t("In progress")} · ${item.readPage}` : t("Unread")}
          </Badge>}
          <span className="text-xs text-muted">{relative(item.at)}</span>
        </div>
        <Link to={`/series/${item.seriesId}`} className="font-medium hover:text-accent-2">{item.seriesTitle}</Link>
        {chapter && (
          <p className="truncate text-sm text-muted">{t("Chapter") + " "}{item.number}{item.title ? ` · ${item.title}` : ""}</p>
        )}
      </div>
      {chapter && item.readable ? (
        <Link to={`/read/${item.chapterId}`}><Button variant="primary" icon={<BookOpen className="size-4" />}>{item.readState === "in_progress" ? t("Continue") : t("Read")}</Button></Link>
      ) : !chapter ? (
        <Link to={`/series/${item.seriesId}`}><Button icon={<BookPlus className="size-4" />}>{t("View")}</Button></Link>
      ) : null}
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
