import { useEffect, useMemo, useRef } from "react";
import type { S } from "../../api/client";
import type { ReaderSettings } from "./settings";
import { tapAction } from "./zones";
import { displayWidth, fit, imageWidth, isWide, PageImage, pageUrl, placeholderUrl, useViewport, type Dims, type Half } from "./page";
import { Transition } from "./Transition";

type Chapter = S["ReadChapter"];

/** A screen of the paged viewer: one or two pages, a half page, or a chapter edge. */
export type View = { pages: number[]; half?: Half } | { edge: "start" | "end" };

/** Exact rendered size for a page in a paged-reader screen. */
export function pageLayout(page: number, view: Extract<View, { pages: number[] }>, dims: Record<number, Dims>, s: ReaderSettings, viewport: { w: number; h: number }) {
  const d = dims[page];
  let aspect = 0.7;
  let naturalW: number | undefined;
  if (d) {
    const width = (s.crop ? d.w : d.width) / (view.half ? 2 : 1);
    aspect = width / (s.crop ? d.h : d.height);
    naturalW = width;
  }
  return fit(aspect, viewport.w / view.pages.length, viewport.h, s.scale, naturalW);
}

/** pageSrc is the image a paged screen asks for: sized for the whole image, of
 * which a crop or a half may show only part. */
export function pageSrc(chapterId: number, page: number, view: Extract<View, { pages: number[] }>, dims: Record<number, Dims>, s: ReaderSettings, viewport: { w: number; h: number }) {
  const w = pageLayout(page, view, dims, s, viewport).w;
  return pageUrl(chapterId, page, displayWidth(imageWidth(w, dims[page], s.crop, false, view.half)));
}

/** buildViews lays pages out into screens. */
export function buildViews(count: number, dims: Record<number, Dims>, s: ReaderSettings, landscape: boolean): View[] {
  const out: View[] = [{ edge: "start" }];
  const double = s.direction !== "vertical" && (s.spread === "double" || (s.spread === "auto" && landscape));
  if (!double) {
    for (let p = 1; p <= count; p++) {
      if (s.splitWide && s.direction !== "vertical" && isWide(dims[p])) {
        const [a, b]: Half[] = s.direction === "rtl" ? ["right", "left"] : ["left", "right"];
        out.push({ pages: [p], half: a }, { pages: [p], half: b });
      } else out.push({ pages: [p] });
    }
  } else {
    let p = 1;
    if (s.coverAlone && count > 0) out.push({ pages: [p++] });
    while (p <= count) {
      if (p === count || isWide(dims[p]) || isWide(dims[p + 1])) out.push({ pages: [p++] });
      else {
        out.push({ pages: [p, p + 1] });
        p += 2;
      }
    }
  }
  out.push({ edge: "end" });
  return out;
}

/** indexOf finds the screen showing a page (and half). */
export function indexOf(views: View[], page: number, half?: Half) {
  const i = views.findIndex((v) => "pages" in v && v.pages.includes(page) && (!half || v.half === half || !v.half));
  return i < 0 ? 1 : i;
}

export function PagedViewer({
  chapter,
  settings: s,
  views,
  index,
  setIndex,
  dims,
  natural,
  onChapter,
  onMenu,
}: {
  chapter: Chapter;
  settings: ReaderSettings;
  views: View[];
  index: number;
  setIndex: (i: number) => void;
  dims: Record<number, Dims>;
  natural: (n: number, w: number, h: number) => void;
  onChapter: (dir: "prev" | "next") => void;
  onMenu: () => void;
}) {
  const vp = useViewport();
  const scroller = useRef<HTMLDivElement>(null);
  const view = views[Math.min(index, views.length - 1)];

  const go = useMemo(
    () => (dir: "prev" | "next") => {
      if (dir === "next") {
        if (index >= views.length - 1) onChapter("next");
        else setIndex(index + 1);
      } else if (index <= 0) onChapter("prev");
      else setIndex(index - 1);
    },
    [index, views.length, setIndex, onChapter],
  );

  // keys: arrows follow the reading direction
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement) return;
      const rtl = s.direction === "rtl";
      const map: Record<string, "prev" | "next"> = {
        ArrowRight: rtl ? "prev" : "next",
        ArrowLeft: rtl ? "next" : "prev",
        ArrowDown: "next",
        ArrowUp: "prev",
        PageDown: "next",
        PageUp: "prev",
        " ": e.shiftKey ? "prev" : "next",
      };
      if (e.key === "Home") return setIndex(1);
      if (e.key === "End") return setIndex(views.length - 2);
      const dir = map[e.key];
      if (!dir) return;
      // in a page taller than the screen, arrows scroll it first
      const el = scroller.current;
      if (el && (e.key === "ArrowDown" || e.key === " ") && el.scrollTop + el.clientHeight < el.scrollHeight - 4) return;
      if (el && e.key === "ArrowUp" && el.scrollTop > 4) return;
      e.preventDefault();
      go(dir);
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [go, s.direction, setIndex, views.length]);

  // a new screen starts at its top
  useEffect(() => {
    scroller.current?.scrollTo({ top: 0, left: s.direction === "rtl" ? scroller.current.scrollWidth : 0 });
  }, [index, s.direction]);

  // taps and swipes
  const start = useRef<{ x: number; y: number; t: number } | null>(null);
  const zoomed = () => (window.visualViewport?.scale ?? 1) > 1.05;
  const onPointerDown = (e: React.PointerEvent) => {
    start.current = { x: e.clientX, y: e.clientY, t: Date.now() };
  };
  const onPointerUp = (e: React.PointerEvent) => {
    const st = start.current;
    start.current = null;
    if (!st || zoomed()) return;
    const dx = e.clientX - st.x;
    const dy = e.clientY - st.y;
    if (e.pointerType !== "mouse" && Math.max(Math.abs(dx), Math.abs(dy)) > 50 && Date.now() - st.t < 800) {
      if (s.direction === "vertical") {
        if (Math.abs(dy) > Math.abs(dx)) go(dy < 0 ? "next" : "prev");
      } else if (Math.abs(dx) > Math.abs(dy)) {
        const forward = s.direction === "rtl" ? dx > 0 : dx < 0;
        go(forward ? "next" : "prev");
      }
      return;
    }
    if (Math.abs(dx) > 10 || Math.abs(dy) > 10) return; // a drag, not a tap
    const a = tapAction(e.clientX / vp.w, e.clientY / vp.h, s);
    if (a === "menu") onMenu();
    else go(a);
  };

  let content: React.ReactNode;
  if (!view) content = null;
  else if ("edge" in view) {
    content = (
      <Transition
        edge={view.edge}
        chapter={chapter}
        onGo={() => onChapter(view.edge === "end" ? "next" : "prev")}
        onBack={() => setIndex(view.edge === "end" ? views.length - 2 : 1)}
      />
    );
  } else {
    const order = s.direction === "rtl" ? [...view.pages].reverse() : view.pages;
    content = (
      <div className="flex min-h-full min-w-full items-center justify-center" style={{ width: "max-content", height: "max-content" }}>
        {order.map((p) => {
          const d = dims[p];
          const size = pageLayout(p, view, dims, s, vp);
          return (
            <PageImage
              key={`${chapter.id}-${p}-${view.half ?? ""}`}
              src={pageSrc(chapter.id, p, view, dims, s, vp)}
              placeholder={placeholderUrl(chapter.id, p)}
              dims={d}
              crop={s.crop}
              half={view.half}
              width={size.w}
              height={size.h}
              eager
              onNatural={(w, h) => natural(p, w, h)}
            />
          );
        })}
      </div>
    );
  }
  return (
    <div
      ref={scroller}
      className="h-full w-full touch-pan-x touch-pan-y overflow-auto"
      onPointerDown={onPointerDown}
      onPointerUp={onPointerUp}
      style={{ touchAction: "pan-x pan-y pinch-zoom" }}
    >
      {content}
    </div>
  );
}
