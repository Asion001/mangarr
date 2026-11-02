import { t } from "../../lib/i18n/core";
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { Pause, Play, Plus, Search, Square, X } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Button, Confirm, EmptyState, Field, Input, Loading, Modal, Switch } from "../../components/ui";
import { dbGain, tint, type Scene } from "../../lib/soundtrack/core";
import { AmbienceEngine } from "../../lib/soundtrack/engine";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { useMoods, useMySpotify } from "../../lib/soundtrack/spotify";
import { categoryColor, categoryName, TrackIcon, trackUrl } from "../../lib/soundtrack/tracks";
import { useToast } from "../../lib/toast";
import { SceneArt, useScenes } from "../reader/StartScene";
import { ConnectPrompt, SpotifyPicker } from "./Soundtracks";

type Track = S["SoundtrackTrackView"];
type Input = S["SoundtrackSceneInput"];
type Layer = { trackId: number; volume: number; title: string; category: string; icon: string; gainDb: number; pack: string; license: string };
type Pad = { trackId: number; label: string; title: string; category: string; icon: string; gainDb: number };
type Draft = Omit<Input, "ambience" | "sfx"> & { ambience: Layer[]; sfx: Pad[] };

const empty: Draft = { name: "", description: "", spotifyUri: "", spotifyName: "", spotifyImageUrl: "", shuffle: true, ambience: [], sfx: [] };

function draftOf(s: Scene): Draft {
  return {
    name: s.name,
    description: s.description,
    moodId: s.moodId,
    spotifyUri: s.spotifyUri,
    spotifyName: s.spotifyName,
    spotifyImageUrl: s.spotifyImageUrl,
    shuffle: s.shuffle,
    ambience: s.ambience.map((a) => ({ trackId: a.trackId, volume: a.volume, title: a.title, category: a.category, icon: a.icon, gainDb: a.gainDb, pack: a.pack, license: a.license })),
    sfx: s.sfx.map((a) => ({ trackId: a.trackId, label: a.label ?? "", title: a.title, category: a.category, icon: a.icon, gainDb: a.gainDb })),
  };
}

/** usePreview plays sounds in the editor: one engine, stopped when the editor goes. */
function usePreview() {
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

/** SceneEditor edits one scene: its mood and music, ambience layers and effects. */
function SceneEditor({ scene, onSaved, onDeleted }: { scene: Scene | null; onSaved: (s: Scene) => void; onDeleted: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const moods = useMoods();
  const me = useMySpotify();
  const [d, setD] = useState<Draft>(scene ? draftOf(scene) : empty);
  const [ownMusic, setOwnMusic] = useState(!!scene?.spotifyUri);
  const [adding, setAdding] = useState<"ambience" | "sfx" | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const preview = usePreview();
  const patch = (p: Partial<Draft>) => setD((v) => ({ ...v, ...p }));
  const mood = (moods.data ?? []).find((m) => m.id === d.moodId);
  const save = async () => {
    setSaving(true);
    try {
      const body: Input = {
        name: d.name,
        description: d.description,
        moodId: d.moodId,
        spotifyUri: ownMusic ? d.spotifyUri : "",
        spotifyName: ownMusic ? d.spotifyName : "",
        spotifyImageUrl: ownMusic ? d.spotifyImageUrl : "",
        shuffle: d.shuffle,
        ambience: d.ambience.map((a) => ({ trackId: a.trackId, volume: a.volume })),
        sfx: d.sfx.map((p) => ({ trackId: p.trackId, label: p.label || undefined })),
      };
      const saved = await unwrap(scene ? api.PUT("/api/v1/audio/scenes/{id}", { params: { path: { id: scene.id } }, body }) : api.POST("/api/v1/audio/scenes", { body }));
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      toast.success(t("Scene saved"));
      onSaved(saved);
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!scene) return;
    try {
      await unwrap(api.DELETE("/api/v1/audio/scenes/{id}", { params: { path: { id: scene.id } } }));
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      onDeleted();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const sceneKey = "scene";
  return (
    <div className="flex min-w-0 flex-col gap-5 rounded-xl border border-border bg-panel p-5">
      <Field label={t("Scene name")}>
        <Input value={d.name} onChange={(e) => patch({ name: e.target.value })} maxLength={80} />
      </Field>

      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 className="text-sm font-semibold">{t("Mood")}</h3>
          <span className="text-xs text-muted">{t("The scene starts from the mood: its music, colour and icon.")}</span>
        </div>
        <div role="radiogroup" aria-label={t("Mood")} className="flex flex-wrap gap-2">
          {(moods.data ?? []).map((m) => {
            const on = d.moodId === m.id;
            return (
              <button
                key={m.id}
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => patch({ moodId: on ? undefined : m.id })}
                className="flex h-10 items-center gap-1.5 rounded-full px-3.5 text-[13px] font-medium"
                style={{ color: m.color, background: tint(m.color, on ? "33" : "14"), border: on ? `2px solid ${m.color}` : `1px solid ${tint(m.color, "55")}` }}
              >
                <MoodIcon icon={m.icon} className="size-4" />
                <span className="text-fg">{moodName(m)}</span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-semibold">{t("Music · Spotify")}</h3>
        <Switch
          checked={ownMusic}
          onChange={setOwnMusic}
          label={ownMusic ? t("Its own music") : mood?.spotifyName ? t("The mood's music: {name}", { name: mood.spotifyName }) : t("The mood's music")}
        />
        {ownMusic &&
          (d.spotifyUri ? (
            <div className="flex items-center gap-3 rounded-lg border border-border bg-bg p-2">
              {d.spotifyImageUrl && <img src={d.spotifyImageUrl} alt="" className="size-12 rounded object-cover" />}
              <div className="min-w-0 flex-1 truncate text-sm font-medium">{d.spotifyName || d.spotifyUri}</div>
              <Button size="sm" variant="ghost" onClick={() => patch({ spotifyUri: "", spotifyName: "", spotifyImageUrl: "" })}>
                {t("Remove")}
              </Button>
            </div>
          ) : me.data?.connected ? (
            <SpotifyPicker value={d.spotifyUri ?? ""} onPick={(it) => patch({ spotifyUri: it.uri, spotifyName: it.name, spotifyImageUrl: it.imageUrl ?? "" })} />
          ) : (
            <ConnectPrompt text={t("Connect your Spotify account to search it.")} />
          ))}
        <Switch checked={!!d.shuffle} onChange={(shuffle) => patch({ shuffle })} label={t("Shuffle")} />
      </div>

      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between">
          <h3 className="text-sm font-semibold">{t("Ambience layers · from sound packs")}</h3>
          <span className="text-xs text-muted">{t("Up to 4, looped")}</span>
        </div>
        {d.ambience.map((a, i) => {
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
                onChange={(e) => patch({ ambience: d.ambience.map((x, k) => (k === i ? { ...x, volume: Number(e.target.value) / 100 } : x)) })}
                aria-label={t("{name} volume", { name: a.title })}
                className="accent-accent"
              />
              <button type="button" className="flex size-11 items-center justify-center rounded hover:bg-border" aria-label={preview.playing === key ? t("Stop") : t("Preview")} onClick={() => preview.play(key, [{ id: a.trackId, gain: a.volume * dbGain(a.gainDb) }])}>
                {preview.playing === key ? <Pause className="size-4" /> : <Play className="size-4" />}
              </button>
              <button type="button" className="flex size-11 items-center justify-center rounded text-muted hover:bg-border hover:text-fg" aria-label={t("Remove")} onClick={() => patch({ ambience: d.ambience.filter((_, k) => k !== i) })}>
                <X className="size-4" />
              </button>
            </div>
          );
        })}
        {d.ambience.length < 4 && (
          <button type="button" onClick={() => setAdding("ambience")} className="flex h-11 items-center justify-center gap-1.5 rounded-lg border border-dashed border-border text-[13px] font-medium hover:border-muted/60">
            <Plus className="size-4" aria-hidden="true" /> {t("Add ambience")}
          </button>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-semibold">{t("Sound effects")}</h3>
        <div className="grid grid-cols-[repeat(auto-fill,minmax(6rem,1fr))] gap-2">
          {d.sfx.map((p, i) => {
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
                  onClick={() => patch({ sfx: d.sfx.filter((_, k) => k !== i) })}
                  className="absolute -top-1.5 -right-1.5 flex size-6 items-center justify-center rounded-full border border-border bg-panel text-muted hover:text-fg"
                  aria-label={t("Remove")}
                >
                  <X className="size-3" />
                </button>
              </div>
            );
          })}
          {d.sfx.length < 8 && (
            <button type="button" onClick={() => setAdding("sfx")} className="h-18 rounded-xl border border-dashed border-border text-xs font-medium text-muted hover:text-fg">
              {t("+ Add effect")}
            </button>
          )}
        </div>
        <p className="text-xs text-muted">{t("Each effect gets an icon and colour by its kind: weather, objects, combat, body, magic, voices.")}</p>
      </div>

      <div className="flex flex-wrap justify-end gap-2.5">
        {scene && (
          <Button variant="danger" className="mr-auto" onClick={() => setDeleting(true)}>
            {t("Delete")}
          </Button>
        )}
        {d.ambience.length > 0 && (
          <Button onClick={() => preview.play(sceneKey, d.ambience.map((a) => ({ id: a.trackId, gain: a.volume * dbGain(a.gainDb) })))}>
            {preview.playing === sceneKey ? t("Stop preview") : t("Preview scene")}
          </Button>
        )}
        <Button variant="primary" loading={saving} disabled={!d.name.trim()} onClick={() => void save()}>
          {t("Save")}
        </Button>
      </div>

      {adding && (
        <TrackPicker
          kind={adding}
          preview={preview}
          onClose={() => setAdding(null)}
          onPick={(tr) => {
            if (adding === "ambience" && !d.ambience.some((a) => a.trackId === tr.id))
              patch({ ambience: [...d.ambience, { trackId: tr.id, volume: 0.6, title: tr.title, category: tr.category, icon: tr.icon, gainDb: tr.gainDb, pack: tr.pack, license: tr.license }] });
            if (adding === "sfx" && !d.sfx.some((a) => a.trackId === tr.id)) patch({ sfx: [...d.sfx, { trackId: tr.id, label: "", title: tr.title, category: tr.category, icon: tr.icon, gainDb: tr.gainDb }] });
            setAdding(null);
          }}
        />
      )}
      <Confirm
        open={deleting}
        danger
        title={t("Delete the scene?")}
        message={t("Cues that used it play their mood, or nothing.")}
        confirmLabel={t("Delete")}
        onConfirm={() => void remove()}
        onClose={() => setDeleting(false)}
      />
    </div>
  );
}

/** ScenesTab lists the scenes and edits the one picked. */
export function ScenesTab() {
  const { data: scenes, isLoading } = useScenes();
  const moods = useMoods();
  const [q, setQ] = useState("");
  const [picked, setPicked] = useState<number | "new" | null>(null);
  const editor = useRef<HTMLDivElement>(null);
  const list = (scenes ?? []).filter((s) => s.name.toLowerCase().includes(q.trim().toLowerCase()));
  const current = picked === "new" ? null : (scenes ?? []).find((s) => s.id === (picked ?? list[0]?.id)) ?? null;
  const showing = picked === "new" || current;
  const moodById = new Map((moods.data ?? []).map((m) => [m.id, m]));
  if (isLoading) return <Loading />;
  const pick = (p: number | "new") => {
    setPicked(p);
    requestAnimationFrame(() => editor.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }));
  };
  return (
    <div className="flex flex-wrap items-start gap-5">
      <div className="flex w-full flex-col gap-2 md:w-72">
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("rain, battle, calm…")} aria-label={t("Find a scene")} className="w-full pl-9" />
        </div>
        {list.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => pick(s.id)}
            aria-pressed={current?.id === s.id && picked !== "new"}
            className={clsx("flex items-center gap-2.5 rounded-lg border p-2 text-left text-sm font-medium", current?.id === s.id && picked !== "new" ? "border-accent bg-accent/10" : "border-border bg-panel hover:border-muted/60")}
          >
            <SceneArt scene={s} mood={s.moodId ? moodById.get(s.moodId) : null} />
            <span className="min-w-0 truncate">{s.name}</span>
          </button>
        ))}
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => pick("new")}>
          {t("New scene")}
        </Button>
      </div>
      <div ref={editor} className="min-w-0 flex-[999_1_32rem]">
        {showing ? (
          <SceneEditor key={picked === "new" ? "new" : current?.id} scene={picked === "new" ? null : current} onSaved={(s) => setPicked(s.id)} onDeleted={() => setPicked(null)} />
        ) : (
          <EmptyState title={t("No scenes yet")}>{t("A scene layers ambience and sound effects from sound packs over a mood's music. Make one, or install a pack that brings some.")}</EmptyState>
        )}
      </div>
    </div>
  );
}
