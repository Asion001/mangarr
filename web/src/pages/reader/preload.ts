import { useEffect, useRef } from "react";

type ImageFactory = () => HTMLImageElement;

/** Keeps image preloads alive and cancels requests that are no longer useful. */
export class ImagePreloader {
  private readonly images = new Map<string, HTMLImageElement>();

  constructor(private readonly makeImage: ImageFactory = () => new Image()) {}

  update(load: readonly string[], retain: readonly string[] = []) {
    const wanted = new Set([...load, ...retain]);
    for (const [url, image] of this.images) {
      if (wanted.has(url)) continue;
      image.removeAttribute("src");
      this.images.delete(url);
    }
    for (const url of load) {
      if (this.images.has(url)) continue;
      const image = this.makeImage();
      image.decoding = "async";
      image.src = url;
      this.images.set(url, image);
    }
  }

  clear() {
    for (const image of this.images.values()) image.removeAttribute("src");
    this.images.clear();
  }
}

/** Preload changing image URLs without leaving obsolete requests running. */
export function useImagePreload(load: readonly string[], retain: readonly string[] = [], shared?: ImagePreloader) {
  const owned = useRef<ImagePreloader | null>(null);
  owned.current ??= new ImagePreloader();
  const preloader = shared ?? owned.current;
  useEffect(() => preloader.update(load, retain), [load, retain, preloader]);
  useEffect(() => {
    if (shared) return;
    return () => preloader.clear();
  }, [preloader, shared]);
}

/**
 * widen adds pages from..to (clamped to 1..count) to a set of pages to load,
 * returning the same set when nothing is new so React can skip a render.
 */
export function widen(pages: Set<number>, from: number, to: number, count: number): Set<number> {
  let next: Set<number> | null = null;
  for (let k = Math.max(1, from); k <= Math.min(count, to); k++) {
    if (pages.has(k)) continue;
    next ??= new Set(pages);
    next.add(k);
  }
  return next ?? pages;
}
