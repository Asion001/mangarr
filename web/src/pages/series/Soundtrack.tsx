import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Music } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Button, Card, Segmented } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { MoodPicker } from "../../lib/soundtrack/MoodPicker";
import { connectSpotify, useMoods, useMySpotify } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";

type Doc = S["Doc"];
type Cue = S["Cue"];

/** titleMood is a soundtrack's title-wide mood (the chapter 0 cue). */
const titleMood = (d?: Doc | null) => d?.cues?.find((c) => c.chapterNumber === 0 && c.page == null)?.moodId ?? null;

/** withTitleMood sets the title-wide mood, keeping the other cues. */
function withTitleMood(d: Doc | null | undefined, moodId: number | null): Cue[] {
  const rest = (d?.cues ?? []).filter((c) => !(c.chapterNumber === 0 && c.page == null));
  return [{ chapterNumber: 0, ...(moodId ? { moodId } : {}) }, ...rest];
}

/**
 * SeriesSoundtrack is the music that plays while reading the title: the
 * default admins set, and each reader's own version (or mute).
 */
export function SeriesSoundtrack({ seriesId }: { seriesId: number }) {
  const { isAdmin, account } = useAccount();
  const qc = useQueryClient();
  const toast = useToast();
  const me = useMySpotify();
  const moods = useMoods();
  const key = ["soundtrack", "series", seriesId];
  const { data } = useQuery({ queryKey: key, queryFn: () => unwrap(api.GET("/api/v1/series/{id}/soundtrack", { params: { path: { id: seriesId } } })) });
  const user = account?.kind === "user";
  const [tab, setTab] = useState<"shared" | "mine" | null>(null);
  const [busy, setBusy] = useState(false);
  if (!me.data || !data || !moods.data || (!me.data.available && !isAdmin)) return null;
  const view = tab ?? (data.personal && user ? "mine" : "shared");
  const personal = data.personal ?? null;

  const run = async (p: Promise<{ data?: S["SeriesSoundtrack"]; error?: unknown; response: Response }>) => {
    setBusy(true);
    try {
      qc.setQueryData(key, await unwrap(p));
      void qc.invalidateQueries({ queryKey: ["soundtrack", "chapter"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  const path = { params: { path: { id: seriesId } } };
  const setShared = (moodId: number | null) => run(api.PUT("/api/v1/series/{id}/soundtrack", { ...path, body: { cues: withTitleMood(data.shared, moodId) } }));
  const setMine = (moodId: number | null, muted = personal?.muted ?? false) =>
    run(api.PUT("/api/v1/me/series/{id}/soundtrack", { ...path, body: { cues: withTitleMood(personal ?? data.shared, moodId), muted } }));
  const reset = () => run(api.DELETE("/api/v1/me/series/{id}/soundtrack", path));
  const mineMood = personal ? titleMood(personal) : titleMood(data.shared);

  return (
    <Card
      title={
        <span className="inline-flex items-center gap-2">
          <Music className="size-4 text-muted" aria-hidden="true" />
          {t("Soundtrack")}
        </span>
      }
      actions={
        user && (
          <Segmented
            label={t("Soundtrack version")}
            value={view}
            onChange={setTab}
            options={[
              { value: "shared", label: t("Default") },
              { value: "mine", label: t("My version") },
            ]}
          />
        )
      }
    >
      <div className="flex flex-col gap-3">
        {view === "shared" ? (
          <>
            <p className="text-sm text-muted">
              {isAdmin ? t("The mood everyone hears while reading this title, unless they make their own version.") : t("The mood admins picked for this title.")}
            </p>
            <MoodPicker label={t("Title mood")} moods={moods.data} value={titleMood(data.shared)} onChange={(id) => void setShared(id)} disabled={!isAdmin || busy} />
          </>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-bg px-3 py-2 text-sm text-muted">
              <span className="min-w-0 flex-1">{personal ? t("You changed the soundtrack from the default.") : t("You hear the default. Pick a mood to make your own version.")}</span>
              {personal && (
                <Button size="sm" variant="ghost" disabled={busy} onClick={() => void reset()}>
                  {t("Reset to default")}
                </Button>
              )}
            </div>
            <MoodPicker label={t("Title mood")} moods={moods.data} value={mineMood} onChange={(id) => void setMine(id)} disabled={busy} />
            <label className="flex items-center gap-2.5 text-sm">
              <input type="checkbox" className="size-5 accent-accent" checked={!!personal?.muted} disabled={busy} onChange={(e) => void setMine(mineMood, e.target.checked)} />
              {t("Mute soundtrack for this title")}
            </label>
          </>
        )}
        {me.data.available && !me.data.connected && user && (
          <div className="flex flex-wrap items-center gap-3 text-sm text-muted">
            <span className="min-w-0 flex-1">{t("Connect your Spotify account to hear it in the reader. Playing needs Spotify Premium.")}</span>
            <Button size="sm" onClick={() => void connectSpotify().catch((e) => toast.fromError(e))}>{t("Connect Spotify")}</Button>
          </div>
        )}
        {!me.data.available && isAdmin && <p className="text-sm text-muted">{t("Music plays once Spotify is set up in Settings › Soundtracks.")}</p>}
      </div>
    </Card>
  );
}
