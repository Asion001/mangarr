import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { BookCheck, CalendarDays, Clock, RotateCw, Trophy } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Button, Card, ErrorBox, Progress, Spinner } from "../../components/ui";
import { getLocale, plural, t } from "../../lib/i18n/core";
import { calendarMonth, languageName, readingTime } from "../../lib/format";
import { genreName } from "../../lib/genres";

type Stats = S["ReadingStats"];

const chapterForms = {
  en: { one: "{count} chapter", other: "{count} chapters" },
  ru: { one: "{count} глава", few: "{count} главы", many: "{count} глав", other: "{count} главы" },
  uk: { one: "{count} розділ", few: "{count} розділи", many: "{count} розділів", other: "{count} розділу" },
};
const chapters = (count: number) => plural(count, chapterForms[getLocale()]);

/** summary is a row's "3 hr 5 min · 12 chapters", leaving out time the web reader never saw. */
const summary = (seconds: number, completed: number) => (seconds > 0 ? `${readingTime(seconds)} · ${chapters(completed)}` : chapters(completed));

/** ReadingStatsCard: the signed-in reader's own reading time and finished chapters. It loads on its own, so a slow or failed request leaves the rest of the account page alone. */
export function ReadingStatsCard() {
  const { data, error, isPending, isFetching, refetch } = useQuery({
    queryKey: ["me", "reading-stats"],
    queryFn: () => unwrap(api.GET("/api/v1/me/reading-stats")),
  });
  return (
    <Card title={t("Reading statistics")}>
      {isPending ? (
        <div role="status" aria-label={t("Loading reading statistics")} className="flex justify-center py-6">
          <Spinner />
        </div>
      ) : error ? (
        <div className="flex flex-col items-start gap-3">
          <p className="text-sm text-muted">{t("Could not load reading statistics")}</p>
          <ErrorBox error={error} />
          <Button size="sm" icon={<RotateCw className="size-3.5" />} loading={isFetching} onClick={() => void refetch()}>{t("Try again")}</Button>
        </div>
      ) : (
        <StatsBody stats={data} />
      )}
    </Card>
  );
}

function StatsBody({ stats }: { stats: Stats }) {
  const total = stats.totalActiveSeconds ?? 0;
  const completed = stats.completedChapters ?? 0;
  const languages = stats.languages ?? [];
  const genres = (stats.genres ?? []).slice(0, 8);
  if (total <= 0 && completed <= 0) {
    return <p className="text-sm text-muted">{t("Nothing read yet. Time in the web reader and the chapters you finish show up here.")}</p>;
  }
  const month = stats.activeMonth;
  const top = stats.topSeries;
  return (
    <div className="flex flex-col gap-5">
      <dl className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Tile icon={<Clock className="size-4" />} label={t("Reading time")} value={readingTime(total)} />
        <Tile icon={<BookCheck className="size-4" />} label={t("Chapters finished")} value={new Intl.NumberFormat(getLocale()).format(completed)} />
        <Tile
          icon={<CalendarDays className="size-4" />}
          label={t("Most active month")}
          value={month ? calendarMonth(month.month) : "—"}
          detail={month ? readingTime(month.activeSeconds) : undefined}
        />
        <Tile
          icon={<Trophy className="size-4" />}
          label={t("Most-read series")}
          value={top ? <Link to={`/series/${top.seriesId}`} title={top.title} className="hover:text-accent-2 hover:underline">{top.title}</Link> : "—"}
          detail={top ? summary(top.activeSeconds, top.completedChapters) : undefined}
        />
      </dl>
      {(languages.length > 0 || genres.length > 0) && (
        <div className="grid gap-5 md:grid-cols-2">
          {languages.length > 0 && <Breakdown title={t("Languages")} rows={languages.map((l) => ({ key: l.language, name: languageName(l.language), seconds: l.activeSeconds, completed: l.completedChapters }))} by="seconds" />}
          {genres.length > 0 && <Breakdown title={t("Genres")} rows={genres.map((g) => ({ key: g.genre, name: genreName(g.genre), seconds: g.activeSeconds, completed: g.completedChapters }))} by="completed" />}
        </div>
      )}
      <p className="text-xs text-muted">{t("Reading time counts the web reader only. Finished chapters also include progress from reading apps and library servers.")}</p>
    </div>
  );
}

function Tile({ icon, label, value, detail }: { icon: ReactNode; label: string; value: ReactNode; detail?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-md bg-panel-2 px-3 py-2.5">
      <dt className="flex items-center gap-1.5 text-xs text-muted">
        {icon}
        {label}
      </dt>
      <dd className="truncate text-lg font-semibold">{value}</dd>
      {detail && <dd className="text-xs text-muted">{detail}</dd>}
    </div>
  );
}

type Row = { key: string; name: string; seconds: number; completed: number };

/** Breakdown lists rows in the server's order, with bars sized by what that order ranks (time or chapters), falling back to the other when it is all zero. */
function Breakdown({ title, rows, by }: { title: string; rows: Row[]; by: "seconds" | "completed" }) {
  const max = (pick: (r: Row) => number) => Math.max(0, ...rows.map(pick));
  const primary = by === "seconds" ? (r: Row) => r.seconds : (r: Row) => r.completed;
  const secondary = by === "seconds" ? (r: Row) => r.completed : (r: Row) => r.seconds;
  const weight = max(primary) > 0 ? primary : secondary;
  const top = max(weight);
  return (
    <section aria-label={title}>
      <h3 className="mb-2 text-xs font-medium uppercase tracking-wide text-muted">{title}</h3>
      <ul className="flex flex-col gap-2.5">
        {rows.map((r) => (
          <li key={r.key} className="flex flex-col gap-1 text-sm">
            <div className="flex items-baseline justify-between gap-3">
              <span className="truncate">{r.name}</span>
              <span className="shrink-0 text-xs text-muted">{summary(r.seconds, r.completed)}</span>
            </div>
            <Progress value={top > 0 ? (weight(r) / top) * 100 : 0} />
          </li>
        ))}
      </ul>
    </section>
  );
}
