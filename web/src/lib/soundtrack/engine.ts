// The ambience engine: one AudioContext playing a mood's loops, decoded
// once and looped without gaps, with gain ramps to crossfade between
// moods, and sound effects played once over them.
import { diffLayers, type Layer } from "./core";

type Playing = { src: AudioBufferSourceNode; gain: GainNode; layer: Layer };

/** fadeSeconds is how long moods crossfade. */
export const fadeSeconds = 2;

export class AmbienceEngine {
  private ctx: AudioContext | null = null;
  private master: GainNode | null = null;
  private bed: GainNode | null = null;
  private playing = new Map<number, Playing>();
  private buffers = new Map<string, Promise<AudioBuffer>>();
  private current: Layer[] = [];
  private volume = 0.6;

  /** unlock makes (or resumes) the AudioContext: call it from a tap or click. */
  unlock() {
    if (!this.ctx) {
      const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
      if (!Ctx) return;
      this.ctx = new Ctx();
      this.master = this.ctx.createGain();
      this.master.gain.value = this.volume;
      this.master.connect(this.ctx.destination);
      this.bed = this.ctx.createGain();
      this.bed.connect(this.master);
    }
    if (this.ctx.state === "suspended") void this.ctx.resume().catch(() => {});
  }

  get ready() {
    return !!this.ctx;
  }

  /** load fetches and decodes a sound once. */
  load(url: string): Promise<AudioBuffer> {
    const ctx = this.ctx;
    if (!ctx) return Promise.reject(new Error("audio is locked"));
    let p = this.buffers.get(url);
    if (!p) {
      p = fetch(url, { credentials: "same-origin" })
        .then((r) => {
          if (!r.ok) throw new Error(`HTTP ${r.status}`);
          return r.arrayBuffer();
        })
        .then((b) => ctx.decodeAudioData(b));
      p.catch(() => this.buffers.delete(url));
      this.buffers.set(url, p);
    }
    return p;
  }

  /** prefetch decodes sounds that will play soon. */
  prefetch(urls: string[]) {
    if (!this.ctx) return;
    for (const u of urls) void this.load(u).catch(() => {});
  }

  /** setLayers crossfades to a new set of loops. */
  async setLayers(next: Layer[]) {
    const ctx = this.ctx;
    const bed = this.bed;
    if (!ctx || !bed) {
      this.current = next;
      return;
    }
    const { start, stop, change } = diffLayers(this.current, next);
    this.current = next;
    const now = ctx.currentTime;
    for (const l of stop) {
      const p = this.playing.get(l.id);
      if (!p) continue;
      this.playing.delete(l.id);
      p.gain.gain.cancelScheduledValues(now);
      p.gain.gain.setValueAtTime(p.gain.gain.value, now);
      p.gain.gain.linearRampToValueAtTime(0, now + fadeSeconds);
      p.src.stop(now + fadeSeconds + 0.05);
    }
    for (const l of change) {
      const p = this.playing.get(l.id);
      if (!p) continue;
      p.layer = l;
      p.gain.gain.cancelScheduledValues(now);
      p.gain.gain.setValueAtTime(p.gain.gain.value, now);
      p.gain.gain.linearRampToValueAtTime(l.gain, now + fadeSeconds);
    }
    await Promise.all(
      start.map(async (l) => {
        let buf: AudioBuffer;
        try {
          buf = await this.load(l.url);
        } catch {
          return;
        }
        // the mood may have moved on while this loaded
        if (!this.current.some((c) => c.id === l.id) || this.playing.has(l.id)) return;
        const src = ctx.createBufferSource();
        src.buffer = buf;
        src.loop = true;
        const gain = ctx.createGain();
        const t = ctx.currentTime;
        gain.gain.setValueAtTime(0, t);
        gain.gain.linearRampToValueAtTime(l.gain, t + fadeSeconds);
        src.connect(gain).connect(bed);
        // loops start at a random point so the same sound in two moods doesn't phase
        src.start(t, Math.random() * buf.duration);
        this.playing.set(l.id, { src, gain, layer: l });
      }),
    );
  }

  /** layers are the loops playing now. */
  get layers() {
    return this.current;
  }

  /** setVolume sets the ambience and effects volume (0-1). */
  setVolume(v: number) {
    this.volume = v;
    if (this.ctx && this.master) this.master.gain.setTargetAtTime(v, this.ctx.currentTime, 0.05);
  }

  /**
   * playEffect plays a sound once over the ambience, ducking the loops while
   * it plays; it resolves with how long it lasts (seconds).
   */
  async playEffect(url: string, gain = 1): Promise<number> {
    this.unlock();
    const ctx = this.ctx;
    if (!ctx || !this.master || !this.bed) return 0;
    const buf = await this.load(url);
    const src = ctx.createBufferSource();
    src.buffer = buf;
    const g = ctx.createGain();
    g.gain.value = gain;
    src.connect(g).connect(this.master);
    const t = ctx.currentTime;
    const duck = this.bed.gain;
    duck.cancelScheduledValues(t);
    duck.setValueAtTime(duck.value, t);
    duck.linearRampToValueAtTime(0.4, t + 0.1);
    duck.setValueAtTime(0.4, t + buf.duration);
    duck.linearRampToValueAtTime(1, t + buf.duration + 0.6);
    src.start(t);
    return buf.duration;
  }

  /** pause and resume hold everything (the browser's media keys use them). */
  pause() {
    void this.ctx?.suspend().catch(() => {});
  }

  resume() {
    void this.ctx?.resume().catch(() => {});
  }

  get paused() {
    return this.ctx?.state === "suspended";
  }

  /** close stops everything and lets the AudioContext go. */
  close() {
    for (const p of this.playing.values()) {
      try {
        p.src.stop();
      } catch {
        /* already stopped */
      }
    }
    this.playing.clear();
    this.current = [];
    void this.ctx?.close().catch(() => {});
    this.ctx = null;
    this.master = null;
    this.bed = null;
    this.buffers.clear();
  }
}
