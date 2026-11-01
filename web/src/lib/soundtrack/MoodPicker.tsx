import { t } from "../i18n/core";
import { VolumeX } from "lucide-react";
import clsx from "clsx";
import { tint, type Mood } from "./core";
import { MoodIcon, moodName } from "./moods";

/** MoodPicker picks one mood (or none) from the presets, as coloured tiles. */
export function MoodPicker({ moods, value, onChange, disabled, label }: { moods: Mood[]; value: number | null; onChange: (id: number | null) => void; disabled?: boolean; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="grid grid-cols-[repeat(auto-fill,minmax(5.5rem,1fr))] gap-2">
      {moods.map((m) => {
        const on = value === m.id;
        return (
          <button
            key={m.id}
            type="button"
            role="radio"
            aria-checked={on}
            disabled={disabled}
            onClick={() => onChange(m.id)}
            className="flex h-16 flex-col items-center justify-center gap-1 rounded-xl disabled:cursor-default"
            style={{ color: m.color, background: tint(m.color, on ? "33" : "14"), border: on ? `2px solid ${m.color}` : `1px solid ${tint(m.color, "55")}`, opacity: disabled && !on ? 0.6 : 1 }}
          >
            <MoodIcon icon={m.icon} className="size-5" />
            <span className="max-w-full truncate px-1 text-[11px] font-medium text-fg">{moodName(m)}</span>
          </button>
        );
      })}
      <button
        type="button"
        role="radio"
        aria-checked={value === null}
        disabled={disabled}
        onClick={() => onChange(null)}
        className={clsx("flex h-16 flex-col items-center justify-center gap-1 rounded-xl border text-muted disabled:cursor-default", value === null ? "border-2 border-muted bg-panel-2" : "border-border", disabled && value !== null && "opacity-60")}
      >
        <VolumeX className="size-5" aria-hidden="true" />
        <span className="text-[11px] font-medium text-fg">{t("No music")}</span>
      </button>
    </div>
  );
}
