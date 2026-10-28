import { getLocale, t as tr, t } from "../../lib/i18n/core";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { ExternalLink, Search } from "lucide-react";
import { api, unwrap, type LookupResult } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, ErrorBox, Input, Loading } from "../../components/ui";
import { useAccount } from "../../lib/account";

/** isNovel: formats no manga source has chapters of. */
export const isNovel = (format?: string) => /novel/i.test(format ?? "");

/** MetadataSearch looks up series across metadata modules. */
export function MetadataSearch({
  initialQuery = "",
  query: controlled,
  setQuery: setControlled,
  onPick,
  action,
  placeholder = "Search by title (AniList and other metadata modules)",
  language = "",
  hideInput = false,
  hideErrors = false,
  moduleId: fixedModule,
}: {
  initialQuery?: string;
  query?: string;
  setQuery?: (q: string) => void;
  onPick?: (c: LookupResult) => void;
  /** action replaces the Select button (e.g. Request). */
  action?: (c: LookupResult) => ReactNode;
  placeholder?: string;
  language?: string;
  /** hideInput: the query comes from a search box elsewhere on the page. */
  hideInput?: boolean;
  /** hideErrors: no provider errors or "nothing found" notes. */
  hideErrors?: boolean;
  /** moduleId searches just that provider, with no chips to switch. */
  moduleId?: number;
}) {
  const { isAdmin } = useAccount();
  const [local, setLocal] = useState(initialQuery);
  const query = controlled ?? local;
  const setQuery = setControlled ?? setLocal;
  const [draft, setDraft] = useState(query);
  useEffect(() => setDraft(query), [query]);
  // picked: a provider chosen with the chips; without one, the first by
  // priority that has results answers
  const [picked, setPicked] = useState<number | undefined>(undefined);
  useEffect(() => setPicked(undefined), [query]);
  const moduleId = fixedModule ?? picked;
  const { data, isLoading, error } = useQuery({
    // without a language, results are titled in the interface one
    queryKey: ["lookup", query, language || getLocale(), moduleId ?? 0],
    queryFn: () => unwrap(api.GET("/api/v1/series/lookup", { params: { query: { q: query, lang: language || undefined, moduleId } } })),
    enabled: query.length > 0,
    staleTime: 5 * 60_000,
  });
  const modules = data?.modules ?? [];
  const shown = modules.find((m) => m.id === data?.moduleId);
  return (
    <div>
      {!hideInput && <form
        className="mb-4 flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery(draft.trim());
        }}
      >
        <Input autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={placeholder} />
        <Button type="submit" variant="primary" icon={<Search className="size-4" />}>{t("Search")}</Button>
      </form>}
      {!fixedModule && modules.length > 1 && query && (
        <div className="mb-3 flex flex-wrap gap-1.5" role="group" aria-label={tr("Metadata provider")}>
          {modules.map((m) => {
            const on = m.id === (moduleId ?? data?.moduleId);
            return (
              <button
                key={m.id}
                type="button"
                aria-pressed={on}
                onClick={() => setPicked(m.id)}
                className={clsx("rounded-full border px-3 py-1 text-xs", on ? "border-accent bg-panel-2 text-fg" : "border-border text-muted hover:text-fg")}
              >
                {m.name}
                {on && data ? <span className="text-muted"> · {data.results.length}</span> : null}
              </button>
            );
          })}
        </div>
      )}
      {!hideErrors && !picked && shown && (data?.skipped?.length ?? 0) > 0 && (
        <p className="mb-2 text-xs text-muted">{tr("Nothing on {skipped}, showing {provider}", { skipped: data!.skipped.join(", "), provider: shown.name })}</p>
      )}
      {isLoading && <Loading />}
      {error && !hideErrors && <ErrorBox error={error} />}
      {!hideErrors && data?.errors?.map((e) => (
        <p key={e} className="mb-2 text-xs text-warn">
          {e}
        </p>
      ))}
      <div className="flex flex-col gap-2">
        {data?.results?.map((r) => (
          <div key={r.provider + r.id} className={clsx("flex flex-wrap gap-3 rounded-lg border border-border bg-panel p-3 sm:flex-nowrap", isNovel(r.format) && "opacity-60")}>
            <Cover src={r.coverUrl} alt={r.title} className="aspect-[2/3] w-16 shrink-0" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <span className="line-clamp-2 font-medium [overflow-wrap:anywhere]" title={r.title}>{r.title}</span>
                {r.year ? <Badge>{r.year}</Badge> : null}
                {r.format && <Badge tone={isNovel(r.format) ? "warn" : "default"} title={isNovel(r.format) ? tr("Manga sources have no chapters of novels") : undefined}>{r.format}</Badge>}
                {r.status && <Badge tone="info">{r.status}</Badge>}
                <Badge tone="accent">{r.moduleName}</Badge>
                {r.also?.map((a) => (
                  <Badge key={a.provider}>{a.provider}</Badge>
                ))}
              </div>
              {r.altTitles && <p className="mt-0.5 line-clamp-1 text-xs text-muted">{r.altTitles.slice(0, 3).join(" · ")}</p>}
              {r.description && <p className="mt-1 line-clamp-2 text-sm text-fg/80">{r.description}</p>}
              {r.url && (
                <a href={r.url} target="_blank" rel="noopener noreferrer" className="mt-1 inline-flex items-center gap-1 text-xs text-accent-2 hover:underline">
                  {tr("Open on {provider}", { provider: r.moduleName || r.provider })}
                  <ExternalLink className="size-3" />
                </a>
              )}
            </div>
            {/* on phones the buttons go under the text, lined up with it */}
            <div className="ml-[4.75rem] basis-full sm:ml-0 sm:basis-auto sm:shrink-0 sm:self-center">
              {action ? (
                action(r)
              ) : r.existingSeriesId ? (
                <Link to={`/series/${r.existingSeriesId}`}>
                  <Button size="sm">{t("In library")}</Button>
                </Link>
              ) : (
                <Button size="sm" variant="primary" onClick={() => onPick?.(r)}>{t("Select")}</Button>
              )}
            </div>
          </div>
        ))}
        {!hideErrors && data && data.results.length === 0 && data.providers > 0 && <p className="text-sm text-muted">{t("No metadata found.")}</p>}
        {!hideErrors && data && data.providers === 0 && (
          <p className="text-sm text-warn">
            {t("No metadata provider is set up, so nothing was searched.")}{" "}
            {isAdmin ? <Link to="/settings/metadata" className="text-accent-2 hover:underline">{t("Add one in Settings → Metadata")}</Link> : t("Ask an admin to add one.")}
          </p>
        )}
      </div>
    </div>
  );
}
