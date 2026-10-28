import { t } from "../../lib/i18n/core";
import { useMemo, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { api, unwrap, type S, type Series } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, ErrorBox, Loading, Modal, Tabs } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { MetadataSearch } from "./MetadataSearch";

type Option = S["MetadataOption"];
type Picks = S["MetadataPicks"];
type Field = "coverUrl" | "description" | "genres" | "title" | "status" | "year";
/** Pick is a provider's value for a field, or "auto" to hand it back. */
type Pick = { provider: string; value: unknown } | "auto";
export type MetadataTab = "match" | "pick";

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null);

/** MetadataModal changes a title's metadata match, or picks its cover and
 * info field by field from the providers it is linked to. */
export function MetadataModal({ series, tab: initialTab = "pick", onClose }: { series: Series; tab?: MetadataTab; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [tab, setTab] = useState<MetadataTab>(initialTab);
  const [picks, setPicks] = useState<Partial<Record<Field, Pick>>>({});
  const [saving, setSaving] = useState(false);
  const { data, isLoading, error } = useQuery({
    queryKey: ["series", series.id, "metadata-options"],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/metadata/options", { params: { path: { id: series.id } } })),
    enabled: tab === "pick",
    staleTime: 60_000,
  });
  const options = useMemo(() => (data?.options ?? []).filter((o) => !o.error), [data]);
  const md = series.metadata;
  const locks = md.locks ?? [];
  const prov = md.provenance ?? {};

  const send = async (body: Picks, done: string) => {
    setSaving(true);
    try {
      await unwrap(api.PUT("/api/v1/series/{id}/metadata/picks", { params: { path: { id: series.id } }, body }));
      qc.invalidateQueries({ queryKey: ["series"] });
      toast.success(done);
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const save = () => {
    const body: Picks = { from: {}, automatic: [] };
    for (const [field, p] of Object.entries(picks) as [Field, Pick][]) {
      if (p === "auto") {
        body.automatic!.push(...(field === "genres" ? (["genres", "tags"] as const) : ([field] as const)));
        continue;
      }
      if (field === "genres") {
        const v = p.value as { genres: string[]; tags: string[] };
        body.genres = v.genres;
        body.tags = v.tags;
        body.from!.genres = p.provider;
        body.from!.tags = p.provider;
      } else {
        (body as Record<string, unknown>)[field] = p.value;
        body.from![field] = p.provider;
      }
    }
    void send(body, t("Metadata saved"));
  };
  const automatic = () => void send({ automatic: ["title", "description", "coverUrl", "status", "year", "genres", "tags"] }, t("The providers fill the info again"));

  if (tab === "match") {
    return (
      <Modal open onClose={onClose} title={t("Metadata · {title}", { title: series.title })} size="lg">
        <Tabs<MetadataTab> value={tab} onChange={setTab} tabs={[{ value: "match", label: t("Match") }, { value: "pick", label: t("Pick info") }]} />
        <MetadataSearch
          initialQuery={series.title}
          language={series.language}
          onPick={async (c) => {
            try {
              await unwrap(api.PUT("/api/v1/series/{id}/metadata", { params: { path: { id: series.id } }, body: { moduleId: c.moduleId, provider: c.provider, id: c.id } }));
              qc.invalidateQueries({ queryKey: ["series"] });
              toast.success(t("Metadata updated"));
              onClose();
            } catch (e) {
              toast.fromError(e);
            }
          }}
        />
      </Modal>
    );
  }

  /** state says where a field's value comes from now. */
  const state = (field: string) => {
    const from = options.find((o) => o.provider === prov[field])?.moduleName ?? prov[field];
    if (locks.includes(field)) return <span className="text-xs text-warn">{from ? t("you picked {provider}", { provider: from }) : t("you set it")}</span>;
    return from ? <span className="text-xs text-muted">{t("now from {provider}", { provider: from })}</span> : null;
  };
  const chosen = (field: Field, provider: string, value: unknown, current: unknown) => {
    const p = picks[field];
    if (p === "auto") return false;
    if (p) return p.provider === provider && same(p.value, value);
    return same(value, current);
  };
  const choose = (field: Field, provider: string, value: unknown) => setPicks((ps) => ({ ...ps, [field]: { provider, value } }));

  const covers = options.flatMap((o) => (o.covers?.length ? o.covers.map((c) => ({ o, url: c.url, thumb: c.thumb, label: c.label ? `${o.moduleName} · ${c.label}` : o.moduleName })) : o.coverUrl ? [{ o, url: o.coverUrl, thumb: undefined, label: o.moduleName }] : []));
  const scalar = (field: "title" | "status" | "year", get: (o: Option) => string | number | undefined, current: string | number | undefined, label: string) => {
    const values = options.filter((o) => get(o));
    if (!values.length) return null;
    const agree = values.every((o) => same(get(o), get(values[0])));
    return (
      <FieldBox key={field} title={label} state={state(field)} folded={agree}>
        <div className="flex flex-col gap-2">
          {values.map((o) => (
            <Choice key={o.moduleId} on={chosen(field, o.provider, get(o), current)} onClick={() => choose(field, o.provider, get(o))}>
              <Badge tone="accent">{o.moduleName}</Badge> <span className="text-sm">{String(get(o))}</span>
            </Choice>
          ))}
        </div>
      </FieldBox>
    );
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("Metadata · {title}", { title: series.title })}
      size="lg"
      footer={
        <>
          <Button variant="ghost" className="mr-auto" disabled={saving} onClick={automatic}>{t("Back to automatic")}</Button>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!Object.keys(picks).length} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <Tabs<MetadataTab> value={tab} onChange={setTab} tabs={[{ value: "match", label: t("Match") }, { value: "pick", label: t("Pick info") }]} />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && !options.length && <p className="text-sm text-muted">{t("No metadata provider is linked to this title. Find its match on the Match tab first.")}</p>}
      {options.length > 0 && (
        <>
          <p className="text-xs text-muted">
            {t("What each linked provider says. Pick one per field; a picked field keeps your choice when the title refreshes. Linked: {providers}.", { providers: options.map((o) => o.moduleName).join(", ") })}
          </p>
          {data?.options.filter((o) => o.error).map((o) => <p key={o.moduleId} className="mt-1 text-xs text-warn">{o.moduleName}: {o.error}</p>)}
          {covers.length > 0 && (
            <FieldBox title={t("Cover")} state={state("coverUrl")}>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(6.5rem,1fr))] gap-2.5">
                {covers.map((c) => (
                  <button
                    key={c.url}
                    type="button"
                    title={c.label}
                    aria-pressed={chosen("coverUrl", c.o.provider, c.url, md.coverUrl)}
                    onClick={() => choose("coverUrl", c.o.provider, c.url)}
                    className={clsx("flex min-w-0 flex-col gap-1.5 rounded-lg border p-1.5 text-left", chosen("coverUrl", c.o.provider, c.url, md.coverUrl) ? "border-accent ring-2 ring-accent" : "border-border hover:border-accent")}
                  >
                    <Cover src={c.thumb || c.url} alt="" className="aspect-[2/3] w-full rounded" />
                    <span className="truncate text-xs">{c.label}</span>
                  </button>
                ))}
              </div>
            </FieldBox>
          )}
          {options.some((o) => o.description) && (
            <FieldBox title={t("Description")} state={state("description")} folded={options.filter((o) => o.description).length < 2}>
              <div className="flex flex-col gap-2">
                {options.filter((o) => o.description).map((o) => (
                  <Choice key={o.moduleId} on={chosen("description", o.provider, o.description, md.description)} onClick={() => choose("description", o.provider, o.description)}>
                    <Badge tone="accent">{o.moduleName}</Badge>
                    <p className="mt-1 line-clamp-4 whitespace-pre-line text-sm">{o.description}</p>
                  </Choice>
                ))}
              </div>
            </FieldBox>
          )}
          {options.some((o) => o.genres?.length || o.tags?.length) && (
            <FieldBox title={t("Genres and tags")} state={state("genres")}>
              <div className="flex flex-col gap-2">
                {options.filter((o) => o.genres?.length || o.tags?.length).map((o) => {
                  const v = { genres: o.genres ?? [], tags: o.tags ?? [] };
                  const p = picks.genres;
                  const on = p && p !== "auto" ? p.provider === o.provider : !p && locks.includes("genres") && prov.genres === o.provider;
                  return (
                    <Choice key={o.moduleId} on={!!on} onClick={() => choose("genres", o.provider, v)}>
                      <Badge tone="accent">{o.moduleName}</Badge>
                      <div className="mt-1.5 flex flex-wrap gap-1">
                        {[...v.genres, ...v.tags].slice(0, 14).map((g) => <Badge key={g}>{g}</Badge>)}
                      </div>
                    </Choice>
                  );
                })}
                <Choice on={picks.genres === "auto" || (!picks.genres && !locks.includes("genres"))} onClick={() => setPicks((ps) => ({ ...ps, genres: "auto" }))}>
                  <span className="text-sm font-medium">{t("All of them together")}</span>
                </Choice>
              </div>
            </FieldBox>
          )}
          {scalar("title", (o) => o.title, series.title, t("Title"))}
          {scalar("status", (o) => o.status, series.status, t("Status"))}
          {scalar("year", (o) => o.year, md.year, t("Year"))}
        </>
      )}
    </Modal>
  );
}

/** FieldBox is one field to pick, folded when the providers agree. */
function FieldBox({ title, state, folded, children }: { title: string; state?: ReactNode; folded?: boolean; children: ReactNode }) {
  const head = (
    <span className="flex flex-wrap items-baseline justify-between gap-2">
      <span className="font-semibold">{title}</span>
      {state}
    </span>
  );
  if (folded) {
    return (
      <details className="mt-3 border-t border-border pt-3">
        <summary className="cursor-pointer list-none">{head}</summary>
        <div className="mt-2">{children}</div>
      </details>
    );
  }
  return (
    <section className="mt-3 border-t border-border pt-3" aria-label={title}>
      <div className="mb-2">{head}</div>
      {children}
    </section>
  );
}

function Choice({ on, onClick, children }: { on: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={onClick}
      className={clsx("flex w-full items-start gap-2.5 rounded-lg border p-2.5 text-left", on ? "border-accent ring-1 ring-accent" : "border-border hover:border-accent")}
    >
      <span className={clsx("mt-0.5 size-4 shrink-0 rounded-full border-2", on ? "border-[5px] border-accent" : "border-border")} aria-hidden />
      <span className="min-w-0 flex-1">{children}</span>
    </button>
  );
}
