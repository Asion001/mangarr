import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import clsx from "clsx";
import { Star, X } from "lucide-react";
import type { S, Series } from "../../api/client";
import { IconButton } from "../../components/ui";
import { getLocale, t } from "../../lib/i18n/core";
import { languageName } from "../../lib/format";

type Rating = S["Rating"];

// providerNames are the metadata providers' names, as their links are keyed.
const providerNames: Record<string, string> = {
  anilist: "AniList",
  mangaupdates: "MangaUpdates",
  mangadex: "MangaDex",
  kitsu: "Kitsu",
  shikimori: "Shikimori",
};

/** scoreText is a rating in its site's own scale: "86%" or "8.5". */
export function scoreText(r: Rating): string {
  if (r.max === 100) return `${Math.round(r.score)}%`;
  return new Intl.NumberFormat(getLocale(), { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(r.score);
}

/** outOf10 is a score out of 10 with one decimal: "8.6". */
export function outOf10(score: number): string {
  return new Intl.NumberFormat(getLocale(), { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(score);
}

function votesText(votes?: number): string {
  if (!votes) return "";
  return new Intl.NumberFormat(getLocale(), { notation: "compact", maximumFractionDigits: 1 }).format(votes);
}

/** averageScore is the providers' scores averaged out of 10, like the server's. */
export function averageScore(ratings: Record<string, Rating> | undefined): number | undefined {
  const list = Object.values(ratings ?? {}).filter((r) => r.max > 0 && r.score > 0 && (!r.votes || r.votes >= 20));
  if (list.length === 0) return undefined;
  return list.reduce((sum, r) => sum + (r.score * 10) / r.max, 0) / list.length;
}

/** ScoreBadge is a title's one score: a star and "8.6". */
export function ScoreBadge({ score, className }: { score: number; className?: string }) {
  return (
    <span className={clsx("inline-flex items-center gap-0.5 font-semibold tabular-nums", className)} title={t("Rating")}>
      <Star className="size-3 fill-accent text-accent" aria-hidden />
      {outOf10(score)}
    </span>
  );
}

/** RatingPill is one provider's score, in its own scale, with its vote count. */
export function RatingPill({ rating, className }: { rating?: Rating | null; className?: string }) {
  if (!rating || !(rating.score > 0)) return null;
  const votes = votesText(rating.votes);
  return (
    <span
      className={clsx("inline-flex items-center gap-1 whitespace-nowrap rounded-full border border-accent/60 px-2 text-xs font-semibold tabular-nums", className)}
      title={rating.votes ? t("{count} ratings", { count: rating.votes }) : t("Rating")}
    >
      <Star className="size-2.5 fill-accent text-accent" aria-hidden />
      {scoreText(rating)}
      {votes && <span className="font-normal text-muted">· {votes}</span>}
    </span>
  );
}

type Row = { key: string; name: string; note?: string; rating?: Rating; url?: string };

/**
 * RatingChip is the title's score in its facts line. It opens what each
 * metadata provider and each followed source scores it, each linking to
 * the title on that site: a popover on wide screens, a sheet on a phone.
 */
export function RatingChip({ series: s }: { series: Series }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [shift, setShift] = useState(0);
  const titleId = useId();
  useLayoutEffect(() => {
    const el = panel.current;
    if (!open || !el || window.matchMedia("(max-width: 639px)").matches) return setShift(0);
    const over = el.getBoundingClientRect().right - (document.documentElement.clientWidth - 8);
    setShift(over > 0 ? -over : 0);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        ref.current?.querySelector<HTMLButtonElement>("button")?.focus();
      }
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("keydown", key);
    };
  }, [open]);

  const md = s.metadata;
  const links = md.links ?? {};
  const providers: Row[] = Object.entries(md.ratings ?? {})
    .map(([key, rating]) => {
      const name = providerNames[key] ?? key;
      return { key, name, rating, url: links[name] };
    })
    .sort((a, b) => b.rating.score / b.rating.max - a.rating.score / a.rating.max);
  // MyAnimeList shares no score without an API key: a link only
  if (links.MyAnimeList && !md.ratings?.mal) providers.push({ key: "mal", name: "MyAnimeList", url: links.MyAnimeList });
  const sources: Row[] = (s.sources ?? [])
    .filter((l) => l.rating)
    .map((l) => ({ key: `source-${l.id}`, name: l.sourceName, note: l.lang ? languageName(l.lang) : undefined, rating: l.rating ?? undefined, url: l.webUrl || undefined }));
  const score = s.score ?? averageScore(md.ratings);
  if (score === undefined && sources.length === 0) return null;
  return (
    <div ref={ref} className="relative inline-flex items-center gap-2.5">
      <span className="text-muted/60" aria-hidden="true">·</span>
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="inline-flex h-7 items-center rounded-md bg-panel-2 px-1.5 text-fg hover:bg-border sm:h-6"
      >
        {score !== undefined ? <ScoreBadge score={score} /> : <Star className="size-3 text-muted" aria-label={t("Rating")} />}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40 bg-black/50 sm:hidden" aria-hidden onClick={() => setOpen(false)} />
          <div
            ref={panel}
            role="dialog"
            aria-labelledby={titleId}
            style={shift ? { transform: `translateX(${shift}px)` } : undefined}
            className={clsx(
              "z-50 flex flex-col gap-0.5 border border-border bg-panel p-2 text-sm text-fg shadow-2xl",
              "fixed inset-x-0 bottom-0 max-h-[75vh] overflow-y-auto rounded-t-2xl pb-6",
              "sm:absolute sm:inset-x-auto sm:bottom-auto sm:left-0 sm:top-full sm:mt-2 sm:max-h-[60vh] sm:w-72 sm:rounded-xl sm:pb-2",
            )}
          >
            <div className="mx-auto mb-2 h-1 w-9 rounded-full bg-border sm:hidden" aria-hidden />
            <div className="flex items-center justify-between px-2.5 pb-1">
              <h2 id={titleId} className="text-xs font-normal text-muted">{t("Ratings")}</h2>
              <IconButton title={t("Close")} className="sm:hidden" onClick={() => setOpen(false)}>
                <X className="size-4" />
              </IconButton>
            </div>
            {providers.map((r) => <RatingRow key={r.key} row={r} />)}
            {sources.length > 0 && (
              <>
                <div className="mt-1 border-t border-border px-2.5 pb-1 pt-2 text-xs text-muted">{t("Sources you follow")}</div>
                {sources.map((r) => <RatingRow key={r.key} row={r} />)}
              </>
            )}
            <p className="mt-1 border-t border-border px-2.5 pt-2 text-xs text-muted">{t("Each row opens the title on that site.")}</p>
          </div>
        </>
      )}
    </div>
  );
}

function RatingRow({ row }: { row: Row }) {
  const body = (
    <>
      <span className="min-w-0 flex-1 truncate">
        {row.name}
        {row.note && <span className="ml-1.5 text-xs text-muted">{row.note}</span>}
      </span>
      {row.rating ? (
        <>
          <b className="font-semibold tabular-nums">{scoreText(row.rating)}</b>
          <span className="w-10 text-right text-xs tabular-nums text-muted" title={row.rating.votes ? t("{count} ratings", { count: row.rating.votes }) : undefined}>
            {votesText(row.rating.votes)}
          </span>
        </>
      ) : (
        <span className="text-xs text-muted">↗</span>
      )}
    </>
  );
  const cls = "flex min-h-9 items-center gap-2 rounded-md px-2.5 py-1 sm:min-h-7";
  return row.url ? (
    <a href={row.url} target="_blank" rel="noreferrer" className={clsx(cls, "hover:bg-panel-2", !row.rating && "text-muted")}>
      {body}
    </a>
  ) : (
    <div className={cls}>{body}</div>
  );
}
