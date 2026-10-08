import { t as tr, t } from "../../lib/i18n/core";
import type { ReactNode } from "react";
import { X } from "lucide-react";
import clsx from "clsx";
import type { ReaderSettings } from "./settings";
import { canFullscreen } from "./fullscreen";

function Choice<T extends string>({ value, options, onChange }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={clsx(
            "rounded-md border px-2.5 py-1 text-xs",
            value === o.value ? "border-accent bg-accent/20 text-accent-2" : "border-border text-fg/80 hover:border-muted/60",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-xs font-medium uppercase tracking-wide text-muted">{label}</div>
      {children}
    </div>
  );
}

function Toggle({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <label className="flex cursor-pointer items-center justify-between gap-3 text-sm text-fg">
      {label}
      <input type="checkbox" className="size-4 accent-accent" checked={checked} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

/** SettingsPanel edits the reader settings (saved for this series). */
export function SettingsPanel({
  s,
  set,
  onClose,
  hasOwn,
  saveAsDefault,
  reset,
}: {
  s: ReaderSettings;
  set: (p: Partial<ReaderSettings>) => void;
  onClose: () => void;
  hasOwn: boolean;
  saveAsDefault: () => void;
  reset: () => void;
}) {
  const paged = s.mode === "paged";
  return (
    <div
      className="absolute inset-y-0 right-0 z-30 flex w-full max-w-sm flex-col overflow-y-auto border-l border-border bg-bg/95 p-4 text-fg backdrop-blur"
      style={{ paddingTop: "max(1rem, env(safe-area-inset-top))", paddingBottom: "max(1rem, env(safe-area-inset-bottom))" }}
      onClick={(e) => e.stopPropagation()}
      onPointerUp={(e) => e.stopPropagation()}
    >
      <div className="mb-4 flex items-center justify-between">
        <h2 className="font-semibold">{t("Reader settings")}</h2>
        <button type="button" className="rounded p-1 hover:bg-panel-2" onClick={onClose} aria-label={t("Close")}>
          <X className="size-5" />
        </button>
      </div>
      <div className="flex flex-col gap-5">
        <Row label={t("Reading mode")}>
          <Choice
            value={paged ? s.direction : "webtoon"}
            options={[
              { value: "rtl", label: "Right to left" },
              { value: "ltr", label: "Left to right" },
              { value: "vertical", label: "Vertical" },
              { value: "webtoon", label: "Webtoon" },
            ]}
            onChange={(v) => set(v === "webtoon" ? { mode: "webtoon" } : { mode: "paged", direction: v })}
          />
        </Row>
        {paged && (
          <>
            <Row label={t("Scale")}>
              <Choice
                value={s.scale}
                options={[
                  { value: "screen", label: "Fit screen" },
                  { value: "width", label: "Fit width" },
                  { value: "height", label: "Fit height" },
                  { value: "original", label: "Original" },
                ]}
                onChange={(scale) => set({ scale })}
              />
            </Row>
            {s.direction !== "vertical" && (
              <Row label={t("Pages")}>
                <Choice
                  value={s.spread}
                  options={[
                    { value: "single", label: "One" },
                    { value: "double", label: "Two" },
                    { value: "auto", label: "Two on wide screens" },
                  ]}
                  onChange={(spread) => set({ spread })}
                />
                {s.spread !== "single" && <Toggle checked={s.coverAlone} onChange={(coverAlone) => set({ coverAlone })} label={t("First page alone (cover)")} />}
                {s.spread === "single" && <Toggle checked={s.splitWide} onChange={(splitWide) => set({ splitWide })} label={t("Split double pages")} />}
              </Row>
            )}
          </>
        )}
        {!paged && (
          <Row label={t("Webtoon")}>
            <div className="flex items-center gap-3 text-sm text-fg">{t("Side padding")}<input type="range" min={0} max={30} step={5} value={s.padding} onChange={(e) => set({ padding: Number(e.target.value) })} className="flex-1 accent-accent" />
              <span className="w-10 text-right text-xs text-muted">{s.padding}%</span>
            </div>
            <Toggle checked={s.gap} onChange={(gap) => set({ gap })} label={t("Gap between pages")} />
          </Row>
        )}
        <Toggle checked={s.crop} onChange={(crop) => set({ crop })} label={paged ? tr("Crop borders") : tr("Crop side borders")} />
        <Row label={t("Tap zones")}>
          <Choice
            value={s.tapZones}
            options={[
              { value: "auto", label: "Default" },
              { value: "lshape", label: "L-shaped" },
              { value: "kindle", label: "Kindle-ish" },
              { value: "edge", label: "Edges" },
              { value: "leftright", label: "Left and right" },
              { value: "off", label: "Off" },
            ]}
            onChange={(tapZones) => set({ tapZones })}
          />
          <Toggle checked={s.invertTaps} onChange={(invertTaps) => set({ invertTaps })} label={t("Invert tap zones")} />
          {s.tapZones !== "off" && <Toggle checked={s.showTapZones} onChange={(showTapZones) => set({ showTapZones })} label={t("Show tap zones when opening a chapter")} />}
        </Row>
        <Row label={t("Background")}>
          <Choice
            value={s.background}
            options={[
              { value: "black", label: "Black" },
              { value: "gray", label: "Gray" },
              { value: "white", label: "White" },
            ]}
            onChange={(background) => set({ background })}
          />
        </Row>
        <div className="flex flex-col gap-2.5">
          <Toggle checked={s.autoNext} onChange={(autoNext) => set({ autoNext })} label={t("Go on to the next chapter")} />
          <Toggle checked={s.keepAwake} onChange={(keepAwake) => set({ keepAwake })} label={t("Keep the screen on")} />
          <Toggle checked={s.showPageNumber} onChange={(showPageNumber) => set({ showPageNumber })} label={t("Show the page number")} />
          {canFullscreen() && <Toggle checked={s.quickFullscreen} onChange={(quickFullscreen) => set({ quickFullscreen })} label={t("Quick full screen button")} />}
        </div>
        <p className="text-xs text-muted">{t("Changes are kept for this series.")}</p>
        <div className="flex flex-wrap gap-2">
          <button type="button" className="rounded-md border border-border px-3 py-1.5 text-xs hover:border-muted/60" onClick={saveAsDefault}>{t("Use for all series")}</button>
          {hasOwn && (
            <button type="button" className="rounded-md border border-border px-3 py-1.5 text-xs hover:border-muted/60" onClick={reset}>{t("Back to my defaults")}</button>
          )}
        </div>
      </div>
    </div>
  );
}
