import { t } from "../../lib/i18n/core";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import clsx from "clsx";
import { Check, X } from "lucide-react";
import { Segmented } from "../../components/ui";
import { genreName } from "../../lib/genres";

export type GenreMatch = "all" | "any";

/**
 * GenrePicker picks several genres and tags: type to narrow the list, tick
 * the ones a series needs, remove them as chips. Picked ones stay on top.
 */
export function GenrePicker({
  genres,
  counts,
  value,
  onChange,
  match,
  onMatch,
}: {
  /** the library's genres and tags, most common first */
  genres: string[];
  counts?: Record<string, number>;
  value: string[];
  onChange: (v: string[]) => void;
  match: GenreMatch;
  onMatch: (m: GenreMatch) => void;
}) {
  const id = useId();
  const listId = `${id}-list`;
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  // the highlighted option, by name, so it stays put when ticking one moves it to the top
  const [activeName, setActiveName] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);

  // the list closes on a tap outside the picker, not on blur, so a phone can put its keyboard away and still scroll it
  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    return () => document.removeEventListener("pointerdown", outside);
  }, [open]);

  const options = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    const matches = (g: string) => !needle || genreName(g).toLocaleLowerCase().includes(needle) || g.toLocaleLowerCase().includes(needle);
    // a picked genre the library no longer has still shows, so it can be unticked
    const picked = [...value].sort((a, b) => genreName(a).localeCompare(genreName(b))).filter(matches);
    const rest = genres.filter((g) => !value.includes(g) && matches(g));
    return { picked, rest, all: [...picked, ...rest] };
  }, [genres, value, query]);

  const active = Math.max(0, options.all.indexOf(activeName));
  // moving with the arrow keys scrolls the highlight into view; a click leaves the list where it is
  const setActive = (i: number) => {
    setActiveName(options.all[i] ?? "");
    requestAnimationFrame(() => listRef.current?.querySelector(`[data-index="${i}"]`)?.scrollIntoView({ block: "nearest" }));
  };
  useEffect(() => setActiveName(""), [query]);

  const toggle = (g: string) => onChange(value.includes(g) ? value.filter((v) => v !== g) : [...value, g]);
  const optionId = (i: number) => `${id}-opt-${i}`;

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    const n = options.all.length;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      if (open && n) setActive((active + 1) % n);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setOpen(true);
      if (open && n) setActive((active - 1 + n) % n);
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (open && options.all[active]) toggle(options.all[active]);
      else setOpen(true);
    } else if (e.key === "Backspace" && query === "" && value.length > 0) {
      onChange(value.slice(0, -1));
    } else if (e.key === "Escape" && (open || query)) {
      // the first Escape closes the list, the next one the filters
      e.stopPropagation();
      if (query) setQuery("");
      else setOpen(false);
    }
  };

  const row = (g: string, i: number) => {
    const on = value.includes(g);
    const count = counts?.[g];
    return (
      <li
        key={g}
        id={optionId(i)}
        data-index={i}
        role="option"
        aria-selected={on}
        // keeps the focus in the search field
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => {
          toggle(g);
          setActiveName(g);
        }}
        className={clsx("flex min-h-9 cursor-pointer items-center gap-2.5 rounded-md px-2.5 text-sm text-fg max-sm:min-h-11", i === active ? "bg-panel-2" : "hover:bg-panel-2")}
      >
        <span aria-hidden className={clsx("inline-flex size-4 shrink-0 items-center justify-center rounded border", on ? "border-primary bg-primary text-white" : "border-border")}>
          {on && <Check className="size-3" strokeWidth={3} />}
        </span>
        <span className="min-w-0 truncate">{genreName(g)}</span>
        {count !== undefined && <span className="ml-auto text-xs tabular-nums text-muted">{count}</span>}
      </li>
    );
  };

  return (
    <div ref={rootRef} className="flex flex-col gap-1.5 text-xs text-muted">
      <div className="flex items-center justify-between gap-2">
        <label htmlFor={`${id}-input`}>{t("Genres and tags")}</label>
        <Segmented<GenreMatch>
          label={t("Series must have")}
          value={match}
          onChange={onMatch}
          options={[
            { value: "all", label: t("All of them") },
            { value: "any", label: t("Any of them") },
          ]}
        />
      </div>
      {/* a click anywhere in the box puts the cursor in the search */}
      <div
        onClick={() => {
          inputRef.current?.focus();
          setOpen(true);
        }}
        className={clsx("flex min-h-10 cursor-text flex-wrap items-center gap-1.5 rounded-md border bg-bg px-2 py-1.5", open ? "border-accent" : "border-border")}
      >
        {value.map((g) => (
          <span key={g} className="inline-flex h-6 max-w-full items-center gap-0.5 rounded-full bg-accent/15 pl-2.5 pr-0.5 text-xs font-medium text-accent-2">
            <span className="truncate">{genreName(g)}</span>
            <button
              type="button"
              aria-label={t("Remove {name}", { name: genreName(g) })}
              onClick={(e) => {
                e.stopPropagation();
                toggle(g);
              }}
              className="inline-flex size-5 shrink-0 items-center justify-center rounded-full hover:bg-accent/25"
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        <input
          ref={inputRef}
          id={`${id}-input`}
          type="text"
          role="combobox"
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={open && options.all.length ? optionId(active) : undefined}
          autoComplete="off"
          value={query}
          placeholder={value.length ? t("Add more…") : t("Search {count} genres and tags…", { count: genres.length })}
          onFocus={() => setOpen(true)}
          onChange={(e) => {
            setQuery(e.target.value);
            setOpen(true);
          }}
          onKeyDown={onKeyDown}
          className="h-7 min-w-24 flex-1 bg-transparent px-1 text-sm text-fg placeholder:text-muted/60 focus:outline-none max-sm:text-base"
        />
      </div>
      {open && (
        <ul ref={listRef} id={listId} role="listbox" aria-multiselectable="true" aria-label={t("Genres and tags")} className="flex max-h-60 flex-col overflow-y-auto overscroll-contain rounded-lg border border-border bg-panel p-1">
          {options.picked.map((g, i) => row(g, i))}
          {options.picked.length > 0 && options.rest.length > 0 && <li role="presentation" className="mx-1.5 my-1 h-px bg-border" />}
          {options.rest.map((g, i) => row(g, options.picked.length + i))}
          {options.all.length === 0 && <li role="presentation" className="px-2.5 py-2 text-sm text-muted">{t("No genre or tag matches “{query}”.", { query: query.trim() })}</li>}
        </ul>
      )}
    </div>
  );
}
