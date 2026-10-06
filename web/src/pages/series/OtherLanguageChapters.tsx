import { t } from "../../lib/i18n/core";
import { useMemo, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { Link } from "react-router";
import { BookOpen, ChevronDown, ChevronRight } from "lucide-react";
import { api, unwrap, type Chapter, type S } from "../../api/client";
import { Badge } from "../../components/ui";
import { languageName } from "../../lib/format";
import { borrowChapters } from "./borrow";

/** OtherLanguageChapters lists chapters only another language edition of
 * the title has, tagged with their language; they open in that edition's
 * reader and share the title's progress. The list starts collapsed to one
 * summary row so it doesn't push the title's own chapters down. */
export function OtherLanguageChapters({ seriesId, editions, own }: { seriesId: number; editions: S["EditionSummary"][]; own: Chapter[] }) {
  const others = editions.filter((e) => e.id !== seriesId);
  const lists = useQueries({
    queries: others.map((e) => ({ queryKey: ["series", e.id, "chapters"], queryFn: () => unwrap(api.GET("/api/v1/series/{id}/chapters", { params: { path: { id: e.id } } })) })),
  });
  const ready = lists.map((q) => q.data);
  const borrowed = useMemo(
    () => borrowChapters(own, others.map((edition, i) => ({ edition, chapters: ready[i] ?? [] }))),
    [own, others.map((e) => e.id).join(","), ...ready],
  );
  const [open, setOpen] = useState(false);
  if (!borrowed.length) return null;
  const langs = [...new Set(borrowed.map(({ edition }) => (edition.language ? languageName(edition.language) : "?")))].join(", ");
  // borrowChapters sorts newest first.
  const hi = borrowed[0].chapter.number;
  const lo = borrowed[borrowed.length - 1].chapter.number;
  const range = lo === hi ? String(lo) : `${lo}–${hi}`;
  return (
    <section aria-label={t("Chapters from other languages")} className="mb-3 overflow-hidden rounded-lg border border-info/30 bg-info/5">
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:text-accent-2">
        {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
        <span className="min-w-0 flex-1 truncate">{t("Chapters only in {langs}: {count}", { langs, count: borrowed.length })}</span>
        <span className="shrink-0 text-xs tabular-nums text-muted">{range}</span>
      </button>
      {open && (
        <>
      <p className="border-y border-info/20 px-3 py-2 text-xs text-muted">{t("Only in another language: read them there, progress is shared.")}</p>
      <ul className="divide-y divide-border">
        {borrowed.map(({ chapter: c, edition }) => (
          <li key={c.id} className="flex items-center gap-3 px-3 py-2 text-sm">
            <span className="w-14 shrink-0 font-semibold tabular-nums">{c.number}</span>
            <span className="min-w-0 flex-1 truncate">{c.title}</span>
            <Badge tone="info">{edition.language ? languageName(edition.language) : "?"}</Badge>
            <Link to={`/read/${c.id}`} className="flex items-center gap-1 text-accent-2 hover:underline">
              <BookOpen className="size-3.5" />
              {t("Read")}
            </Link>
          </li>
        ))}
      </ul>
        </>
      )}
    </section>
  );
}
