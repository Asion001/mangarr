import { t as tr, t } from "../../lib/i18n/core";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Check, Download, Library, Play, RefreshCw, Search, Tags } from "lucide-react";
import { api, apiUrl, unwrap, type S } from "../../api/client";
import { useProfiles, useReaders, useRootFolders } from "../../api/queries";
import { Cover } from "../../components/Cover";
import { Badge, Button, Card, Confirm, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Spinner, Switch, Table, Td, Th } from "../../components/ui";
import { date, languageName } from "../../lib/format";
import { useListParam, useQueryParam } from "../../lib/urlState";
import { useToast } from "../../lib/toast";
import { MetadataSearch } from "../series/AddSeries";
import { SourceSearchModal } from "../series/SourceSearch";
import { formatLabel, statusLabel, statusTone } from "./Imports";

type Entry = S["ImportEntryView"];
type Options = S["ImportOptions"];
type Patch = S["ImportEntriesPatch"];

const PAGE = 100;

const states = [
  { value: "", label: "All" },
  { value: "ready", label: "Ready" },
  { value: "review", label: "Needs review" },
  { value: "extension", label: "Needs extension" },
  { value: "library", label: "In library" },
  { value: "imported", label: "Imported" },
  { value: "failed", label: "Failed" },
] as const;

const stateTone = (s: string) =>
  s === "ready" || s === "imported" ? "ok" : s === "review" ? "warn" : s === "extension" ? "info" : s === "failed" ? "err" : s === "library" ? "accent" : "default";

const stateLabel = (s: string) => states.find((x) => x.value === s)?.label ?? s;

function howLabel(how: string, score?: number) {
  switch (how) {
    case "exact":
      return "same source";
    case "rule":
      return "converted link";
    case "path":
      return "link checked";
    case "title":
      return `title ${Math.round((score ?? 0) * 100)}%`;
    case "tracker":
      return "tracker id";
    case "lookup":
      return "tracker id (converted)";
    case "manual":
      return "picked";
  }
  return how;
}

/** ImportDetailPage reviews how a backup's manga were matched and runs the import. */
export function ImportDetailPage() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const toast = useToast();
  const [state, setState] = useListParam("state");
  const [q, setQ] = useQueryParam("q");
  const [pageStr, setPage] = useListParam("page", "1");
  const page = Number(pageStr) || 1;
  const imp = useQuery({ queryKey: ["import", id], queryFn: () => unwrap(api.GET("/api/v1/imports/{id}", { params: { path: { id } } })) });
  const entries = useQuery({
    queryKey: ["import-entries", id, state, q, page],
    queryFn: () =>
      unwrap(
        api.GET("/api/v1/imports/{id}/entries", {
          params: { path: { id }, query: { state: (state || undefined) as never, q: q || undefined, page, pageSize: PAGE } },
        }),
      ),
    placeholderData: keepPreviousData,
  });
  const [pickSource, setPickSource] = useState<Entry | null>(null);
  const [pickMeta, setPickMeta] = useState<Entry | null>(null);
  const [confirmRun, setConfirmRun] = useState(false);
  const busy = imp.data?.busy || imp.data?.status === "mapping" || imp.data?.status === "running";

  // poll while matching or importing (progress lines aren't pushed as events)
  useEffect(() => {
    if (!busy) return;
    const t = window.setInterval(() => {
      qc.invalidateQueries({ queryKey: ["import", id] });
      qc.invalidateQueries({ queryKey: ["import-entries", id] });
    }, 2000);
    return () => window.clearInterval(t);
  }, [busy, id, qc]);

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["import", id] });
    qc.invalidateQueries({ queryKey: ["import-entries", id] });
    qc.invalidateQueries({ queryKey: ["imports"] });
  };
  const patch = async (body: Patch) => {
    try {
      await unwrap(api.PATCH("/api/v1/imports/{id}/entries", { params: { path: { id } }, body }));
      refresh();
    } catch (e) {
      toast.fromError(e, tr("Update failed"));
    }
  };
  const command = async (path: "remap" | "install-extensions" | "run", label: string) => {
    try {
      if (path === "remap") await unwrap(api.POST("/api/v1/imports/{id}/remap", { params: { path: { id } }, body: {} }));
      if (path === "install-extensions") await unwrap(api.POST("/api/v1/imports/{id}/install-extensions", { params: { path: { id } } }));
      if (path === "run") await unwrap(api.POST("/api/v1/imports/{id}/run", { params: { path: { id } } }));
      toast.info(label);
      window.setTimeout(refresh, 300);
    } catch (e) {
      toast.fromError(e, tr("Couldn't start"));
    }
  };

  if (imp.isLoading) return <Loading />;
  if (imp.error) return <ErrorBox error={imp.error} />;
  const d = imp.data!;
  const counts = d.counts;
  const selectedReady = (counts["selected:ready"] ?? 0) + (counts["selected:library"] ?? 0) + (counts["selected:failed"] ?? 0);
  const items = entries.data?.items ?? [];
  const total = entries.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / PAGE));
  const filter = { state: (state || undefined) as never, q: q || undefined };

  return (
    <>
      <Link to="/import" className="mb-2 inline-flex items-center gap-1 text-xs text-muted hover:text-fg">
        <ArrowLeft className="size-3.5" />{" " + t("Imports")}</Link>
      <PageHeader
        title={d.fileName}
        subtitle={
          <span className="flex flex-wrap items-center gap-2">
            {formatLabel(d.format)}
            {d.info.backupDate && <span>{t("· made") + " "}{date(d.info.backupDate)}</span>}
            <span>· {d.info.entries}{" " + t("manga")}</span>
            <Badge tone={statusTone(d.status)}>{statusLabel(d.status)}</Badge>
            {busy && <Spinner className="size-3.5" />}
            {d.progress && <span className="text-xs">{d.progress}</span>}
          </span>
        }
        actions={
          <>
            <Button icon={<RefreshCw className="size-4" />} disabled={busy} onClick={() => command("remap", "Matching again")}>{t("Match again")}</Button>
            {(counts.extension ?? 0) > 0 && (
              <Button icon={<Download className="size-4" />} disabled={busy} onClick={() => command("install-extensions", "Installing extensions")}>{t("Install extensions (")}{counts.extension})
              </Button>
            )}
            <Button variant="primary" icon={<Play className="size-4" />} disabled={busy || selectedReady === 0} onClick={() => setConfirmRun(true)}>{t("Import") + " "}{selectedReady}{" " + t("selected")}</Button>
          </>
        }
      />
      {d.error && (
        <div className="mb-4 rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">{d.error}</div>
      )}

      <OptionsCard imp={d} disabled={busy} onSaved={refresh} />

      <div className="mb-3 mt-5 flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1">
          {states.map((s) => (
            <Button key={s.value} size="sm" variant={state === s.value ? "primary" : "secondary"} onClick={() => (setState(s.value), setPage("1"))}>
              {s.label} <span className="opacity-70">{s.value ? (counts[s.value] ?? 0) : (counts.all ?? 0)}</span>
            </Button>
          ))}
        </div>
        <div className="relative ml-auto w-full max-w-xs">
          <Search className="absolute left-2.5 top-2.5 size-4 text-muted" />
          <Input className="pl-8" placeholder={t("Filter titles…")} defaultValue={q} onChange={(e) => (setQ(e.target.value), setPage("1"))} />
        </div>
      </div>

      <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-muted">
        <span>
          {counts.selected ?? 0}{" " + t("of") + " "}{counts.all ?? 0}{" " + t("selected")}</span>
        <Button size="sm" disabled={busy} onClick={() => patch({ filter, selected: true })}>{t("Select all") + " "}{state ? stateLabel(state).toLowerCase() : ""} ({total})
        </Button>
        <Button size="sm" disabled={busy} onClick={() => patch({ filter, selected: false })}>{t("Deselect them")}</Button>
        {(counts.review ?? 0) > 0 && (
          <Button size="sm" icon={<Check className="size-3.5" />} disabled={busy} onClick={() => patch({ filter: { state: "review" }, accept: true })}>{t("Accept all suggestions")}</Button>
        )}
      </div>

      {entries.error && <ErrorBox error={entries.error} />}
      <Table>
        <thead>
          <tr>
            <Th className="w-8" />
            <Th>{t("Manga in the backup")}</Th>
            <Th>{t("Source")}</Th>
            <Th>{t("Metadata")}</Th>
            <Th>{t("Status")}</Th>
          </tr>
        </thead>
        <tbody>
          {items.map((e) => (
            <EntryRow
              key={e.id}
              e={e}
              disabled={busy || e.state === "imported"}
              onSelect={(v) => patch({ ids: [e.id], selected: v })}
              onAccept={() => patch({ ids: [e.id], accept: true })}
              onPickSource={() => setPickSource(e)}
              onPickMeta={() => setPickMeta(e)}
            />
          ))}
          {items.length === 0 && !entries.isLoading && (
            <tr>
              <Td colSpan={5} className="py-6 text-center text-muted">{t("Nothing here.")}</Td>
            </tr>
          )}
        </tbody>
      </Table>
      {pages > 1 && (
        <div className="mt-3 flex items-center justify-center gap-2 text-sm">
          <Button size="sm" disabled={page <= 1} onClick={() => setPage(String(page - 1))}>{t("Previous")}</Button>
          <span className="text-muted">
            {page} / {pages}
          </span>
          <Button size="sm" disabled={page >= pages} onClick={() => setPage(String(page + 1))}>{t("Next")}</Button>
        </div>
      )}

      {pickSource && (
        <SourceSearchModal
          title={`Source for ${pickSource.title}`}
          initialQuery={pickSource.title}
          onClose={() => setPickSource(null)}
          onPick={(m, g) => {
            patch({
              ids: [pickSource.id],
              source: { moduleId: g.moduleId, sourceId: g.sourceId, sourceName: g.sourceName, lang: g.lang, url: m.url, title: m.title, thumbnailUrl: m.thumbnailUrl, how: "manual" },
            });
            setPickSource(null);
          }}
        />
      )}
      {pickMeta && (
        <Modal open title={`Metadata for ${pickMeta.title}`} size="lg" onClose={() => setPickMeta(null)}>
          <MetadataSearch
            initialQuery={pickMeta.title}
            onPick={(c) => {
              patch({ ids: [pickMeta.id], metadata: { moduleId: c.moduleId, provider: c.provider, id: c.id, title: c.title, coverUrl: c.coverUrl, how: "manual" } });
              setPickMeta(null);
            }}
          />
          {pickMeta.metadata && (
            <Button
              className="mt-3"
              size="sm"
              onClick={() => {
                patch({ ids: [pickMeta.id], clearMetadata: true });
                setPickMeta(null);
              }}
            >{t("Add without metadata")}</Button>
          )}
        </Modal>
      )}
      <Confirm
        open={confirmRun}
        title={`Import ${selectedReady} manga?`}
        message={
          <div className="flex flex-col gap-2 text-sm">
            <p>
              {counts["selected:ready"] ?? 0}{" " + t("series are added,") + " "}{counts["selected:library"] ?? 0}{" " + t("merge into series you already have.")}{(counts["selected:failed"] ?? 0) > 0 && ` ${counts["selected:failed"]} that failed before are retried.`}
            </p>
            <p className="text-muted">{t("Each series' chapters are fetched from its source while importing (with the usual request throttling), so large libraries take a while.")}</p>
          </div>
        }
        confirmLabel={t("Import")}
        onClose={() => setConfirmRun(false)}
        onConfirm={() => {
          setConfirmRun(false);
          command("run", "Import started");
        }}
      />
    </>
  );
}

function EntryRow({
  e,
  disabled,
  onSelect,
  onAccept,
  onPickSource,
  onPickMeta,
}: {
  e: Entry;
  disabled: boolean;
  onSelect: (v: boolean) => void;
  onAccept: () => void;
  onPickSource: () => void;
  onPickMeta: () => void;
}) {
  const src = e.source;
  // proxied (and resized) by the server: backup links are never hotlinked
  const thumb = apiUrl(`api/v1/imports/${e.importId}/entries/${e.id}/cover`, { v: src ? `${src.sourceId}:${src.url}` : "" });
  return (
    <tr className={`hover:bg-panel-2/60 ${e.selected ? "" : "opacity-70"}`}>
      <Td className="w-8">
        <input type="checkbox" aria-label={`Import ${e.title}`} checked={e.selected} disabled={disabled} onChange={(ev) => onSelect(ev.target.checked)} />
      </Td>
      <Td>
        <div className="flex items-start gap-3">
          <Cover src={thumb} alt={e.title} className="aspect-[2/3] w-10 shrink-0" />
          <div className="min-w-0">
            <div className="font-medium">{e.title}</div>
            <div className="mt-0.5 flex flex-wrap items-center gap-1 text-xs text-muted">
              <span>{e.data.sourceName || e.data.sourceId}</span>
              {e.chapterCount > 0 && (
                <span>
                  · {e.readCount}/{e.chapterCount}{" " + t("read")}</span>
              )}
              {!e.data.favorite && <Badge>{t("history only")}</Badge>}
              {(e.data.categories ?? []).map((c) => (
                <Badge key={c}>
                  <Tags className="mr-1 inline size-3" />
                  {c}
                </Badge>
              ))}
            </div>
          </div>
        </div>
      </Td>
      <Td>
        {src ? (
          <div className="text-xs">
            <div className="font-medium text-fg">{src.sourceName}</div>
            {src.title && src.title !== e.title && <div className="text-muted">“{src.title}”</div>}
            <Badge tone={src.how === "title" && (src.score ?? 0) < 0.88 ? "warn" : "default"}>{howLabel(src.how, src.score)}</Badge>
          </div>
        ) : e.extension ? (
          <div className="text-xs text-muted">
            {e.extension.name} ({languageName(e.extension.lang)}{t(") isn't installed")}</div>
        ) : (
          <span className="text-xs text-muted">—</span>
        )}
        {e.state !== "imported" && (
          <div className="mt-1 flex gap-1">
            <Button size="sm" variant="ghost" disabled={disabled} onClick={onPickSource}>
              {src ? tr("Change") : tr("Pick source")}
            </Button>
            {e.state === "review" && src && (
              <Button size="sm" variant="ghost" icon={<Check className="size-3.5" />} disabled={disabled} onClick={onAccept}>{t("Accept")}</Button>
            )}
          </div>
        )}
      </Td>
      <Td>
        {e.metadata ? (
          <div className="text-xs">
            <div className="font-medium text-fg">{e.metadata.title || e.title}</div>
            <div className="text-muted">
              {e.metadata.provider} · {howLabel(e.metadata.how, e.metadata.score)}
            </div>
          </div>
        ) : (
          <span className="text-xs text-muted">{e.state === "library" ? "—" : tr("from the source")}</span>
        )}
        {e.state !== "imported" && e.state !== "library" && (
          <Button size="sm" variant="ghost" disabled={disabled} onClick={onPickMeta}>
            {e.metadata ? tr("Change") : tr("Pick")}
          </Button>
        )}
      </Td>
      <Td>
        <Badge tone={stateTone(e.state)}>{stateLabel(e.state)}</Badge>
        {e.message && <div className="mt-1 max-w-xs text-xs text-muted">{e.message}</div>}
        {e.seriesId && (
          <Link to={`/series/${e.seriesId}`} className="mt-1 inline-flex items-center gap-1 text-xs text-accent-2 hover:underline">
            <Library className="size-3" />{" " + t("Open series")}</Link>
        )}
      </Td>
    </tr>
  );
}

function OptionsCard({ imp, disabled, onSaved }: { imp: S["ImportResource"]; disabled: boolean; onSaved: () => void }) {
  const toast = useToast();
  const { data: roots } = useRootFolders();
  const { data: profiles } = useProfiles();
  const { data: readers } = useReaders();
  const [o, setO] = useState<Options>(imp.options);
  useEffect(() => setO(imp.options), [imp.options]);
  const save = async (next: Options) => {
    setO(next);
    try {
      await unwrap(api.PUT("/api/v1/imports/{id}/options", { params: { path: { id: imp.id } }, body: next }));
      onSaved();
    } catch (e) {
      toast.fromError(e, tr("Couldn't save options"));
    }
  };
  const set = <K extends keyof Options>(k: K, v: Options[K]) => save({ ...o, [k]: v });
  const cats = imp.info.categories ?? [];
  const catRule = (c: string) => o.categories?.[c] ?? {};
  const setCat = (c: string, patch: Partial<S["ImportCategory"]>) => save({ ...o, categories: { ...(o.categories ?? {}), [c]: { ...catRule(c), ...patch } } });

  return (
    <Card title={t("Import options")}>
      <fieldset disabled={disabled} className="grid gap-4 md:grid-cols-3">
        <Field label={t("Root folder")}>
          <Select value={o.rootFolderId} onChange={(e) => set("rootFolderId", Number(e.target.value))}>
            <option value={0}>{t("Choose…")}</option>
            {roots?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.path}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Profile")}>
          <Select value={o.profileId || profiles?.find((p) => p.isDefault)?.id || 0} onChange={(e) => set("profileId", Number(e.target.value))}>
            {profiles?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
                {p.isDefault ? tr(" (default)") : ""}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Monitor")} help={t("From the first unread: chapters after the last one you read are downloaded.")}>
          <Select value={o.monitor} onChange={(e) => set("monitor", e.target.value as Options["monitor"])}>
            <option value="unread">{t("From the first unread chapter")}</option>
            <option value="all">{t("All chapters")}</option>
            <option value="future">{t("Only new chapters")}</option>
            <option value="none">{t("Nothing")}</option>
          </Select>
        </Field>
        <div className="flex flex-col gap-2">
          <Switch checked={o.searchMissing} onChange={(v) => set("searchMissing", v)} label={t("Download monitored chapters right away")} />
          <Switch checked={o.monitorNew === "all"} onChange={(v) => set("monitorNew", v ? "all" : "none")} label={t("Monitor new chapters")} />
          <Switch checked={o.onlyFavorites} onChange={(v) => set("onlyFavorites", v)} label={t("Only library manga (not history)")} />
        </div>
        <div className="flex flex-col gap-2">
          <Switch checked={o.readState} onChange={(v) => set("readState", v)} label={t("Import read chapters")} />
          {o.readState && (
            <Select value={o.readerId} onChange={(e) => set("readerId", Number(e.target.value))}>
              <option value={0}>{t("New reader “")}{imp.format === "aidoku" ? "Aidoku" : "Mihon"}{" " + t("backup”")}</option>
              {readers?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
          )}
          {o.readState && (
            <Switch checked={o.pushProgress} onChange={(v) => set("pushProgress", v)} label={t("Mark them read in Komga/Kavita once downloaded")} />
          )}
        </div>
        <div className="flex flex-col gap-2">
          <Switch checked={o.matchMetadata} onChange={(v) => set("matchMetadata", v)} label={t("Link metadata (tracker ids, titles)")} />
          <Switch checked={o.findByTitle} onChange={(v) => set("findByTitle", v)} label={t("Search by title when a source can't be matched")} />
          <Switch checked={o.blockScanlators} onChange={(v) => set("blockScanlators", v)} label={t("Keep excluded scanlators blocked")} />
          <Switch checked={o.categoryTags} onChange={(v) => set("categoryTags", v)} label={t("Tag series with their categories")} />
        </div>
      </fieldset>
      {cats.length > 0 && (
        <div className="mt-4">
          <div className="mb-2 text-sm font-medium">{t("Categories")}</div>
          <div className="grid gap-2">
            {cats.map((c) => (
              <div key={c} className="flex flex-wrap items-center gap-2 text-sm">
                <span className="w-40 truncate font-medium">{c}</span>
                <Select className="w-auto max-w-full" disabled={disabled} value={catRule(c).rootFolderId ?? 0} onChange={(e) => setCat(c, { rootFolderId: Number(e.target.value) })}>
                  <option value={0}>{t("Root folder above")}</option>
                  {roots?.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.path}
                    </option>
                  ))}
                </Select>
                <Select className="w-auto max-w-full" disabled={disabled} value={catRule(c).profileId ?? 0} onChange={(e) => setCat(c, { profileId: Number(e.target.value) })}>
                  <option value={0}>{t("Profile above")}</option>
                  {profiles?.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))}
                </Select>
                <Switch checked={!!catRule(c).skip} disabled={disabled} onChange={(v) => setCat(c, { skip: v })} label={t("Skip")} />
              </div>
            ))}
          </div>
        </div>
      )}
    </Card>
  );
}
