import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Search } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Input, Modal, Segmented } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { tint, type Mood } from "../../lib/soundtrack/core";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { useMoods } from "../../lib/soundtrack/spotify";
import { TrackIcon } from "../../lib/soundtrack/tracks";
import { useToast } from "../../lib/toast";

/** MoodArt is a mood's tile: its colour, and its sounds' icons (or its own). */
export function MoodArt({ mood, className = "size-10" }: { mood: Mood; className?: string }) {
  const icons = [...(mood.ambience ?? []), ...(mood.sfx ?? [])].slice(0, 4);
  return (
    <span className={`grid shrink-0 grid-cols-2 place-items-center overflow-hidden rounded-md p-1 ${className}`} style={{ background: tint(mood.color, "33"), color: mood.color }} aria-hidden="true">
      {icons.length === 0 ? <MoodIcon icon={mood.icon} className="col-span-2 size-1/2" /> : icons.map((t, i) => <TrackIcon key={i} icon={t.icon} className="size-3.5" />)}
    </span>
  );
}

/**
 * StartMood picks a mood to start on this page: for the
 * reader's own version, or (admins) for everyone.
 */
export function StartMood({ seriesId, chapter, page, pageCount, onClose }: { seriesId: number; chapter: number; page: number; pageCount: number; onClose: () => void }) {
  const { isAdmin } = useAccount();
  const qc = useQueryClient();
  const toast = useToast();
  const [target, setTarget] = useState<"mine" | "shared">("mine");
  const [busy, setBusy] = useState(false);
  const save = async (pick: { moodId?: number }) => {
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
    <Modal open onClose={onClose} title={t("Start a mood here")}>
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

/** SoundPicker finds a mood, with its artwork and what it plays. */
export function SoundPicker({ busy, onPick }: { busy?: boolean; onPick: (pick: { moodId?: number }) => void }) {
  const moods = useMoods();
  const [q, setQ] = useState("");
  const list = (moods.data ?? []).filter((m) => moodName(m).toLowerCase().includes(q.trim().toLowerCase()));
  return (
    <div className="flex flex-col gap-3">
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("rain, battle, calm…")} aria-label={t("Find a mood")} className="w-full pl-9" autoFocus />
      </div>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-1.5">
        {list.map((m) => (
          <button
            key={m.id}
            type="button"
            disabled={busy}
            onClick={() => onPick({ moodId: m.id })}
            className="press flex min-h-14 items-center gap-3 rounded-lg border p-2 text-left text-sm font-medium"
            style={{ borderColor: tint(m.color, "55"), background: tint(m.color, "14") }}
          >
            <MoodArt mood={m} />
            <span className="min-w-0 flex-1">
              <span className="block truncate">{moodName(m)}</span>
              <span className="block truncate text-xs font-normal text-muted">{(m.ambience ?? []).map((a) => a.title).join(" + ") || m.spotifyName}</span>
            </span>
          </button>
        ))}
      </div>
    </div>
  );
}
