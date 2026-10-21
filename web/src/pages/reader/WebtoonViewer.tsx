import { t as tr, t } from "../../lib/i18n/core";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { S } from "../../api/client";
import { autoScrollPxPerSecond, type ReaderSettings } from "./settings";
import { tapAction } from "./zones";
import { displayWidth, imageWidth, PageImage, pageUrl, placeholderUrl, useDims, useViewport } from "./page";
import { widen } from "./preload";
import { chapterQuery } from "./query";
import { scrollToTop } from "../../lib/scroll";
import { pageScrolls } from "./fullscreen";

type Chapter = S["ReadChapter"];
type ChapterLink = NonNullable<Chapter["next"]>;

/** scrollBox is what scrolls the strip: the page itself, or the strip's own box. */
function scrollBox(strip: HTMLElement, page: boolean) {
  return page ? (document.scrollingElement as HTMLElement) : strip;
}

const label = (c: { number: string; title?: string }) => `${t("Ch.")} ${c.number}${c.title && c.title !== c.number && !c.title.endsWith(c.number) ? ` · ${c.title}` : ""}`;

/**
 * WebtoonViewer is one long vertical strip that runs on from chapter to
 * chapter, with a divider between them. Pages load well ahead of the
 * screen, and the page at the top third is the current one.
 *
 * Positions on the strip count pages across all its chapters from 1, so
 * they stay put as later chapters are added at the end.
 */
export function WebtoonViewer({
  chapterId,
  settings: s,
  startPage,
  onPage,
  wantBounds,
  onChapter,
  onMenu,
  autoScroll = false,
  onAutoScrollEnd,
}: {
  chapterId: number;
  settings: ReaderSettings;
  startPage: number;
  onPage: (chapter: Chapter, page: number) => void;
  wantBounds: boolean;
  onChapter: (dir: "prev" | "next") => void;
  onMenu: () => void;
  /** Scroll down on its own at the speed in the settings. */
  autoScroll?: boolean;
  /** Auto scroll reached the end of the last chapter. */
  onAutoScrollEnd?: () => void;
}) {
  const vp = useViewport();
  const qc = useQueryClient();
  // the strip starts where it opened and keeps that; the chapter it has
  // moved on to comes back as chapterId, which matters only when the strip
  // opens again (after a switch to paged and back)
  const [startId] = useState(chapterId);
  const first = useQuery(chapterQuery(startId)).data;
  const scroller = useRef<HTMLDivElement>(null);
  const [pageScroll] = useState(pageScrolls);
  const items = useRef<(HTMLDivElement | null)[]>([]);
  const observer = useRef<IntersectionObserver | null>(null);
  // the chapters after the first, added as the reader gets near them
  const [more, setMore] = useState<Chapter[]>([]);
  const [nextFailed, setNextFailed] = useState(false);
  const chapters = useMemo(() => (first ? [first, ...more] : []), [first, more]);
  const strip = useMemo(() => chapters.flatMap((c) => c.pages.map((p) => ({ chapter: c, page: p.number }))), [chapters]);
  const stripRef = useRef(strip);
  stripRef.current = strip;
  const total = strip.length;
  // pages ahead of the current one to have loaded before they scroll in
  const ahead = 2;
  const [near, setNear] = useState<Set<number>>(() => widen(new Set(), startPage - 1, startPage + ahead, first?.pages.length ?? 0));
  const current = useRef(startPage);
  // a readable column on wide screens; side padding narrows it further
  const width = Math.min(vp.w, 900) * (1 - (2 * s.padding) / 100);

  // Any page within the screens set to load ahead pulls in the pages after it, so the
  // window keeps sliding ahead of the reader even across pages already
  // loading (a fast fling skips the current-page check below).
  useEffect(() => {
    const root = scroller.current;
    if (!root) return;
    const io = new IntersectionObserver(
      (entries) => {
        setNear((prev) => {
          let next = prev;
          for (const e of entries) {
            const k = Number((e.target as HTMLElement).dataset.pos);
            if (e.isIntersecting) next = widen(next, k, k + ahead, stripRef.current.length);
          }
          return next;
        });
      },
      { root: pageScroll ? null : root, rootMargin: `${s.aheadScreens * 100}% 0px` },
    );
    observer.current = io;
    items.current.forEach((el) => el && io.observe(el));
    return () => {
      io.disconnect();
      observer.current = null;
    };
  }, [ahead, first, s.aheadScreens, pageScroll]);
  const register = useCallback((k: number, el: HTMLDivElement | null) => {
    items.current[k - 1] = el;
    if (el) observer.current?.observe(el);
  }, []);

  // a chapter added at the end joins the window if it's close enough
  useEffect(() => setNear((prev) => widen(prev, current.current, current.current + ahead, total)), [total, ahead]);

  // Once the last chapter's pages are about to load, fetch the one after it
  // so its first pages are there by the time the reader scrolls past the
  // divider.
  const last = chapters[chapters.length - 1];
  const lastStart = total - (last?.pages.length ?? 0) + 1;
  const wantNext = !!last?.next && (last.pages.length === 0 || [...near].some((k) => k >= lastStart));
  const nextId = last?.next?.id;
  useEffect(() => {
    if (!wantNext || !nextId) return;
    let live = true;
    setNextFailed(false);
    qc.fetchQuery(chapterQuery(nextId))
      .then((c) => live && setMore((cs) => (cs.some((x) => x.id === c.id) ? cs : [...cs, c])))
      .catch(() => live && setNextFailed(true));
    return () => {
      live = false;
    };
  }, [wantNext, nextId, qc]);

  // open at the saved page
  useEffect(() => {
    if (!first) return;
    const el = items.current[startPage - 1];
    if (pageScroll) {
      // the page may still be scrolled from the chapter before
      const box = document.scrollingElement as HTMLElement;
      box.scrollTop = 0;
      if (el && startPage > 1) box.scrollTop = el.getBoundingClientRect().top;
    } else if (el && startPage > 1) scrollToTop(el);
    current.current = startPage;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [!!first]);

  // the page at the top third of the screen is the current one
  useEffect(() => {
    if (!scroller.current) return;
    const root = scrollBox(scroller.current, pageScroll);
    let raf = 0;
    const set = (k: number) => {
      const at = stripRef.current[k - 1];
      if (!at || current.current === k) return;
      current.current = k;
      onPage(at.chapter, at.page);
      setNear((prev) => widen(prev, k - 1, k + ahead, stripRef.current.length));
    };
    const on = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const line = (pageScroll ? 0 : root.getBoundingClientRect().top) + root.clientHeight / 3;
        for (let i = 0; i < stripRef.current.length; i++) {
          const r = items.current[i]?.getBoundingClientRect();
          if (r && r.top <= line && r.bottom > line) {
            set(i + 1);
            break;
          }
        }
        // at the very end, the last page counts as read
        if (root.scrollTop + root.clientHeight >= root.scrollHeight - 2) set(stripRef.current.length);
      });
    };
    const target = pageScroll ? window : root;
    target.addEventListener("scroll", on, { passive: true });
    return () => target.removeEventListener("scroll", on);
  }, [onPage, ahead, first, pageScroll]);

  // Auto scroll moves the strip a little every frame. A finger or the
  // wheel on the strip holds it while you scroll by hand, and it carries
  // on from wherever you left it a moment after you let go.
  const speed = autoScrollPxPerSecond(s.autoScrollSpeed);
  const lastChapterDone = !last?.next;
  const autoEnd = useRef(onAutoScrollEnd);
  autoEnd.current = onAutoScrollEnd;
  useEffect(() => {
    const el = scroller.current;
    if (!autoScroll || !el) return;
    const root = scrollBox(el, pageScroll);
    let raf = 0;
    let prev = 0;
    let at = root.scrollTop;
    let heldUntil = 0;
    let touching = false;
    const hold = () => (heldUntil = performance.now() + 1000);
    const down = () => (touching = true);
    const up = () => {
      touching = false;
      hold();
    };
    const frame = (now: number) => {
      raf = requestAnimationFrame(frame);
      const dt = prev ? Math.min(now - prev, 100) / 1000 : 0;
      prev = now;
      // scrolled by hand (or by a tap): carry on from there
      if (Math.abs(root.scrollTop - at) > 2) at = root.scrollTop;
      if (touching || now < heldUntil) return;
      const end = root.scrollHeight - root.clientHeight;
      if (at >= end - 2) {
        // the end of the last chapter; otherwise the next one is on its way
        if (lastChapterDone) autoEnd.current?.();
        return;
      }
      at = Math.min(end, at + speed * dt);
      root.scrollTop = at;
    };
    raf = requestAnimationFrame(frame);
    el.addEventListener("pointerdown", down);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
    el.addEventListener("wheel", hold, { passive: true });
    return () => {
      cancelAnimationFrame(raf);
      el.removeEventListener("pointerdown", down);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
      el.removeEventListener("wheel", hold);
    };
  }, [autoScroll, speed, lastChapterDone, first, pageScroll]);

  // keys scroll by most of a screen
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (!scroller.current || e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement) return;
      const root = scrollBox(scroller.current, pageScroll);
      const step = root.clientHeight * s.tapScroll;
      const nudge = root.clientHeight * 0.3;
      const by: Record<string, number> = { ArrowDown: nudge, ArrowUp: -nudge, PageDown: step, PageUp: -step, " ": e.shiftKey ? -step : step };
      if (e.key === "Home") root.scrollTo({ top: 0 });
      else if (e.key === "End") root.scrollTo({ top: root.scrollHeight });
      else if (by[e.key] !== undefined) root.scrollBy({ top: by[e.key], behavior: "smooth" });
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [s.tapScroll, pageScroll]);

  const onClick = (e: React.MouseEvent) => {
    if ((window.visualViewport?.scale ?? 1) > 1.05) return;
    const a = tapAction(e.clientX / vp.w, e.clientY / vp.h, s);
    const root = scroller.current && scrollBox(scroller.current, pageScroll);
    if (a === "menu" || !root) onMenu();
    // at the very end of the last chapter, a page turn leaves like the paged reader does
    else if (a === "next" && root.scrollTop + root.clientHeight >= root.scrollHeight - 2) onChapter("next");
    else root.scrollBy({ top: (a === "next" ? 1 : -1) * root.clientHeight * s.tapScroll, behavior: "smooth" });
  };

  if (!first) return null;
  let offset = 0;
  return (
    <div ref={scroller} className={pageScroll ? "w-full overflow-x-clip" : "h-full w-full overflow-y-auto overflow-x-hidden"} onClick={onClick} style={{ touchAction: "pan-y pinch-zoom" }}>
      <div className="mx-auto flex flex-col items-center" style={{ width }}>
        {first.prev && (
          <div className="w-full py-6 text-center text-sm text-muted">
            <button type="button" className="rounded-md border border-muted/40 px-3 py-1.5" onClick={(e) => (e.stopPropagation(), onChapter("prev"))}>{t("Previous chapter: ch.") + " "}{first.prev.number}
            </button>
          </div>
        )}
        {chapters.map((c, i) => {
          const start = offset;
          offset += c.pages.length;
          const following = i === chapters.length - 1;
          return (
            <Segment
              key={c.id}
              chapter={c}
              offset={start}
              near={near}
              width={width}
              settings={s}
              wantBounds={wantBounds}
              register={register}
              divider={<Divider chapter={c} next={c.next} failed={following && nextFailed} onNext={() => onChapter("next")} />}
            />
          );
        })}
      </div>
    </div>
  );
}

/** Segment is one chapter's pages on the strip, then its divider. */
function Segment({
  chapter,
  offset,
  near,
  width,
  settings: s,
  wantBounds,
  register,
  divider,
}: {
  chapter: Chapter;
  offset: number;
  near: Set<number>;
  width: number;
  settings: ReaderSettings;
  wantBounds: boolean;
  register: (k: number, el: HTMLDivElement | null) => void;
  divider: React.ReactNode;
}) {
  const { dims, need, natural } = useDims(chapter.id, wantBounds);
  const [previewAspects, setPreviewAspects] = useState<Record<number, number>>({});
  // pages not loaded yet borrow the shape of one that is (webtoon pages
  // share a width), so the strip doesn't jump as they arrive
  const guess = useMemo(() => {
    const d = Object.values(dims)[0];
    return d ? (s.crop ? d.w : d.width) / d.height : 0.7;
  }, [dims, s.crop]);
  const previewNatural = useCallback((page: number, w: number, h: number) => {
    if (!w || !h) return;
    const aspect = w / h;
    setPreviewAspects((current) => current[page] === aspect ? current : { ...current, [page]: aspect });
  }, []);
  const wanted = useMemo(() => chapter.pages.filter((_, i) => near.has(offset + i + 1)).map((p) => p.number), [chapter, near, offset]);
  useEffect(() => need(wanted), [wanted, need]);

  return (
    <>
      {chapter.pages.map((p, i) => {
        const k = offset + i + 1;
        const d = dims[p.number];
        const w = d ? (s.crop ? d.w : d.width) : 0;
        const aspect = d ? w / d.height : previewAspects[p.number] ?? guess;
        const h = width / aspect;
        return (
          <div
            key={p.number}
            ref={(el) => register(k, el)}
            data-pos={k}
            data-chapter={chapter.id}
            data-page={p.number}
            style={{ width, minHeight: d ? undefined : h, marginBottom: s.gap ? 12 : 0 }}
          >
            {/* eager: the window above already decides what loads, and a
                browser's lazy loading (Safari's especially) would wait
                until the page is almost on screen */}
            {near.has(k) ? (
              <PageImage
                src={pageUrl(chapter.id, p.number, displayWidth(imageWidth(width, d, s.crop, true)))}
                placeholder={placeholderUrl(chapter.id, p.number)}
                dims={d}
                crop={s.crop}
                sidesOnly
                eager
                width={width}
                height={h}
                onNatural={(nw, nh) => natural(p.number, nw, nh)}
                onPlaceholderNatural={(nw, nh) => previewNatural(p.number, nw, nh)}
              />
            ) : (
              <div style={{ width, height: h }} />
            )}
          </div>
        );
      })}
      {divider}
    </>
  );
}

/** Divider sits between two chapters on the strip, or after the last one. */
function Divider({ chapter, next, failed, onNext }: { chapter: Chapter; next?: ChapterLink; failed: boolean; onNext: () => void }) {
  const line = <div className="h-px flex-1 bg-muted/30" />;
  return (
    // after the last chapter, room for its link to clear the reader's bottom bar
    <div className="flex w-full items-center gap-3 px-4 py-8 text-fg" data-divider={chapter.id} style={next ? undefined : { paddingBottom: "calc(6rem + env(safe-area-inset-bottom))" }}>
      {line}
      <div className="flex max-w-[75%] flex-col items-center gap-1 text-center">
        {next ? (
          <>
            <div className="text-xs uppercase tracking-wide text-muted">{tr("End of ch.") + " "}{chapter.number}</div>
            <div className="text-sm font-semibold">{label(next)}</div>
            {failed && (
              <button type="button" className="mt-2 rounded-md border border-muted/40 px-3 py-1.5 text-sm" onClick={(e) => (e.stopPropagation(), onNext())}>{t("Open the next chapter")}</button>
            )}
          </>
        ) : (
          <>
            <div className="text-xs uppercase tracking-wide text-muted">{tr("Finished") + " · "}{label(chapter)}</div>
            <div className="text-sm text-muted">{tr("There's no next chapter yet.")}</div>
            <Link
              to={`/series/${chapter.seriesId}`}
              className="mt-2 rounded-md bg-primary px-3 py-2 text-sm font-medium text-white"
              onClick={(e) => e.stopPropagation()}
            >{t("Back to the series")}</Link>
          </>
        )}
      </div>
      {line}
    </div>
  );
}
