import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { clampVolume, currentMood, isPhone, setPrefs, turnOn, useSoundPrefs, withMode, type SoundMode } from "../../lib/soundtrack/core";
import { startSdk, useMoods, useMySpotify, type NowPlaying, type SdkPlayer } from "../../lib/soundtrack/spotify";

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/**
 * useSoundtrack plays a chapter's soundtrack while music is on. On a
 * computer this tab becomes a Spotify device (Web Playback SDK); on phones,
 * where the SDK doesn't run, Spotify plays on the reader's own app or
 * speaker and mangarr only tells it what to play.
 */
export function useSoundtrack(chapterId: number, page: number) {
  const prefs = useSoundPrefs();
  const on = prefs.mode !== "off";
  const me = useMySpotify();
  const connected = !!me.data?.connected;
  const moods = useMoods(on || connected);
  const resolved = useQuery({
    queryKey: ["soundtrack", "chapter", chapterId],
    queryFn: () => unwrap(api.GET("/api/v1/read/chapters/{id}/soundtrack", { params: { path: { id: chapterId } } })),
    enabled: chapterId > 0 && (on || connected),
  });
  const mood = currentMood(prefs, resolved.data, page, moods.data ?? []);
  const phone = useMemo(() => isPhone(navigator.userAgent, navigator.maxTouchPoints), []);

  // this browser as a Spotify device (computers)
  const [sdk, setSdk] = useState<{ player: SdkPlayer; deviceId: string } | null>(null);
  const [sdkError, setSdkError] = useState("");
  const [sdkNow, setSdkNow] = useState<NowPlaying | null>(null);
  const sdkRef = useRef(sdk);
  sdkRef.current = sdk;
  const starting = useRef(false);
  useEffect(() => {
    if (!on || !connected || phone || sdk || sdkError || starting.current) return;
    starting.current = true;
    startSdk(prefs.volume, setSdkNow).then(setSdk, (e) => setSdkError(errorText(e))).finally(() => (starting.current = false));
  }, [on, connected, phone, sdk, sdkError, prefs.volume]);
  useEffect(() => () => sdkRef.current?.player.disconnect(), []);

  // where music plays: this browser unless another device was picked
  const inBrowser = !!sdk && (prefs.deviceId === "" || prefs.deviceId === sdk.deviceId);
  const deviceId = inBrowser ? sdk.deviceId : prefs.deviceId;
  // a computer waits for its own player before playing (unless it failed or
  // music goes elsewhere)
  const ready = connected && (phone || !!sdk || !!sdkError || prefs.deviceId !== "");

  const [error, setError] = useState("");
  const played = useRef<string>("");
  const deviceRef = useRef(deviceId);
  deviceRef.current = deviceId;
  const uri = on ? (mood?.spotifyUri ?? "") : "";
  useEffect(() => {
    if (!ready) return;
    const key = uri && `${uri}|${deviceId}`;
    if (key === played.current) return;
    const was = played.current;
    played.current = key;
    if (!uri) {
      // off, muted or no music here: stop what mangarr started
      if (!was) return;
      if (sdkRef.current && was.endsWith("|" + sdkRef.current.deviceId)) void sdkRef.current.player.pause().catch(() => {});
      else void unwrap(api.PUT("/api/v1/me/spotify/pause", { body: { deviceId: was.split("|")[1] || undefined } })).catch(() => {});
      return;
    }
    setError("");
    unwrap(
      api.PUT("/api/v1/me/spotify/play", {
        body: { uri, deviceId: deviceId || undefined, shuffle: mood?.shuffle, volume: inBrowser ? undefined : clampVolume(prefs.volume) },
      }),
    ).catch((e) => {
      played.current = "";
      setError(errorText(e));
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- plays when the music or the device changes
  }, [ready, uri, deviceId]);

  // leaving the reader stops the music it started on another device
  useEffect(
    () => () => {
      const was = played.current;
      if (was && !sdkRef.current) void unwrap(api.PUT("/api/v1/me/spotify/pause", { body: { deviceId: was.split("|")[1] || undefined } })).catch(() => {});
    },
    [],
  );

  // volume: the browser's player at once, other devices after a pause in dragging
  useEffect(() => {
    if (!on || !connected) return;
    if (inBrowser) {
      void sdk.player.setVolume(prefs.volume / 100).catch(() => {});
      return;
    }
    if (!played.current) return;
    const timer = window.setTimeout(() => {
      void unwrap(api.PUT("/api/v1/me/spotify/volume", { body: { volume: prefs.volume, deviceId: deviceRef.current || undefined } })).catch(() => {});
    }, 400);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- follows the slider
  }, [prefs.volume]);

  // what plays elsewhere, asked every now and then
  const remote = useQuery({
    queryKey: ["soundtrack", "player"],
    queryFn: () => unwrap(api.GET("/api/v1/me/spotify/player")),
    enabled: on && connected && !inBrowser,
    refetchInterval: 15_000,
  });
  const now: NowPlaying | null = inBrowser
    ? sdkNow
    : remote.data?.track
      ? { playing: remote.data.isPlaying, track: remote.data.track, artists: remote.data.artists ?? "", imageUrl: remote.data.imageUrl ?? "", device: remote.data.device?.name ?? "" }
      : null;

  const setMode = useCallback(
    (mode: SoundMode) => {
      if (mode !== "off") void sdkRef.current?.player.activateElement().catch(() => {});
      setPrefs((p) => withMode(p, mode));
    },
    [],
  );
  const toggleOn = useCallback(() => {
    void sdkRef.current?.player.activateElement().catch(() => {});
    setPrefs((p) => (p.mode === "off" ? turnOn(p) : withMode(p, "off")));
  }, []);
  const pickMood = useCallback((id: number) => {
    void sdkRef.current?.player.activateElement().catch(() => {});
    setPrefs((p) => ({ ...withMode(p, "manual"), moodId: id }));
  }, []);
  const setVolume = useCallback((v: number) => setPrefs({ volume: clampVolume(v) }), []);
  const setDevice = useCallback((id: string) => {
    played.current = "";
    setPrefs({ deviceId: id });
  }, []);
  const togglePlay = useCallback(async () => {
    try {
      if (inBrowser) {
        await (sdkNow?.playing ? sdk.player.pause() : sdk.player.resume());
      } else {
        const body = { deviceId: deviceRef.current || undefined };
        await unwrap(now?.playing ? api.PUT("/api/v1/me/spotify/pause", { body }) : api.PUT("/api/v1/me/spotify/resume", { body }));
        void remote.refetch();
      }
    } catch (e) {
      setError(errorText(e));
    }
  }, [inBrowser, sdk, sdkNow, now, remote]);

  return {
    prefs,
    on,
    mood,
    moods: moods.data ?? [],
    resolved: resolved.data,
    spotify: me.data,
    connected,
    phone,
    browserDevice: sdk?.deviceId ?? "",
    sdkError,
    deviceId,
    now,
    error,
    setMode,
    toggleOn,
    pickMood,
    setVolume,
    setDevice,
    togglePlay,
  };
}

export type Soundtrack = ReturnType<typeof useSoundtrack>;
