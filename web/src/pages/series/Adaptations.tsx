import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import clsx from "clsx";
import { ExternalLink, Play, X } from "lucide-react";
import type { S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { IconButton } from "../../components/ui";
import { t } from "../../lib/i18n/core";

type Adaptation = S["AdaptationResource"];

function formatName(format: string) {
  switch (format) {
    case "tv": return t("TV");
    case "tv_short": return t("TV short");
    case "movie": return t("Movie");
    case "ova": return t("OVA");
    case "ona": return t("ONA");
    case "special": return t("Special");
  }
  return format.toUpperCase();
}

// AniList first: it's where adaptations come from
function mainLink(a: Adaptation): [string, string] | undefined {
  const links = Object.entries(a.links ?? {});
  return links.find(([k]) => k.toLowerCase() === "anilist") ?? links[0];
}

function chipLabel(list: Adaptation[]) {
  const watchable = list.filter((a) => a.watchLinks.length > 0).length;
  const servers = new Set(list.flatMap((a) => a.watchLinks.map((w) => w.serverName)));
  let label = t("Anime · {count}", { count: list.length });
  if (watchable > 0) {
    label += " · " + (servers.size === 1 ? t("{count} on {server}", { count: watchable, server: [...servers][0] }) : t("{count} to watch", { count: watchable }));
  }
  return label;
}

/**
 * AdaptationsChip is a small chip next to the title's links that opens the
 * list of its anime and movie adaptations: a popover on wide screens, a
 * sheet from the bottom on a phone. Each one links to the media servers
 * that have it, or to AniList when none does.
 */
export function AdaptationsChip({ adaptations }: { adaptations: Adaptation[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [shift, setShift] = useState(0);
  const titleId = useId();
  // keep the popover on screen when the chip sits near the right edge
  useLayoutEffect(() => {
    const el = panel.current;
    if (!open || !el || window.matchMedia("(max-width: 639px)").matches) return setShift(0);
    const over = el.getBoundingClientRect().right - (document.documentElement.clientWidth - 8);
    setShift(over > 0 ? -over : 0);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const outside = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        ref.current?.querySelector<HTMLButtonElement>("button")?.focus();
      }
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("keydown", key);
    };
  }, [open]);
  if (adaptations.length === 0) return null;
  const watchable = adaptations.some((a) => a.watchLinks.length > 0);
  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={clsx(
          "inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium sm:h-6",
          watchable ? "border-ok/50 bg-ok/15 text-ok hover:bg-ok/25" : "border-border bg-panel-2 text-fg hover:border-accent/60",
        )}
      >
        <Play className="size-3 fill-current" />
        {chipLabel(adaptations)}
      </button>
      {open && (
        <>
          <div className="fixed inset-0 z-40 bg-black/50 sm:hidden" aria-hidden onClick={() => setOpen(false)} />
          <div
            ref={panel}
            role="dialog"
            aria-labelledby={titleId}
            style={shift ? { transform: `translateX(${shift}px)` } : undefined}
            className={clsx(
              "z-50 flex flex-col gap-0.5 border border-border bg-panel p-2 shadow-2xl",
              "fixed inset-x-0 bottom-0 max-h-[75vh] overflow-y-auto rounded-t-2xl pb-6",
              "sm:absolute sm:inset-x-auto sm:bottom-auto sm:left-0 sm:top-full sm:mt-2 sm:max-h-[60vh] sm:w-[28rem] sm:rounded-xl sm:pb-2",
            )}
          >
            <div className="mx-auto mb-2 h-1 w-9 rounded-full bg-border sm:hidden" aria-hidden />
            <div className="flex items-center justify-between px-2.5 pb-1.5">
              <h2 id={titleId} className="text-xs font-normal text-muted">{t("Adaptations · from AniList")}</h2>
              <IconButton title={t("Close")} className="sm:hidden" onClick={() => setOpen(false)}>
                <X className="size-4" />
              </IconButton>
            </div>
            {adaptations.map((a, i) => (
              <AdaptationRow key={a.externalIds?.anilist ?? i} adaptation={a} />
            ))}
          </div>
        </>
      )}
    </div>
  );
}

function AdaptationRow({ adaptation: a }: { adaptation: Adaptation }) {
  const link = mainLink(a);
  const meta = [formatName(a.format), a.year ? String(a.year) : ""].filter(Boolean).join(" · ");
  const several = a.watchLinks.length > 1;
  return (
    <div className="flex min-h-14 items-center gap-2.5 rounded-md px-2.5 py-1.5 hover:bg-panel-2">
      <Cover src={a.coverUrl} alt="" className="aspect-[2/3] w-9 shrink-0 rounded-sm" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium" title={a.title}>{a.title}</div>
        <div className="text-xs text-muted">{meta}</div>
      </div>
      {a.watchLinks.map((w) => (
        <a
          key={w.url}
          href={w.url}
          target="_blank"
          rel="noreferrer"
          title={t("Watch on {server}", { server: w.serverName })}
          className="inline-flex h-9 shrink-0 items-center gap-1.5 rounded-md border border-ok/50 bg-ok/15 px-3 text-xs font-medium text-ok hover:bg-ok/25 sm:h-7 sm:px-2.5"
        >
          <Play className="size-3 fill-current" />
          {several ? w.serverName : t("Watch")}
        </a>
      ))}
      {link && (
        <a
          href={link[1]}
          target="_blank"
          rel="noreferrer"
          className={clsx(
            "h-9 shrink-0 items-center gap-1 rounded-md border border-border bg-panel-2 px-3 text-xs font-medium text-fg hover:bg-border sm:h-7 sm:px-2.5",
            // on a phone a watch link is enough
            a.watchLinks.length > 0 ? "hidden sm:inline-flex" : "inline-flex",
          )}
        >
          <ExternalLink className="size-3" />
          {link[0]}
        </a>
      )}
    </div>
  );
}

