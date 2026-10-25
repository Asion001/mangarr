import { t } from "../../lib/i18n/core";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { useSeriesList } from "../../api/queries";
import { Badge, Button, ErrorBox, Loading, PageHeader, Select, Table, Td, Th } from "../../components/ui";
import { dateTime } from "../../lib/format";
import { TitlePicker } from "../../components/TitlePicker";
import { useListParam } from "../../lib/urlState";

const tone: Record<string, "ok" | "warn" | "err" | "info" | "default" | "accent"> = {
  imported: "ok",
  upgraded: "info",
  grabbed: "default",
  failed: "err",
  blocklisted: "err",
  deleted: "warn",
  cleaned: "warn",
  restored: "info",
  upscaled: "accent",
  processed: "accent",
  moved: "info",
  progressRestored: "ok",
  readAhead: "info",
  unparsed: "warn",
};
type HistorySort = "newest" | "oldest" | "series" | "event";
const historySorts: HistorySort[] = ["newest", "oldest", "series", "event"];

export function HistoryPage() {
  const [pageParam, setPage] = useListParam("page", "1");
  const [eventType, setEventType] = useListParam("event");
  const [seriesParam, setSeries] = useListParam("series");
  const [sortParam, setSort] = useListParam("sort", "newest");
  const page = Math.max(1, Number(pageParam) || 1);
  const seriesId = Number(seriesParam) || undefined;
  const sort: HistorySort = historySorts.includes(sortParam as HistorySort) ? sortParam as HistorySort : "newest";
  const { data: series } = useSeriesList();
  const titles = new Map((series ?? []).map((s) => [s.id, s.title]));
  const { data, isLoading, error } = useQuery({
    queryKey: ["history", page, eventType, seriesId, sort],
    queryFn: () => unwrap(api.GET("/api/v1/history", { params: { query: {
      page, pageSize: 50, eventType: eventType || undefined, seriesId, sort,
    } } })),
  });
  const pages = data ? Math.max(1, Math.ceil(data.total / data.pageSize)) : 1;
  return (
    <>
      <PageHeader
        title={t("History")}
        actions={
          <div className="flex flex-wrap items-center justify-end gap-2">
            <TitlePicker className="w-full sm:w-56" placeholder={t("All titles")} value={seriesId ?? 0} onChange={(id) => (setSeries(id ? String(id) : ""), setPage("1"))} />
            <Select className="w-44" value={eventType} onChange={(e) => (setEventType(e.target.value), setPage("1"))}>
              <option value="">{t("All events")}</option>
              {Object.keys(tone).map((event) => <option key={event} value={event}>{event}</option>)}
            </Select>
            <Select className="w-48" value={sort} onChange={(e) => (setSort(e.target.value), setPage("1"))}>
              <option value="newest">{t("Sort: recently added")}</option>
              <option value="oldest">{t("Sort: oldest")}</option>
              <option value="series">{t("Sort: title")}</option>
              <option value="event">{t("Sort: event")}</option>
            </Select>
          </div>
        }
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <Table>
            <thead>
              <tr>
                <Th>{t("Event")}</Th>
                <Th>{t("Series")}</Th>
                <Th>{t("Release")}</Th>
                <Th>{t("Details")}</Th>
                <Th>{t("Date")}</Th>
              </tr>
            </thead>
            <tbody>
              {data.items.map((h) => (
                <tr key={h.id}>
                  <Td>
                    <Badge tone={tone[h.eventType] ?? "default"}>{h.eventType}</Badge>
                  </Td>
                  <Td>
                    <Link to={`/series/${h.seriesId}`} className="hover:text-accent-2">
                      {titles.get(h.seriesId) ?? `#${h.seriesId}`}
                    </Link>
                  </Td>
                  <Td className="max-w-xs truncate" >{h.sourceTitle}</Td>
                  <Td className="max-w-md text-xs text-muted">
                    {Object.entries(h.data ?? {})
                      .filter(([, v]) => v && v !== "false")
                      .map(([k, v]) => `${k}: ${v}`)
                      .join(" · ")}
                  </Td>
                  <Td className="whitespace-nowrap text-muted">{dateTime(h.createdAt)}</Td>
                </tr>
              ))}
            </tbody>
          </Table>
          <div className="mt-3 flex items-center justify-end gap-2 text-sm">
            <Button size="sm" disabled={page <= 1} onClick={() => setPage(String(page - 1))}>{t("Previous")}</Button>
            <span className="text-muted">
              {page} / {pages}
            </span>
            <Button size="sm" disabled={page >= pages} onClick={() => setPage(String(page + 1))}>{t("Next")}</Button>
          </div>
        </>
      )}
    </>
  );
}
