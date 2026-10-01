import { getLocale, t } from "../lib/i18n/core";
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Plus, X } from "lucide-react";
import { basePath, type S } from "../api/client";
import { useRootFolders } from "../api/queries";
import { useAccount } from "../lib/account";
import { languageHints, languageMatches, languageName, sortLanguages } from "../lib/format";
import { editionLang } from "./LanguageSelect";

/** useSourcesSettings reads Settings → Search (admins only; shares the
 * settings page's cache). */
export function useSourcesSettings() {
  const { isAdmin } = useAccount();
  return useQuery({
    queryKey: ["settings", "sources"],
    enabled: isAdmin,
    queryFn: async () => {
      const r = await fetch(`${basePath}/api/v1/settings/sources`);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return (await r.json()) as S["Sources"];
    },
  });
}

/** useSearchLanguages is Settings → Search's "search languages by default";
 * without access to it (not an admin) or with none set, the languages of
 * your root folders. */
export function useSearchLanguages(): string[] {
  const { data: src } = useSourcesSettings();
  const { data: roots } = useRootFolders();
  return useMemo(() => {
    const set = src?.defaultLanguages ?? [];
    if (set.length) return set;
    return [...new Set((roots ?? []).map((r) => editionLang(r.language)).filter((l) => l && l !== "*"))];
  }, [src, roots]);
}

/** LanguageLabel is a language's name, with its code when another option
 * has the same name. */
export function LanguageLabel({ code, hint }: { code: string; hint?: boolean }) {
  return (
    <>
      {languageName(code)}
      {hint && <span className="ml-1.5 text-xs text-muted">{code}</span>}
    </>
  );
}

/**
 * LanguageMenu is a button that opens a searchable list of languages; name
 * and code both match. Picking one closes it.
 */
export function LanguageMenu({
  label,
  options,
  exclude = [],
  onPick,
  className,
  align = "left",
  top,
}: {
  label: ReactNode;
  options: string[];
  exclude?: string[];
  onPick: (code: string) => void;
  className?: string;
  align?: "left" | "right";
  /** top entries above the languages (e.g. "Any language"). */
  top?: { label: string; value: string }[];
}) {
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const listId = useId();
  const sorted = useMemo(() => sortLanguages(options.filter((o) => o && !exclude.includes(o))), [options, exclude]);
  const hints = useMemo(() => languageHints(sorted), [sorted]);
  // the best match first: the exact code, then names and codes starting with the query
  const rank = (c: string) => {
    const query = q.trim().toLocaleLowerCase(getLocale());
    if (!query) return 0;
    if (c.toLowerCase() === query) return 0;
    if (languageName(c).toLocaleLowerCase(getLocale()).startsWith(query)) return 1;
    return c.toLowerCase().startsWith(query) ? 2 : 3;
  };
  const shown = sorted.filter((c) => languageMatches(c, q)).sort((a, b) => rank(a) - rank(b));
  const entries = [...(q ? [] : (top ?? []).map((e) => ({ ...e, code: "" }))), ...shown.map((c) => ({ label: "", value: c, code: c }))];
  useEffect(() => {
    if (!open) return;
    const outside = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", outside);
    return () => document.removeEventListener("mousedown", outside);
  }, [open]);
  useEffect(() => setActive(0), [q, open]);
  const pick = (value: string) => {
    onPick(value);
    setOpen(false);
    setQ("");
  };
  return (
    <div ref={ref} className="relative inline-block">
      <button type="button" aria-haspopup="listbox" aria-expanded={open} className={className} onClick={() => setOpen(!open)}>
        {label}
      </button>
      {open && (
        <div className={clsx("absolute z-30 mt-1 w-72 max-w-[calc(100vw-2rem)] overflow-hidden rounded-xl border border-border bg-panel-2 shadow-xl", align === "right" ? "right-0" : "left-0")}>
          <div className="border-b border-border p-2">
            <input
              autoFocus
              role="combobox"
              aria-controls={listId}
              aria-expanded
              aria-activedescendant={entries[active] ? `${listId}-${active}` : undefined}
              aria-label={t("Find a language")}
              placeholder={t("Find a language…")}
              className="h-9 w-full rounded-lg border border-border bg-bg px-2.5 text-sm outline-none focus:border-accent"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") setOpen(false);
                else if (e.key === "ArrowDown") (e.preventDefault(), setActive((i) => Math.min(i + 1, entries.length - 1)));
                else if (e.key === "ArrowUp") (e.preventDefault(), setActive((i) => Math.max(i - 1, 0)));
                else if (e.key === "Enter" && entries[active]) (e.preventDefault(), pick(entries[active].value));
              }}
            />
          </div>
          <ul id={listId} role="listbox" aria-label={t("Languages")} className="max-h-72 overflow-y-auto p-1">
            {entries.map((e, i) => (
              <li
                key={`${e.code}:${e.value}`}
                id={`${listId}-${i}`}
                role="option"
                aria-selected={i === active}
                className={clsx("flex cursor-pointer items-center justify-between gap-2 rounded-lg px-2.5 py-2 text-sm", i === active ? "bg-border" : "hover:bg-border/60")}
                onMouseEnter={() => setActive(i)}
                onMouseDown={(ev) => ev.preventDefault()}
                onClick={() => pick(e.value)}
              >
                <span className="truncate">{e.code ? languageName(e.code) : e.label}</span>
                {e.code && (hints.has(e.code) || q) && <span className="shrink-0 text-xs text-muted">{e.code}</span>}
              </li>
            ))}
            {!entries.length && <li className="px-2.5 py-2 text-sm text-muted">{t("No language matches")}</li>}
          </ul>
        </div>
      )}
    </div>
  );
}

const chipCls = (on: boolean) => clsx("rounded-full border px-3 py-1 text-sm", on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg");

/**
 * LanguageChips filters by one language: "Any", your search languages as
 * chips, and "More…" for the rest. A language picked from the list shows as
 * a chip while it is chosen.
 */
export function LanguageChips({
  value,
  onChange,
  primary,
  options,
  anyLabel = t("Any language"),
}: {
  value: string;
  onChange: (code: string) => void;
  primary: string[];
  options: string[];
  anyLabel?: string;
}) {
  const chips = sortLanguages(value && !primary.includes(value) ? [...primary, value] : primary);
  const hints = languageHints([...new Set([...chips, ...options])]);
  const rest = options.filter((o) => !chips.includes(o));
  return (
    <div role="group" aria-label={t("Language")} className="flex flex-wrap items-center gap-2">
      <button type="button" aria-pressed={!value} className={chipCls(!value)} onClick={() => onChange("")}>{anyLabel}</button>
      {chips.map((code) => (
        <button key={code} type="button" aria-pressed={value === code} className={chipCls(value === code)} onClick={() => onChange(value === code ? "" : code)}>
          <LanguageLabel code={code} hint={hints.has(code)} />
        </button>
      ))}
      {rest.length > 0 && <LanguageMenu label={t("More…")} options={rest} onPick={onChange} className={clsx(chipCls(false), "border-dashed text-fg")} />}
    </div>
  );
}

/**
 * LanguageList edits a set of languages: the chosen ones as a list with
 * remove buttons, and "Add language…" opening the searchable list.
 */
export function LanguageList({
  value,
  onChange,
  options,
  disabled,
  empty,
}: {
  value: string[];
  onChange: (codes: string[]) => void;
  options: string[];
  disabled?: boolean;
  /** empty says what no language means. */
  empty?: string;
}) {
  const chosen = sortLanguages(value);
  const hints = languageHints([...new Set([...chosen, ...options])]);
  return (
    <div className="flex flex-col gap-2">
      {chosen.length > 0 ? (
        <ul className="overflow-hidden rounded-lg border border-border">
          {chosen.map((code) => (
            <li key={code} className="flex items-center gap-3 border-b border-border px-3 py-2 text-sm last:border-b-0">
              <span className="flex-1">{languageName(code)}</span>
              {hints.has(code) && <span className="text-xs text-muted">{code}</span>}
              <button
                type="button"
                disabled={disabled}
                aria-label={t("Remove {name}", { name: languageName(code) })}
                className="flex size-8 items-center justify-center rounded-md border border-border text-muted hover:text-fg disabled:opacity-50"
                onClick={() => onChange(value.filter((v) => v !== code))}
              >
                <X className="size-3.5" />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        empty && <p className="text-sm text-muted">{empty}</p>
      )}
      {!disabled && (
        <LanguageMenu
          label={<><Plus className="mr-1 inline size-3.5" />{t("Add language…")}</>}
          options={options}
          exclude={value}
          onPick={(code) => onChange([...value, code])}
          className="h-8 rounded-md border border-border bg-panel-2 px-3 text-sm hover:border-muted"
        />
      )}
    </div>
  );
}
