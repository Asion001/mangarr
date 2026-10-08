import { t } from "../../lib/i18n/core";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, apiUrl, unwrap } from "../../api/client";

/** Dims are a page's size and the box inside its borders (source pixels). */
export type Dims = { width: number; height: number; x: number; y: number; w: number; h: number; measured?: boolean };

/** Widths the server keeps copies at (internal/imagedeliver). */
const widths = [320, 720, 1080, 1440, 2160];

/** displayWidth rounds a CSS width (in real pixels) up to a size the server keeps. */
export function displayWidth(cssWidth: number): number {
  const want = Math.round(cssWidth * (window.devicePixelRatio || 1));
  return widths.find((w) => want <= w) ?? 0; // 0: the page as it is
}

/** Signed page links of the chapters opened, when a CDN may cache images. */
const pageBases = new Map<number, string>();
export function rememberPageBase(chapterId: number, base?: string) {
  if (base) pageBases.set(chapterId, base);
  else pageBases.delete(chapterId);
}

/** pageUrl is a page, optionally at a display size instead of the full scan. */
export const pageUrl = (chapterId: number, n: number, width = 0) => {
  const base = pageBases.get(chapterId);
  return apiUrl(base ? `${base}/${n}` : `api/v1/read/chapters/${chapterId}/pages/${n}`, width > 0 ? { w: width } : undefined);
};

/** placeholderUrl is a small copy shown (blurred) until the page arrives. */
export const placeholderUrl = (chapterId: number, n: number) => pageUrl(chapterId, n, 320);

/**
 * useDims keeps page sizes for a chapter: from the server's bounds (when
 * cropping or splitting needs them) or from the loaded image.
 */
export function useDims(chapterId: number, wantBounds: boolean) {
  const [dims, setDims] = useState<Record<number, Dims>>({});
  const asked = useRef(new Set<number>());
  const failed = useRef(new Set<number>());
  const naturalSizes = useRef(new Map<number, { width: number; height: number }>());
  // pages wanted before the chapter's bounds answered; asked for afterwards
  const waiting = useRef<number[]>([]);
  const ready = useRef(false);
  useEffect(() => {
    setDims({});
    asked.current = new Set();
    failed.current = new Set();
    naturalSizes.current = new Map();
    waiting.current = [];
    ready.current = false;
  }, [chapterId]);

  const fallback = useCallback((n: number) => {
    failed.current.add(n);
    const size = naturalSizes.current.get(n);
    if (size) setDims((d) => ({ ...d, [n]: { ...size, x: 0, y: 0, w: size.width, h: size.height, measured: true } }));
  }, []);

  const ask = useCallback(
    (n: number) => {
      if (asked.current.has(n)) return;
      asked.current.add(n);
      unwrap(api.GET("/api/v1/read/chapters/{id}/pages/{n}/bounds", { params: { path: { id: chapterId, n } } }))
        .then((b) => setDims((d) => ({ ...d, [n]: { ...b, measured: true } })))
        .catch(() => fallback(n));
    },
    [chapterId, fallback],
  );

  // one request for the whole chapter, instead of one per page
  useEffect(() => {
    if (!wantBounds) return;
    let live = true;
    unwrap(api.GET("/api/v1/read/chapters/{id}/bounds", { params: { path: { id: chapterId } } }))
      .then((list) => {
        if (!live) return;
        // mark them here, not inside the updater: React runs updaters later,
        // and by then the pages would already have been asked for again
        for (const b of list) asked.current.add(b.number);
        setDims((d) => {
          const next = { ...d };
          // A fast image decode may already have recorded its natural size.
          // Measured server bounds must replace that full-page fallback.
          for (const b of list) next[b.number] = { ...b, measured: true };
          return next;
        });
      })
      .catch(() => undefined)
      .finally(() => {
        if (!live) return;
        ready.current = true;
        for (const n of waiting.current) ask(n); // the server hadn't measured these yet
        waiting.current = [];
      });
    return () => {
      live = false;
    };
  }, [chapterId, wantBounds, ask]);

  /** need asks for the bounds of pages about to be shown. */
  const need = useCallback(
    (pages: number[]) => {
      if (!wantBounds) return;
      for (const n of pages) {
        if (asked.current.has(n)) continue;
        if (!ready.current) {
          if (!waiting.current.includes(n)) waiting.current.push(n);
          continue;
        }
        ask(n);
      }
    },
    [wantBounds, ask],
  );
  /** natural records a loaded image's size and supplies a full-page fallback
   * only when measuring that page failed. */
  const natural = useCallback((n: number, width: number, height: number) => {
    naturalSizes.current.set(n, { width, height });
    setDims((d) => {
      if (d[n]?.measured) return d;
      return { ...d, [n]: { width, height, x: 0, y: 0, w: width, h: height, measured: failed.current.has(n) } };
    });
  }, []);
  return { dims, need, natural };
}

export type Half = "left" | "right";

/** boxOf is the part of a page to show (cropped, or one half). */
export function boxOf(d: Dims | undefined, crop: boolean, sidesOnly: boolean, half?: Half) {
  if (!d) return null;
  let b = crop ? { x: d.x, y: sidesOnly ? 0 : d.y, w: d.w, h: sidesOnly ? d.height : d.h } : { x: 0, y: 0, w: d.width, h: d.height };
  if (half) b = { ...b, w: b.w / 2, x: half === "right" ? b.x + b.w / 2 : b.x };
  return b;
}

/** isWide reports a double page (landscape). */
export const isWide = (d?: Dims) => !!d && d.width > d.height * 1.1;

/** fit sizes a box of aspect (w/h) into the space, by the scale setting. */
export function fit(aspect: number, availW: number, availH: number, scale: string, naturalW?: number) {
  switch (scale) {
    case "width":
      return { w: availW, h: availW / aspect };
    case "height":
      return { w: availH * aspect, h: availH };
    case "original": {
      const w = naturalW ?? availW;
      return { w, h: w / aspect };
    }
    default: {
      const w = Math.min(availW, availH * aspect);
      return { w, h: w / aspect };
    }
  }
}

/** PageImage shows a page, or the box of it, at a given size. */
export function PageImage({
  src,
  dims,
  crop,
  sidesOnly = false,
  half,
  width,
  height,
  onNatural,
  onPlaceholderNatural,
  eager = false,
  placeholder,
}: {
  src: string;
  dims?: Dims;
  crop: boolean;
  sidesOnly?: boolean;
  half?: Half;
  width: number;
  height: number;
  onNatural?: (w: number, h: number) => void;
  onPlaceholderNatural?: (w: number, h: number) => void;
  eager?: boolean;
  /** placeholder is a small copy shown until the page itself is decoded. */
  placeholder?: string;
}) {
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const box = boxOf(dims, crop, sidesOnly, half);
  const load = (e: React.SyntheticEvent<HTMLImageElement>) => {
    setLoaded(true);
    onNatural?.(e.currentTarget.naturalWidth, e.currentTarget.naturalHeight);
  };
  if (failed) {
    return (
      <div style={{ width, height }} className="flex items-center justify-center text-sm text-muted">
        <button type="button" className="rounded border border-muted/40 px-3 py-1.5" onClick={() => setFailed(false)}>{t("Couldn't load this page — retry")}</button>
      </div>
    );
  }
  const common = { src, alt: "", draggable: false, decoding: "async" as const, loading: eager ? ("eager" as const) : ("lazy" as const), onLoad: load, onError: () => setFailed(true) };
  // Load the image so its natural size is available as an error fallback, but
  // do not show an uncropped frame while the server is still measuring it.
  if ((crop || half) && (!dims || dims.measured === false)) {
    return (
      <div style={{ width, height, position: "relative" }} data-reader-state="measuring-borders">
        <img {...common} style={{ width, height, maxWidth: "none", objectFit: "contain", visibility: "hidden" }} className="select-none" />
      </div>
    );
  }
  const transformed = box && dims && (crop || half) ? {
    position: "absolute" as const,
    maxWidth: "none",
    width: `${(dims.width / box.w) * 100}%`,
    height: `${(dims.height / box.h) * 100}%`,
    left: `${(-box.x / box.w) * 100}%`,
    top: `${(-box.y / box.h) * 100}%`,
  } : undefined;
  const blur = placeholder && !loaded && (
    <img
      src={placeholder}
      alt=""
      aria-hidden
      draggable={false}
      className="pointer-events-none absolute select-none blur-sm"
      style={transformed ?? { inset: 0, width: "100%", height: "100%", objectFit: "contain" }}
      onLoad={(e) => onPlaceholderNatural?.(e.currentTarget.naturalWidth, e.currentTarget.naturalHeight)}
    />
  );
  if (!box || !dims || (!crop && !half)) {
    if (!blur) return <img {...common} style={{ width, height, maxWidth: "none", objectFit: "contain" }} className="select-none" />;
    return (
      <div style={{ width, height, position: "relative" }}>
        {blur}
        <img {...common} style={{ width, height, maxWidth: "none", objectFit: "contain", position: "relative" }} className="select-none" />
      </div>
    );
  }
  return (
    <div style={{ width, height, overflow: "hidden", position: "relative" }}>
      {blur}
      <img
        {...common}
        className="select-none"
        style={transformed}
      />
    </div>
  );
}

/** useViewport is the window's size, kept up to date. */
export function useViewport() {
  const [size, setSize] = useState({ w: window.innerWidth, h: window.innerHeight });
  useEffect(() => {
    const on = () => {
      if ((window.visualViewport?.scale ?? 1) > 1.05) return;
      const next = { w: window.innerWidth, h: window.innerHeight };
      setSize((current) => current.w === next.w && current.h === next.h ? current : next);
    };
    window.addEventListener("resize", on);
    // visualViewport resizes on every pinch-zoom frame. Re-fitting the page to
    // that shrinking viewport cancels the visible zoom and makes the gesture
    // rerender the reader continuously; only layout-viewport changes belong here.
    // Its resize still matters once the zoom is back to 1: a rotation made
    // while zoomed in fired no window resize that got through, and the
    // window size hasn't changed on a plain zoom, so nothing rerenders.
    const vv = window.visualViewport;
    vv?.addEventListener("resize", on);
    return () => {
      window.removeEventListener("resize", on);
      vv?.removeEventListener("resize", on);
    };
  }, []);
  return size;
}
