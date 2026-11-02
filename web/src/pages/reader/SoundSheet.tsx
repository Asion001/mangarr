import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Check, Laptop, Pause, Play, Plus, RefreshCw, SkipForward, Smartphone, Speaker, X } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Spinner } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { soundName, tint, type SoundMode } from "../../lib/soundtrack/core";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { connectSpotify } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";
import { categoryColor, TrackIcon } from "../../lib/soundtrack/tracks";
import { StartScene } from "./StartScene";
import type { Soundtrack } from "./sound";

function DeviceIcon({ type }: { type: string }) {
  if (type === "Smartphone" || type === "Tablet") return <Smartphone className="size-4" aria-hidden="true" />;
  if (type === "Computer" || type === "browser") return <Laptop className="size-4" aria-hidden="true" />;
  return <Speaker className="size-4" aria-hidden="true" />;
}

/** Devices picks where Spotify plays the music. */
function Devices({ s }: { s: Soundtrack }) {
  const { data, isFetching, refetch, error } = useQuery({ queryKey: ["soundtrack", "devices"], queryFn: () => unwrap(api.GET("/api/v1/me/spotify/devices")) });
  const options: { id: string; name: string; type: string }[] = [];
  if (s.browserDevice) options.push({ id: "", name: t("This browser"), type: "browser" });
  else if (s.phone) options.push({ id: "", name: t("Wherever Spotify plays now"), type: "Smartphone" });
  for (const d of data ?? []) if (d.id !== s.browserDevice) options.push({ id: d.id, name: d.name, type: d.type });
  const picked = s.prefs.deviceId === s.browserDevice ? "" : s.prefs.deviceId;
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-border bg-bg p-1">
      {options.map((o) => (
        <button
          key={o.id || "default"}
          type="button"
          onClick={() => s.setDevice(o.id)}
          aria-pressed={picked === o.id}
          className={clsx("flex min-h-11 items-center gap-3 rounded-lg px-3 text-left text-sm", picked === o.id ? "bg-panel-2 text-fg" : "text-fg/85 hover:bg-panel-2")}
        >
          <DeviceIcon type={o.type} />
          <span className="min-w-0 flex-1 truncate">{o.name}</span>
          {picked === o.id && <Check className="size-4 text-accent-2" aria-hidden="true" />}
        </button>
      ))}
      <div className="flex items-center gap-2 px-3 py-1.5 text-xs text-muted">
        <span className="flex-1">{error ? (error as Error).message : t("Open Spotify on a phone or speaker to see it here.")}</span>
        <button type="button" className="rounded p-1.5 hover:bg-panel-2 hover:text-fg" onClick={() => void refetch()} aria-label={t("Refresh devices")}>
          {isFetching ? <Spinner className="size-4" /> : <RefreshCw className="size-4" />}
        </button>
      </div>
    </div>
  );
}

/** SoundSheet is the reader's soundtrack: on/off, what plays, volume, moods and device. */
export function SoundSheet({ s, onClose, here }: { s: Soundtrack; onClose: () => void; here: { seriesId: number; chapter: number; page: number; pageCount: number } }) {
  const { isAdmin, account } = useAccount();
  const toast = useToast();
  const [picking, setPicking] = useState(false);
  const [starting, setStarting] = useState(false);
  const modes: { value: SoundMode; label: string }[] = [
    { value: "auto", label: t("Auto") },
    { value: "manual", label: t("Manual") },
    { value: "off", label: t("Off") },
  ];
  const m = s.mood;
  const source =
    s.prefs.mode === "manual"
      ? t("Picked by hand")
      : s.sound?.source === "personal"
        ? t("Your version")
        : s.sound?.source === "suggested"
          ? t("Suggested from genres")
          : s.sound
            ? t("Title soundtrack")
            : t("No soundtrack for this title yet");
  const color = m?.color ?? "#9aa2b1";
  const spotify = s.spotify;
  return (
    <>
      <div className="fixed inset-0 z-30 bg-black/40" onClick={onClose} onPointerUp={(e) => e.stopPropagation()} aria-hidden="true" />
      <section
        role="dialog"
        aria-label={t("Soundtrack")}
        className="fixed inset-x-0 bottom-0 z-40 mx-auto flex max-h-[85dvh] w-full max-w-lg flex-col gap-3 overflow-y-auto rounded-t-2xl border border-border bg-panel px-4 pt-2 text-fg shadow-2xl"
        style={{ paddingBottom: "max(1rem, env(safe-area-inset-bottom))" }}
        onClick={(e) => e.stopPropagation()}
        onPointerUp={(e) => e.stopPropagation()}
      >
        <div className="mx-auto h-1 w-10 shrink-0 rounded-full bg-border" aria-hidden="true" />
        <header className="flex items-center justify-between gap-2">
          <h2 className="text-base font-semibold">{t("Soundtrack")}</h2>
          <div className="flex items-center gap-1">
            <div role="radiogroup" aria-label={t("Soundtrack mode")} className="flex rounded-full border border-border bg-bg p-0.5">
              {modes.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  role="radio"
                  aria-checked={s.prefs.mode === o.value}
                  onClick={() => s.setMode(o.value)}
                  className={clsx("h-8 rounded-full px-3.5 text-[13px] font-medium", s.prefs.mode === o.value ? "bg-primary text-white" : "text-muted hover:text-fg")}
                >
                  {o.label}
                </button>
              ))}
            </div>
            <button type="button" className="rounded p-2 hover:bg-panel-2" onClick={onClose} aria-label={t("Close")}>
              <X className="size-5" />
            </button>
          </div>
        </header>

        {spotify && !spotify.available && (
          <p className="rounded-lg border border-border bg-bg px-3 py-2.5 text-sm text-muted">
            {t("Music comes from Spotify, which isn't set up on this server yet.")}{" "}
            {isAdmin && <Link className="text-accent-2 hover:underline" to="/settings/soundtracks?tab=spotify">{t("Set it up")}</Link>}
          </p>
        )}
        {spotify?.available && !spotify.connected && (
          <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-bg px-3 py-2.5">
            <p className="min-w-0 flex-1 text-sm text-muted">{t("Connect your Spotify account to hear music while you read. Playing needs Spotify Premium.")}</p>
            <Button variant="primary" size="sm" onClick={() => void connectSpotify().catch((e) => toast.fromError(e))}>{t("Connect Spotify")}</Button>
          </div>
        )}
        {spotify?.connected && !spotify.premium && <p className="rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-sm">{t("Spotify plays music only for Premium accounts.")}</p>}
        {(s.error || (s.sdkError && s.on && !s.prefs.deviceId)) && (
          <p className="rounded-lg border border-err/40 bg-err/10 px-3 py-2 text-sm">
            {s.error || t("Spotify's player didn't start in this browser: pick a device to play on.") + " (" + s.sdkError + ")"}
          </p>
        )}

        <div className="flex items-center gap-3 rounded-xl border bg-panel-2 p-2.5" style={{ borderColor: s.on && m ? color : undefined }}>
          <div className="flex size-13 shrink-0 items-center justify-center overflow-hidden rounded-lg" style={{ background: tint(color, "33"), color }}>
            {s.now?.imageUrl ? <img src={s.now.imageUrl} alt="" className="size-full object-cover" /> : <MoodIcon icon={m?.icon ?? "music"} className="size-6" />}
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
            <div className="truncate text-xs font-medium" style={{ color: m ? color : undefined }}>
              {!s.on ? t("Music is off") : s.sound ? soundName(s.sound, moodName) + " · " + source : source}
            </div>
            <div className="truncate text-sm font-medium">
              {s.now?.track ? s.now.track + (s.now.artists ? " · " + s.now.artists : "") : s.sound?.music?.name || (s.on && s.sound && !s.sound.music ? t("No music here") : "—")}
            </div>
            {s.on && (s.now?.device || s.layers.length > 0) && (
              <div className="truncate text-xs text-muted">
                {[s.now?.device ? t("Spotify on {device}", { device: s.now.device }) : "", s.layers.map((l) => l.title.toLowerCase()).join(" + ")].filter(Boolean).join(" · ")}
              </div>
            )}
          </div>
          {s.connected && s.on && (
            <div className="flex shrink-0 items-center gap-1">
              <button type="button" onClick={() => void s.togglePlay()} className="flex size-11 items-center justify-center rounded-full bg-fg text-bg" aria-label={s.now?.playing ? t("Pause") : t("Play")}>
                {s.now?.playing ? <Pause className="size-4" fill="currentColor" /> : <Play className="size-4" fill="currentColor" />}
              </button>
              {s.sound?.music && (
                <button type="button" onClick={() => void s.nextTrack()} className="flex size-11 items-center justify-center rounded-full text-fg hover:bg-border" aria-label={t("Next track")}>
                  <SkipForward className="size-5" fill="currentColor" />
                </button>
              )}
            </div>
          )}
        </div>

        <label className="grid grid-cols-[5rem_1fr_2rem] items-center gap-2 text-[13px] text-muted">
          {t("Music")}
          <input type="range" min={0} max={100} value={s.prefs.volume} onChange={(e) => s.setVolume(Number(e.target.value))} className="accent-accent" />
          <span className="text-right tabular-nums">{s.prefs.volume}</span>
        </label>
        <label className="-mt-2 grid grid-cols-[5rem_1fr_2rem] items-center gap-2 text-[13px] text-muted">
          {t("Ambience")}
          <input type="range" min={0} max={100} value={s.prefs.ambience} onChange={(e) => s.setAmbience(Number(e.target.value))} className="accent-accent" />
          <span className="text-right tabular-nums">{s.prefs.ambience}</span>
        </label>

        <div className="flex flex-col gap-2">
          <div className="flex items-baseline justify-between">
            <h3 className="text-[13px] font-semibold">{t("Moods")}</h3>
            <span className="text-xs text-muted">{t("One tap presets")}</span>
          </div>
          <div className="grid grid-cols-4 gap-2">
            {s.moods.map((mood) => {
              const active = s.on && m?.id === mood.id;
              return (
                <button
                  key={mood.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => s.pickMood(mood.id)}
                  className="flex h-16 flex-col items-center justify-center gap-1 rounded-xl"
                  style={{ color: mood.color, background: tint(mood.color, active ? "33" : "1a"), border: active ? `2px solid ${mood.color}` : `1px solid ${tint(mood.color, "55")}` }}
                >
                  <MoodIcon icon={mood.icon} className="size-5" />
                  <span className="max-w-full truncate px-1 text-[11px] font-medium text-fg">{moodName(mood)}</span>
                </button>
              );
            })}
          </div>
        </div>

        {s.effects.length > 0 && (
          <div className="flex flex-col gap-2">
            <div className="flex items-baseline justify-between">
              <h3 className="text-[13px] font-semibold">{t("Sound effects")}</h3>
              <span className="text-xs text-muted">{t("Tap to play once")}</span>
            </div>
            <div className="grid grid-cols-4 gap-2">
              {s.effects.map((fx) => {
                const c = categoryColor(fx.category);
                return (
                  <button
                    key={fx.trackId}
                    type="button"
                    onClick={() => void s.playEffect(fx)}
                    aria-label={fx.label || fx.title}
                    className="flex h-16 flex-col items-center justify-center gap-1 rounded-xl active:scale-95"
                    style={{ color: c, background: tint(c, "1a"), border: `1px solid ${tint(c, "55")}` }}
                  >
                    <TrackIcon icon={fx.icon} className="size-5" />
                    <span className="max-w-full truncate px-1 text-[11px] font-medium text-fg">{fx.label || fx.title}</span>
                  </button>
                );
              })}
            </div>
          </div>
        )}

        <div className="flex gap-2">
          {account?.kind === "user" && here.seriesId > 0 && (
            <button type="button" onClick={() => setStarting(true)} className="flex h-11 flex-1 items-center justify-center gap-1.5 rounded-xl border border-border text-[13px] font-medium hover:bg-panel-2">
              <Plus className="size-4" aria-hidden="true" /> {t("Start a scene here")}
            </button>
          )}
          {s.connected && (
            <button type="button" onClick={() => setPicking(!picking)} aria-expanded={picking} className="h-11 flex-1 rounded-xl border border-border text-[13px] font-medium hover:bg-panel-2">
              {t("Play music on…")}
            </button>
          )}
        </div>
        {picking && s.connected && <Devices s={s} />}
        {s.layers.length > 0 && (
          <p className="text-[11px] leading-relaxed text-muted">
            {t("Credits")}: {[...s.layers, ...s.effects].map((l) => `“${l.title}”${l.license ? ` (${l.license}${l.attribution ? ", " + l.attribution : ""})` : ""}`).join(", ")}
            {s.sound?.music ? ". " + t("Music from Spotify.") : ""}
          </p>
        )}
        {starting && <StartScene seriesId={here.seriesId} chapter={here.chapter} page={here.page} pageCount={here.pageCount} onClose={() => setStarting(false)} />}
      </section>
    </>
  );
}
