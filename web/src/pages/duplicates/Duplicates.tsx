import { t } from "../../lib/i18n/core";
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { RefreshCw } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, Confirm, EmptyState, ErrorBox, Loading, Modal, PageHeader, Switch, Tabs } from "../../components/ui";
import { languageName, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { MetadataSearch } from "../series/MetadataSearch";
import { useSettingsDoc } from "../settings/useSettingsDoc";

type Finding = S["DuplicateFinding"];
type Side = S["DuplicateTitle"];
type Ref = { moduleId: number; provider: string; id: string };
type Tab = "review" | "metadata" | "primary" | "auto" | "dismissed";
type Primary = NonNullable<S["DuplicatesList"]["primary"]>;

const providerName = (p: string) => ({ anilist: "AniList", shikimori: "Shikimori", mal: "MyAnimeList", mangaupdates: "MangaUpdates", kitsu: "Kitsu", mangadex: "MangaDex" })[p] ?? p;

/** reason says in words why two titles look like one work. */
function reason(r: Finding["reasons"][number]): string {
  const p = r.params ?? {};
  switch (r.code) {
    case "id": return t("{provider} gives both the same id", { provider: providerName(p.provider ?? "") });
    case "mal": return t("MyAnimeList gives both the same id");
    case "name": return t("both are called “{name}”", { name: p.name ?? "" });
    case "author": return t("same author, {name}", { name: p.name ?? "" });
  }
  return r.code;
}

/** DuplicatesPage (Library check) lists titles that look like one work,
 * titles without metadata, and titles not on the primary provider. */
export function DuplicatesPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const [tab, setTab] = useState<Tab>("review");
  const { data, isLoading, error } = useQuery({
    queryKey: ["duplicates"],
    queryFn: () => unwrap(api.GET("/api/v1/duplicates")),
    refetchInterval: (q) => (q.state.data?.scanning ? 3000 : false),
  });
  const cfg = useSettingsDoc<S["Duplicates"]>("duplicates");
  const findings = useMemo(() => data?.findings ?? [], [data]);
  const lists = useMemo(() => ({
    review: findings.filter((f) => f.kind === "pair" && f.state === "open"),
    metadata: findings.filter((f) => f.kind === "metadata" && (f.state === "open" || f.state === "none")),
    primary: findings.filter((f) => f.kind === "primary" && (f.state === "open" || f.state === "none")),
    auto: findings.filter((f) => f.state === "auto" && (f.kind === "pair" || !findings.some((p) => p.kind === "pair" && p.state === "auto" && (p.seriesA === f.seriesA || p.seriesB === f.seriesA)))),
    dismissed: findings.filter((f) => f.state === "dismissed"),
  }), [findings]);
  const lastChecked = findings.reduce((max, f) => (f.checkedAt > max ? f.checkedAt : max), "");
  const refresh = () => qc.invalidateQueries({ queryKey: ["duplicates"] });
  const scan = async (seriesIds?: number[]) => {
    try {
      await unwrap(api.POST("/api/v1/duplicates/scan", { body: { seriesIds } }));
      toast.success(seriesIds ? t("Searching {count} titles again", { count: seriesIds.length }) : t("Looking for duplicates"));
      await refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <>
      <PageHeader
        title={t("Library check")}
        subtitle={data?.scanning ? t("Checking the library…") : lastChecked ? t("Duplicates, titles without metadata, and titles to move to the primary provider. Last checked {when}.", { when: relative(lastChecked) }) : t("Duplicates, titles without metadata, and titles to move to the primary provider.")}
        actions={
          <div className="flex flex-wrap items-center justify-end gap-3">
            {cfg.value && (
              <Switch
                checked={cfg.value.autoGroup}
                disabled={!!cfg.lock("autoGroup")}
                onChange={(v) => void cfg.save({ ...cfg.value!, autoGroup: v })}
                label={t("Group sure matches in other languages by itself")}
              />
            )}
            <Button icon={<RefreshCw className={clsx("size-4", data?.scanning && "animate-spin")} />} disabled={data?.scanning} onClick={() => scan()}>{t("Scan now")}</Button>
          </div>
        }
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <>
          <div className="overflow-x-auto [&_button]:shrink-0 [&_button]:whitespace-nowrap">
          <Tabs<Tab>
            value={tab}
            onChange={setTab}
            tabs={[
              { value: "review", label: `${t("Duplicates")} · ${lists.review.length}` },
              { value: "metadata", label: `${t("No metadata")} · ${lists.metadata.length}` },
              ...(data.primary ? [{ value: "primary" as Tab, label: `${t("Not on {provider}", { provider: data.primary.name })} · ${lists.primary.length}` }] : []),
              { value: "auto", label: `${t("Done by itself")} · ${lists.auto.length}` },
              { value: "dismissed", label: `${t("Not duplicates")} · ${lists.dismissed.length}` },
            ]}
          />
          </div>
          {tab === "review" && (lists.review.length ? lists.review.map((f) => <PairCard key={f.id} f={f} onDone={refresh} />) : (
            <EmptyState title={t("No duplicates to review")}>{t("Titles that look like the same work show up here after a scan.")}</EmptyState>
          ))}
          {tab === "metadata" && <MetadataList findings={lists.metadata} onDone={refresh} onSearchAgain={scan} />}
          {tab === "primary" && data.primary && <PrimaryList primary={data.primary} findings={lists.primary} onDone={refresh} onSearchAgain={scan} />}
          {tab === "auto" && <SettledList findings={lists.auto} onDone={refresh} />}
          {tab === "dismissed" && <SettledList findings={lists.dismissed} onDone={refresh} restore />}
        </>
      )}
    </>
  );
}

function SideBox({ s, keep }: { s: Side; keep?: boolean }) {
  return (
    <div className={clsx("flex min-w-0 gap-3 rounded-lg bg-panel-2 p-3", keep && "ring-1 ring-accent")}>
      <Cover src={apiUrl(s.coverUrl)} alt="" className="aspect-[2/3] w-16 shrink-0" />
      <div className="min-w-0 text-sm">
        <Link to={`/series/${s.id}`} className="block truncate font-medium hover:underline">{s.title}</Link>
        <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-xs text-muted">
          <dt>{t("Language")}</dt><dd className="text-fg">{s.language ? languageName(s.language) : "—"}</dd>
          <dt>{t("Chapters")}</dt><dd className="text-fg">{t("{count} · {files} on disk", { count: s.chapters, files: s.files })}</dd>
          {s.sources.length > 0 && <><dt>{t("Source")}</dt><dd className="truncate text-fg">{s.sources.join(", ")}</dd></>}
          <dt>{t("Added")}</dt><dd className="text-fg">{relative(s.addedAt)}</dd>
        </dl>
      </div>
    </div>
  );
}

function MatchLine({ m, onChange }: { m?: Finding["match"]; onChange?: () => void }) {
  if (!m) return null;
  return (
    <div className="mt-3 flex flex-wrap items-center gap-3 rounded-lg border border-border p-2.5">
      <Cover src={m.coverUrl} alt="" className="aspect-[2/3] w-8 shrink-0 rounded" />
      <div className="min-w-48 flex-1 text-sm">
        <div className="text-xs text-muted">{t("Metadata to link")}</div>
        <span className="font-medium">{m.title}</span>
        <span className="text-xs text-muted"> · {providerName(m.provider)}{m.year ? ` · ${m.year}` : ""}{m.authors?.length ? ` · ${m.authors.join(", ")}` : ""}</span>
      </div>
      {onChange && <Button size="sm" onClick={onChange}>{t("Change…")}</Button>}
    </div>
  );
}

function PairCard({ f, onDone }: { f: Finding; onDone: () => void }) {
  const toast = useToast();
  const [override, setOverride] = useState<(Ref & { title: string; coverUrl?: string; year?: number }) | null>(null);
  const [choosing, setChoosing] = useState(false);
  const [swapped, setSwapped] = useState(false);
  const [merging, setMerging] = useState(false);
  const b = f.b!;
  const keepA = (f.keepId === f.a.id) !== swapped;
  const [left, right] = keepA ? [f.a, b] : [b, f.a];
  const needsMetadata = !f.a.hasMetadata || !b.hasMetadata;
  const match = override ? { ...override, score: 1 } : f.match;
  const dismiss = async () => {
    try {
      await unwrap(api.PUT("/api/v1/duplicates/{id}/dismissed", { params: { path: { id: f.id } }, body: { dismissed: true } }));
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const group = async () => {
    try {
      await unwrap(api.POST("/api/v1/duplicates/{id}/group", { params: { path: { id: f.id } }, body: { match: override ? { moduleId: override.moduleId, provider: override.provider, id: override.id } : undefined } }));
      toast.success(t("Grouped as one title"));
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const merge = async () => {
    try {
      await unwrap(api.POST("/api/v1/duplicates/{id}/merge", { params: { path: { id: f.id } }, body: { keepId: left.id } }));
      toast.success(t("Merged into {title}", { title: left.title }));
      setMerging(false);
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <section className="mb-3 rounded-xl border border-border bg-panel p-4" aria-label={`${left.title} · ${right.title}`}>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm font-medium">{f.sameLanguage ? t("Same title, same language") : t("Same title, another language")}</span>
        <Badge tone={f.sure ? "ok" : "warn"}>{f.sure ? t("sure") : t("likely")}</Badge>
      </div>
      <div className="grid items-center gap-2 md:grid-cols-[1fr_auto_1fr]">
        <SideBox s={left} keep />
        <span className="justify-self-center text-lg text-muted" aria-hidden>{f.sameLanguage ? "←" : "+"}</span>
        <SideBox s={right} />
      </div>
      <p className="mt-3 text-xs text-muted">{t("Why: {reasons}.", { reasons: f.reasons.map(reason).join("; ") })}</p>
      {needsMetadata && <MatchLine m={match} onChange={() => setChoosing(true)} />}
      <div className="mt-4 flex flex-wrap justify-end gap-2">
        <Button variant="ghost" onClick={dismiss}>{t("Not duplicates")}</Button>
        {f.sameLanguage ? (
          <>
            <Button onClick={() => setSwapped(!swapped)}>{t("Swap which to keep")}</Button>
            <Button variant="primary" onClick={() => setMerging(true)}>{t("Merge into the left one")}</Button>
          </>
        ) : (
          <Button variant="primary" onClick={group}>{needsMetadata && match ? t("Group and link metadata") : t("Group as one title")}</Button>
        )}
      </div>
      <Confirm
        open={merging}
        title={t("Merge titles")}
        danger
        confirmLabel={t("Merge")}
        message={t("{other} is merged into {kept}: its sources move over, chapters {kept} has no file for move into its folder, and reading progress counts for {kept}. {other} is then deleted and its folder goes to the recycle bin.", { kept: left.title, other: right.title })}
        onConfirm={merge}
        onClose={() => setMerging(false)}
      />
      {choosing && (
        <Modal open size="lg" title={t("Metadata for {title}", { title: left.title })} onClose={() => setChoosing(false)}>
          <MetadataSearch
            initialQuery={f.match?.title || left.title}
            onPick={(c) => {
              setOverride({ moduleId: c.moduleId, provider: c.provider, id: c.id, title: c.title, coverUrl: c.coverUrl, year: c.year });
              setChoosing(false);
            }}
          />
        </Modal>
      )}
    </section>
  );
}

/** MetadataList is the titles without metadata, with batch linking. */
function MetadataList({ findings, onDone, onSearchAgain }: { findings: Finding[]; onDone: () => void; onSearchAgain: (ids: number[]) => Promise<void> }) {
  const toast = useToast();
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [searching, setSearching] = useState<Side | null>(null);
  const sorted = useMemo(() => [...findings].sort((x, y) => Number(!x.match) - Number(!y.match) || (y.match?.score ?? 0) - (x.match?.score ?? 0) || x.a.title.localeCompare(y.a.title)), [findings]);
  const chosen = sorted.filter((f) => picked.has(f.seriesA));
  const withMatch = chosen.filter((f) => f.match);
  const all = sorted.length > 0 && chosen.length === sorted.length;
  const toggle = (id: number) => setPicked((p) => {
    const next = new Set(p);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return next;
  });
  const link = async (ids: number[]) => {
    try {
      const r = await unwrap(api.POST("/api/v1/duplicates/link", { body: { seriesIds: ids } }));
      toast.success(t("Linked metadata to {count} titles", { count: r.linked }));
      setPicked(new Set());
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (!sorted.length) return <EmptyState title={t("Every title has metadata")}>{t("Titles no metadata provider is linked to show up here after a scan.")}</EmptyState>;
  return (
    <div>
      <div className="sticky top-0 z-10 mb-2 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-panel px-3 py-2 text-sm">
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-accent" checked={all} onChange={(e) => setPicked(new Set(e.target.checked ? sorted.map((f) => f.seriesA) : []))} />
          {chosen.length ? t("{count} selected", { count: chosen.length }) : t("Select all")}
        </label>
        {chosen.length > 0 && <span className="text-xs text-muted">{t("{count} have a match", { count: withMatch.length })}</span>}
        <span className="flex-1" />
        <Button size="sm" disabled={!chosen.length} icon={<RefreshCw className="size-3.5" />} onClick={() => onSearchAgain(chosen.map((f) => f.seriesA)).then(() => setPicked(new Set()))}>{t("Search selected again")}</Button>
        <Button size="sm" variant="primary" disabled={!withMatch.length} onClick={() => link(withMatch.map((f) => f.seriesA))}>{t("Link {count} matches", { count: withMatch.length })}</Button>
      </div>
      <ul className="divide-y divide-border rounded-lg border border-border bg-panel">
        {sorted.map((f) => (
          <li key={f.id} className="flex flex-wrap items-center gap-3 px-3 py-2.5">
            <input type="checkbox" aria-label={t("Select {title}", { title: f.a.title })} className="size-4 accent-accent" checked={picked.has(f.seriesA)} onChange={() => toggle(f.seriesA)} />
            <Cover src={apiUrl(f.a.coverUrl)} alt="" className="aspect-[2/3] w-10 shrink-0" />
            <div className="min-w-48 flex-1 text-sm">
              <Link to={`/series/${f.a.id}`} className="font-medium hover:underline">{f.a.title}</Link>
              <span className="ml-2 text-xs text-muted">{[f.a.language.toUpperCase(), ...f.a.sources, t("{count} ch.", { count: f.a.chapters })].filter(Boolean).join(" · ")}</span>
              <div className="mt-0.5 text-xs">
                {f.match ? (
                  <>
                    <span className="text-muted">{t("Best match:")} </span>
                    <span className="font-medium">{f.match.title}</span>
                    <span className="text-muted"> · {providerName(f.match.provider)}{f.match.year ? ` · ${f.match.year}` : ""} · {t("names match {percent}%", { percent: Math.round(f.match.score * 100) })}</span>
                  </>
                ) : (
                  <span className="text-muted">{t("No match at the providers")}</span>
                )}
              </div>
            </div>
            <Button size="sm" onClick={() => setSearching(f.a)}>{f.match ? t("Other…") : t("Search…")}</Button>
            {f.match && <Button size="sm" variant="primary" onClick={() => link([f.seriesA])}>{t("Link")}</Button>}
          </li>
        ))}
      </ul>
      {searching && (
        <Modal open size="lg" title={t("Metadata for {title}", { title: searching.title })} onClose={() => setSearching(null)}>
          <MetadataSearch
            initialQuery={searching.title}
            language={searching.language}
            onPick={async (c) => {
              try {
                await unwrap(api.PUT("/api/v1/series/{id}/metadata", { params: { path: { id: searching.id } }, body: { moduleId: c.moduleId, provider: c.provider, id: c.id } }));
                toast.success(t("Metadata linked"));
                setSearching(null);
                await unwrap(api.POST("/api/v1/duplicates/scan", { body: { seriesIds: [searching.id] } }));
                onDone();
              } catch (e) {
                toast.fromError(e);
              }
            }}
          />
        </Modal>
      )}
    </div>
  );
}

/** PrimaryList is the titles linked to lower providers only, with what the
 * primary provider has for them, to move them over in a batch. */
function PrimaryList({ primary, findings, onDone, onSearchAgain }: { primary: Primary; findings: Finding[]; onDone: () => void; onSearchAgain: (ids: number[]) => Promise<void> }) {
  const toast = useToast();
  const [picked, setPicked] = useState<Set<number>>(new Set());
  const [searching, setSearching] = useState<Side | null>(null);
  const sorted = useMemo(() => [...findings].sort((x, y) => Number(!x.match) - Number(!y.match) || Number(y.sure) - Number(x.sure) || (y.match?.score ?? 0) - (x.match?.score ?? 0) || x.a.title.localeCompare(y.a.title)), [findings]);
  const chosen = sorted.filter((f) => picked.has(f.seriesA));
  const withMatch = chosen.filter((f) => f.match);
  const all = sorted.length > 0 && chosen.length === sorted.length;
  const toggle = (id: number) => setPicked((p) => {
    const next = new Set(p);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    return next;
  });
  const move = async (ids: number[]) => {
    try {
      const r = await unwrap(api.POST("/api/v1/duplicates/link", { body: { seriesIds: ids, kind: "primary" } }));
      toast.success(t("Moved {count} titles to {provider}", { count: r.linked, provider: primary.name }));
      setPicked(new Set());
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (!sorted.length) return <EmptyState title={t("Every title is on {provider}", { provider: primary.name })}>{t("Titles linked to other providers only show up here after a scan.")}</EmptyState>;
  return (
    <div>
      <p className="mb-2 text-xs text-muted">{t("Titles linked to a lower provider only. The scan looked each one up at {provider} by its ids first, then by name.", { provider: primary.name })}</p>
      <div className="sticky top-0 z-10 mb-2 flex flex-wrap items-center gap-3 rounded-lg border border-border bg-panel px-3 py-2 text-sm">
        <label className="flex items-center gap-2">
          <input type="checkbox" className="size-4 accent-accent" checked={all} onChange={(e) => setPicked(new Set(e.target.checked ? sorted.map((f) => f.seriesA) : []))} />
          {chosen.length ? t("{count} selected", { count: chosen.length }) : t("Select all")}
        </label>
        {chosen.length > 0 && <span className="text-xs text-muted">{t("{count} have a {provider} match", { count: withMatch.length, provider: primary.name })}</span>}
        <span className="flex-1" />
        <Button size="sm" disabled={!chosen.length} icon={<RefreshCw className="size-3.5" />} onClick={() => onSearchAgain(chosen.map((f) => f.seriesA)).then(() => setPicked(new Set()))}>{t("Look up selected again")}</Button>
        <Button size="sm" variant="primary" disabled={!withMatch.length} onClick={() => move(withMatch.map((f) => f.seriesA))}>{t("Move {count} to {provider}", { count: withMatch.length, provider: primary.name })}</Button>
      </div>
      <ul className="divide-y divide-border rounded-lg border border-border bg-panel">
        {sorted.map((f) => (
          <li key={f.id} className="flex flex-wrap items-center gap-3 px-3 py-2.5">
            <input type="checkbox" aria-label={t("Select {title}", { title: f.a.title })} className="size-4 accent-accent" checked={picked.has(f.seriesA)} onChange={() => toggle(f.seriesA)} />
            <Cover src={apiUrl(f.a.coverUrl)} alt="" className="aspect-[2/3] w-10 shrink-0" />
            <div className="min-w-48 flex-1 text-sm">
              <Link to={`/series/${f.a.id}`} className="font-medium hover:underline">{f.a.title}</Link>
              {f.a.language && <span className="ml-2 text-xs text-muted">{f.a.language.toUpperCase()}</span>}
              <div className="text-xs text-muted">{t("Now: {providers}", { providers: (f.a.providers ?? []).filter((p) => p !== "mal").map(providerName).join(" · ") || "—" })}</div>
              <div className="mt-0.5 text-xs">
                {f.match ? (
                  <>
                    <span className="text-muted">{primary.name}: </span>
                    <span className="font-medium">{f.match.title}</span>
                    {f.match.year ? <span className="text-muted"> · {f.match.year}</span> : null}{" "}
                    <Badge tone={f.match.how === "id" ? "ok" : "warn"}>
                      {f.match.how === "id" ? t("same {provider} id", { provider: providerName(f.match.by ?? "") }) : t("by name, {percent}%", { percent: Math.round(f.match.score * 100) })}
                    </Badge>
                  </>
                ) : (
                  <span className="text-muted">{t("{provider} has no entry for it", { provider: primary.name })}</span>
                )}
              </div>
            </div>
            <Button size="sm" onClick={() => setSearching(f.a)}>{f.match ? t("Other…") : t("Search {provider}…", { provider: primary.name })}</Button>
            {f.match && <Button size="sm" variant="primary" onClick={() => move([f.seriesA])}>{t("Move")}</Button>}
          </li>
        ))}
      </ul>
      {searching && (
        <Modal open size="lg" title={t("{provider} for {title}", { provider: primary.name, title: searching.title })} onClose={() => setSearching(null)}>
          <MetadataSearch
            initialQuery={searching.title}
            language={searching.language}
            moduleId={primary.moduleId}
            onPick={async (c) => {
              try {
                await unwrap(api.POST("/api/v1/series/{id}/metadata/refs", { params: { path: { id: searching.id } }, body: { moduleId: c.moduleId, provider: c.provider, id: c.id } }));
                toast.success(t("Moved to {provider}", { provider: primary.name }));
                setSearching(null);
                await unwrap(api.POST("/api/v1/duplicates/scan", { body: { seriesIds: [searching.id] } }));
                onDone();
              } catch (e) {
                toast.fromError(e);
              }
            }}
          />
        </Modal>
      )}
    </div>
  );
}

/** SettledList is what was grouped or linked by itself, or marked not duplicates. */
function SettledList({ findings, onDone, restore }: { findings: Finding[]; onDone: () => void; restore?: boolean }) {
  const toast = useToast();
  if (!findings.length) return <EmptyState title={restore ? t("Nothing marked as not duplicates") : t("Nothing done by itself yet")} />;
  const bringBack = async (id: number) => {
    try {
      await unwrap(api.PUT("/api/v1/duplicates/{id}/dismissed", { params: { path: { id } }, body: { dismissed: false } }));
      onDone();
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <ul className="divide-y divide-border rounded-lg border border-border bg-panel">
      {findings.map((f) => (
        <li key={f.id} className="flex flex-wrap items-center gap-3 px-3 py-2.5 text-sm">
          <Cover src={apiUrl(f.a.coverUrl)} alt="" className="aspect-[2/3] w-8 shrink-0 rounded" />
          <div className="min-w-48 flex-1">
            <Link to={`/series/${f.a.id}`} className="font-medium hover:underline">{f.a.title}</Link>
            {f.b && <> <span className="text-muted">+</span> <Link to={`/series/${f.b.id}`} className="font-medium hover:underline">{f.b.title}</Link></>}
            <div className="text-xs text-muted">
              {f.kind === "pair" ? (restore ? t("Kept apart") : t("Grouped as one title")) : f.match ? t("Linked to {title}", { title: f.match.title }) : t("No metadata")}
              {" · "}{relative(f.updatedAt)}
            </div>
          </div>
          {restore && <Button size="sm" onClick={() => bringBack(f.id)}>{t("Bring back")}</Button>}
        </li>
      ))}
    </ul>
  );
}
