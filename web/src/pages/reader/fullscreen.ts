import { useEffect, useState } from "react";

// older iPad Safari only has the prefixed calls
type WebkitDocument = Document & { webkitFullscreenElement?: Element | null; webkitFullscreenEnabled?: boolean; webkitExitFullscreen?: () => void };
type WebkitElement = HTMLElement & { webkitRequestFullscreen?: () => void };

const doc = () => document as WebkitDocument;

/** canFullscreen says whether the browser lets a page go full screen (no iPhone browser does: they all run on Safari's engine). */
export function canFullscreen() {
  return !!(document.fullscreenEnabled || doc().webkitFullscreenEnabled);
}

/**
 * needsHomeScreen says full screen is missing only because this is a browser tab on iPhone
 * (Safari, Chrome, any of them): opened from the Home Screen the app has no browser bars at all.
 */
export function needsHomeScreen() {
  if (canFullscreen()) return false;
  return !isStandalone() && /iPhone|iPod/.test(navigator.userAgent);
}

/** isStandalone says the app was opened from the Home Screen, with no browser bars around it. */
export function isStandalone() {
  return matchMedia("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

/**
 * pageScrolls says the webtoon strip scrolls the page itself rather than a box
 * inside it. In a browser tab that's what lets a phone browser shrink its
 * address bar and toolbar while you read. A home screen app has no bars to
 * shrink, and its page doesn't scroll there (#root does, see index.css).
 */
export function pageScrolls() {
  return !isStandalone();
}

function isFullscreen() {
  return !!(document.fullscreenElement || doc().webkitFullscreenElement);
}

/** toggleFullscreen enters or leaves full screen. */
export function toggleFullscreen() {
  if (isFullscreen()) {
    if (document.exitFullscreen) void document.exitFullscreen();
    else doc().webkitExitFullscreen?.();
    return;
  }
  const el = document.documentElement as WebkitElement;
  if (el.requestFullscreen) void el.requestFullscreen();
  else el.webkitRequestFullscreen?.();
}

/** onFullscreenChange calls on whenever the page enters or leaves full screen, until the returned function is called. */
export function onFullscreenChange(on: () => void) {
  document.addEventListener("fullscreenchange", on);
  document.addEventListener("webkitfullscreenchange", on);
  return () => {
    document.removeEventListener("fullscreenchange", on);
    document.removeEventListener("webkitfullscreenchange", on);
  };
}

/** useFullscreen tracks whether the page is full screen. */
export function useFullscreen() {
  const [full, setFull] = useState(isFullscreen);
  useEffect(() => onFullscreenChange(() => setFull(isFullscreen())), []);
  return full;
}
