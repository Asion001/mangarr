import { t as tr, t as translateUI } from "../../lib/i18n/core";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2, Pencil, PlugZap } from "lucide-react";
import { api, unwrap, type Implementation, type ModuleResource } from "../../api/client";
import { useModules, useSchema } from "../../api/queries";
import { DynamicForm, defaultsOf } from "../../components/DynamicForm";
import { Badge, Button, Confirm, EmptyState, ErrorBox, Field, IconButton, Input, Loading, Modal, PageHeader, Switch } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { MessengerBots } from "./MessengerBots";

const titles: Record<string, { title: string; subtitle: string }> = {
  source: { title: "Source modules", subtitle: "Engines that find and download chapters (e.g. Suwayomi running Keiyoushi extensions)." },
  metadata: { title: "Metadata", subtitle: "Providers searched by priority; their data is merged field by field." },
  library: { title: "Library servers", subtitle: "Komga / Kavita: rescanned after imports; per-reader progress powers cleanup." },
  notify: { title: "Notifications", subtitle: "Where to send new-chapter digests, failures and health alerts." },
  mediaserver: { title: "Media servers", subtitle: "Jellyfin / Silo: series pages link anime adaptations to where you can watch them." },
  upscale: { title: "Upscalers", subtitle: "mangarr-upscaler workers used by profiles with upscaling enabled." },
};

const eventLabels: Record<string, string> = {
  "chapter.imported": "Chapters downloaded (digest)",
  "chapter.upgraded": "Chapters upgraded",
  "series.added": "Series added",
  "series.deleted": "Series deleted",
  "download.failed": "Download failed",
  "cleanup.done": "Cleanup finished",
  "health.issue": "Health issue",
  "health.restored": "Health restored",
  "extension.update": "Extension updates",
  "manual.required": "Manual action required",
  "request.created": "New requests",
};

/** personalEventLabels name what a user's own targets can get. */
const personalEventLabels: Record<string, string> = {
  "chapter.imported": "New chapters of series you follow",
  "request.updated": "News about your requests",
};

export type Draft = {
  id?: number;
  implementation: string;
  name: string;
  enabled: boolean;
  priority: number;
  events: string[];
  settings: Record<string, unknown>;
  /** fields and meta pinned by environment variables */
  lock?: { fields: Record<string, string>; meta: Record<string, string> };
};

export function ModulesPage({ kind }: { kind: string }) {
  const { data: mods, isLoading, error } = useModules(kind);
  const { data: impls } = useSchema(kind);
  const qc = useQueryClient();
  const toast = useToast();
  const [picking, setPicking] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [deleting, setDeleting] = useState<ModuleResource | null>(null);
  const t = titles[kind];
  const implOf = (name: string) => impls?.find((i) => i.name === name);

  const startNew = (impl: Implementation) => {
    setPicking(false);
    setDraft({
      implementation: impl.name,
      name: impl.displayName,
      enabled: true,
      priority: (mods?.length ?? 0) + 1,
      events: kind === "notify" ? ["chapter.imported", "download.failed", "health.issue"] : [],
      settings: defaultsOf(impl.fields),
    });
  };
  const startEdit = (m: ModuleResource) =>
    setDraft({
      id: m.id,
      implementation: m.implementation,
      name: m.name,
      enabled: m.enabled,
      priority: m.priority,
      events: m.events ?? [],
      settings: { ...m.settings },
      lock: m.envLock ? { fields: m.envLock.fields ?? {}, meta: m.envLock.meta ?? {} } : undefined,
    });

  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/modules/{id}", { params: { path: { id: deleting.id } } }));
      qc.invalidateQueries({ queryKey: ["modules"] });
      setDeleting(null);
    } catch (e) {
      toast.fromError(e);
    }
  };

  return (
    <>
      <PageHeader
        title={t.title}
        subtitle={t.subtitle}
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setPicking(true)}>{translateUI("Add")}</Button>
        }
      />
      {kind === "notify" && <MessengerBots />}
      {kind === "notify" && (
        <div className="mb-3">
          <h2 className="text-base font-semibold">{tr("Broadcast targets")}</h2>
          <p className="text-sm text-muted">{tr("Install-wide events to a service of yours: health, failed downloads, new requests.")}</p>
        </div>
      )}
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {mods?.length === 0 && <EmptyState title={translateUI("Nothing configured yet")}>{translateUI("Click “Add” to set one up.")}</EmptyState>}
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {mods?.map((m) => (
          <div key={m.id} className="flex flex-col gap-2 rounded-lg border border-border bg-panel p-4">
            <div className="flex items-start justify-between gap-2">
              <div>
                <div className="font-medium">{m.name}</div>
                <div className="text-xs text-muted">{implOf(m.implementation)?.displayName ?? m.implementation}</div>
              </div>
              <div className="flex">
                <IconButton title={translateUI("Edit")} onClick={() => startEdit(m)}>
                  <Pencil className="size-4" />
                </IconButton>
                <IconButton
                  title={m.managedBy?.startsWith("env:") ? tr("Defined by environment variables") : tr("Delete")}
                  disabled={!!m.managedBy?.startsWith("env:")}
                  onClick={() => setDeleting(m)}
                >
                  <Trash2 className="size-4" />
                </IconButton>
              </div>
            </div>
            <div className="flex flex-wrap gap-1">
              {m.enabled ? <Badge tone="ok">{translateUI("enabled")}</Badge> : <Badge>{translateUI("disabled")}</Badge>}
              {m.managedBy?.startsWith("env:") && (
                <Badge tone="warn" title={`Defined by MANGARR_MODULE_${m.managedBy.replace(/^env:/, "")}_* variables`}>
                  env
                </Badge>
              )}
              <Badge>{translateUI("priority") + " "}{m.priority}</Badge>
              {m.capabilities.map((c) => (
                <Badge key={c} tone="info">
                  {c}
                </Badge>
              ))}
            </div>
            {m.error && <p className="text-xs text-err">{m.error}</p>}
          </div>
        ))}
      </div>

      <Modal open={picking} onClose={() => setPicking(false)} title={`Add ${t.title.toLowerCase()}`}>
        <div className="flex flex-col gap-2">
          {impls?.map((i) => (
            <button key={i.name} onClick={() => startNew(i)} className="rounded-lg border border-border p-3 text-left hover:border-accent hover:bg-panel-2">
              <div className="font-medium">{i.displayName}</div>
              <div className="mt-0.5 text-sm text-muted">{i.description}</div>
            </button>
          ))}
          {impls?.length === 0 && <p className="text-sm text-muted">{translateUI("No implementations available.")}</p>}
        </div>
      </Modal>

      {draft && <ModuleEditor kind={kind} draft={draft} impl={implOf(draft.implementation)} onClose={() => setDraft(null)} />}
      <Confirm open={!!deleting} title={translateUI("Delete module")} danger confirmLabel={translateUI("Delete")} message={`Delete ${deleting?.name}?`} onConfirm={remove} onClose={() => setDeleting(null)} />
    </>
  );
}

/** ModuleEditor edits a module; personal: one of the signed-in user's notification targets. */
export function ModuleEditor({
  kind,
  draft: initial,
  impl,
  onClose,
  personal,
}: {
  kind: string;
  draft: Draft;
  impl?: Implementation;
  onClose: () => void;
  personal?: boolean;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const [d, setD] = useState<Draft>(initial);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const body = () => ({ kind: kind as "source", implementation: d.implementation, name: d.name, enabled: d.enabled, priority: d.priority, events: d.events, settings: d.settings });

  const own = () => ({ implementation: d.implementation, name: d.name, enabled: d.enabled, events: d.events, settings: d.settings });

  const test = async () => {
    setTesting(true);
    setError(null);
    try {
      if (personal) await unwrap(api.POST("/api/v1/me/notifications/test", { body: { ...own(), id: d.id } }));
      else await unwrap(api.POST("/api/v1/modules/test", { body: { ...body(), id: d.id } }));
      toast.success(tr("Test succeeded"));
    } catch (e) {
      setError(e);
    } finally {
      setTesting(false);
    }
  };
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (personal && d.id) await unwrap(api.PUT("/api/v1/me/notifications/{id}", { params: { path: { id: d.id } }, body: own() }));
      else if (personal) await unwrap(api.POST("/api/v1/me/notifications", { body: own() }));
      else if (d.id) await unwrap(api.PUT("/api/v1/modules/{id}", { params: { path: { id: d.id } }, body: body() }));
      else await unwrap(api.POST("/api/v1/modules", { body: body() }));
      qc.invalidateQueries({ queryKey: ["modules"] });
      qc.invalidateQueries({ queryKey: ["me-notifications"] });
      qc.invalidateQueries({ queryKey: ["sources"] });
      toast.success(`${d.name} saved`);
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={`${d.id ? "Edit" : "Add"} ${impl?.displayName ?? d.implementation}`}
      size="lg"
      footer={
        <>
          <Button className="mr-auto" icon={<PlugZap className="size-4" />} loading={testing} onClick={test}>{translateUI("Test")}</Button>
          <Button onClick={onClose}>{translateUI("Cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{translateUI("Save")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {impl?.description && <p className="text-sm text-muted">{impl.description}</p>}
        <div className={personal ? "" : "grid gap-4 md:grid-cols-[1fr_120px]"}>
          <Field label={translateUI("Name")} env={d.lock?.meta.name}>
            <Input value={d.name} onChange={(e) => setD({ ...d, name: e.target.value })} />
          </Field>
          {!personal && (
            <Field label={translateUI("Priority")} help={translateUI("Lower first")} env={d.lock?.meta.priority}>
              <Input type="number" value={d.priority} onChange={(e) => setD({ ...d, priority: Number(e.target.value) })} />
            </Field>
          )}
        </div>
        <Switch checked={d.enabled} onChange={(v) => setD({ ...d, enabled: v })} label={translateUI("Enabled")} env={d.lock?.meta.enabled} />
        {impl && <DynamicForm fields={impl.fields} values={d.settings} locks={d.lock?.fields} onChange={(settings) => setD({ ...d, settings })} />}
        {kind === "notify" && impl?.events && (
          <Field label={translateUI("Send on")} env={d.lock?.meta.events}>
            <div className="grid gap-1.5 sm:grid-cols-2">
              {impl.events.map((ev) => (
                <label key={ev} className="flex items-center gap-2 text-sm">
                  <input type="checkbox" checked={d.events.includes(ev)} onChange={(e) => setD({ ...d, events: e.target.checked ? [...d.events, ev] : d.events.filter((x) => x !== ev) })} />
                  {(personal ? personalEventLabels[ev] : eventLabels[ev]) ?? ev}
                </label>
              ))}
            </div>
          </Field>
        )}
        {error !== null && <ErrorBox error={error} />}
      </div>
    </Modal>
  );
}
