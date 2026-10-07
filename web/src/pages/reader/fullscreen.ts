import { useEffect, useState } from "react";

// older iPad Safari only has the prefixed calls
type WebkitDocument = Document & { webkitFullscreenElement?: Element | null; webkitFullscreenEnabled?: boolean; webkitExitFullscreen?: () => void };
type WebkitElement = HTMLElement & { webkitRequestFullscreen?: () => void };

const doc = () => document as WebkitDocument;

/** canFullscreen says whether the browser lets a page go full screen (iPhone Safari doesn't). */
export function canFullscreen() {
  return !!(document.fullscreenEnabled || doc().webkitFullscreenEnabled);
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

/** useFullscreen tracks whether the page is full screen. */
export function useFullscreen() {
  const [full, setFull] = useState(isFullscreen);
  useEffect(() => {
    const on = () => setFull(isFullscreen());
    document.addEventListener("fullscreenchange", on);
    document.addEventListener("webkitfullscreenchange", on);
    return () => {
      document.removeEventListener("fullscreenchange", on);
      document.removeEventListener("webkitfullscreenchange", on);
    };
  }, []);
  return full;
}
