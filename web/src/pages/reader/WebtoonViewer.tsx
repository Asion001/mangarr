import { t } from "../../lib/i18n/core";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { S } from "../../api/client";
import type { ReaderSettings } from "./settings";
import { tapAction } from "./zones";
import { displayWidth, PageImage, pageUrl, placeholderUrl, useViewport, type Dims } from "./page";
import { Transition } from "./Transition";
import { widen } from "./preload";

type Chapter = S["ReadChapter"];

/**
 * WebtoonViewer is one long vertical strip. Pages load well ahead of the
 * screen, the page at the top third is the current one, and reaching the end moves
 * on to the next chapter (with autoNext).
 */
export function WebtoonViewer({
  chapter,
  settings: s,
  startPage,
  onPage,
  dims,
  need,
  natural,
  onChapter,
  onMenu,
}: {
  chapter: Chapter;
  settings: ReaderSettings;
  startPage: number;
  onPage: (n: number) => void;
  dims: Record<number, Dims>;
  need: (pages: number[]) => void;
  natural: (n: number, w: number, h: number) => void;
  onChapter: (dir: "prev" | "next") => void;
  onMenu: () => void;
}) {
  const vp = useViewport();
  const scroller = useRef<HTMLDivElement>(null);
  const items = useRef<(HTMLDivElement | null)[]>([]);
  const count = chapter.pages.length;
  // pages ahead of the current one to have loaded before they scroll in
  const ahead = Math.max(s.preload, 3);
  const [near, setNear] = useState<Set<number>>(() => widen(new Set(), startPage - 1, startPage + ahead, count));
  const [previewAspects, setPreviewAspects] = useState<Record<number, number>>({});
  const current = useRef(startPage);
  // a readable column on wide screens; side padding narrows it further
  const width = Math.min(vp.w, 900) * (1 - (2 * s.padding) / 100);
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

  // Any page within a few screens pulls in the pages after it, so the
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
            const n = Number((e.target as HTMLElement).dataset.page);
            if (e.isIntersecting) next = widen(next, n, n + ahead, count);
          }
          return next;
        });
      },
      { root, rootMargin: "300% 0px" },
    );
    items.current.forEach((el) => el && io.observe(el));
    return () => io.disconnect();
  }, [chapter.id, count, ahead]);

  useEffect(() => need([...near]), [near, need]);

  // open at the saved page
  useEffect(() => {
    const el = items.current[startPage - 1];
    if (el && startPage > 1) el.scrollIntoView({ block: "start" });
    current.current = startPage;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [chapter.id]);

  // the page at the top third of the screen is the current one
  useEffect(() => {
    const root = scroller.current;
    if (!root) return;
    let raf = 0;
    const on = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        const line = root.getBoundingClientRect().top + root.clientHeight / 3;
        for (let i = 0; i < items.current.length; i++) {
          const r = items.current[i]?.getBoundingClientRect();
          if (r && r.top <= line && r.bottom > line) {
            if (current.current !== i + 1) {
              current.current = i + 1;
              onPage(i + 1);
              setNear((prev) => widen(prev, i, i + 1 + ahead, count));
            }
            break;
          }
        }
        // at the very end, the last page counts as read
        if (root.scrollTop + root.clientHeight >= root.scrollHeight - 2 && current.current !== count) {
          current.current = count;
          onPage(count);
        }
      });
    };
    root.addEventListener("scroll", on, { passive: true });
    return () => root.removeEventListener("scroll", on);
  }, [count, onPage, ahead]);

  // the end: move on after a moment
  const end = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const root = scroller.current;
    const el = end.current;
    if (!root || !el || !s.autoNext || !chapter.next) return;
    let timer = 0;
    const io = new IntersectionObserver(
      ([e]) => {
        window.clearTimeout(timer);
        if (e.intersectionRatio > 0.6) timer = window.setTimeout(() => onChapter("next"), 1200);
      },
      { root, threshold: [0, 0.6, 1] },
    );
    io.observe(el);
    return () => {
      io.disconnect();
      window.clearTimeout(timer);
    };
  }, [chapter.id, chapter.next, s.autoNext, onChapter]);

  // keys scroll by most of a screen
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      const root = scroller.current;
      if (!root || e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement) return;
      const step = root.clientHeight * 0.85;
      const by: Record<string, number> = { ArrowDown: step / 3, ArrowUp: -step / 3, PageDown: step, PageUp: -step, " ": e.shiftKey ? -step : step };
      if (e.key === "Home") root.scrollTo({ top: 0 });
      else if (e.key === "End") root.scrollTo({ top: root.scrollHeight });
      else if (by[e.key] !== undefined) root.scrollBy({ top: by[e.key], behavior: "smooth" });
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);

  const onClick = (e: React.MouseEvent) => {
    if ((window.visualViewport?.scale ?? 1) > 1.05) return;
    const a = tapAction(e.clientX / vp.w, e.clientY / vp.h, s);
    const root = scroller.current;
    if (a === "menu" || !root) onMenu();
    // at the very end, a page turn moves on like the paged reader does
    else if (a === "next" && root.scrollTop + root.clientHeight >= root.scrollHeight - 2) onChapter("next");
    else root.scrollBy({ top: (a === "next" ? 1 : -1) * root.clientHeight * 0.85, behavior: "smooth" });
  };

  return (
    <div ref={scroller} className="h-full w-full overflow-y-auto overflow-x-hidden" onClick={onClick} style={{ touchAction: "pan-y pinch-zoom" }}>
      <div className="mx-auto flex flex-col items-center" style={{ width }}>
        {chapter.prev && (
          <div className="w-full py-6 text-center text-sm text-muted">
            <button type="button" className="rounded-md border border-muted/40 px-3 py-1.5" onClick={(e) => (e.stopPropagation(), onChapter("prev"))}>{t("Previous chapter: ch.") + " "}{chapter.prev.number}
            </button>
          </div>
        )}
        {chapter.pages.map((p, i) => {
          const d = dims[p.number];
          const w = d ? (s.crop ? d.w : d.width) : 0;
          const aspect = d ? w / d.height : previewAspects[p.number] ?? guess;
          const h = width / aspect;
          return (
            <div
              key={`${chapter.id}-${p.number}`}
              ref={(el) => {
                items.current[i] = el;
              }}
              data-page={p.number}
              style={{ width, minHeight: d ? undefined : h, marginBottom: s.gap ? 12 : 0 }}
            >
              {/* eager: the window above already decides what loads, and a
                  browser's lazy loading (Safari's especially) would wait
                  until the page is almost on screen */}
              {near.has(p.number) ? (
                <PageImage
                  src={pageUrl(chapter.id, p.number, displayWidth(width))}
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
        <div ref={end} className="w-full" style={{ height: vp.h * 0.8 }}>
          <Transition edge="end" chapter={chapter} onGo={() => onChapter("next")} onBack={() => scroller.current?.scrollBy({ top: -vp.h })} />
        </div>
      </div>
    </div>
  );
}
