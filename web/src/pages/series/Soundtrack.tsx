import { t } from "../../lib/i18n/core";
import { useRef, useState } from "react";
import { Link, useParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { ArrowLeft, Download, Music, Plus, Upload, X } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Badge, Button, ButtonLink, Card, ErrorBox, Input, Loading, Modal, PageHeader, Segmented } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { useDocumentTitle } from "../../lib/documentTitle";
import { tint, type Mood, type Scene } from "../../lib/soundtrack/core";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { MoodPicker } from "../../lib/soundtrack/MoodPicker";
import { connectSpotify, useMoods, useMySpotify, useSpotifyReturn } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";
import { useSeries } from "../../api/queries";
import { SceneArt, SoundPicker, useScenes } from "../reader/StartScene";

type Doc = S["SoundtrackDoc"];
type Cue = S["SoundtrackCue"];
type View = S["SeriesSoundtrack"];
type Version = "shared" | "mine";

/** titleMood is a soundtrack's title-wide mood (the chapter 0 cue). */
const titleMood = (d?: Doc | null) => d?.cues?.find((c) => c.chapterNumber === 0 && c.page == null)?.moodId ?? null;

const cueKey = (c: Pick<Cue, "chapterNumber" | "page">) => `${c.chapterNumber}:${c.page ?? 0}`;

function useSoundtrackView(seriesId: number) {
  return useQuery({ queryKey: ["soundtrack", "series", seriesId], queryFn: () => unwrap(api.GET("/api/v1/series/{id}/soundtrack", { params: { path: { id: seriesId } } })) });
}

/** useSoundtrackEdits changes one version of a series' soundtrack. */
function useSoundtrackEdits(seriesId: number) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const path = { params: { path: { id: seriesId } } };
  const run = async (p: Promise<{ data?: View; error?: unknown; response: Response }>) => {
    setBusy(true);
    try {
      qc.setQueryData(["soundtrack", "series", seriesId], await unwrap(p));
      void qc.invalidateQueries({ queryKey: ["soundtrack", "chapter"] });
      return true;
    } catch (e) {
      toast.fromError(e);
      return false;
    } finally {
      setBusy(false);
    }
  };
  return {
    busy,
    setCue: (v: Version, cue: Cue) =>
      run(v === "shared" ? api.PUT("/api/v1/series/{id}/soundtrack/cue", { ...path, body: cue }) : api.PUT("/api/v1/me/series/{id}/soundtrack/cue", { ...path, body: cue })),
    setMuted: (doc: Doc | null | undefined, muted: boolean) => run(api.PUT("/api/v1/me/series/{id}/soundtrack", { ...path, body: { cues: doc?.cues ?? [], muted } })),
    reset: () => run(api.DELETE("/api/v1/me/series/{id}/soundtrack", path)),
  };
}

/**
 * SeriesSoundtrack is the title page's soundtrack card: the title's mood,
 * the default admins set or the reader's own, and a way to the cues.
 */
export function SeriesSoundtrack({ seriesId }: { seriesId: number }) {
  const { isAdmin, account } = useAccount();
  const toast = useToast();
  const me = useMySpotify();
  const moods = useMoods();
  const { data } = useSoundtrackView(seriesId);
  const edits = useSoundtrackEdits(seriesId);
  const user = account?.kind === "user";
  const [tab, setTab] = useState<Version | null>(null);
  if (!me.data || !data || !moods.data || (!me.data.available && !isAdmin && (data.credits?.length ?? 0) === 0 && !data.shared)) return null;
  const view = tab ?? (data.personal && user ? "mine" : "shared");
  const personal = data.personal ?? null;
  const cues = (view === "shared" ? data.shared?.cues : personal?.cues)?.filter((c) => !(c.chapterNumber === 0 && c.page == null)).length ?? 0;
  const mineMood = titleMood(personal) ?? titleMood(data.shared);
  return (
    <Card
      title={
        <span className="inline-flex items-center gap-2">
          <Music className="size-4 text-muted" aria-hidden="true" />
          {t("Soundtrack")}
        </span>
      }
      actions={
        <div className="flex items-center gap-2">
          {user && (
            <Segmented
              label={t("Soundtrack version")}
              value={view}
              onChange={setTab}
              options={[
                { value: "shared", label: t("Default") },
                { value: "mine", label: t("My version") },
              ]}
            />
          )}
          <ButtonLink to={`/series/${seriesId}/soundtrack`} size="sm">
            {t("Cues")}
            {cues > 0 ? ` · ${cues}` : ""}
          </ButtonLink>
        </div>
      }
    >
      <div className="flex flex-col gap-3">
        {view === "shared" ? (
          <>
            <p className="text-sm text-muted">
              {isAdmin ? t("The mood everyone hears while reading this title, unless they make their own version.") : t("The mood admins picked for this title.")}
            </p>
            <MoodPicker label={t("Title mood")} moods={moods.data} value={titleMood(data.shared)} onChange={(id) => void edits.setCue("shared", { chapterNumber: 0, moodId: id ?? undefined })} disabled={!isAdmin || edits.busy} />
          </>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-bg px-3 py-2 text-sm text-muted">
              <span className="min-w-0 flex-1">{personal ? t("You changed the soundtrack from the default.") : t("You hear the default. Pick a mood to make your own version.")}</span>
              {personal && (
                <Button size="sm" variant="ghost" disabled={edits.busy} onClick={() => void edits.reset()}>
                  {t("Reset to default")}
                </Button>
              )}
            </div>
            <MoodPicker label={t("Title mood")} moods={moods.data} value={mineMood} onChange={(id) => void edits.setCue("mine", { chapterNumber: 0, moodId: id ?? undefined })} disabled={edits.busy} />
            <label className="flex items-center gap-2.5 text-sm">
              <input type="checkbox" className="size-5 accent-accent" checked={!!personal?.muted} disabled={edits.busy} onChange={(e) => void edits.setMuted(personal, e.target.checked)} />
              {t("Mute soundtrack for this title")}
            </label>
          </>
        )}
        {me.data.available && !me.data.connected && user && (
          <div className="flex flex-wrap items-center gap-3 text-sm text-muted">
            <span className="min-w-0 flex-1">{t("Connect your Spotify account to hear its music in the reader (ambience plays without it). Playing needs Spotify Premium.")}</span>
            <Button size="sm" onClick={() => void connectSpotify().catch((e) => toast.fromError(e))}>{t("Connect Spotify")}</Button>
          </div>
        )}
        {!me.data.available && isAdmin && <p className="text-sm text-muted">{t("Music plays once Spotify is set up in Settings › Soundtracks.")}</p>}
      </div>
    </Card>
  );
}

/** CueArt is a cue's tile: its scene's, else its mood's. */
function CueArt({ cue, moods, scenes }: { cue: Cue; moods: Map<number, Mood>; scenes: Map<number, Scene> }) {
  const scene = cue.sceneId ? scenes.get(cue.sceneId) : undefined;
  if (scene) return <SceneArt scene={scene} mood={scene.moodId ? moods.get(scene.moodId) : null} className="size-11" />;
  const m = cue.moodId ? moods.get(cue.moodId) : undefined;
  const color = m?.color ?? "#9aa2b1";
  return (
    <span className="flex size-11 shrink-0 items-center justify-center rounded-md" style={{ background: tint(color, "33"), color }} aria-hidden="true">
      <MoodIcon icon={m?.icon ?? "music"} className="size-5" />
    </span>
  );
}

/** AddCue places a cue: a chapter (or the whole title), a page, and what plays. */
function AddCue({ data, onPick, onClose, busy }: { data: View; onPick: (cue: Cue) => void; onClose: () => void; busy: boolean }) {
  const [chapter, setChapter] = useState<number>(0);
  const [page, setPage] = useState("");
  const pages = data.chapters.find((c) => c.number === chapter)?.pages ?? 0;
  const p = Number(page);
  const validPage = page === "" || (Number.isInteger(p) && p >= 1 && (pages === 0 || p <= pages));
  return (
    <Modal open onClose={onClose} title={t("Add a cue")} size="lg">
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <span className="text-sm font-medium">{t("Starts at")}</span>
          <div role="radiogroup" aria-label={t("Chapter")} className="flex max-h-36 flex-wrap gap-1.5 overflow-y-auto">
            {[{ number: 0, label: t("Whole title") }, ...data.chapters.map((c) => ({ number: c.number, label: t("Ch.") + " " + c.label }))].map((c) => (
              <button
                key={c.number}
                type="button"
                role="radio"
                aria-checked={chapter === c.number}
                onClick={() => (setChapter(c.number), c.number === 0 && setPage(""))}
                className={clsx("h-9 rounded-full border px-3 text-xs font-medium", chapter === c.number ? "border-accent bg-accent/20 text-accent-2" : "border-border text-fg/85 hover:border-muted/60")}
              >
                {c.label}
              </button>
            ))}
          </div>
        </div>
        {chapter > 0 && (
          <label className="flex flex-wrap items-center gap-2 text-sm">
            {t("From page")}
            <Input type="number" min={1} max={pages || undefined} value={page} onChange={(e) => setPage(e.target.value)} placeholder="1" className="w-24" />
            <span className="text-xs text-muted">{pages ? t("of {count}", { count: pages }) : t("empty: the chapter's start")}</span>
          </label>
        )}
        <SoundPicker
          busy={busy || !validPage}
          onPick={(pick) => onPick({ chapterNumber: chapter, page: chapter > 0 && page !== "" && p > 1 ? p : undefined, pageCount: pages || undefined, ...pick })}
        />
      </div>
    </Modal>
  );
}

/** SoundtrackPage edits a title's cues: the default and the reader's own. */
export function SoundtrackPage() {
  const id = Number(useParams().id);
  const { isAdmin, account } = useAccount();
  const toast = useToast();
  const { data: series } = useSeries(id);
  useDocumentTitle(series ? t("Soundtrack") + " · " + (series.displayTitle || series.title) : t("Soundtrack"));
  useSpotifyReturn();
  const { data, isLoading, error } = useSoundtrackView(id);
  const moods = useMoods();
  const scenes = useScenes();
  const edits = useSoundtrackEdits(id);
  const qc = useQueryClient();
  const user = account?.kind === "user";
  const [tab, setTab] = useState<Version | null>(null);
  const [adding, setAdding] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  if (isLoading || !moods.data) return <Loading />;
  if (error || !data) return <ErrorBox error={error} />;
  const view: Version = tab ?? (user && (data.personal || !isAdmin) ? "mine" : "shared");
  const canEdit = view === "mine" ? user : isAdmin;
  const moodById = new Map(moods.data.map((m) => [m.id, m]));
  const sceneById = new Map((scenes.data ?? []).map((s) => [s.id, s]));
  const mine = new Set((data.personal?.cues ?? []).map(cueKey));
  // My version shows the default's cues with mine over them
  const rows: { cue: Cue; mine: boolean }[] =
    view === "shared"
      ? (data.shared?.cues ?? []).map((cue) => ({ cue, mine: false }))
      : [...(data.shared?.cues ?? []).filter((c) => !mine.has(cueKey(c))).map((cue) => ({ cue, mine: false })), ...(data.personal?.cues ?? []).map((cue) => ({ cue, mine: true }))];
  rows.sort((a, b) => a.cue.chapterNumber - b.cue.chapterNumber || (a.cue.page ?? 0) - (b.cue.page ?? 0));
  const label = (c: Cue) => {
    if (c.chapterNumber === 0 && c.page == null) return t("Title default");
    const ch = data.chapters.find((x) => x.number === c.chapterNumber)?.label ?? String(c.chapterNumber);
    return c.page ? t("Ch. {chapter} · from page {page}", { chapter: ch, page: c.page }) : t("Ch.") + " " + ch;
  };
  const name = (c: Cue) => {
    const sc = c.sceneId ? sceneById.get(c.sceneId) : undefined;
    if (sc) return sc.name;
    const m = c.moodId ? moodById.get(c.moodId) : undefined;
    return m ? moodName(m) : t("No music");
  };
  const importFile = async (f: File) => {
    try {
      const body = JSON.parse(await f.text());
      const path = { params: { path: { id } } };
      const res = await unwrap(view === "shared" ? api.POST("/api/v1/series/{id}/soundtrack/import", { ...path, body }) : api.POST("/api/v1/me/series/{id}/soundtrack/import", { ...path, body }));
      qc.setQueryData(["soundtrack", "series", id], res.soundtrack);
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      const r = res.result;
      toast.success(
        t("Imported {count} cues", { count: r.cues }),
        [r.scenesCreated ? t("{count} scenes made", { count: r.scenesCreated }) : "", r.missingSounds ? t("{count} sounds aren't installed here", { count: r.missingSounds }) : "", r.missingMoods ? t("{count} moods weren't found", { count: r.missingMoods }) : ""].filter(Boolean).join(" · "),
      );
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4">
      <PageHeader
        title={
          <span className="inline-flex items-center gap-2">
            <Link to={`/series/${id}`} className="rounded p-1 hover:bg-panel-2" aria-label={t("Back to the series")}>
              <ArrowLeft className="size-5" />
            </Link>
            {t("Soundtrack")}
          </span>
        }
        subtitle={series?.displayTitle || series?.title}
        actions={
          <>
            <a href={apiUrl(`api/v1/series/${id}/soundtrack/export?version=${view === "mine" ? "personal" : "shared"}`)} download className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border bg-panel-2 px-3 text-sm hover:bg-border">
              <Download className="size-4" aria-hidden="true" /> {t("Export")}
            </a>
            {canEdit && (
              <Button icon={<Upload className="size-4" />} onClick={() => file.current?.click()}>
                {t("Import")}
              </Button>
            )}
            <input
              ref={file}
              type="file"
              accept="application/json,.json"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) void importFile(f);
              }}
            />
          </>
        }
      />
      {user && (
        <Segmented
          label={t("Soundtrack version")}
          value={view}
          onChange={setTab}
          options={[
            { value: "shared", label: t("Default (admin)") },
            { value: "mine", label: t("My version") },
          ]}
        />
      )}
      {view === "mine" && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-panel px-3 py-2.5 text-sm text-muted">
          <span className="min-w-0 flex-1">
            {data.personal?.cues.length ? t("You changed {count} cues from the default.", { count: data.personal.cues.length }) : t("You hear the default. Add a cue to make your own version.")}
          </span>
          {data.personal && (
            <Button size="sm" variant="ghost" disabled={edits.busy} onClick={() => void edits.reset()}>
              {t("Reset to default")}
            </Button>
          )}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-border bg-panel">
        {rows.length === 0 && <p className="p-4 text-sm text-muted">{t("No cues yet: nothing plays for this title.")}</p>}
        {rows.map(({ cue, mine: own }) => (
          <div key={cueKey(cue) + (own ? "m" : "")} className="flex items-center gap-3 border-b border-border p-3 last:border-b-0">
            <CueArt cue={cue} moods={moodById} scenes={sceneById} />
            <div className="min-w-0 flex-1">
              <div className={clsx("text-xs", cue.needsReview ? "text-warn" : "text-muted")}>
                {label(cue)}
                {cue.needsReview && " · " + t("page count changed, check it")}
              </div>
              <div className="truncate text-sm font-medium">{name(cue)}</div>
            </div>
            {view === "mine" && own && <Badge tone="accent">{t("Mine")}</Badge>}
            {canEdit && (view === "shared" || own) && (
              <button
                type="button"
                className="flex size-11 items-center justify-center rounded text-muted hover:bg-panel-2 hover:text-fg"
                disabled={edits.busy}
                onClick={() => void edits.setCue(view, { chapterNumber: cue.chapterNumber, page: cue.page })}
                aria-label={t("Remove the cue")}
              >
                <X className="size-4" />
              </button>
            )}
          </div>
        ))}
      </div>
      {canEdit && (
        <button type="button" onClick={() => setAdding(true)} className="flex h-11 items-center justify-center gap-1.5 rounded-xl border border-dashed border-border text-sm font-medium hover:border-muted/60">
          <Plus className="size-4" aria-hidden="true" /> {t("Add a cue")}
        </button>
      )}
      {view === "mine" && user && (
        <label className="flex items-center gap-2.5 px-0.5 text-sm">
          <input type="checkbox" className="size-5 accent-accent" checked={!!data.personal?.muted} disabled={edits.busy} onChange={(e) => void edits.setMuted(data.personal, e.target.checked)} />
          {t("Mute soundtrack for this title")}
        </label>
      )}
      {(data.credits?.length ?? 0) > 0 && (
        <p className="text-xs leading-relaxed text-muted">
          {t("Credits")}: {data.credits.map((c) => `“${c.title}” (${[c.license, c.attribution].filter(Boolean).join(", ") || c.pack})`).join(", ")}. {t("Music from Spotify.")}
        </p>
      )}
      {adding && (
        <AddCue
          data={data}
          busy={edits.busy}
          onClose={() => setAdding(false)}
          onPick={(cue) => void edits.setCue(view, cue).then((ok) => ok && setAdding(false))}
        />
      )}
    </div>
  );
}
