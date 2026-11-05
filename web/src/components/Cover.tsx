import { useEffect, useLayoutEffect, useRef, useState } from "react";
import clsx from "clsx";
import { BookImage } from "lucide-react";

/** odd: an image far from the usual 2:3 shape (0.67; the usual variation runs 0.5 to 0.85). */
const isOdd = (w: number, h: number) => h > 0 && (w / h < 0.5 || w / h > 0.85);

// Covers that finish loading close together are shown together: each load
// waits a moment for the next, up to MAX_WAIT after the first, so a grid
// fades in as a whole instead of one cover at a time.
const BATCH_GAP = 60;
const MAX_WAIT = 250;
let waiting: (() => void)[] = [];
let gapTimer = 0;
let maxTimer = 0;
function flushReveals() {
  window.clearTimeout(gapTimer);
  window.clearTimeout(maxTimer);
  maxTimer = 0;
  const list = waiting;
  waiting = [];
  list.forEach((show) => show());
}
function reveal(show: () => void) {
  waiting.push(show);
  window.clearTimeout(gapTimer);
  gapTimer = window.setTimeout(flushReveals, BATCH_GAP);
  if (!maxTimer) maxTimer = window.setTimeout(flushReveals, MAX_WAIT);
}

/**
 * Cover shows a series or manga cover in a fixed-ratio box. Images far from
 * the usual 2:3 shape (banners, square art) are shown whole over a blurred
 * copy instead of being cropped. Until the image is in, the box shimmers;
 * the image then fades in (at once when the browser already has it). A new
 * link for a cover already shown keeps the old picture until the new one is
 * in, so a refreshed list never blanks it.
 */
export function Cover({ src, alt, className, eager }: { src?: string; alt: string; className?: string; eager?: boolean }) {
  // shown is the link on screen, with its shape; it only changes once that image has loaded
  const [shown, setShown] = useState<{ src: string; odd: boolean; instant: boolean }>();
  const [failed, setFailed] = useState<string>();
  const img = useRef<HTMLImageElement>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => void (alive.current = false);
  }, []);

  const current = shown ? shown.src : src;
  // the browser already has it (memory cache): no shimmer, no fade
  useLayoutEffect(() => {
    const el = img.current;
    if (!shown && src && el?.complete && el.naturalWidth > 0) setShown({ src, odd: isOdd(el.naturalWidth, el.naturalHeight), instant: true });
  }, [src]);

  // a new link for a cover on screen loads off screen and swaps in when ready
  useEffect(() => {
    if (!src || !shown || shown.src === src) return;
    const next = new Image();
    next.referrerPolicy = "no-referrer";
    next.onload = () => alive.current && setShown({ src, odd: isOdd(next.naturalWidth, next.naturalHeight), instant: true });
    next.onerror = () => alive.current && setFailed(src);
    next.src = src;
    return () => {
      next.onload = next.onerror = null;
    };
  }, [src, shown]);

  const ready = !!shown;
  const odd = shown?.odd ?? false;
  const broken = !src || (failed === src && !shown);
  return (
    <div className={clsx("relative overflow-hidden rounded-md bg-panel-2", className)}>
      {!broken && current ? (
        <>
          {!ready && <div aria-hidden className="cover-shimmer absolute inset-0" />}
          <div className={clsx("absolute inset-0", !ready && "opacity-0", ready && !shown.instant && "cover-fade-in")}>
            {odd && <img src={current} alt="" aria-hidden referrerPolicy="no-referrer" className="absolute inset-0 h-full w-full scale-110 object-cover opacity-50 blur-md" />}
            <img
              ref={img}
              src={current}
              alt={alt}
              loading={eager ? "eager" : "lazy"}
              fetchPriority={eager ? "high" : undefined}
              decoding="async"
              // some providers (MangaDex) answer a link from another site with a placeholder
              referrerPolicy="no-referrer"
              onError={() => current === src && setFailed(src)}
              onLoad={(e) => {
                if (shown || !src) return;
                const { naturalWidth: w, naturalHeight: h } = e.currentTarget;
                reveal(() => alive.current && setShown((cur) => cur ?? { src, odd: isOdd(w, h), instant: false }));
              }}
              className={clsx("relative h-full w-full", odd ? "object-contain" : "object-cover")}
            />
          </div>
        </>
      ) : (
        <div className="flex h-full w-full items-center justify-center text-muted">
          <BookImage className="size-8" />
        </div>
      )}
    </div>
  );
}
