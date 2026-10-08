import { useEffect, useMemo, useState } from "react";
import { Clock } from "lucide-react";
import type { Chapter } from "../../api/client";
import { Modal } from "../../components/ui";
import { getLocale, t } from "../../lib/i18n/core";
import { date, dateTime, relative } from "../../lib/format";
import { releaseSchedule, type ReleaseSchedule } from "../../lib/releaseSchedule";

/** ReleaseLine: one quiet line on the title page saying when it last got a
 * chapter and when the next one is due; it opens the release schedule. */
export function ReleaseLine({ chapters, status }: { chapters?: Chapter[]; status: string }) {
  const now = useNow(60_000);
  const schedule = useMemo(() => releaseSchedule(chapters, status, now), [chapters, status, now]);
  const [open, setOpen] = useState(false);
  if (!schedule) return null;
  const next = nextLabel(schedule);
  const warn = schedule.state === "late" || schedule.state === "break";
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        title={t("Release schedule")}
        className="mt-2 inline-flex flex-wrap items-center gap-x-1.5 text-xs text-muted hover:text-fg"
      >
        <Clock className="size-3.5" />
        <span>{t("Updated {when}", { when: relative(new Date(schedule.last.at).toISOString()) })}</span>
        {next && (
          <>
            <span>·</span>
            <span className={warn ? "text-warn" : undefined}>{next}</span>
          </>
        )}
      </button>
      {open && <ReleaseModal schedule={schedule} now={now} onClose={() => setOpen(false)} />}
    </>
  );
}

function nextLabel(s: ReleaseSchedule): string | null {
  const when = s.next === undefined ? "" : relative(new Date(s.next).toISOString());
  switch (s.state) {
    case "regular":
      return t("next {when}", { when });
    case "irregular":
      return t("next ~{when}", { when });
    case "late":
      return t("next was due {when}", { when });
    case "break":
      return t("may be on a break");
    default:
      return null;
  }
}

function ReleaseModal({ schedule: s, now, onClose }: { schedule: ReleaseSchedule; now: number; onClose: () => void }) {
  const tick = useNow(1000);
  const lastIso = new Date(s.last.at).toISOString();
  return (
    <Modal open onClose={onClose} title={t("Release schedule")}>
      <div className="grid grid-cols-1 gap-4 text-sm sm:grid-cols-3">
        <Fact label={t("Last chapter")} value={t("Ch. {number} · {date}", { number: s.last.number, date: date(lastIso) })} note={relative(lastIso)} title={dateTime(lastIso)} />
        <Fact label={t("Next expected")} {...nextFact(s, tick)} />
        <Fact label={t("Usually")} {...cadenceFact(s)} />
      </div>
      {s.days.length > 1 && <Timeline s={s} now={now} />}
      <p className="mt-4 text-xs text-muted">
        {t("A guess from the gaps between recent release dates, so a break or a skipped week throws it off.")}
      </p>
    </Modal>
  );
}

function nextFact(s: ReleaseSchedule, now: number): { value: string; note?: string; warn?: boolean } {
  switch (s.state) {
    case "finished":
      return { value: t("none"), note: t("The series has ended") };
    case "few":
      return { value: t("not sure yet"), note: t("Needs a few more releases") };
    case "break":
      return { value: t("may be on a break"), note: t("No new chapter for a while"), warn: true };
  }
  const next = s.next!;
  const value = s.state === "irregular" ? t("around {date}", { date: date(new Date(next).toISOString()) }) : date(new Date(next).toISOString());
  if (s.state === "late") return { value, note: t("{time} late", { time: countdown(now - next) }), warn: true };
  return { value, note: t("in {time}", { time: countdown(next - now) }) };
}

function cadenceFact(s: ReleaseSchedule): { value: string; note?: string } {
  if (s.gapDays === undefined) return { value: "—" };
  const days = new Intl.NumberFormat(getLocale(), { style: "unit", unit: "day", unitDisplay: "long" });
  const value = s.spread ? days.formatRange(s.spread[0], s.spread[1]) : days.format(Math.max(1, Math.round(s.gapDays)));
  return { value: t("every {span}", { span: value }), note: t("from recent releases") };
}

function Fact({ label, value, note, warn, title }: { label: string; value: string; note?: string; warn?: boolean; title?: string }) {
  return (
    <div title={title}>
      <div className="text-xs text-muted">{label}</div>
      <div className={warn ? "font-medium text-warn" : "font-medium"}>{value}</div>
      {note && <div className="text-xs text-muted">{note}</div>}
    </div>
  );
}

/** Timeline: recent releases as dots on a line, with today and the expected next one. */
function Timeline({ s, now }: { s: ReleaseSchedule; now: number }) {
  const start = s.days[0];
  const end = Math.max(now, s.next ?? 0, s.days[s.days.length - 1]);
  const span = end - start || 1;
  const at = (ms: number) => `${((ms - start) / span) * 100}%`;
  return (
    <div className="relative mx-1.5 mt-5 h-10" aria-hidden="true">
      <div className="absolute inset-x-0 top-[9px] h-0.5 bg-border" />
      {s.days.map((d) => (
        <span key={d} title={date(new Date(d).toISOString())} className="absolute top-1 size-3 -translate-x-1/2 rounded-full bg-accent" style={{ left: at(d) }} />
      ))}
      {s.next !== undefined && s.state !== "break" && (
        <span className={`absolute top-0.5 size-3.5 -translate-x-1/2 rounded-full border-2 border-dashed ${s.state === "late" ? "border-warn" : "border-muted"}`} style={{ left: at(s.next) }} />
      )}
      <span className="absolute top-0 h-5 w-0.5 -translate-x-1/2 bg-muted" style={{ left: at(now) }} />
      <span className="absolute left-0 top-6 text-[11px] text-muted">{date(new Date(start).toISOString())}</span>
      <span className="absolute right-0 top-6 text-[11px] text-muted">{t("today")}</span>
    </div>
  );
}

/** countdown: "4d 18h", "3h 12m" or "45m", in the interface language. */
export function countdown(ms: number): string {
  const minutes = Math.max(0, Math.floor(ms / 60_000));
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  const unit = (n: number, u: "day" | "hour" | "minute") => new Intl.NumberFormat(getLocale(), { style: "unit", unit: u, unitDisplay: "narrow" }).format(n);
  if (days > 0) return `${unit(days, "day")} ${unit(hours, "hour")}`;
  if (hours > 0) return `${unit(hours, "hour")} ${unit(minutes % 60, "minute")}`;
  return unit(minutes, "minute");
}

function useNow(every: number) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), every);
    return () => window.clearInterval(id);
  }, [every]);
  return now;
}
