import { useId, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { Check, PlusCircle } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { genreName } from "../../lib/genres";
import { t } from "../../lib/i18n/core";
import { AskModal } from "../requests/Requests";
import { useOpenPreview } from "./Preview";

type Item = S["RecommendationItem"];

function relationName(relation: string) {
  switch (relation) {
    case "sequel": return t("Sequel");
    case "prequel": return t("Prequel");
    case "side_story": return t("Side story");
    case "spin_off": return t("Spin-off");
    case "alternative": return t("Alternative version");
    case "parent": return t("Main story");
    case "source": return t("Original");
    case "summary": return t("Summary");
    case "compilation": return t("Compilation");
    case "contains": return t("Contains");
  }
  return relation.replace(/_/g, " ");
}

function why(item: Item) {
  if (item.sharedGenres?.length) return t("Same genres: {genres}", { genres: item.sharedGenres.slice(0, 3).map(genreName).join(", ") });
  return t("Recommended on {provider}", { provider: item.moduleName });
}

/**
 * Recommendations closes a title's page: titles in the same story (sequels,
 * prequels, side stories) and titles like it, from the metadata provider's
 * recommendations and the library's titles with the same genres. Each says
 * whether it's in the library or offers the add or request flow.
 */
export function Recommendations({ seriesId }: { seriesId: number }) {
  const { data, refetch } = useQuery({
    queryKey: ["series", seriesId, "recommendations"],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/recommendations", { params: { path: { id: seriesId } } })),
    staleTime: 10 * 60_000,
  });
  const [filter, setFilter] = useState<"all" | "library" | "new">("all");
  const [asking, setAsking] = useState<Item | null>(null);
  const relatedId = useId();
  const similarId = useId();
  // an answer without both lists (an older server, a stubbed one) shows nothing
  if (!Array.isArray(data?.related) || !Array.isArray(data?.similar) || (data.related.length === 0 && data.similar.length === 0)) return null;
  const inLibrary = data.similar.filter((i) => i.existingSeriesId).length;
  const notAdded = data.similar.length - inLibrary;
  const similar = data.similar.filter((i) => filter === "all" || (filter === "library") === !!i.existingSeriesId);
  const filters = [
    { value: "all" as const, label: `${t("All")} ${data.similar.length}` },
    { value: "library" as const, label: `${t("In library")} ${inLibrary}` },
    { value: "new" as const, label: `${t("Not added")} ${notAdded}` },
  ];
  return (
    <div className="mt-8 flex flex-col gap-8 border-t border-border pt-6">
      {data.related.length > 0 && (
        <section aria-labelledby={relatedId}>
          <div className="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h2 id={relatedId} className="text-lg font-semibold">{t("Same story")}</h2>
            <span className="text-xs text-muted">{t("Sequels, prequels and spin-offs")}</span>
          </div>
          <div className="grid grid-cols-[minmax(0,1fr)] gap-2 md:grid-cols-2 xl:grid-cols-3">
            {data.related.map((item) => (
              <RelatedRow key={`${item.moduleId}:${item.id}`} item={item} onAsk={setAsking} />
            ))}
          </div>
        </section>
      )}
      {data.similar.length > 0 && (
        <section aria-labelledby={similarId}>
          <div className="mb-3 flex flex-wrap items-center gap-3">
            <h2 id={similarId} className="text-lg font-semibold">{t("More like this")}</h2>
            {inLibrary > 0 && notAdded > 0 && (
              <div role="group" aria-label={t("Filter recommendations")} className="flex gap-0.5 rounded-lg border border-border bg-panel p-0.5">
                {filters.map((f) => (
                  <button
                    key={f.value}
                    type="button"
                    aria-pressed={filter === f.value}
                    onClick={() => setFilter(f.value)}
                    className={clsx(
                      "h-8 rounded-md px-3 text-sm sm:h-7",
                      filter === f.value ? "bg-panel-2 font-semibold text-fg" : "text-muted hover:text-fg",
                    )}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            )}
          </div>
          <div className="-mx-4 flex gap-4 overflow-x-auto px-4 pb-2 sm:mx-0 sm:px-0">
            {similar.map((item) => (
              <Card key={item.existingSeriesId ? `s${item.existingSeriesId}` : `${item.moduleId}:${item.id}`} item={item} onAsk={setAsking} />
            ))}
          </div>
        </section>
      )}
      {asking && (
        <AskModal
          result={asking}
          onClose={() => setAsking(null)}
          onDone={() => void refetch()}
        />
      )}
    </div>
  );
}

function coverOf(item: Item) {
  return item.seriesCoverUrl ? apiUrl(item.seriesCoverUrl) : item.coverUrl;
}

/** useOpenItem opens a title: its page when it's in the library, else a preview. */
function useOpenItem() {
  const { open, opening } = useOpenPreview();
  const go = (item: Item) =>
    void open(`${item.moduleId}:${item.id}`, {
      metadata: { moduleId: item.moduleId, provider: item.provider, id: item.id },
      title: item.title,
      titles: (item.altTitles ?? []).slice(0, 10),
    });
  return { go, opening };
}

/** Action is In library, Requested, or the add or request button. */
function Action({ item, onAsk, compact }: { item: Item; onAsk: (i: Item) => void; compact?: boolean }) {
  const { can } = useAccount();
  const nav = useNavigate();
  if (item.existingSeriesId) return compact ? <Badge tone="ok"><Check className="size-3" />{t("In library")}</Badge> : null;
  if (can("library.add")) {
    return (
      <Button size="sm" icon={<PlusCircle className="size-4" />} onClick={() => nav(`/add/${item.moduleId}/${encodeURIComponent(item.id)}/sources`)}>
        {t("Add…")}
      </Button>
    );
  }
  if (!can("requests.create")) return null;
  if (item.request?.mine) return <Badge tone="info"><Check className="size-3" />{t("Requested")}</Badge>;
  return (
    <Button size="sm" variant="primary" icon={<PlusCircle className="size-4" />} onClick={() => onAsk(item)}>
      {item.request ? t("Request too") : t("Request")}
    </Button>
  );
}

function RelatedRow({ item, onAsk }: { item: Item; onAsk: (i: Item) => void }) {
  const { go, opening } = useOpenItem();
  const meta = [item.relation ? relationName(item.relation) : "", item.format ?? "", item.year ? String(item.year) : ""].filter(Boolean).join(" · ");
  const body = (
    <>
      <Cover src={coverOf(item)} alt="" className="aspect-[2/3] w-9 shrink-0 rounded-sm" />
      <span className="min-w-0 flex-1">
        <span className="line-clamp-2 text-sm font-medium leading-snug" title={item.title}>{item.title}</span>
        <span className="block text-xs text-muted">{meta}</span>
      </span>
    </>
  );
  return (
    <div className="flex min-h-16 items-center gap-2.5 rounded-lg border border-border bg-panel py-2 pl-2 pr-3">
      {item.existingSeriesId ? (
        <Link to={`/series/${item.existingSeriesId}`} className="flex min-w-0 flex-1 items-center gap-2.5 hover:text-accent-2">{body}</Link>
      ) : (
        <button type="button" disabled={!!opening} onClick={() => go(item)} className="flex min-w-0 flex-1 items-center gap-2.5 text-left hover:text-accent-2">{body}</button>
      )}
      <Action item={item} onAsk={onAsk} compact />
    </div>
  );
}

function Card({ item, onAsk }: { item: Item; onAsk: (i: Item) => void }) {
  const { go, opening } = useOpenItem();
  const cover = (
    <>
      <Cover src={coverOf(item)} alt={item.title} className="aspect-[2/3] w-full" />
      {item.existingSeriesId ? (
        <span className="absolute left-1.5 top-1.5 inline-flex items-center gap-1 rounded-full bg-ok px-2 py-0.5 text-xs font-semibold text-bg shadow">
          <Check className="size-3" />{t("In library")}
        </span>
      ) : item.request?.mine ? (
        <span className="absolute left-1.5 top-1.5 rounded-full bg-info px-2 py-0.5 text-xs font-semibold text-bg shadow">{t("Requested")}</span>
      ) : null}
    </>
  );
  const title = <span className="line-clamp-2 text-sm font-medium leading-snug" title={item.title}>{item.title}</span>;
  return (
    <div className="flex w-32 shrink-0 flex-col gap-1.5 sm:w-36">
      {item.existingSeriesId ? (
        <Link to={`/series/${item.existingSeriesId}`} className="flex flex-col gap-1.5 hover:text-accent-2">
          <span className="relative block">{cover}</span>
          {title}
        </Link>
      ) : (
        <button type="button" disabled={!!opening} onClick={() => go(item)} className="flex flex-col gap-1.5 text-left hover:text-accent-2">
          <span className="relative block w-full">{cover}</span>
          {title}
        </button>
      )}
      <span className="line-clamp-2 text-xs text-muted">{why(item)}</span>
      {!item.request?.mine && <Action item={item} onAsk={onAsk} />}
    </div>
  );
}
