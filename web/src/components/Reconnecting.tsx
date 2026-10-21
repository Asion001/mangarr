import { useEffect, useState } from "react";
import { useMatch } from "react-router";
import { useReachable } from "../lib/offline/connection";
import { t } from "../lib/i18n/core";
import { useServerDown } from "../lib/connection";
import { Spinner } from "./ui";

/**
 * Reconnecting is a small note at the top while mangarr doesn't answer (it
 * is restarting). Pages keep what they show and pick up once it's back. A
 * blip shorter than a second shows nothing.
 */
export function Reconnecting() {
  // the reader works from what it has (and says so in its own bars); offline,
  // the app's banner says it instead
  const reading = !!useMatch("/read/:id");
  const reachable = useReachable();
  const down = useServerDown() && !reading && reachable;
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!down) return setShown(false);
    const timer = window.setTimeout(() => setShown(true), 1000);
    return () => window.clearTimeout(timer);
  }, [down]);
  if (!shown) return null;
  return (
    <div
      role="status"
      className="pointer-events-none fixed left-1/2 top-2 z-50 flex -translate-x-1/2 items-center gap-1.5 rounded-full bg-black/70 py-1 pl-2 pr-3 text-xs text-fg"
      style={{ marginTop: "var(--safe-top)" }}
    >
      <Spinner className="size-3.5 text-fg/80" />
      {t("Reconnecting…")}
    </div>
  );
}
