import { t } from "../lib/i18n/core";
import clsx from "clsx";
import { Check } from "lucide-react";

// mangarr orange first; each reads on both themes once accentVars adjusts it
export const accents = ["#e8663d", "#3b82f6", "#10b981", "#a855f7", "#e11d48", "#f59e0b"];

/**
 * AccentPicker chooses an accent colour: a few swatches, any colour, and
 * optionally "use the default" (the server's, for a person).
 */
export function AccentPicker({ value: raw, onChange, defaultLabel }: { value?: string; onChange: (hex: string) => void; defaultLabel: string }) {
  const value = raw ?? "";
  const swatch = (hex: string) => (
    <button
      key={hex}
      type="button"
      aria-label={hex}
      aria-pressed={value.toLowerCase() === hex}
      onClick={() => onChange(hex)}
      className={clsx("flex size-8 items-center justify-center rounded-full ring-offset-2 ring-offset-panel", value.toLowerCase() === hex && "ring-2 ring-fg")}
      style={{ background: hex }}
    >
      {value.toLowerCase() === hex && <Check className="size-4 text-white" />}
    </button>
  );
  return (
    <div role="group" aria-label={t("Accent colour")} className="flex flex-wrap items-center gap-2.5">
      <button type="button" aria-pressed={!value} onClick={() => onChange("")} className={clsx("rounded-full border px-3 py-1 text-sm", !value ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg")}>
        {defaultLabel}
      </button>
      {accents.map(swatch)}
      <label className="flex items-center gap-2 text-sm text-muted">
        <input type="color" aria-label={t("Any colour")} value={value || accents[0]} onChange={(e) => onChange(e.target.value)} className="size-8 cursor-pointer rounded-full border border-border bg-transparent p-0" />
        {t("Any colour")}
      </label>
    </div>
  );
}

/** StartPageOptions are the pages mangarr can open on. */
export function StartPageOptions() {
  return (
    <>
      <option value="series">{t("Series")}</option>
      <option value="discover">{t("Discover")}</option>
      <option value="updates">{t("Updates")}</option>
      <option value="continue">{t("Continue reading")}</option>
    </>
  );
}
