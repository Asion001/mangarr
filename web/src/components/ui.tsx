import { t, label as translateLabel } from "../lib/i18n/core";
import { useEffect, useId, useLayoutEffect, useRef, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import clsx from "clsx";
import { ChevronDown, Loader2, Lock, X, Plus, Trash2 } from "lucide-react";

type Variant = "primary" | "secondary" | "danger" | "ghost";

export function Button({
  variant = "secondary",
  size = "md",
  loading,
  icon,
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: "sm" | "md"; loading?: boolean; icon?: ReactNode }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || loading}
      className={clsx(
        "inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        size === "sm" ? "h-7 px-2.5 text-xs" : "h-9 px-3.5 text-sm",
        variant === "primary" && "bg-primary text-white hover:bg-primary-hover",
        variant === "secondary" && "border border-border bg-panel-2 text-fg hover:bg-border",
        variant === "danger" && "bg-danger text-white hover:bg-danger-hover",
        variant === "ghost" && "text-muted hover:bg-panel-2 hover:text-fg",
        className,
      )}
    >
      {loading ? <Loader2 className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ title, className, children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { title: string }) {
  return (
    <button
      {...rest}
      title={title}
      aria-label={title}
      className={clsx("inline-flex size-8 items-center justify-center rounded-md text-muted hover:bg-panel-2 hover:text-fg disabled:opacity-40", className)}
    >
      {children}
    </button>
  );
}

const inputCls =
  "rounded-md border border-border bg-bg px-3 py-2 text-sm text-fg placeholder:text-muted/60 focus:border-accent focus:outline-none disabled:opacity-60";

// full width unless the caller sets an explicit width class
const width = (className?: string) => (/(^|\s)(w-|flex-1)/.test(className ?? "") ? "" : "w-full");

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={clsx(inputCls, width(props.className), props.className)} />;
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={clsx(inputCls, "min-h-20", width(props.className), props.className)} />;
}

export function Select({ children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select {...props} className={clsx(inputCls, "pr-8", width(props.className), props.className)}>
      {children}
    </select>
  );
}

export function Switch({
  checked,
  onChange,
  label,
  disabled,
  env,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  label?: ReactNode;
  disabled?: boolean;
  /** Environment variable that pins this value (renders it locked). */
  env?: string;
}) {
  disabled = disabled || !!env;
  return (
    <label className={clsx("inline-flex cursor-pointer select-none items-center gap-2 text-sm", disabled && "opacity-50")}>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={clsx("relative h-5 w-9 shrink-0 rounded-full transition-colors", checked ? "bg-ok" : "bg-border")}
      >
        <span className={clsx("absolute top-0.5 size-4 rounded-full bg-white transition-all", checked ? "left-4.5" : "left-0.5")} />
      </button>
      {label}
      <EnvLock env={env} />
    </label>
  );
}

/** EnvLock marks a value pinned by an environment variable. */
export function EnvLock({ env }: { env?: string }) {
  if (!env) return null;
  return (
    <span className="inline-flex items-center gap-1 text-xs font-normal text-warn" title={`Set by environment variable ${env}; change it in your container config.`}>
      <Lock size={12} aria-label={t("Locked")} />
      env
    </span>
  );
}

/** Locked disables every control inside when env is set. */
export function Locked({ env, children }: { env?: string; children: ReactNode }) {
  return (
    <fieldset disabled={!!env} className="contents">
      {children}
    </fieldset>
  );
}

export function Field({
  label,
  help,
  children,
  className,
  env,
}: {
  label: ReactNode;
  help?: ReactNode;
  children: ReactNode;
  className?: string;
  /** Environment variable that pins this field (renders it locked). */
  env?: string;
}) {
  return (
    <div className={clsx("flex flex-col gap-1.5", className)}>
      <label className="flex flex-wrap items-center gap-2 text-sm font-medium text-fg">
        {label}
        <EnvLock env={env} />
      </label>
      <Locked env={env}>{children}</Locked>
      {env && (
        <p className="text-xs break-all text-warn">{t("Set by") + " "}<code>{env}</code>
        </p>
      )}
      {help && <p className="text-xs text-muted">{help}</p>}
    </div>
  );
}

export function Card({ title, actions, children, className }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={clsx("rounded-lg border border-border bg-panel", className)}>
      {(title || actions) && (
        <header className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold">{title}</h2>
          <div className="flex items-center gap-2">{actions}</div>
        </header>
      )}
      <div className="p-4">{children}</div>
    </section>
  );
}

export function Badge({ tone = "default", children, title }: { tone?: "default" | "ok" | "warn" | "err" | "info" | "accent"; children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      className={clsx(
        "inline-flex items-center gap-1 whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium",
        tone === "default" && "bg-panel-2 text-muted",
        tone === "ok" && "bg-ok/15 text-ok",
        tone === "warn" && "bg-warn/15 text-warn",
        tone === "err" && "bg-err/15 text-err",
        tone === "info" && "bg-info/15 text-info",
        tone === "accent" && "bg-accent/15 text-accent-2",
      )}
    >
      {children}
    </span>
  );
}

export function Spinner({ className }: { className?: string }) {
  return <Loader2 className={clsx("size-5 animate-spin text-muted", className)} />;
}

export function Loading() {
  return (
    <div className="flex items-center justify-center p-10">
      <Spinner />
    </div>
  );
}

export function EmptyState({ title, children, icon }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border p-10 text-center">
      {icon && <div className="text-muted">{icon}</div>}
      <div className="font-medium">{title}</div>
      {children && <div className="max-w-md text-sm text-muted">{children}</div>}
    </div>
  );
}

export function ErrorBox({ error }: { error: unknown }) {
  return (
    <div className="rounded-md border border-err/50 bg-err/10 p-3 text-sm text-err">{error instanceof Error ? error.message : String(error)}</div>
  );
}

export function Progress({ value, tone = "accent" }: { value: number; tone?: "accent" | "ok" | "warn" | "err" }) {
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-border">
      <div
        className={clsx(
          "h-full rounded-full transition-all",
          tone === "accent" && "bg-accent",
          tone === "ok" && "bg-ok",
          tone === "warn" && "bg-warn",
          tone === "err" && "bg-err",
        )}
        style={{ width: `${Math.max(0, Math.min(100, value))}%` }}
      />
    </div>
  );
}

export type MenuItem = { label: string; onSelect: () => void; icon?: ReactNode; danger?: boolean; hidden?: boolean } | { section: string };

/**
 * Menu is a button that opens a list of actions (role="menu"): arrow keys
 * move between items, Escape or a click outside closes it. The list is
 * nudged sideways when it would run past the edge of the screen, which is
 * what happens on a phone when the button sits near the right edge.
 */
export function Menu({ label, icon, items, align = "left", up = false }: { label: ReactNode; icon?: ReactNode; items: MenuItem[]; align?: "left" | "right"; /** up opens above the button (for bars at the bottom) */ up?: boolean }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const [shift, setShift] = useState(0);
  useLayoutEffect(() => {
    const el = list.current;
    if (!open || !el) return setShift(0);
    const margin = 8;
    const r = el.getBoundingClientRect();
    const width = document.documentElement.clientWidth;
    let dx = 0;
    if (r.right > width - margin) dx = width - margin - r.right;
    if (r.left + dx < margin) dx = margin - r.left;
    setShift(dx);
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const root = ref.current;
    const entries = () => Array.from(root?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []);
    entries()[0]?.focus();
    const outside = (e: MouseEvent) => {
      if (!root?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        root?.querySelector<HTMLButtonElement>("button")?.focus();
      } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const list = entries();
        const i = list.indexOf(document.activeElement as HTMLButtonElement);
        list[(i + (e.key === "ArrowDown" ? 1 : -1) + list.length) % list.length]?.focus();
      }
    };
    document.addEventListener("mousedown", outside);
    document.addEventListener("keydown", key);
    return () => {
      document.removeEventListener("mousedown", outside);
      document.removeEventListener("keydown", key);
    };
  }, [open]);
  const shown = items.filter((it) => !("hidden" in it && it.hidden));
  return (
    <div ref={ref} className="relative inline-block">
      <Button icon={icon} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>
        {label}
        <ChevronDown className="size-3.5" />
      </Button>
      {open && (
        <div ref={list} role="menu" style={shift ? { transform: `translateX(${shift}px)` } : undefined} className={clsx("absolute z-30 w-max max-h-[60vh] overflow-y-auto", up ? "bottom-full mb-1" : "mt-1", "min-w-60 max-w-[calc(100vw-1rem)] rounded-lg border border-border bg-panel-2 p-1 shadow-xl", align === "right" ? "right-0" : "left-0")}>
          {shown.map((it, i) =>
            "section" in it && !it.section ? (
              <div key={i} role="separator" className="my-1 border-t border-border" />
            ) : "section" in it ? (
              <div key={i} role="presentation" className={clsx("px-2.5 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wide text-muted", i > 0 && "mt-1 border-t border-border")}>
                {it.section}
              </div>
            ) : (
              <button
                key={i}
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false);
                  it.onSelect();
                }}
                className={clsx("flex w-full items-center gap-2 whitespace-nowrap rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-border focus:bg-border focus:outline-none", it.danger ? "text-err" : "text-fg")}
              >
                {it.icon}
                {it.label}
              </button>
            ),
          )}
        </div>
      )}
    </div>
  );
}

// unsavedChanges is set while a SaveBar has something to save.
let unsavedChanges = false;

/** confirmLeave asks before navigating away from unsaved changes; true means go ahead. */
export function confirmLeave() {
  return !unsavedChanges || window.confirm(t("Leave without saving your changes?"));
}

/**
 * SaveBar sticks to the bottom of a settings page while it has unsaved
 * changes, and asks before a link or closing the tab throws them away.
 * Navigation that isn't a link (a select, a button) asks with confirmLeave.
 */
export function SaveBar({ dirty, saving, onSave, onDiscard }: { dirty: boolean; saving?: boolean; onSave: () => void; onDiscard: () => void }) {
  useEffect(() => {
    if (!dirty) return;
    unsavedChanges = true;
    const beforeUnload = (e: BeforeUnloadEvent) => e.preventDefault();
    // BrowserRouter can't block navigation, so catch in-app links before they route
    const click = (e: MouseEvent) => {
      const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
      if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || a.target === "_blank") return;
      if (!window.confirm(t("Leave without saving your changes?"))) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    window.addEventListener("beforeunload", beforeUnload);
    document.addEventListener("click", click, true);
    return () => {
      unsavedChanges = false;
      window.removeEventListener("beforeunload", beforeUnload);
      document.removeEventListener("click", click, true);
    };
  }, [dirty]);
  if (!dirty) return null;
  return (
    <div role="region" aria-label={t("Unsaved changes")} className="sticky bottom-4 z-10 mt-6 flex flex-wrap items-center gap-3 rounded-xl border border-border bg-panel-2/95 py-3 pl-4 pr-3 shadow-xl backdrop-blur">
      <span aria-hidden className="size-2 rounded-full bg-warn" />
      <span className="flex-1 text-sm font-medium">{t("Unsaved changes")}</span>
      <Button disabled={saving} onClick={onDiscard}>{t("Discard")}</Button>
      <Button variant="primary" loading={saving} onClick={onSave}>{t("Save changes")}</Button>
    </div>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-semibold">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  size?: "sm" | "md" | "lg" | "xl";
}) {
  const titleId = useId();
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 pt-[8vh]" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(e) => e.stopPropagation()}
        className={clsx(
          "w-full rounded-xl border border-border bg-panel shadow-2xl",
          size === "sm" && "max-w-md",
          size === "md" && "max-w-xl",
          size === "lg" && "max-w-3xl",
          size === "xl" && "max-w-5xl",
        )}
      >
        <header className="flex items-center justify-between border-b border-border px-5 py-3.5">
          <h2 id={titleId} className="font-semibold">{title}</h2>
          <IconButton title={t("Close")} onClick={onClose}>
            <X className="size-4" />
          </IconButton>
        </header>
        <div className="max-h-[70vh] overflow-y-auto px-5 py-4">{children}</div>
        {footer && <footer className="flex justify-end gap-2 border-t border-border px-5 py-3">{footer}</footer>}
      </div>
    </div>
  );
}

export function Confirm({
  open,
  title,
  message,
  confirmLabel = "Confirm",
  danger,
  onConfirm,
  onClose,
  loading,
  children,
}: {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  loading?: boolean;
  children?: ReactNode;
}) {
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant={danger ? "danger" : "primary"} loading={loading} onClick={onConfirm}>
            {translateLabel(confirmLabel)}
          </Button>
        </>
      }
    >
      <div className="text-sm text-muted">{message}</div>
      {children}
    </Modal>
  );
}

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { value: T; label: ReactNode }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div className="mb-4 flex gap-1 border-b border-border">
      {tabs.map((t) => (
        <button
          key={t.value}
          onClick={() => onChange(t.value)}
          className={clsx(
            "-mb-px border-b-2 px-3 py-2 text-sm font-medium",
            value === t.value ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg",
          )}
        >
          {typeof t.label === "string" ? translateLabel(t.label) : t.label}
        </button>
      ))}
    </div>
  );
}

/** Segmented picks one of a few values (a compact radio group). */
export function Segmented<T extends string | number>({
  value,
  options,
  onChange,
  label,
  disabled,
}: {
  value: T;
  options: { value: T; label: ReactNode }[];
  onChange: (v: T) => void;
  /** accessible name of the group */
  label: string;
  disabled?: boolean;
}) {
  return (
    <div role="radiogroup" aria-label={label} className={clsx("inline-flex w-fit rounded-md border border-border bg-bg p-0.5", disabled && "opacity-50")}>
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button
            key={String(o.value)}
            type="button"
            role="radio"
            aria-checked={on}
            disabled={disabled}
            onClick={() => !on && onChange(o.value)}
            className={clsx("rounded px-3 py-1 text-xs font-medium", on ? "bg-panel-2 text-fg" : "text-muted hover:text-fg")}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

/** TagInput edits a list of strings (patterns, URLs, tags). */
export function TagInput({ value, onChange, placeholder }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string }) {
  const [draft, setDraft] = useState("");
  const add = () => {
    const v = draft.trim();
    if (v && !value.includes(v)) onChange([...value, v]);
    setDraft("");
  };
  return (
    <div className="flex flex-col gap-2">
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {value.map((v, i) => (
            <span key={v + i} className="inline-flex max-w-full items-center gap-1 rounded bg-panel-2 px-2 py-1 text-xs">
              <span className="truncate">{v}</span>
              <button type="button" className="text-muted hover:text-err" onClick={() => onChange(value.filter((_, j) => j !== i))}>
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <Input
          value={draft}
          placeholder={placeholder}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
        />
        <Button type="button" onClick={add} icon={<Plus className="size-4" />}>{t("Add")}</Button>
      </div>
    </div>
  );
}

/** KeyValueEditor edits a string map (headers, path mappings). */
export function KeyValueEditor({
  value,
  onChange,
  keyPlaceholder = "key",
  valuePlaceholder = "value",
}: {
  value: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const [rows, setRows] = useState<[string, string][]>(() => Object.entries(value ?? {}));
  const commit = (next: [string, string][]) => {
    setRows(next);
    const out: Record<string, string> = {};
    for (const [k, v] of next) if (k.trim()) out[k.trim()] = v;
    onChange(out);
  };
  return (
    <div className="flex flex-col gap-2">
      {rows.map(([k, v], i) => (
        <div key={i} className="flex gap-2">
          <Input value={k} placeholder={keyPlaceholder} onChange={(e) => commit(rows.map((r, j) => (j === i ? [e.target.value, r[1]] : r)))} />
          <Input value={v} placeholder={valuePlaceholder} onChange={(e) => commit(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))} />
          <IconButton title={t("Remove")} type="button" onClick={() => commit(rows.filter((_, j) => j !== i))}>
            <Trash2 className="size-4" />
          </IconButton>
        </div>
      ))}
      <div>
        <Button type="button" size="sm" onClick={() => setRows([...rows, ["", ""]])} icon={<Plus className="size-3.5" />}>{t("Add row")}</Button>
      </div>
    </div>
  );
}

export function Table({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={clsx("overflow-x-auto rounded-lg border border-border", className)}>
      <table className="w-full text-left text-sm">{children}</table>
    </div>
  );
}

export function Th({ children, className }: { children?: ReactNode; className?: string }) {
  return <th className={clsx("whitespace-nowrap border-b border-border bg-panel px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted", className)}>{children}</th>;
}

export function Td({ children, className, colSpan }: { children?: ReactNode; className?: string; colSpan?: number }) {
  return (
    <td colSpan={colSpan} className={clsx("border-b border-border/60 px-3 py-2 align-middle", className)}>
      {children}
    </td>
  );
}
