import { t } from "../lib/i18n/core";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import clsx from "clsx";
import { ChevronDown, Search, X } from "lucide-react";
import { apiUrl, type Series } from "../api/client";
import { useSeriesList } from "../api/queries";
import { Cover } from "./Cover";

/** fold lowercases a name and drops accents and punctuation, so "Kaguya-sama" finds "kaguya sama". */
export function fold(s: string): string {
  return s.toLocaleLowerCase().normalize("NFKD").replace(/[̀-ͯ]/g, "").replace(/[^\p{L}\p{N}]+/gu, " ").trim();
}

const label = (s: Series) => s.displayTitle || s.title;

/** titleNames lists every name a title is known by: its own, its editions', alt names, names by language. */
export function titleNames(s: Series): string[] {
  const md = s.metadata ?? {};
  const all = [label(s), s.title, s.workTitle ?? "", ...(s.editions ?? []).map((e) => e.title), ...(md.altTitles ?? []), ...Object.values(md.titles ?? {})];
  return [...new Set(all.filter(Boolean))];
}

/** likelySame says whether two titles look like one work: a shared tracker id or a shared name. */
export function likelySame(a: Series, b: Series): boolean {
  const ia = a.metadata?.externalIds ?? {};
  const ib = b.metadata?.externalIds ?? {};
  if (Object.entries(ia).some(([k, v]) => v && ib[k] === v)) return true;
  const names = new Set(titleNames(a).map(fold).filter(Boolean));
  return titleNames(b).some((n) => names.has(fold(n)));
}

type Hit = { s: Series; rank: number; also?: string };

/** match ranks a title for the query: 0 its name starts with it, 1 its name has it, 2 another of its names has it. */
function match(s: Series, words: string[]): Hit | null {
  if (!words.length) return { s, rank: 0 };
  const has = (n: string) => {
    const f = fold(n);
    return words.every((w) => f.includes(w));
  };
  const own = fold(label(s));
  if (words.every((w) => own.includes(w))) return { s, rank: own.startsWith(words[0]) ? 0 : 1 };
  const also = titleNames(s).find(has);
  return also ? { s, rank: 2, also } : null;
}

const byName = (a: Series, b: Series) => label(a).localeCompare(label(b), undefined, { sensitivity: "base", numeric: true });

/**
 * TitleSearch is a search box over the library with the matching titles
 * below it, sorted by name, each with its cover. Any name a title has (in
 * any language, alt names) finds it. Arrow keys move, Enter picks.
 */
export function TitleSearch({
  value,
  onPick,
  exclude,
  suggested = [],
  autoFocus,
  onEscape,
  listClassName,
}: {
  value?: number;
  onPick: (s: Series) => void;
  /** titles left out of the list (the one being grouped, for instance) */
  exclude?: (s: Series) => boolean;
  /** ids shown on top under "Looks like the same title" */
  suggested?: number[];
  autoFocus?: boolean;
  onEscape?: () => void;
  listClassName?: string;
}) {
  const id = useId();
  const { data: library, isLoading } = useSeriesList();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLUListElement>(null);

  const { top, rest } = useMemo(() => {
    const words = fold(query).split(" ").filter(Boolean);
    const hits = (library ?? []).filter((s) => !exclude?.(s)).map((s) => match(s, words)).filter((h): h is Hit => !!h);
    hits.sort((a, b) => a.rank - b.rank || byName(a.s, b.s));
    const want = new Set(suggested);
    return { top: hits.filter((h) => want.has(h.s.id)), rest: hits.filter((h) => !want.has(h.s.id)) };
  }, [library, query, exclude, suggested]);
  const all = useMemo(() => [...top, ...rest], [top, rest]);

  useEffect(() => setActive(0), [query]);
  const move = (i: number) => {
    setActive(i);
    requestAnimationFrame(() => listRef.current?.querySelector(`[data-index="${i}"]`)?.scrollIntoView({ block: "nearest" }));
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const n = all.length;
    if (e.key === "ArrowDown" && n) (e.preventDefault(), move((active + 1) % n));
    else if (e.key === "ArrowUp" && n) (e.preventDefault(), move((active - 1 + n) % n));
    else if (e.key === "Enter" && all[active]) (e.preventDefault(), onPick(all[active].s));
    else if (e.key === "Escape" && (query || onEscape)) {
      e.stopPropagation();
      if (query) setQuery("");
      else onEscape?.();
    }
  };

  const optionId = (i: number) => `${id}-opt-${i}`;
  const row = (h: Hit, i: number) => {
    const s = h.s;
    const langs = [...new Set((s.editions?.length ? s.editions.map((e) => e.language) : [s.language]).filter(Boolean))];
    return (
      <li
        key={s.id}
        id={optionId(i)}
        data-index={i}
        role="option"
        aria-selected={s.id === value}
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => onPick(s)}
        onMouseMove={() => i !== active && setActive(i)}
        className={clsx("flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-1.5 text-sm", i === active ? "bg-panel-2" : "", s.id === value && "text-accent-2")}
      >
        <Cover src={apiUrl(s.coverUrl)} alt="" className="aspect-[2/3] w-8 shrink-0 rounded" />
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{label(s)}</span>
          <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted">
            {langs.map((l) => (
              <span key={l} className="rounded-full border border-border px-1.5 uppercase leading-4">{l}</span>
            ))}
            <span className="shrink-0">{t("{count} ch.", { count: s.stats.chapterCount })}</span>
            {h.also && <span className="truncate">· {t("also “{name}”", { name: h.also })}</span>}
          </span>
        </span>
      </li>
    );
  };
  const section = (text: ReactNode) => (
    <li role="presentation" className="px-2 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wide text-muted">{text}</li>
  );

  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted" />
        <input
          type="text"
          role="combobox"
          aria-expanded
          aria-controls={`${id}-list`}
          aria-autocomplete="list"
          aria-activedescendant={all.length ? optionId(active) : undefined}
          aria-label={t("Search titles…")}
          autoComplete="off"
          autoFocus={autoFocus}
          value={query}
          placeholder={t("Search titles…")}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={onKeyDown}
          className="h-10 w-full rounded-md border border-border bg-bg pl-8 pr-3 text-sm text-fg placeholder:text-muted/60 focus:border-accent focus:outline-none max-sm:text-base"
        />
      </div>
      <ul ref={listRef} id={`${id}-list`} role="listbox" aria-label={t("Titles")} className={clsx("overflow-y-auto overscroll-contain rounded-lg border border-border bg-panel p-1", listClassName ?? "max-h-80")}>
        {top.length > 0 && section(t("Looks like the same title"))}
        {top.map((h, i) => row(h, i))}
        {top.length > 0 && rest.length > 0 && section(query.trim() ? t("Matching “{query}”", { query: query.trim() }) : t("All titles"))}
        {rest.map((h, i) => row(h, top.length + i))}
        {!isLoading && all.length === 0 && <li role="presentation" className="px-2.5 py-2 text-sm text-muted">{query.trim() ? t("No title matches “{query}”.", { query: query.trim() }) : t("No titles yet.")}</li>}
      </ul>
    </div>
  );
}

/**
 * TitlePicker is a filter button showing the chosen title with its cover;
 * it opens TitleSearch in a popover, and ✕ clears the choice.
 */
export function TitlePicker({
  value,
  onChange,
  placeholder,
  className,
}: {
  value: number;
  onChange: (id: number) => void;
  /** shown when nothing is chosen, such as "All titles" */
  placeholder: string;
  className?: string;
}) {
  const { data: library } = useSeriesList();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const chosen = value ? (library ?? []).find((s) => s.id === value || s.editions?.some((e) => e.id === value)) : undefined;
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);
  return (
    <div ref={ref} className={clsx("relative", className ?? "w-full sm:w-56")}>
      <div className="flex h-10 w-full items-center rounded-md border border-border bg-bg text-sm">
        <button type="button" aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen(!open)} className="flex h-full min-w-0 flex-1 items-center gap-2 pl-2 pr-1 text-left">
          {chosen ? (
            <>
              <Cover src={apiUrl(chosen.coverUrl)} alt="" className="aspect-[2/3] w-5 shrink-0 rounded-sm" />
              <span className="truncate text-fg">{label(chosen)}</span>
            </>
          ) : (
            <span className="truncate pl-1 text-muted">{value ? `#${value}` : placeholder}</span>
          )}
          {!value && <ChevronDown className="ml-auto size-3.5 shrink-0 text-muted" />}
        </button>
        {value > 0 && (
          <button type="button" aria-label={t("Clear")} onClick={() => onChange(0)} className="mr-1 inline-flex size-7 shrink-0 items-center justify-center rounded text-muted hover:bg-panel-2 hover:text-fg">
            <X className="size-3.5" />
          </button>
        )}
      </div>
      {open && (
        <div className="absolute right-0 z-30 mt-1 w-[min(24rem,calc(100vw-2rem))] rounded-lg border border-border bg-panel-2 p-2 shadow-xl">
          <TitleSearch
            autoFocus
            value={value}
            onEscape={() => setOpen(false)}
            onPick={(s) => {
              onChange(s.id);
              setOpen(false);
            }}
          />
        </div>
      )}
    </div>
  );
}
