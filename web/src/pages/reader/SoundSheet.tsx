import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Check, Laptop, Pause, Play, RefreshCw, Smartphone, Speaker, X } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Spinner } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { tint, type SoundMode } from "../../lib/soundtrack/core";
import { MoodIcon, moodName } from "../../lib/soundtrack/moods";
import { connectSpotify } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";
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
export function SoundSheet({ s, onClose }: { s: Soundtrack; onClose: () => void }) {
  const { isAdmin } = useAccount();
  const toast = useToast();
  const [picking, setPicking] = useState(false);
  const modes: { value: SoundMode; label: string }[] = [
    { value: "auto", label: t("Auto") },
    { value: "manual", label: t("Manual") },
    { value: "off", label: t("Off") },
  ];
  const m = s.mood;
  const source =
    s.prefs.mode === "manual"
      ? t("Picked by hand")
      : s.resolved?.source === "personal"
        ? t("Your version")
        : s.resolved?.source === "shared"
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
              {!s.on ? t("Music is off") : m ? moodName(m) + " · " + source : source}
            </div>
            <div className="truncate text-sm font-medium">
              {s.now?.track ? s.now.track + (s.now.artists ? " · " + s.now.artists : "") : m?.spotifyName || (s.on && m && !m.spotifyUri ? t("No music chosen for this mood") : "—")}
            </div>
            {s.on && s.now?.device && <div className="truncate text-xs text-muted">{t("Spotify on {device}", { device: s.now.device })}</div>}
          </div>
          {s.connected && s.on && (
            <button type="button" onClick={() => void s.togglePlay()} className="flex size-11 shrink-0 items-center justify-center rounded-full bg-fg text-bg" aria-label={s.now?.playing ? t("Pause") : t("Play")}>
              {s.now?.playing ? <Pause className="size-4" fill="currentColor" /> : <Play className="size-4" fill="currentColor" />}
            </button>
          )}
        </div>

        <label className="grid grid-cols-[5rem_1fr_2rem] items-center gap-2 text-[13px] text-muted">
          {t("Music")}
          <input type="range" min={0} max={100} value={s.prefs.volume} onChange={(e) => s.setVolume(Number(e.target.value))} className="accent-accent" />
          <span className="text-right tabular-nums">{s.prefs.volume}</span>
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

        {s.connected && (
          <div className="flex flex-col gap-2">
            <button type="button" onClick={() => setPicking(!picking)} aria-expanded={picking} className="h-11 rounded-xl border border-border text-[13px] font-medium hover:bg-panel-2">
              {t("Play music on…")}
            </button>
            {picking && <Devices s={s} />}
          </div>
        )}
      </section>
    </>
  );
}
