import { t as tr, t } from "../../lib/i18n/core";
import { useDocumentTitle } from "../../lib/documentTitle";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, ChevronLeft, ChevronRight, Download, List, Maximize, Minimize, Minus, Pause, Play, Plus, Settings2 } from "lucide-react";
import clsx from "clsx";
import { apiUrl, basePath, type S } from "../../api/client";
import { ErrorBox, Spinner } from "../../components/ui";
import { AUTO_SCROLL_MAX, AUTO_SCROLL_MIN, useReaderSettings } from "./settings";
import { displayWidth, pageUrl, useDims, useViewport, type Half } from "./page";
import { chapterQuery } from "./query";
import { buildViews, indexOf, pageLayout, PagedViewer } from "./PagedViewer";
import { WebtoonViewer } from "./WebtoonViewer";
import { SettingsPanel } from "./SettingsPanel";
import { ChapterPicker } from "./ChapterPicker";
import { ImagePreloader, useImagePreload } from "./preload";
import { useReadingTime } from "./time";
import { TapZones } from "./TapZones";
import { canFullscreen, needsHomeScreen, toggleFullscreen as toggleFull, useFullscreen } from "./fullscreen";
import { useToast } from "../../lib/toast";

/** ReaderPage is the full-screen web reader (/read/:id). */
export function ReaderPage() {
  const { id } = useParams();
  // every navigation opens afresh: the webtoon strip moves the address on
  // by itself, so the same id can mean a different chapter than it shows
  const { key } = useLocation();
  const preloader = useMemo(() => new ImagePreloader(), []);
  useEffect(() => () => preloader.clear(), [preloader]);
  return <Reader key={`${id}-${key}`} chapterId={Number(id)} preloader={preloader} />;
}

type Pos = { page: number; half?: Half; edge?: "start" | "end" };
type Chapter = S["ReadChapter"];

function Reader({ chapterId, preloader }: { chapterId: number; preloader: ImagePreloader }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [search] = useSearchParams();
  // the chapter on screen: the one opened, until the webtoon strip scrolls
  // on into another. The strip hands that chapter over itself: asking the
  // cache again could find it gone after a while, and the reader would show
  // a spinner and reopen the strip at its top.
  const [active, setActive] = useState<Chapter | null>(null);
  const { data: opened, error } = useQuery(chapterQuery(chapterId));
  const ch = active ?? opened;
  useReadingTime(ch?.id ?? 0);
  const { settings: s, set, saveAsDefault, reset, hasOwn, loading: settingsLoading } = useReaderSettings(ch?.seriesId ?? 0, ch?.readingDirection ?? "");
  const { dims, need, natural } = useDims(ch?.id ?? chapterId, s.crop || s.splitWide || (s.mode === "paged" && s.spread !== "single"));
  const vp = useViewport();
  const views = useMemo(() => (ch ? buildViews(ch.pages.length, dims, s, vp.w > vp.h) : []), [ch, dims, s, vp.w, vp.h]);
  const [pos, setPos] = useState<Pos | null>(null);
  const [bars, setBars] = useState(true);
  const [panel, setPanel] = useState(false);
  const [pickingChapter, setPickingChapter] = useState(false);
  const full = useFullscreen();
  const toast = useToast();
  // webtoon auto scroll: on until paused or the strip runs out; it holds
  // while the settings or the chapter list are open
  const [auto, setAuto] = useState(false);
  const webtoon = s.mode === "webtoon";
  const autoOn = auto && webtoon;
  const autoStarted = useRef(false);
  useEffect(() => {
    if (settingsLoading || !ch || autoStarted.current) return;
    autoStarted.current = true;
    if (s.autoScrollOnOpen && webtoon) setAuto(true);
  }, [settingsLoading, ch, s.autoScrollOnOpen, webtoon]);
  const speedBy = useCallback(
    (d: number) => set({ autoScrollSpeed: Math.min(AUTO_SCROLL_MAX, Math.max(AUTO_SCROLL_MIN, s.autoScrollSpeed + d)) }),
    [set, s.autoScrollSpeed],
  );
  const startAuto = useCallback(() => {
    setAuto((a) => !a);
    setBars(false);
  }, []);

  // where to start: ?page=, else where you left off
  useEffect(() => {
    if (!ch || pos) return;
    const n = ch.pages.length;
    const q = search.get("page");
    let p = 1;
    if (q === "last") p = n;
    else if (q && Number(q) > 0) p = Math.min(Number(q), n);
    else if (ch.progress.page > 0 && !ch.progress.completed) p = Math.min(ch.progress.page, n);
    setPos({ page: Math.max(p, 1) });
  }, [ch, pos, search]);

  const index = !pos ? 1 : pos.edge ? (pos.edge === "start" ? 0 : views.length - 1) : indexOf(views, pos.page, pos.half);
  const view = views[index];
  const setIndex = useCallback(
    (i: number) => {
      const v = views[i];
      if (!v) return;
      if ("edge" in v) setPos((p) => ({ page: p?.page ?? 1, edge: v.edge }));
      else setPos({ page: v.pages[0], half: v.half });
    },
    [views],
  );
  const [webPage, setWebPage] = useState(0);
  const count = ch?.pages.length ?? 0;
  // the page you're on (for progress): the last one on screen
  const page = !ch
    ? 0
    : s.mode === "webtoon"
      ? webPage || pos?.page || 1
      : view && "pages" in view
        ? Math.max(...view.pages)
        : pos?.edge === "end"
          ? count
          : pos?.page ?? 1;

  // bounds for the pages around the current one (paged mode)
  useEffect(() => {
    if (!ch || s.mode !== "paged") return;
    const around: number[] = [];
    for (let k = Math.max(1, page - 2); k <= Math.min(count, page + s.preload + 1); k++) around.push(k);
    need(around);
  }, [ch, page, count, s.mode, s.preload, need]);

  // Preload the exact image sizes the viewer will request. Keep an in-flight
  // page when it becomes visible, and cancel pages skipped by a fast jump.
  const imagePreloads = useMemo(() => {
    if (!ch || s.mode !== "paged") return { load: [] as string[], retain: [] as string[] };
    const retain = view && "pages" in view
      ? view.pages.map((p) => pageUrl(ch.id, p, displayWidth(pageLayout(p, view, dims, s, vp).w)))
      : [];
    const load: string[] = [];
    const seen = new Set(retain);
    for (let i = index + 1; i < views.length && load.length < s.preload; i++) {
      const candidate = views[i];
      if (!("pages" in candidate)) continue;
      for (const p of candidate.pages) {
        const url = pageUrl(ch.id, p, displayWidth(pageLayout(p, candidate, dims, s, vp).w));
        if (!seen.has(url)) {
          seen.add(url);
          load.push(url);
        }
        if (load.length >= s.preload) break;
      }
    }
    if (ch.next && page >= count - 2) {
      // the next chapter's first screens as its viewer will lay them out
      // before it knows the page sizes, so it asks for these same images
      const nextViews = buildViews(3, {}, s, vp.w > vp.h);
      for (const v of nextViews) {
        if (!("pages" in v)) continue;
        for (const p of v.pages) if (p <= 2) load.push(pageUrl(ch.next.id, p, displayWidth(pageLayout(p, v, {}, s, vp).w)));
      }
    }
    return { load, retain };
  }, [ch, s, view, index, views, dims, vp, page, count]);
  useImagePreload(imagePreloads.load, imagePreloads.retain, preloader);

  // Near the end, also have the next chapter's metadata ready.
  useEffect(() => {
    if (ch?.next && s.mode === "paged" && page >= count - 2) void qc.prefetchQuery(chapterQuery(ch.next.id));
  }, [ch, page, count, s.mode, qc]);

  // save progress (debounced; right away when leaving)
  const saved = useRef(0);
  const pending = useRef(0);
  const flush = useCallback(() => {
    const p = pending.current;
    if (!ch || !p || p === saved.current) return;
    saved.current = p;
    void fetch(`${basePath}/api/v1/read/chapters/${ch.id}/progress`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ page: p }),
      keepalive: true,
    }).then(() => {
      qc.invalidateQueries({ queryKey: ["readers"] });
      qc.invalidateQueries({ queryKey: ["series"] });
    });
  }, [ch, qc]);
  useEffect(() => {
    if (!ch || !page || !pos) return;
    pending.current = page;
    if (page === count) return flush(); // finished
    const t = window.setTimeout(flush, 1500);
    return () => window.clearTimeout(t);
  }, [ch, page, count, pos, flush]);
  useEffect(() => {
    const hide = () => document.visibilityState === "hidden" && flush();
    document.addEventListener("visibilitychange", hide);
    return () => {
      document.removeEventListener("visibilitychange", hide);
      flush();
    };
  }, [flush]);

  // The webtoon strip runs on into the next chapter: once its divider
  // scrolls past, the chapter before is read and the next one takes over
  // the bars, progress and address.
  const onWebPage = useCallback(
    (to: Chapter, n: number) => {
      if (ch && to.id !== ch.id) {
        if (ch.next?.id === to.id) pending.current = ch.pages.length;
        flush();
        pending.current = 0;
        saved.current = 0;
        setActive(to);
        setPos({ page: n });
        window.history.replaceState(window.history.state, "", `${basePath}/read/${to.id}`);
      }
      setWebPage(n);
    },
    [ch, flush],
  );

  // keep the screen on
  useEffect(() => {
    if (!s.keepAwake || !("wakeLock" in navigator)) return;
    let lock: WakeLockSentinel | undefined;
    const get = () => {
      if (document.visibilityState === "visible") navigator.wakeLock.request("screen").then((l) => (lock = l), () => undefined);
    };
    get();
    document.addEventListener("visibilitychange", get);
    return () => {
      document.removeEventListener("visibilitychange", get);
      void lock?.release();
    };
  }, [s.keepAwake]);

  // bars hide on their own after opening
  useEffect(() => {
    const t = window.setTimeout(() => setBars(false), 2500);
    return () => window.clearTimeout(t);
  }, [chapterId]);

  // Like Mihon, show the tap zones for a moment when a chapter opens and
  // when they change; any tap hides them early.
  const [zones, setZones] = useState(false);
  const zonesKey = `${s.tapZones} ${s.invertTaps} ${s.mode} ${s.direction}`;
  useEffect(() => {
    if (settingsLoading || !s.showTapZones || s.tapZones === "off") return setZones(false);
    setZones(true);
    const t = window.setTimeout(() => setZones(false), 3000);
    const hide = () => setZones(false);
    window.addEventListener("pointerdown", hide, { once: true });
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("pointerdown", hide);
    };
  }, [chapterId, zonesKey, s.showTapZones, s.tapZones, settingsLoading]);

  useDocumentTitle(ch ? `${ch.seriesTitle} ch. ${ch.number}` : undefined);

  const goChapter = useCallback(
    (dir: "prev" | "next") => {
      const target = dir === "next" ? ch?.next : ch?.prev;
      if (!ch) return;
      if (!target) {
        // past the last chapter's end: back to the series
        if (dir === "next") {
          flush();
          navigate(`/series/${ch.seriesId}`);
        }
        return;
      }
      flush();
      navigate(`/read/${target.id}${dir === "prev" ? "?page=last" : ""}`, { replace: true });
    },
    [ch, flush, navigate],
  );

  // global keys
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement) return;
      if (e.key === "Escape") {
        if (panel) setPanel(false);
        else if (ch) navigate(`/series/${ch.seriesId}`);
      } else if (e.key === "m") setBars((b) => !b);
      else if (e.key === "f") toggleFull();
      else if (e.key === "n" && ch?.next) goChapter("next");
      else if (e.key === "p" && ch?.prev) goChapter("prev");
      else if (webtoon && e.key === "a") setAuto((a) => !a);
      else if (webtoon && (e.key === "+" || e.key === "=")) speedBy(1);
      else if (webtoon && e.key === "-") speedBy(-1);
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [panel, ch, navigate, goChapter, webtoon, speedBy]);

  const bg = s.background === "white" ? "bg-white" : s.background === "gray" ? "bg-reader-gray" : "bg-black";
  if (error) {
    return (
      <div className="flex h-dvh flex-col items-center justify-center gap-4 bg-black p-6 text-fg">
        <ErrorBox error={error} />
        <button type="button" className="text-sm text-accent-2 hover:underline" onClick={() => navigate(-1)}>{t("Go back")}</button>
      </div>
    );
  }
  if (!ch || !pos || settingsLoading) {
    return (
      <div className="flex h-dvh items-center justify-center bg-black text-muted">
        <Spinner />
      </div>
    );
  }
  const pageViews = views.filter((v) => "pages" in v).length;
  const sliderIndex = Math.min(Math.max(index, 1), pageViews);

  return (
    <div className={clsx("fixed inset-0 select-none overflow-hidden", bg)}>
      {s.mode === "webtoon" ? (
        <WebtoonViewer
          chapterId={ch.id}
          settings={s}
          startPage={pos.page}
          onPage={onWebPage}
          wantBounds={s.crop || s.splitWide}
          onChapter={goChapter}
          onMenu={() => setBars((b) => !b)}
          autoScroll={autoOn && !panel && !pickingChapter}
          onAutoScrollEnd={() => setAuto(false)}
        />
      ) : (
        <PagedViewer
          chapter={ch}
          settings={s}
          views={views}
          index={index}
          setIndex={setIndex}
          dims={dims}
          natural={natural}
          onChapter={goChapter}
          onMenu={() => setBars((b) => !b)}
        />
      )}

      {zones && <TapZones s={s} />}

      {/* top bar */}
      <div
        className={clsx(
          "absolute inset-x-0 top-0 z-20 flex items-center gap-2 bg-bg/90 px-2 py-2 text-fg backdrop-blur transition-transform",
          bars ? "translate-y-0" : "-translate-y-full",
        )}
        style={{ paddingTop: "max(0.5rem, var(--safe-top))", paddingLeft: "max(0.5rem, env(safe-area-inset-left))", paddingRight: "max(0.5rem, env(safe-area-inset-right))" }}
      >
        <Link to={`/series/${ch.seriesId}`} className="rounded p-2 hover:bg-panel-2" aria-label={t("Back to the series")}>
          <ArrowLeft className="size-5" />
        </Link>
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-medium">{ch.seriesTitle}</div>
          <div className="truncate text-xs text-muted">{t("Ch.") + " "}{ch.number}
            {ch.title && ch.title !== ch.number && !ch.title.endsWith(ch.number) ? ` · ${ch.title}` : ""}
            {!ch.downloaded && tr(" · streamed")}
          </div>
        </div>
        {ch.canDownload && (
          <a href={apiUrl(`api/v1/read/chapters/${ch.id}/file`)} download className="rounded p-2 hover:bg-panel-2" aria-label={t("Download the chapter")}>
            <Download className="size-5" />
          </a>
        )}
        <button type="button" className="rounded p-2 hover:bg-panel-2" onClick={() => setPickingChapter(true)} aria-label={t("Choose chapter")}>
          <List className="size-5" />
        </button>
        {needsHomeScreen() && (
          <button
            type="button"
            className="rounded p-2 hover:bg-panel-2"
            onClick={() => toast.info(t("iPhone browsers can't go full screen"), t("Tap Share, then Add to Home Screen, and open mangarr from there to read without the browser bars."))}
            aria-label={t("Full screen")}
          >
            <Maximize className="size-5" />
          </button>
        )}
        {canFullscreen() && (
          <button type="button" className="rounded p-2 hover:bg-panel-2" onClick={toggleFull} aria-label={t("Full screen")}>
            {full ? <Minimize className="size-5" /> : <Maximize className="size-5" />}
          </button>
        )}
        <button type="button" className="rounded p-2 hover:bg-panel-2" onClick={() => setPanel((p) => !p)} aria-label={t("Reader settings")}>
          <Settings2 className="size-5" />
        </button>
      </div>

      {/* bottom bar */}
      <div
        className={clsx(
          "absolute inset-x-0 bottom-0 z-20 flex items-center gap-2 bg-bg/90 px-2 py-2 text-fg backdrop-blur transition-transform",
          bars ? "translate-y-0" : "translate-y-full",
        )}
        style={{ paddingBottom: "max(0.5rem, env(safe-area-inset-bottom))", paddingLeft: "max(0.5rem, env(safe-area-inset-left))", paddingRight: "max(0.5rem, env(safe-area-inset-right))" }}
      >
        <button
          type="button"
          className="rounded p-2 hover:bg-panel-2 disabled:opacity-30"
          disabled={!ch.prev}
          onClick={() => goChapter("prev")}
          aria-label={t("Previous chapter")}
        >
          <ChevronLeft className="size-5" />
        </button>
        {s.mode === "paged" ? (
          <input
            type="range"
            min={1}
            max={Math.max(pageViews, 1)}
            value={sliderIndex}
            onChange={(e) => setIndex(Number(e.target.value))}
            className="flex-1 accent-accent"
            style={{ direction: s.direction === "rtl" ? "rtl" : "ltr" }}
            aria-label={t("Page")}
          />
        ) : (
          <>
            <button
              type="button"
              className={clsx("flex h-9 items-center gap-1.5 rounded-full pl-2.5 pr-3 text-sm font-medium", autoOn ? "border border-accent text-accent-2" : "bg-primary text-white hover:bg-primary-hover")}
              onClick={startAuto}
              aria-label={autoOn ? t("Pause auto scroll") : t("Start auto scroll")}
            >
              {autoOn ? <Pause className="size-4" fill="currentColor" /> : <Play className="size-4" fill="currentColor" />}
              {t("Auto")}
            </button>
            <AutoSpeed speed={s.autoScrollSpeed} by={speedBy} className="h-9 rounded-full border border-border" />
            <div className="flex-1" />
          </>
        )}
        <span className="w-16 text-center text-xs tabular-nums text-fg/80">
          {page} / {count}
        </span>
        <button
          type="button"
          className="rounded p-2 hover:bg-panel-2 disabled:opacity-30"
          disabled={!ch.next}
          onClick={() => goChapter("next")}
          aria-label={t("Next chapter")}
        >
          <ChevronRight className="size-5" />
        </button>
      </div>

      {!bars && autoOn && (
        <div
          className="absolute bottom-2 left-1/2 z-10 flex h-11 -translate-x-1/2 items-center gap-0.5 whitespace-nowrap rounded-full bg-black/70 pl-1 pr-3 text-fg"
          style={{ marginBottom: "env(safe-area-inset-bottom)" }}
          role="group"
          aria-label={t("Auto scroll")}
          onClick={(e) => e.stopPropagation()}
          onPointerUp={(e) => e.stopPropagation()}
        >
          <button type="button" className="flex size-10 items-center justify-center rounded-full text-accent-2 hover:bg-white/10" onClick={() => setAuto(false)} aria-label={t("Pause auto scroll")}>
            <Pause className="size-[18px]" fill="currentColor" />
          </button>
          <AutoSpeed speed={s.autoScrollSpeed} by={speedBy} />
          {s.showPageNumber && (
            <>
              <span className="mx-2 h-[18px] w-px bg-fg/25" />
              <span className="text-xs tabular-nums">
                {page} / {count}
              </span>
            </>
          )}
        </div>
      )}

      {!bars && !autoOn && s.showPageNumber && (
        <div className="pointer-events-none absolute bottom-2 left-1/2 z-10 -translate-x-1/2 rounded-full bg-black/60 px-2.5 py-0.5 text-xs tabular-nums text-fg" style={{ marginBottom: "env(safe-area-inset-bottom)" }}>
          {page} / {count}
        </div>
      )}

      {!bars && !full && s.quickFullscreen && canFullscreen() && (
        <button
          type="button"
          className="absolute bottom-2 right-2 z-10 rounded-full bg-black/50 p-2 text-fg/80 hover:bg-black/70 hover:text-fg"
          style={{ marginBottom: "env(safe-area-inset-bottom)", marginRight: "env(safe-area-inset-right)" }}
          onClick={(e) => {
            e.stopPropagation();
            toggleFull();
          }}
          onPointerUp={(e) => e.stopPropagation()}
          aria-label={t("Full screen")}
        >
          <Maximize className="size-4" />
        </button>
      )}

      {panel && <SettingsPanel s={s} set={set} onClose={() => setPanel(false)} hasOwn={hasOwn} saveAsDefault={saveAsDefault} reset={reset} />}
      {pickingChapter && (
        <ChapterPicker
          seriesId={ch.seriesId}
          currentId={ch.id}
          onClose={() => setPickingChapter(false)}
          onPick={(id) => {
            flush();
            setPickingChapter(false);
            navigate(`/read/${id}`, { replace: true });
          }}
        />
      )}
    </div>
  );
}

/** AutoSpeed is the auto scroll speed with a button either side. */
function AutoSpeed({ speed, by, className }: { speed: number; by: (d: number) => void; className?: string }) {
  return (
    <div className={clsx("flex items-center", className)}>
      <button type="button" className="flex h-full min-h-9 w-9 items-center justify-center rounded-full hover:bg-white/10 disabled:opacity-30" disabled={speed <= AUTO_SCROLL_MIN} onClick={() => by(-1)} aria-label={t("Slower")}>
        <Minus className="size-4" />
      </button>
      <span className="min-w-4 text-center text-sm tabular-nums" aria-label={t("Auto scroll speed")}>{speed}</span>
      <button type="button" className="flex h-full min-h-9 w-9 items-center justify-center rounded-full hover:bg-white/10 disabled:opacity-30" disabled={speed >= AUTO_SCROLL_MAX} onClick={() => by(1)} aria-label={t("Faster")}>
        <Plus className="size-4" />
      </button>
    </div>
  );
}
