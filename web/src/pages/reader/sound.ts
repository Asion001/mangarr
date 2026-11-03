import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../../api/client";
import { clampVolume, currentSound, dbGain, isPhone, layersOf, setPrefs, soundName, sourceOf, turnOn, upcoming, useSoundPrefs, withMode, type MoodTrack, type SoundMode, type SoundSource } from "../../lib/soundtrack/core";
import { AmbienceEngine } from "../../lib/soundtrack/engine";
import { moodName } from "../../lib/soundtrack/moods";
import { startSdk, useMoods, useMySpotify, type NowPlaying, type SdkPlayer } from "../../lib/soundtrack/spotify";
import { Session } from "../../lib/soundtrack/session";
import { trackUrl } from "../../lib/soundtrack/tracks";

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/**
 * playing is what carries on from one chapter's reader to the next: the
 * ambience, this tab's Spotify player and what mangarr asked Spotify to
 * play. Closing the reader stops them.
 */
const playing = new Session(
  () => ({
    engine: null as AmbienceEngine | null,
    sdk: null as { player: SdkPlayer; deviceId: string } | null,
    now: null as NowPlaying | null,
    onNow: (() => {}) as (n: NowPlaying | null) => void,
    // played is the music and device mangarr last asked for ("uri|device")
    played: "",
    // renewed: this tab's player was started again once after Spotify lost it
    renewed: false,
  }),
  (was) => {
    if (was.played && !was.sdk) void unwrap(api.PUT("/api/v1/me/spotify/pause", { body: { deviceId: was.played.split("|")[1] || undefined } })).catch(() => {});
    was.sdk?.player.disconnect();
    was.engine?.close();
  },
);

/** useSettledPage is the page once it has stayed put for a while (webtoon strips scroll past pages). */
export function useSettledPage(page: number, wait: number) {
  const [settled, setSettled] = useState(page);
  useEffect(() => {
    if (wait <= 0) return setSettled(page);
    const timer = window.setTimeout(() => setSettled(page), wait);
    return () => window.clearTimeout(timer);
  }, [page, wait]);
  return wait <= 0 ? page : settled;
}

/**
 * useSoundtrack plays a chapter's soundtrack while it's on: music from
 * Spotify and its mood's ambience and effects from sound packs, or (picked
 * by the reader, or without Spotify) the ambience and effects alone. On a
 * computer this tab becomes a Spotify device (Web Playback SDK); on phones,
 * where the SDK doesn't run, Spotify plays on the reader's own app or
 * speaker and mangarr only tells it what to play. Ambience plays in the
 * tab either way, also without Spotify.
 */
export function useSoundtrack(chapterId: number, page: number, webtoon: boolean, title = "") {
  const prefs = useSoundPrefs();
  const on = prefs.mode !== "off";
  const me = useMySpotify();
  const connected = !!me.data?.connected;
  const source = sourceOf(prefs, connected);
  const spotifyOn = on && source === "spotify";
  const moods = useMoods(on || connected);
  const resolved = useQuery({
    queryKey: ["soundtrack", "chapter", chapterId],
    queryFn: () => unwrap(api.GET("/api/v1/read/chapters/{id}/soundtrack", { params: { path: { id: chapterId } } })),
    enabled: chapterId > 0,
  });
  // a chapter whose soundtrack is still loading changes nothing (the last
  // chapter's music plays on until it's known)
  const loading = chapterId > 0 && resolved.data === undefined && !resolved.isError;
  useEffect(() => playing.hold(), []);
  // with music on, moods are found in this chapter's pages and the next
  // two's ahead of the reader (when the server does that)
  useEffect(() => {
    if (!on || chapterId <= 0) return;
    void api.POST("/api/v1/read/chapters/{id}/soundtrack/prepare", { params: { path: { id: chapterId } } }).catch(() => {});
  }, [on, chapterId]);
  // a webtoon strip waits for the reader to settle on a page before switching
  const settled = useSettledPage(page, webtoon ? 1000 : 0);
  const sound = currentSound(prefs, resolved.data, settled, moods.data ?? []);
  const mood = sound?.mood ?? null;
  const phone = useMemo(() => isPhone(navigator.userAgent, navigator.maxTouchPoints), []);

  // ambience and effects, in this tab
  const engine = useMemo(() => (playing.value.engine ??= new AmbienceEngine()), []);
  // the browser starts audio only after a tap: music remembered as on
  // waits for the first one
  useEffect(() => {
    if (!on || engine.ready) return;
    const unlock = () => {
      engine.unlock();
      void sdkRef.current?.player.activateElement().catch(() => {});
    };
    window.addEventListener("pointerdown", unlock, { once: true });
    window.addEventListener("keydown", unlock, { once: true });
    return () => {
      window.removeEventListener("pointerdown", unlock);
      window.removeEventListener("keydown", unlock);
    };
  }, [on, engine]);
  const layers = useMemo(() => layersOf(on ? sound : null, trackUrl), [on, sound]);
  const layerKey = layers.map((l) => `${l.id}:${l.gain}`).join(",");
  useEffect(() => {
    if (!loading) void engine.setLayers(layers);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- follows what the layers are, not the array
  }, [engine, layerKey, loading]);
  useEffect(() => engine.setVolume(prefs.ambience / 100), [engine, prefs.ambience]);
  const ahead = upcoming(resolved.data, settled, 3);
  const aheadKey = ahead.map((a) => a.mood?.id ?? 0).join(",");
  useEffect(() => {
    if (on) engine.prefetch(ahead.flatMap((a) => (a.mood?.ambience ?? []).map((t) => trackUrl(t.trackId))));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- prefetches when the cues ahead change
  }, [engine, on, aheadKey]);

  // this browser as a Spotify device (computers)
  const [sdk, setSdk] = useState(playing.value.sdk);
  const [sdkError, setSdkError] = useState("");
  const [sdkNow, setSdkNow] = useState(playing.value.now);
  useEffect(() => {
    const session = playing.value;
    session.onNow = (n) => {
      session.now = n;
      setSdkNow(n);
    };
    return () => {
      session.onNow = () => {};
    };
  }, []);
  const sdkRef = useRef(sdk);
  sdkRef.current = sdk;
  const starting = useRef(false);
  useEffect(() => {
    if (!spotifyOn || phone || sdk || sdkError || starting.current) return;
    starting.current = true;
    const session = playing.value;
    startSdk(prefs.volume, (n) => session.onNow(n))
      .then((s) => {
        session.sdk = s;
        setSdk(s);
      }, (e) => setSdkError(errorText(e)))
      .finally(() => (starting.current = false));
  }, [spotifyOn, phone, sdk, sdkError, prefs.volume]);

  // where music plays: this browser unless another device was picked
  const inBrowser = !!sdk && (prefs.deviceId === "" || prefs.deviceId === sdk.deviceId);
  const deviceId = inBrowser ? sdk.deviceId : prefs.deviceId;
  // a computer waits for its own player before playing (unless it failed or
  // music goes elsewhere)
  const ready = connected && (phone || !!sdk || !!sdkError || prefs.deviceId !== "");

  const [error, setError] = useState("");
  // requesting: a play request is on its way to Spotify
  const [requesting, setRequesting] = useState(false);
  const prefsRef = useRef(prefs);
  prefsRef.current = prefs;
  const deviceRef = useRef(deviceId);
  deviceRef.current = deviceId;
  const uri = spotifyOn ? (sound?.music?.uri ?? "") : "";
  useEffect(() => {
    if (!ready || loading) return;
    const key = uri && `${uri}|${deviceId}`;
    if (key === playing.value.played) return;
    const was = playing.value.played;
    playing.value.played = key;
    if (!uri) {
      // off, muted or no music here: stop what mangarr started
      if (!was) return;
      if (sdkRef.current && was.endsWith("|" + sdkRef.current.deviceId)) void sdkRef.current.player.pause().catch(() => {});
      else void unwrap(api.PUT("/api/v1/me/spotify/pause", { body: { deviceId: was.split("|")[1] || undefined } })).catch(() => {});
      return;
    }
    setError("");
    setRequesting(true);
    unwrap(
      api.PUT("/api/v1/me/spotify/play", {
        body: { uri, deviceId: deviceId || undefined, shuffle: sound?.music?.shuffle, volume: inBrowser ? undefined : clampVolume(prefs.volume) },
      }),
    )
      .then(() => {
        playing.value.renewed = false;
      })
      .catch((e) => {
        const session = playing.value;
        session.played = "";
        if (e instanceof ApiError && e.status === 410) {
          if (inBrowser && !session.renewed) {
            // Spotify lost this tab's player: a new one plays once it's ready
            session.renewed = true;
            session.sdk?.player.disconnect();
            session.sdk = null;
            setSdk(null);
            return;
          }
          if (!inBrowser && sdkRef.current) {
            // the device picked went away: back to this browser
            setPrefs({ deviceId: "" });
            return;
          }
        }
        setError(errorText(e));
      })
      .finally(() => setRequesting(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- plays when the music or the device changes
  }, [ready, loading, uri, deviceId]);

  // volume: the browser's player at once, other devices after a pause in dragging
  useEffect(() => {
    if (!spotifyOn) return;
    if (inBrowser) {
      void sdk.player.setVolume(prefs.volume / 100).catch(() => {});
      return;
    }
    if (!playing.value.played) return;
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
    enabled: spotifyOn && !inBrowser,
    refetchInterval: 15_000,
  });
  const now: NowPlaying | null = !spotifyOn
    ? null
    : inBrowser
    ? sdkNow
    : remote.data?.track
      ? { playing: remote.data.isPlaying, track: remote.data.track, artists: remote.data.artists ?? "", imageUrl: remote.data.imageUrl ?? "", device: remote.data.device?.name ?? "" }
      : null;

  // the browser's media keys and lock screen pause the ambience
  const playingName = on ? soundName(sound, moodName) : "";
  useEffect(() => {
    const ms = navigator.mediaSession;
    if (!ms || typeof MediaMetadata === "undefined" || inBrowser || !playingName || layers.length === 0) return;
    ms.metadata = new MediaMetadata({ title: playingName, artist: "mangarr", album: title });
    ms.setActionHandler("play", () => engine.resume());
    ms.setActionHandler("pause", () => engine.pause());
    return () => {
      ms.metadata = null;
      ms.setActionHandler("play", null);
      ms.setActionHandler("pause", null);
    };
  }, [engine, inBrowser, playingName, title, layers.length]);

  const unlock = useCallback(() => {
    engine.unlock();
    void sdkRef.current?.player.activateElement().catch(() => {});
  }, [engine]);
  const setMode = useCallback(
    (mode: SoundMode) => {
      if (mode !== "off") unlock();
      setPrefs((p) => withMode(p, mode));
    },
    [unlock],
  );
  const toggleOn = useCallback(() => {
    unlock();
    setPrefs((p) => (p.mode === "off" ? turnOn(p) : withMode(p, "off")));
  }, [unlock]);
  const pickMood = useCallback(
    (id: number) => {
      unlock();
      setPrefs((p) => ({ ...withMode(p, "manual"), moodId: id }));
    },
    [unlock],
  );
  const setSource = useCallback((v: SoundSource) => setPrefs({ source: v }), []);
  const setVolume = useCallback((v: number) => setPrefs({ volume: clampVolume(v) }), []);
  const setAmbience = useCallback((v: number) => setPrefs({ ambience: clampVolume(v) }), []);
  // an effect ducks the music in this tab while it plays
  const playEffect = useCallback(
    async (t: MoodTrack): Promise<number> => {
      try {
        const secs = await engine.playEffect(trackUrl(t.trackId), dbGain(t.gainDb));
        const player = sdkRef.current?.player;
        if (player && inBrowser && secs > 0) {
          const v = prefsRef.current.volume / 100;
          await player.setVolume(v * 0.4);
          window.setTimeout(() => void player.setVolume(prefsRef.current.volume / 100).catch(() => {}), secs * 1000 + 300);
        }
        return secs;
      } catch (e) {
        setError(errorText(e));
        return 0;
      }
    },
    [engine, inBrowser],
  );
  const setDevice = useCallback((id: string) => {
    playing.value.played = "";
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

  const nextTrack = useCallback(async () => {
    try {
      if (inBrowser) await sdk.player.nextTrack();
      else {
        await unwrap(api.POST("/api/v1/me/spotify/next", { body: { deviceId: deviceRef.current || undefined } }));
        window.setTimeout(() => void remote.refetch(), 800);
      }
    } catch (e) {
      setError(errorText(e));
    }
  }, [inBrowser, sdk, remote]);

  return {
    prefs,
    on,
    sound,
    mood,
    starting: requesting,
    layers: on ? (sound?.mood?.ambience ?? []) : [],
    effects: on ? (sound?.mood?.sfx ?? []) : [],
    source,
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
    setSource,
    setVolume,
    setAmbience,
    playEffect,
    setDevice,
    togglePlay,
    nextTrack,
  };
}

export type Soundtrack = ReturnType<typeof useSoundtrack>;
