// Spotify in the browser: your connection, connecting, and the Web Playback
// SDK that makes this tab a Spotify device on computers.
import { useEffect } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { t } from "../i18n/core";
import { useToast } from "../toast";

/** useMySpotify is whether Spotify is set up here and your account connected. */
export function useMySpotify(enabled = true) {
  return useQuery({ queryKey: ["soundtrack", "me"], queryFn: () => unwrap(api.GET("/api/v1/me/spotify")), enabled, staleTime: 60_000 });
}

/** useMoods lists the mood presets. */
export function useMoods(enabled = true) {
  return useQuery({ queryKey: ["soundtrack", "moods"], queryFn: () => unwrap(api.GET("/api/v1/audio/moods")), enabled, staleTime: 5 * 60_000 });
}

/** connectSpotify sends you to Spotify to connect, coming back to this page. */
export async function connectSpotify() {
  const back = location.pathname.slice(new URL(document.baseURI).pathname.replace(/\/$/, "").length) + location.search;
  const { url } = await unwrap(api.POST("/api/v1/me/spotify/connect", { body: { returnTo: back || "/" } }));
  location.assign(url);
}

/** useSpotifyReturn says how connecting went when Spotify sends you back. */
export function useSpotifyReturn() {
  const [search, setSearch] = useSearchParams();
  const toast = useToast();
  const qc = useQueryClient();
  const ok = search.get("spotify");
  const err = search.get("spotify_error");
  useEffect(() => {
    if (!ok && !err) return;
    if (ok) toast.success(t("Spotify connected"));
    else toast.error(t("Spotify wasn't connected"), err ?? "");
    void qc.invalidateQueries({ queryKey: ["soundtrack"] });
    const next = new URLSearchParams(search);
    next.delete("spotify");
    next.delete("spotify_error");
    setSearch(next, { replace: true });
  }, [ok, err, search, setSearch, toast, qc]);
}

// ---- Web Playback SDK ----------------------------------------------------

type SdkState = {
  paused: boolean;
  track_window: { current_track: { name: string; artists: { name: string }[]; album: { images: { url: string }[] } } | null };
};

/** SdkPlayer is the part of Spotify.Player mangarr uses. */
export type SdkPlayer = {
  connect(): Promise<boolean>;
  disconnect(): void;
  activateElement(): Promise<void>;
  setVolume(v: number): Promise<void>;
  pause(): Promise<void>;
  resume(): Promise<void>;
  nextTrack(): Promise<void>;
  addListener(event: "ready" | "not_ready", cb: (e: { device_id: string }) => void): boolean;
  addListener(event: "player_state_changed", cb: (s: SdkState | null) => void): boolean;
  addListener(event: "initialization_error" | "authentication_error" | "account_error" | "playback_error", cb: (e: { message: string }) => void): boolean;
};

type SdkGlobal = { Player: new (o: { name: string; getOAuthToken: (cb: (token: string) => void) => void; volume?: number }) => SdkPlayer };

declare global {
  interface Window {
    Spotify?: SdkGlobal;
    onSpotifyWebPlaybackSDKReady?: () => void;
  }
}

const sdkURL = "https://sdk.scdn.co/spotify-player.js";
let sdk: Promise<SdkGlobal> | null = null;

/** loadSdk loads Spotify's Web Playback SDK once. */
export function loadSdk(): Promise<SdkGlobal> {
  sdk ??= new Promise<SdkGlobal>((resolve, reject) => {
    if (window.Spotify) return resolve(window.Spotify);
    window.onSpotifyWebPlaybackSDKReady = () => (window.Spotify ? resolve(window.Spotify) : reject(new Error("Spotify player didn't load")));
    const s = document.createElement("script");
    s.src = sdkURL;
    s.async = true;
    s.onerror = () => {
      sdk = null;
      reject(new Error("Spotify player didn't load"));
    };
    document.head.appendChild(s);
  });
  return sdk;
}

/** NowPlaying is what plays, from the SDK or Spotify's player API. */
export type NowPlaying = { playing: boolean; track: string; artists: string; imageUrl: string; device: string };

export function sdkNowPlaying(s: SdkState | null): NowPlaying | null {
  const tr = s?.track_window.current_track;
  if (!s || !tr) return null;
  return { playing: !s.paused, track: tr.name, artists: tr.artists.map((a) => a.name).join(", "), imageUrl: tr.album.images[0]?.url ?? "", device: t("This browser") };
}

/** startSdk makes this tab a Spotify device named mangarr. */
export async function startSdk(volume: number, onState: (s: NowPlaying | null) => void): Promise<{ player: SdkPlayer; deviceId: string }> {
  const Spotify = await loadSdk();
  const player = new Spotify.Player({
    name: "mangarr",
    volume: volume / 100,
    getOAuthToken: (cb) => {
      void unwrap(api.GET("/api/v1/me/spotify/token")).then((tok) => cb(tok.accessToken), () => cb(""));
    },
  });
  const deviceId = await new Promise<string>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error(t("Spotify's player didn't start in this browser"))), 15_000);
    player.addListener("ready", ({ device_id }) => {
      window.clearTimeout(timer);
      resolve(device_id);
    });
    for (const ev of ["initialization_error", "authentication_error", "account_error"] as const) {
      player.addListener(ev, ({ message }) => {
        window.clearTimeout(timer);
        reject(new Error(message));
      });
    }
    player.addListener("player_state_changed", (s) => onState(sdkNowPlaying(s)));
    void player.connect().then((ok) => {
      if (!ok) {
        window.clearTimeout(timer);
        reject(new Error(t("Spotify's player didn't start in this browser")));
      }
    });
  });
  return { player, deviceId };
}
