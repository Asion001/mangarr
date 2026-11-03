import { t } from "../../lib/i18n/core";
import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Pause, Play, Plus, Search, Square, X } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Button, Input, Loading, Modal } from "../../components/ui";
import { dbGain, tint, type Mood } from "../../lib/soundtrack/core";
import { AmbienceEngine } from "../../lib/soundtrack/engine";
import { categoryColor, categoryName, TrackIcon, trackUrl } from "../../lib/soundtrack/tracks";

type Track = S["SoundtrackTrackView"];
export type Layer = { trackId: number; volume: number; title: string; category: string; icon: string; gainDb: number; pack: string; license: string };
export type Pad = { trackId: number; label: string; title: string; category: string; icon: string; gainDb: number };

/** soundsOf are a mood's sounds as the editor keeps them. */
export function soundsOf(m: Mood | null | undefined): { ambience: Layer[]; sfx: Pad[] } {
  return {
    ambience: (m?.ambience ?? []).map((a) => ({ trackId: a.trackId, volume: a.volume, title: a.title, category: a.category, icon: a.icon, gainDb: a.gainDb, pack: a.pack, license: a.license })),
    sfx: (m?.sfx ?? []).map((a) => ({ trackId: a.trackId, label: a.label ?? "", title: a.title, category: a.category, icon: a.icon, gainDb: a.gainDb })),
  };
}

/** usePreview plays sounds in the editor: one engine, stopped when the editor goes. */
export function usePreview() {
  const engine = useMemo(() => new AmbienceEngine(), []);
  useEffect(() => () => engine.close(), [engine]);
  const [playing, setPlaying] = useState<string>("");
  const play = (key: string, layers: { id: number; gain: number }[]) => {
    engine.unlock();
    if (playing === key) {
      void engine.setLayers([]);
      setPlaying("");
      return;
    }
    void engine.setLayers(layers.map((l) => ({ id: l.id, url: trackUrl(l.id), gain: l.gain })));
    setPlaying(key);
  };
  return { engine, playing, play };
}

/** TrackPicker finds an installed ambience loop or effect. */
function TrackPicker({ kind, onPick, onClose, preview }: { kind: "ambience" | "sfx"; onPick: (t: Track) => void; onClose: () => void; preview: ReturnType<typeof usePreview> }) {
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("");
  const { data, isLoading } = useQuery({ queryKey: ["soundtrack", "tracks", kind], queryFn: () => unwrap(api.GET("/api/v1/audio/tracks", { params: { query: { kind } } })) });
  const cats = [...new Set((data ?? []).map((x) => x.category))].sort();
  const list = (data ?? []).filter((x) => (!cat || x.category === cat) && x.title.toLowerCase().includes(q.trim().toLowerCase()));
  return (
    <Modal open onClose={onClose} size="lg" title={kind === "ambience" ? t("Add ambience") : t("Add an effect")}>
      <div className="flex flex-col gap-3">
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("rain, door, crowd…")} aria-label={t("Find a sound")} className="w-full pl-9" autoFocus />
        </div>
        {cats.length > 1 && (
          <div role="radiogroup" aria-label={t("Kind of sound")} className="flex flex-wrap gap-1.5">
            {["", ...cats].map((c) => (
              <button
                key={c || "all"}
                type="button"
                role="radio"
                aria-checked={cat === c}
                onClick={() => setCat(c)}
                className={clsx("h-8 rounded-full border px-3 text-xs font-medium", cat === c ? "border-accent bg-accent/20 text-accent-2" : "border-border text-fg/85")}
              >
                {c ? categoryName(c) : t("All")}
              </button>
            ))}
          </div>
        )}
        {isLoading && <Loading />}
        {data && data.length === 0 && <p className="text-sm text-muted">{t("No sounds installed yet: add a sound pack on the Spotify & packs tab.")}</p>}
        <div className="flex flex-col gap-1.5">
          {list.map((tr) => {
            const c = categoryColor(tr.category);
            const key = "pick" + tr.id;
            return (
              <div key={tr.id} className="flex items-center gap-2 rounded-lg border border-border bg-panel-2 p-1.5">
                <button type="button" onClick={() => onPick(tr)} className="flex min-w-0 flex-1 items-center gap-3 rounded p-1 text-left">
                  <span className="flex size-9 shrink-0 items-center justify-center rounded-md" style={{ color: c, background: tint(c, "26") }}>
                    <TrackIcon icon={tr.icon} className="size-4" />
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-medium">{tr.title}</span>
                    <span className="block truncate text-xs text-muted">{[tr.pack, categoryName(tr.category), tr.license].filter(Boolean).join(" · ")}</span>
                  </span>
                </button>
                <button
                  type="button"
                  className="flex size-10 shrink-0 items-center justify-center rounded hover:bg-border"
                  aria-label={preview.playing === key ? t("Stop") : t("Preview")}
                  onClick={() => (kind === "sfx" ? void preview.engine.playEffect(trackUrl(tr.id), dbGain(tr.gainDb)) : preview.play(key, [{ id: tr.id, gain: dbGain(tr.gainDb) }]))}
                >
                  {preview.playing === key ? <Square className="size-4" /> : <Play className="size-4" />}
                </button>
              </div>
            );
          })}
        </div>
      </div>
    </Modal>
  );
}

/** MoodSounds edits a mood's ambience loops and effect pads. */
export function MoodSounds({ ambience, sfx, onChange, preview }: { ambience: Layer[]; sfx: Pad[]; onChange: (v: { ambience?: Layer[]; sfx?: Pad[] }) => void; preview: ReturnType<typeof usePreview> }) {
  const [adding, setAdding] = useState<"ambience" | "sfx" | null>(null);
  const allKey = "mood";
  return (
    <>
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 className="text-sm font-semibold">{t("Ambience · from sound packs")}</h3>
          <span className="text-xs text-muted">{t("Up to 4 loops; plays for readers who pick Ambience, and quietly under Spotify")}</span>
        </div>
        {ambience.map((a, i) => {
          const key = "layer" + a.trackId;
          return (
            <div key={a.trackId} className="grid grid-cols-[minmax(0,1fr)_minmax(6rem,15rem)_2.75rem_2.75rem] items-center gap-2.5 rounded-lg bg-panel-2 px-2.5 py-2">
              <div className="min-w-0">
                <div className="truncate text-sm">{a.title}</div>
                <div className="truncate text-[11px] text-muted">{[a.pack, a.license].filter(Boolean).join(" · ")}</div>
              </div>
              <input
                type="range"
                min={0}
                max={100}
                value={Math.round(a.volume * 100)}
                onChange={(e) => onChange({ ambience: ambience.map((x, k) => (k === i ? { ...x, volume: Number(e.target.value) / 100 } : x)) })}
                aria-label={t("{name} volume", { name: a.title })}
                className="accent-accent"
              />
              <button type="button" className="flex size-11 items-center justify-center rounded hover:bg-border" aria-label={preview.playing === key ? t("Stop") : t("Preview")} onClick={() => preview.play(key, [{ id: a.trackId, gain: a.volume * dbGain(a.gainDb) }])}>
                {preview.playing === key ? <Pause className="size-4" /> : <Play className="size-4" />}
              </button>
              <button type="button" className="flex size-11 items-center justify-center rounded text-muted hover:bg-border hover:text-fg" aria-label={t("Remove")} onClick={() => onChange({ ambience: ambience.filter((_, k) => k !== i) })}>
                <X className="size-4" />
              </button>
            </div>
          );
        })}
        <div className="flex flex-wrap gap-2">
          {ambience.length < 4 && (
            <button type="button" onClick={() => setAdding("ambience")} className="flex h-11 flex-1 items-center justify-center gap-1.5 rounded-lg border border-dashed border-border text-[13px] font-medium hover:border-muted/60">
              <Plus className="size-4" aria-hidden="true" /> {t("Add ambience")}
            </button>
          )}
          {ambience.length > 0 && (
            <Button onClick={() => preview.play(allKey, ambience.map((a) => ({ id: a.trackId, gain: a.volume * dbGain(a.gainDb) })))}>
              {preview.playing === allKey ? t("Stop preview") : t("Preview ambience")}
            </Button>
          )}
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{t("Sound effects")}</h3>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(6rem,1fr))] gap-2">
          {sfx.map((p, i) => {
            const c = categoryColor(p.category);
            return (
              <div key={p.trackId} className="relative">
                <button
                  type="button"
                  onClick={() => void preview.engine.playEffect(trackUrl(p.trackId), dbGain(p.gainDb))}
                  className="flex h-18 w-full flex-col items-center justify-center gap-1.5 rounded-xl"
                  style={{ color: c, background: tint(c, "1a"), border: `1px solid ${tint(c, "55")}` }}
                  aria-label={t("Play {name}", { name: p.label || p.title })}
                >
                  <TrackIcon icon={p.icon} className="size-5" />
                  <span className="max-w-full truncate px-1 text-xs font-medium text-fg">{p.label || p.title}</span>
                </button>
                <button
                  type="button"
                  onClick={() => onChange({ sfx: sfx.filter((_, k) => k !== i) })}
                  className="absolute -top-1.5 -right-1.5 flex size-6 items-center justify-center rounded-full border border-border bg-panel text-muted hover:text-fg"
                  aria-label={t("Remove")}
                >
                  <X className="size-3" />
                </button>
              </div>
            );
          })}
          {sfx.length < 8 && (
            <button type="button" onClick={() => setAdding("sfx")} className="h-18 rounded-xl border border-dashed border-border text-xs font-medium text-muted hover:text-fg">
              {t("+ Add effect")}
            </button>
          )}
        </div>
      </div>

      {adding && (
        <TrackPicker
          kind={adding}
          preview={preview}
          onClose={() => setAdding(null)}
          onPick={(tr) => {
            if (adding === "ambience" && !ambience.some((a) => a.trackId === tr.id))
              onChange({ ambience: [...ambience, { trackId: tr.id, volume: 0.6, title: tr.title, category: tr.category, icon: tr.icon, gainDb: tr.gainDb, pack: tr.pack, license: tr.license }] });
            if (adding === "sfx" && !sfx.some((a) => a.trackId === tr.id)) onChange({ sfx: [...sfx, { trackId: tr.id, label: "", title: tr.title, category: tr.category, icon: tr.icon, gainDb: tr.gainDb }] });
            setAdding(null);
          }}
        />
      )}
    </>
  );
}
