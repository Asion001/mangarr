import { useEffect } from "react";
import { basePath } from "../../api/client";

const heartbeatMs = 15_000;
const idleAfterMs = 2 * 60_000;

/** useReadingTime reports active, visible web-reader time as an idempotent
 * cumulative session, one per chapter (the webtoon strip changes chapter
 * without reopening the reader). */
export function useReadingTime(chapterId: number) {
  useEffect(() => {
    if (!chapterId) return;
    const session = crypto.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}-reader`;
    let activeSeconds = 0;
    let sentSeconds = 0;
    let lastTick = performance.now();
    let lastActivity = Date.now();
    let visible = document.visibilityState === "visible";
    const activity = () => { lastActivity = Date.now(); };
    const measure = () => {
      const now = performance.now();
      const elapsed = Math.min(20, Math.max(0, (now - lastTick) / 1000));
      if (visible && Date.now() - lastActivity < idleAfterMs) activeSeconds += elapsed;
      lastTick = now;
    };
    const send = () => {
      const seconds = Math.floor(activeSeconds);
      if (seconds <= sentSeconds) return;
      sentSeconds = seconds;
      void fetch(`${basePath}/api/v1/read/chapters/${chapterId}/time`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sessionId: session, activeSeconds: seconds }),
        keepalive: true,
      });
    };
    const heartbeat = () => { measure(); send(); };
    const visibility = () => {
      measure();
      visible = document.visibilityState === "visible";
      if (visible) activity();
      else send();
    };
    for (const event of ["pointerdown", "keydown", "wheel", "touchstart"] as const) {
      window.addEventListener(event, activity, { passive: true });
    }
    document.addEventListener("visibilitychange", visibility);
    window.addEventListener("pagehide", heartbeat);
    const timer = window.setInterval(heartbeat, heartbeatMs);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", visibility);
      window.removeEventListener("pagehide", heartbeat);
      for (const event of ["pointerdown", "keydown", "wheel", "touchstart"] as const) window.removeEventListener(event, activity);
      heartbeat();
    };
  }, [chapterId]);
}
