import { t as tr, t } from "../../lib/i18n/core";
import { LanguageMenu } from "../../components/LanguageChooser";
import { useMemo, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import { api, unwrap, type Catalog, type Extension, type ModuleResource, type S } from "../../api/client";
import { Badge, Button, ErrorBox, IconButton, Input, Loading, Modal, Switch } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { useQueryParam } from "../../lib/urlState";
import { useSettingsDoc } from "../settings/useSettingsDoc";
import { languageMatches, languageName } from "../../lib/format";
import { CatalogIcon } from "./Catalogs";

/** useExtensions lists a module's extensions; refresh asks the stores again. */
export function useExtensions(module: ModuleResource, refresh = false) {
  return useQuery({
    queryKey: ["extensions", module.id, refresh],
    queryFn: () => unwrap(api.GET("/api/v1/modules/{id}/extensions", { params: { path: { id: module.id }, query: { refresh } } })),
    enabled: module.capabilities.includes("extensions"),
    staleTime: 5 * 60_000,
  });
}

/**
 * AddCatalogs is where catalogs come from: extensions to install or update,
 * updates first, and the stores they are listed in.
 */
export function AddCatalogs({ module }: { module: ModuleResource }) {
  const qc = useQueryClient();
  const toast = useToast();
  const src = useSettingsDoc<S["Sources"]>("sources").value;
  const [refresh, setRefresh] = useState(false);
  const { data, isFetching, error } = useExtensions(module, refresh);
  const stores = useQuery({ queryKey: ["stores", module.id], queryFn: () => unwrap(api.GET("/api/v1/modules/{id}/stores", { params: { path: { id: module.id } } })) });
  const [q, setQ] = useQueryParam("ext", "");
  const [picked, setPicked] = useState<string[] | null>(null);
  const [showInstalled, setShowInstalled] = useState(false);
  const [busy, setBusy] = useState("");
  const [managingStores, setManagingStores] = useState(false);
  const [choosing, setChoosing] = useState<{ ext: Extension; catalogs: Catalog[] } | null>(null);

  const all = data ?? [];
  const hideNsfw = src?.hideNsfw ?? true;
  // your search languages are the starting filter, first in the chips
  const preferred = src?.defaultLanguages ?? [];
  const langs = useMemo(
    () => [...preferred, ...Array.from(new Set(all.map((e) => e.lang))).filter((l) => l !== "all" && l !== "multi" && !preferred.includes(l)).sort((a, b) => languageName(a).localeCompare(languageName(b)))],
    [all, preferred.join(",")],
  );
  const chosen = picked ?? preferred;
  const visible = all.filter(
    (e) =>
      !(hideNsfw && e.nsfw) &&
      (!chosen.length || chosen.includes(e.lang) || e.lang === "all" || e.lang === "multi") &&
      (!q || e.name.toLowerCase().includes(q.toLowerCase()) || languageMatches(e.lang, q)),
  );
  const updates = visible.filter((e) => e.installed && e.hasUpdate);
  const rest = visible.filter((e) => (showInstalled ? !e.hasUpdate || !e.installed : !e.installed)).sort((a, b) => a.name.localeCompare(b.name));

  const afterChange = () => Promise.all([qc.invalidateQueries({ queryKey: ["extensions"] }), qc.invalidateQueries({ queryKey: ["catalogs"] }), qc.invalidateQueries({ queryKey: ["sources"] })]);
  const act = async (e: Extension, action: "install" | "update" | "uninstall") => {
    setBusy(e.pkg);
    try {
      await unwrap(api.POST("/api/v1/modules/{id}/extensions/{pkg}/{action}", { params: { path: { id: module.id, pkg: e.pkg, action } } }));
      await afterChange();
      if (action === "install") {
        // several languages: ask which to turn on, your search languages ticked
        const list = await unwrap(api.GET("/api/v1/catalogs", { params: { query: { refresh: true } } }));
        const mine = list.items.filter((c) => c.moduleId === module.id && c.extension === e.pkg);
        if (mine.length > 1) setChoosing({ ext: e, catalogs: mine });
        else toast.success(tr("{name} installed", { name: e.name }));
      } else {
        toast.success(action === "update" ? tr("{name} updated", { name: e.name }) : tr("{name} uninstalled", { name: e.name }));
      }
    } catch (err) {
      toast.fromError(err);
    } finally {
      setBusy("");
    }
  };
  const updateAll = async () => {
    for (const e of updates) await act(e, "update");
  };

  const icon = (e: Extension) => <CatalogIcon moduleId={module.id} iconUrl={e.iconUrl} name={e.name} />;
  const row = (e: Extension, action: ReactNode) => (
    <div key={e.pkg} className="flex items-center gap-3 border-t border-border px-4 py-2.5">
      {icon(e)}
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5">
          <span className="truncate font-semibold">{e.name}</span>
          {e.nsfw && <Badge tone="err">18+</Badge>}
          {e.obsolete && <Badge tone="warn">{t("obsolete")}</Badge>}
        </div>
        <div className="text-xs text-muted">{[languageName(e.lang), `v${e.versionName}`].join(" · ")}</div>
      </div>
      {action}
    </div>
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Input className="max-w-80" aria-label={t("Search extensions")} placeholder={t("Search {n} extensions…", { n: all.length })} value={q} onChange={(e) => setQ(e.target.value)} />
        <div role="group" aria-label={t("Languages")} className="flex flex-wrap gap-1.5">
          {langs.filter((l) => preferred.includes(l) || chosen.includes(l)).map((l) => {
            const on = chosen.includes(l);
            return (
              <button
                key={l}
                type="button"
                aria-pressed={on}
                onClick={() => setPicked(on ? chosen.filter((x) => x !== l) : [...chosen, l])}
                className={clsx("rounded-full border px-2.5 py-0.5 text-xs font-medium", on ? "border-accent bg-accent/12 text-fg" : "border-border text-muted hover:text-fg")}
              >
                {languageName(l)}
              </button>
            );
          })}
          <LanguageMenu
            label={t("More…")}
            options={langs}
            exclude={[...preferred, ...chosen]}
            onPick={(l) => setPicked([...chosen, l])}
            className="rounded-full border border-dashed border-border px-2.5 py-0.5 text-xs font-medium text-fg hover:border-muted"
          />
        </div>
        <span className="flex-1" />
        <span className="text-sm text-muted">
          {t("From {n} stores", { n: stores.data?.length ?? 0 })} ·{" "}
          <button type="button" className="font-semibold text-accent-2 hover:underline" onClick={() => setManagingStores(true)}>{t("Manage stores")}</button>
        </span>
        <IconButton title={t("Check stores for updates")} disabled={isFetching} onClick={() => (setRefresh(true), qc.invalidateQueries({ queryKey: ["extensions"] }))}>
          <RefreshCw className={clsx("size-4", isFetching && "animate-spin")} />
        </IconButton>
      </div>
      {isFetching && !data && <Loading />}
      {error && <ErrorBox error={error} />}

      {updates.length > 0 && (
        <section aria-labelledby="ext-updates" className="rounded-xl border border-border bg-panel">
          <header className="flex items-center gap-3 px-4 py-3">
            <h2 id="ext-updates" className="flex-1 text-sm font-semibold">
              {t("Updates")} <span className="font-normal text-muted">{updates.length}</span>
            </h2>
            <Button size="sm" variant="primary" loading={!!busy} onClick={updateAll}>{t("Update all")}</Button>
          </header>
          {updates.map((e) => row(e, <Button size="sm" loading={busy === e.pkg} onClick={() => act(e, "update")}>{t("Update")}</Button>))}
        </section>
      )}

      <section aria-labelledby="ext-rest" className="rounded-xl border border-border bg-panel">
        <header className="flex flex-wrap items-center gap-3 px-4 py-3">
          <h2 id="ext-rest" className="flex-1 text-sm font-semibold">
            {showInstalled ? t("All extensions") : t("Not installed")} <span className="font-normal text-muted">{chosen.length ? t("for {langs}", { langs: chosen.map(languageName).join(", ") }) + " · " : ""}{rest.length}</span>
          </h2>
          <Switch checked={showInstalled} onChange={setShowInstalled} label={t("Show installed too")} />
        </header>
        <div className="grid md:grid-cols-2">
          {rest.slice(0, 200).map((e) =>
            row(
              e,
              e.installed ? (
                <IconButton title={t("Uninstall")} disabled={busy === e.pkg} onClick={() => act(e, "uninstall")}>
                  <Trash2 className="size-4" />
                </IconButton>
              ) : (
                <Button size="sm" loading={busy === e.pkg} onClick={() => act(e, "install")}>{t("Install")}</Button>
              ),
            ),
          )}
        </div>
        {rest.length > 200 && <p className="border-t border-border px-4 py-3 text-center text-sm text-muted">{t("Showing 200 of {n}; refine the search.", { n: rest.length })}</p>}
        {!rest.length && data && <p className="border-t border-border px-4 py-3 text-sm text-muted">{t("Nothing matches.")}</p>}
      </section>

      {managingStores && <StoresDialog module={module} onClose={() => setManagingStores(false)} />}
      {choosing && <LanguagePicker module={module} ext={choosing.ext} catalogs={choosing.catalogs} preferred={src?.defaultLanguages ?? []} onClose={() => setChoosing(null)} />}
    </div>
  );
}

/** LanguagePicker turns on the chosen catalogs of a just-installed extension. */
function LanguagePicker({ ext, catalogs, preferred, onClose }: { module: ModuleResource; ext: Extension; catalogs: Catalog[]; preferred: string[]; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [on, setOn] = useState<Set<string>>(() => new Set(catalogs.filter((c) => preferred.includes(c.lang)).map((c) => c.id)));
  const [all, setAll] = useState(false);
  const [saving, setSaving] = useState(false);
  const sorted = [...catalogs].sort((a, b) => Number(preferred.includes(b.lang)) - Number(preferred.includes(a.lang)) || languageName(a.lang).localeCompare(languageName(b.lang)));
  const shown = all ? sorted : sorted.slice(0, 8);
  const save = async () => {
    setSaving(true);
    try {
      await unwrap(api.PUT("/api/v1/catalogs", { body: Object.fromEntries(catalogs.map((c) => [`${c.moduleId}:${c.id}`, { enabled: on.has(c.id) }])) }));
      await qc.invalidateQueries({ queryKey: ["catalogs"] });
      toast.success(tr("{name} installed", { name: ext.name }), tr("{n} catalogs on", { n: on.size }));
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Turn on which {name} catalogs?", { name: ext.name })}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Later")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{t("Turn on {n}", { n: on.size })}</Button>
        </>
      }
    >
      <p className="mb-3 text-sm text-muted">{t("{name} has {n} languages. Your search languages are ticked.", { name: ext.name, n: catalogs.length })}</p>
      <div className="flex flex-col gap-2">
        {shown.map((c) => (
          <label key={c.id} className="flex items-center gap-2.5 text-sm">
            <input
              type="checkbox"
              className="size-4 accent-ok"
              checked={on.has(c.id)}
              onChange={(e) => setOn((cur) => {
                const next = new Set(cur);
                if (e.target.checked) next.add(c.id);
                else next.delete(c.id);
                return next;
              })}
            />
            <span className="flex-1">{c.displayName}</span>
            <Badge>{languageName(c.lang)}</Badge>
          </label>
        ))}
      </div>
      {!all && sorted.length > shown.length && (
        <button type="button" className="mt-2 text-sm font-semibold text-accent-2 hover:underline" onClick={() => setAll(true)}>{t("Show all {n}", { n: sorted.length })}</button>
      )}
    </Modal>
  );
}

/** StoresDialog edits the extension stores (index URLs) a module reads. */
function StoresDialog({ module, onClose }: { module: ModuleResource; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const key = ["stores", module.id];
  const { data, isLoading, error } = useQuery({ queryKey: key, queryFn: () => unwrap(api.GET("/api/v1/modules/{id}/stores", { params: { path: { id: module.id } } })) });
  const [url, setUrl] = useState("");
  const changed = () => Promise.all([qc.invalidateQueries({ queryKey: key }), qc.invalidateQueries({ queryKey: ["extensions"] })]);
  const add = async () => {
    try {
      await unwrap(api.POST("/api/v1/modules/{id}/stores", { params: { path: { id: module.id } }, body: { url } }));
      setUrl("");
      await changed();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const remove = async (u: string) => {
    try {
      await unwrap(api.DELETE("/api/v1/modules/{id}/stores", { params: { path: { id: module.id }, query: { url: u } } }));
      await changed();
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <Modal open onClose={onClose} title={t("Extension stores")} size="md">
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      <div className="flex flex-col gap-2">
        {data?.map((u) => (
          <div key={u} className="flex items-center gap-2 rounded bg-panel-2 px-3 py-2 text-sm">
            <span className="flex-1 truncate font-mono text-xs">{u}</span>
            <IconButton title={t("Remove")} onClick={() => remove(u)}>
              <Trash2 className="size-4" />
            </IconButton>
          </div>
        ))}
        <div className="mt-2 flex gap-2">
          <Input aria-label={t("Store address")} value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://…/index.min.json" />
          <Button icon={<Plus className="size-4" />} disabled={!url} onClick={add}>{t("Add store")}</Button>
        </div>
        <p className="text-xs text-muted">{t("Keiyoushi is added by default. Only add stores you trust: extensions run as code inside the engine.")}</p>
      </div>
    </Modal>
  );
}
