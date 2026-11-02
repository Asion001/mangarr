import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Input, Modal, Segmented } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { tint, type Mood, type Scene } from "../../lib/soundtrack/core";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { useMoods } from "../../lib/soundtrack/spotify";
import { TrackIcon } from "../../lib/soundtrack/tracks";
import { useToast } from "../../lib/toast";

/** useScenes lists the scenes. */
export function useScenes(enabled = true) {
  return useQuery({ queryKey: ["soundtrack", "scenes"], queryFn: () => unwrap(api.GET("/api/v1/audio/scenes")), enabled, staleTime: 60_000 });
}

/** SceneArt is a scene's tile: its mood's colour and its sounds' icons. */
export function SceneArt({ scene, mood, className = "size-10" }: { scene: Scene; mood?: Mood | null; className?: string }) {
  const color = mood?.color ?? "#9aa2b1";
  const icons = [...scene.ambience, ...scene.sfx].slice(0, 4);
  return (
    <span className={`grid shrink-0 grid-cols-2 place-items-center overflow-hidden rounded-md p-1 ${className}`} style={{ background: tint(color, "33"), color }} aria-hidden="true">
      {icons.length === 0 ? <MoodIcon icon={mood?.icon ?? "music"} className="col-span-2 size-1/2" /> : icons.map((t, i) => <TrackIcon key={i} icon={t.icon} className="size-3.5" />)}
    </span>
  );
}

/**
 * StartScene picks a mood or a scene to start on this page: for the
 * reader's own version, or (admins) for everyone.
 */
export function StartScene({ seriesId, chapter, page, pageCount, onClose }: { seriesId: number; chapter: number; page: number; pageCount: number; onClose: () => void }) {
  const { isAdmin } = useAccount();
  const qc = useQueryClient();
  const toast = useToast();
  const [target, setTarget] = useState<"mine" | "shared">("mine");
  const [busy, setBusy] = useState(false);
  const save = async (pick: { moodId?: number; sceneId?: number }) => {
    setBusy(true);
    try {
      const body = { chapterNumber: chapter, page: page > 1 ? page : undefined, pageCount: page > 1 && pageCount > 0 ? pageCount : undefined, ...pick };
      const path = { params: { path: { id: seriesId } } };
      await unwrap(target === "shared" ? api.PUT("/api/v1/series/{id}/soundtrack/cue", { ...path, body }) : api.PUT("/api/v1/me/series/{id}/soundtrack/cue", { ...path, body }));
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      toast.success(page > 1 ? t("Starts on page {page}", { page }) : t("Starts with this chapter"));
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal open onClose={onClose} title={t("Start a scene here")}>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-muted">{page > 1 ? t("From chapter {chapter}, page {page}.", { chapter, page }) : t("From the start of chapter {chapter}.", { chapter })}</p>
        {isAdmin && (
          <Segmented
            label={t("Who hears it")}
            value={target}
            onChange={setTarget}
            options={[
              { value: "mine", label: t("Just me") },
              { value: "shared", label: t("Everyone") },
            ]}
          />
        )}
        <SoundPicker busy={busy} onPick={(pick) => void save(pick)} />
        <div className="flex justify-end">
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => void save({})}>
            {t("Remove the cue here")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

/** SoundPicker finds a scene or a mood, with their artwork. */
export function SoundPicker({ busy, onPick }: { busy?: boolean; onPick: (pick: { moodId?: number; sceneId?: number }) => void }) {
  const moods = useMoods();
  const scenes = useScenes();
  const [q, setQ] = useState("");
  const match = (name: string) => name.toLowerCase().includes(q.trim().toLowerCase());
  const moodById = new Map((moods.data ?? []).map((m) => [m.id, m]));
  const save = onPick;
  return (
    <div className="flex flex-col gap-3">
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("rain, battle, calm…")} aria-label={t("Find a scene")} className="w-full pl-9" autoFocus />
      </div>
      <div className="flex flex-col gap-1.5">
        {(scenes.data ?? []).filter((s) => match(s.name)).map((s) => (
          <button key={"s" + s.id} type="button" disabled={busy} onClick={() => void save({ sceneId: s.id })} className="flex min-h-14 items-center gap-3 rounded-lg border border-border bg-panel-2 p-2 text-left text-sm font-medium hover:border-muted/60">
            <SceneArt scene={s} mood={s.moodId ? moodById.get(s.moodId) : null} />
            <span className="min-w-0 flex-1">
            <span className="block truncate">{s.name}</span>
            <span className="block truncate text-xs font-normal text-muted">{s.ambience.map((a) => a.title).join(" + ") || (s.moodId && moodById.get(s.moodId) ? moodName(moodById.get(s.moodId)!) : "")}</span>
            </span>
          </button>
        ))}
        <div className="grid grid-cols-[repeat(auto-fill,minmax(6.5rem,1fr))] gap-2 pt-1">
          {(moods.data ?? []).filter((m) => match(moodName(m))).map((m) => (
            <button
            key={"m" + m.id}
            type="button"
            disabled={busy}
            onClick={() => void save({ moodId: m.id })}
            className="flex h-14 items-center gap-2 rounded-lg px-2 text-left text-xs font-medium"
            style={{ color: m.color, background: tint(m.color, "1a"), border: `1px solid ${tint(m.color, "55")}` }}
            >
            <MoodIcon icon={m.icon} className="size-5 shrink-0" />
            <span className="truncate text-fg">{moodName(m)}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
