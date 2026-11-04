import { t } from "../../lib/i18n/core";
import type { S } from "../../api/client";

type Track = S["SoundtrackTrackView"];

/** packsOf groups the installed sounds by pack, each by title. */
export function packsOf(tracks: Track[]): { name: string; licenses: string[]; sounds: Track[] }[] {
  const byPack = new Map<string, Track[]>();
  for (const tr of tracks) {
    const name = tr.pack || t("Imported");
    byPack.set(name, [...(byPack.get(name) ?? []), tr]);
  }
  return [...byPack.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, sounds]) => ({
      name,
      licenses: [...new Set(sounds.map((s) => s.license).filter(Boolean))].sort(),
      sounds: [...sounds].sort((a, b) => a.title.localeCompare(b.title)),
    }));
}
