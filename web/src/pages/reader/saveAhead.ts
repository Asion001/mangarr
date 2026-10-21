import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { S } from "../../api/client";
import { offlineReady, readKept, removeChapter, removeRead, saveChapter, savedChapters, staleAhead } from "../../lib/offline/store";
import { chapterQuery } from "./query";

/**
 * useSaveAhead keeps the open chapter and the next `count` saved on this
 * device while reading, so the reader carries on when the server restarts
 * or the network drops, and removes chapters it saved ahead that are no
 * longer ahead (read, or skipped past).
 */
export function useSaveAhead(ch: S["ReadChapter"] | null | undefined, count: number, width: number) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!ch || !offlineReady()) return;
    if (removeRead()) for (const id of readKept(savedChapters(), ch.seriesId, ch.id)) void removeChapter(id);
    if (count <= 0) {
      // off: nothing is kept for this series but what was saved on purpose
      for (const id of staleAhead(savedChapters(), ch.seriesId, new Set())) void removeChapter(id);
      return;
    }
    const stop = new AbortController();
    const signal = stop.signal;
    void (async () => {
      const keep = new Set([ch.id]);
      await saveChapter(ch, "ahead", width, signal);
      let next = ch.next;
      for (let i = 0; i < count && next && !signal.aborted; i++) {
        const nc = await qc.fetchQuery(chapterQuery(next.id));
        keep.add(nc.id);
        await saveChapter(nc, "ahead", width, signal);
        next = nc.next;
      }
      // only once everything ahead is saved: offline, nothing is removed
      if (signal.aborted) return;
      for (const id of staleAhead(savedChapters(), ch.seriesId, keep)) await removeChapter(id);
    })().catch(() => undefined);
    return () => stop.abort();
    // a chapter's details refetching doesn't start the saving over
  }, [ch?.id, count, width, qc]);

  // a chapter saved on purpose and read to the end goes once the reader leaves it
  useEffect(() => {
    const id = ch?.id;
    return () => {
      const c = id ? savedChapters()[id] : undefined;
      if (c?.kind === "kept" && c.readAt && removeRead()) void removeChapter(c.id);
    };
  }, [ch?.id]);
}
