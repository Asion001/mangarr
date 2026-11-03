import { t } from "../../lib/i18n/core";
import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { ArrowLeft, ChevronLeft, ChevronRight } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { useSeries } from "../../api/queries";
import { Button, EmptyState, ErrorBox, Loading, Modal, PageHeader } from "../../components/ui";
import { useDocumentTitle } from "../../lib/documentTitle";
import type { Mood } from "../../lib/soundtrack/core";
import { moodName } from "../../lib/soundtrack/moods";
import { useMoods } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";
import { pageUrl } from "../reader/page";
import { SoundPicker } from "../reader/StartMood";

type Cue = S["SoundtrackCue"];

const grey = "#9aa2b1";

/** ranked is a page's moods, most likely first. */
function ranked(scores: Record<string, number> | undefined): { id: number; p: number }[] {
  return Object.entries(scores ?? {})
    .map(([id, p]) => ({ id: Number(id), p }))
    .sort((a, b) => b.p - a.p);
}

/**
 * PageMoodsPage reviews the moods found in a title's pages, a chapter at a
 * time: each page's strongest mood, where the music switches, and the
 * found cues to keep, change or turn down.
 */
export function PageMoodsPage() {
  const id = Number(useParams().id);
  const { data: series } = useSeries(id);
  useDocumentTitle(t("Page moods") + (series ? " · " + (series.displayTitle || series.title) : ""));
  const qc = useQueryClient();
  const toast = useToast();
  const moods = useMoods();
  const [chapter, setChapter] = useState<number | null>(null);
  const [picked, setPicked] = useState(1);
  const [choosing, setChoosing] = useState(false);
  const [busy, setBusy] = useState(false);
  const review = useQuery({
    queryKey: ["soundtrack", "page-moods", id, chapter],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/page-moods", { params: { path: { id }, query: chapter == null ? {} : { chapter: String(chapter) } } })),
  });
  const soundtrack = useQuery({ queryKey: ["soundtrack", "series", id], queryFn: () => unwrap(api.GET("/api/v1/series/{id}/soundtrack", { params: { path: { id } } })) });
  const strip = useRef<HTMLDivElement>(null);
  const detail = review.data?.chapter;
  useEffect(() => {
    strip.current?.querySelector(`[data-page="${picked}"]`)?.scrollIntoView({ block: "nearest", inline: "center", behavior: "smooth" });
  }, [picked, detail?.number]);

  if (review.isLoading || !moods.data) return <Loading />;
  if (review.error || !review.data) return <ErrorBox error={review.error} />;
  const moodById = new Map((moods.data ?? []).map((m) => [m.id, m]));
  const chapters = review.data.chapters;
  const at = detail ? chapters.findIndex((c) => c.number === detail.number) : -1;
  const label = (n: number) => soundtrack.data?.chapters.find((c) => c.number === n)?.label ?? String(n);
  const go = (n: number) => {
    setChapter(n);
    setPicked(1);
  };

  const header = (
    <PageHeader
      title={
        <span className="inline-flex items-center gap-2">
          <Link to={`/series/${id}/soundtrack`} className="rounded p-1 hover:bg-panel-2" aria-label={t("Back to the soundtrack")}>
            <ArrowLeft className="size-5" />
          </Link>
          {t("Page moods")}
        </span>
      }
      subtitle={series?.displayTitle || series?.title}
    />
  );
  if (!detail) {
    return (
      <div className="mx-auto flex max-w-3xl flex-col gap-4">
        {header}
        <EmptyState title={t("No pages scored yet")}>{t("Moods are found in a chapter's pages once it's downloaded and finding moods in pages is on (Settings › Soundtracks › Spotify & packs).")}</EmptyState>
      </div>
    );
  }

  // the cues in this chapter, by the page they start on (1: its start)
  const cues = new Map<number, Cue>();
  for (const c of soundtrack.data?.shared?.cues ?? []) if (c.chapterNumber === detail.number && c.moodId) cues.set(c.page ?? 1, c);
  const found = [...cues.values()].filter((c) => c.source === "auto");
  const pages = Math.max(detail.scores.length, detail.pages);
  const top = (n: number) => ranked(detail.scores[n - 1])[0];
  const colorOf = (n: number) => {
    const best = top(n);
    return best ? (moodById.get(best.id)?.color ?? grey) : grey;
  };
  const opacity = (n: number) => 0.35 + (top(n)?.p ?? 0) * 0.65;
  const inChapter = [...new Set(Array.from({ length: pages }, (_, i) => top(i + 1)?.id).filter((x): x is number => !!x))]
    .map((mid) => moodById.get(mid))
    .filter((m): m is Mood => !!m);
  const carry = detail.carryIn ? moodById.get(detail.carryIn) : undefined;

  const cue = cues.get(picked);
  const cueMood = cue?.moodId ? moodById.get(cue.moodId) : undefined;
  const dismissed = detail.dismissed.includes(picked > 1 ? picked : 0);
  const best = ranked(detail.scores[picked - 1]).slice(0, 3);
  const bestMood = best[0] ? moodById.get(best[0].id) : undefined;
  const change = detail.changes.find((c) => (c.page > 1 ? c.page : 1) === picked);
  const say = cue
    ? cue.source === "auto"
      ? t("{mood} leads from here, so the music switches here.", { mood: cueMood ? moodName(cueMood) : "" })
      : t("A cue someone set starts {mood} here.", { mood: cueMood ? moodName(cueMood) : "" })
    : dismissed
      ? t("You said no found mood starts here.")
      : change
        ? t("{mood} takes over here, but a cue someone set nearby wins.", { mood: moodById.get(change.moodId) ? moodName(moodById.get(change.moodId)!) : "" })
        : bestMood
          ? t("Mostly {mood}: the music stays as it is.", { mood: moodName(bestMood) })
          : t("This page wasn't scored.");

  const refresh = () => void qc.invalidateQueries({ queryKey: ["soundtrack"] });
  const run = async (fn: () => Promise<unknown>, done?: string) => {
    setBusy(true);
    try {
      await fn();
      refresh();
      if (done) toast.success(done);
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  const setCue = (page: number, moodId?: number) =>
    unwrap(
      api.PUT("/api/v1/series/{id}/soundtrack/cue", {
        params: { path: { id } },
        body: { chapterNumber: detail.number, page: page > 1 ? page : undefined, pageCount: page > 1 ? pages : undefined, moodId },
      }),
    );
  const keep = (c: Cue) => setCue(c.page ?? 1, c.moodId);
  const keepAll = () => run(async () => {
    for (const c of found) await keep(c);
  }, t("Kept {count} cues", { count: found.length }));
  const notHere = () => run(() => unwrap(api.POST("/api/v1/series/{id}/page-moods/dismiss", { params: { path: { id } }, body: { chapterNumber: detail.number, page: picked > 1 ? picked : 0 } })));

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      {header}

      <div className="flex items-center gap-1 rounded-lg border border-border bg-panel p-0.5">
        <button type="button" className="press flex size-11 items-center justify-center rounded disabled:opacity-40" disabled={at <= 0} onClick={() => go(chapters[at - 1].number)} aria-label={t("Previous chapter")}>
          <ChevronLeft className="size-5" />
        </button>
        <div className="min-w-0 flex-1 text-center">
          <div className="truncate text-sm font-semibold">{t("Ch.") + " " + label(detail.number)}</div>
          <div className="truncate text-[11px] text-muted">
            {!detail.scored
              ? t("Not scored yet")
              : [t("{count} pages", { count: pages }), t("{count} found cues", { count: found.length }), carry ? t("starts {mood}", { mood: moodName(carry) }) : ""].filter(Boolean).join(" · ")}
          </div>
        </div>
        <button type="button" className="press flex size-11 items-center justify-center rounded disabled:opacity-40" disabled={at < 0 || at >= chapters.length - 1} onClick={() => go(chapters[at + 1].number)} aria-label={t("Next chapter")}>
          <ChevronRight className="size-5" />
        </button>
      </div>

      {detail.scored && (
        <>
          <div className="flex flex-col gap-1.5">
            <div className="text-xs text-muted">{t("Strongest mood per page (fainter: less sure)")}</div>
            <div className="flex h-7 gap-0.5">
              {Array.from({ length: pages }, (_, i) => i + 1).map((n) => (
                <button
                  key={n}
                  type="button"
                  onClick={() => setPicked(n)}
                  aria-label={t("Page {page}", { page: n })}
                  aria-pressed={picked === n}
                  className={clsx("min-w-0 flex-1 rounded-sm", picked === n && "outline-2 outline-offset-1 outline-fg")}
                  style={{ background: colorOf(n), opacity: opacity(n) }}
                />
              ))}
            </div>
            <div className="flex h-3.5 gap-0.5 text-[10px] leading-[14px]" aria-hidden="true">
              {Array.from({ length: pages }, (_, i) => i + 1).map((n) => {
                const c = cues.get(n);
                return (
                  <span key={n} className={clsx("min-w-0 flex-1 text-center", c?.source === "auto" ? "text-accent-2" : "text-fg")}>
                    {c ? (c.source === "auto" ? "▲" : "●") : ""}
                  </span>
                );
              })}
            </div>
            <div className="flex flex-wrap gap-3 text-[11px] text-muted">
              {inChapter.map((m) => (
                <span key={m.id} className="inline-flex items-center gap-1.5">
                  <span className="size-2.5 rounded-sm" style={{ background: m.color }} />
                  {moodName(m)}
                </span>
              ))}
              <span className="text-accent-2">{"▲ " + t("found cue")}</span>
              <span className="text-fg">{"● " + t("cue someone set")}</span>
            </div>
          </div>

          <div ref={strip} className="flex gap-2 overflow-x-auto pb-1">
            {Array.from({ length: pages }, (_, i) => i + 1).map((n) => (
              <button
                key={n}
                type="button"
                data-page={n}
                onClick={() => setPicked(n)}
                aria-label={t("Page {page}", { page: n })}
                aria-pressed={picked === n}
                className={clsx("flex w-14 shrink-0 flex-col overflow-hidden rounded-md border bg-panel", picked === n ? "border-2 border-accent" : "border-border")}
              >
                <img src={pageUrl(detail.id, n, 160)} alt="" loading="lazy" className="h-20 w-full bg-panel-2 object-cover object-top" />
                <span className="h-1.5 w-full" style={{ background: colorOf(n), opacity: opacity(n) }} />
                <span className="py-0.5 text-[10px] text-muted">{n}</span>
              </button>
            ))}
          </div>

          <div className="flex flex-col gap-3 rounded-xl border border-border bg-panel p-3.5">
            <div className="flex items-baseline gap-2">
              <h2 className="flex-1 text-[15px] font-semibold">{t("Page {page}", { page: picked })}</h2>
              {cue?.source === "auto" && cue.confidence != null && (
                <span className="rounded-full border border-info px-2 py-0.5 text-[11px] text-info">{t("Auto") + " · " + Math.round(cue.confidence * 100) + "%"}</span>
              )}
            </div>
            <p className="text-[13px] text-muted">{say}</p>
            <div className="flex flex-col gap-2">
              {best.map((b) => {
                const m = moodById.get(b.id);
                return (
                  <div key={b.id} className="flex items-center gap-2.5 text-[13px]">
                    <span className="w-24 truncate">{m ? moodName(m) : "?"}</span>
                    <span className="h-2 flex-1 overflow-hidden rounded-full bg-panel-2">
                      <span className="block h-full" style={{ width: `${Math.round(b.p * 100)}%`, background: m?.color ?? grey }} />
                    </span>
                    <span className="w-9 text-right text-muted tabular-nums">{Math.round(b.p * 100) + "%"}</span>
                  </div>
                );
              })}
            </div>
            <div className="flex flex-wrap gap-2">
              {cue?.source === "auto" ? (
                <>
                  <Button variant="primary" className="flex-1" disabled={busy} onClick={() => void run(() => keep(cue), t("Kept"))}>
                    {t("Keep")}
                  </Button>
                  <Button className="flex-1" disabled={busy} onClick={() => setChoosing(true)}>
                    {t("Other mood…")}
                  </Button>
                  <Button className="flex-1" disabled={busy} onClick={() => void notHere()}>
                    {t("Not here")}
                  </Button>
                </>
              ) : cue ? (
                <>
                  <Button className="flex-1" disabled={busy} onClick={() => setChoosing(true)}>
                    {t("Other mood…")}
                  </Button>
                  <Button className="flex-1" disabled={busy} onClick={() => void run(() => setCue(picked))}>
                    {t("Remove the cue")}
                  </Button>
                </>
              ) : (
                <>
                  <Button className="flex-1" disabled={busy} onClick={() => setChoosing(true)}>
                    {t("Start a mood here…")}
                  </Button>
                  <Link to={`/read/${detail.id}?page=${picked}`} className="flex h-10 flex-1 items-center justify-center rounded-md border border-border text-sm font-medium hover:bg-panel-2">
                    {t("Open in the reader")}
                  </Link>
                </>
              )}
            </div>
          </div>

          {found.length > 0 && (
            <div className="flex flex-col gap-2">
              <Button disabled={busy} onClick={() => void keepAll()}>
                {t("Keep all {count} found cues in this chapter", { count: found.length })}
              </Button>
              <p className="text-center text-[11px] leading-relaxed text-muted">
                {t("Keep turns a found cue into a normal one, so a new scan won't move it. Not here removes it and stops mangarr placing it again.")}
              </p>
            </div>
          )}
        </>
      )}

      {choosing && (
        <Modal open onClose={() => setChoosing(false)} title={t("Mood from page {page}", { page: picked })}>
          <SoundPicker
            busy={busy}
            onPick={(p) =>
              void run(async () => {
                await setCue(picked, p.moodId);
                setChoosing(false);
              })
            }
          />
        </Modal>
      )}
    </div>
  );
}
