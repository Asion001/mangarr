import { t } from "../../lib/i18n/core";
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, Plus, Trash2 } from "lucide-react";
import { api, unwrap, type ModuleResource, type S } from "../../api/client";
import { useModules } from "../../api/queries";
import { Badge, Button, Card, ErrorBox, Field, IconButton, Input, Loading, Modal, PageHeader, Select, Switch, Table, Td, Th } from "../../components/ui";
import { bytes, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "../settings/useSettingsDoc";

type Worker = S["WorkerResource"];
type Downloads = S["Downloads"];
type UpscaleModel = { name: string; description?: string };

const roles = [
  { key: "download", label: "Download", help: "Fetches chapters from their source and uploads the pages here" },
  { key: "upscale", label: "Upscale", help: "Runs the upscaler on pages" },
  { key: "encode", label: "Encode", help: "Processes downloaded pages (resize, split, upscale, re-encode) when the server runs with MANGARR_PROCESSING=workers" },
];

/** modelsOf reads the upscaling models a worker said it has when it last dialled in. */
function modelsOf(w: Worker): UpscaleModel[] {
  const list = (w.info as { models?: unknown } | undefined)?.models;
  return Array.isArray(list) ? list.filter((m): m is UpscaleModel => typeof (m as UpscaleModel)?.name === "string") : [];
}

/**
 * WorkersPage lists every machine that does work for this server, this
 * server included, in one priority order.
 */
export function WorkersPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const limits = useSettingsDoc<Downloads>("downloads");
  const { data: engines } = useModules("upscale");
  const { data, isLoading, error } = useQuery({
    queryKey: ["workers"],
    queryFn: () => unwrap(api.GET("/api/v1/workers")),
    refetchInterval: 15000,
  });
  const [adding, setAdding] = useState(false);
  const [issued, setIssued] = useState<{ name: string; key: string } | null>(null);
  const [removing, setRemoving] = useState<Worker | null>(null);

  // the built-in upscaler is this server's upscale role; the "workers"
  // module only hands batches to the machines listed here
  const local = engines?.find((e) => e.implementation === "local");
  const pool = engines?.find((e) => e.implementation === "workers");

  const reload = () => qc.invalidateQueries({ queryKey: ["workers"] });
  const updateEngine = async (engine: ModuleResource, patch: { enabled?: boolean; priority?: number; model?: string }) => {
    try {
      await unwrap(api.PUT("/api/v1/modules/{id}", {
        params: { path: { id: engine.id } },
        body: {
          kind: "upscale",
          implementation: engine.implementation,
          name: engine.name,
          enabled: patch.enabled ?? engine.enabled,
          priority: patch.priority ?? engine.priority,
          tags: engine.tags,
          events: engine.events,
          settings: patch.model === undefined ? engine.settings : { ...engine.settings, model: patch.model },
        },
      }));
      qc.invalidateQueries({ queryKey: ["modules", "upscale"] });
      qc.invalidateQueries({ queryKey: ["upscaler-info"] });
    } catch (e) {
      toast.fromError(e, t("Could not update this server"));
    }
  };
  const update = async (w: Worker, body: { enabled?: boolean; roles?: string[]; priority?: number; concurrent?: number; pageConcurrency?: number; upscaleModel?: string }) => {
    try {
      await unwrap(api.PUT("/api/v1/workers/{id}", { params: { path: { id: w.id } }, body }));
      reload();
    } catch (e) {
      toast.fromError(e, t("Could not update the worker"));
    }
  };

  return (
    <>
      <PageHeader
        title={t("Workers")}
        subtitle={t("Machines that download, upscale and encode for this server")}
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setAdding(true)}>{t("Add worker")}</Button>
        }
      />
      <p className="mb-3 text-sm text-muted">{t("Work goes to the lowest priority number that is online and has room. This server does whatever no worker takes.")}</p>
      {pool && !pool.enabled && (
        <div className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2 text-sm">
          <span className="flex-1">{t("Upscaling on remote workers is switched off, so only this server upscales.")}</span>
          <Button size="sm" onClick={() => updateEngine(pool, { enabled: true })}>{t("Switch on")}</Button>
        </div>
      )}
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <Table>
          <thead>
            <tr>
              <Th>{t("Worker")}</Th>
              <Th>{t("Roles")}</Th>
              <Th>{t("Upscale model")}</Th>
              <Th>{t("Priority")}</Th>
              <Th>{t("Concurrent tasks")}</Th>
              <Th>{t("Pages at a time")}</Th>
              <Th>{t("Doing now")}</Th>
              <Th>{t("Last 24 hours")}</Th>
              <Th>{t("Lifetime")}</Th>
              <Th>{t("Enabled")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            <ServerRow engine={local} onUpdate={updateEngine} limits={limits.value} onLimits={(patch) => limits.value && limits.save({ ...limits.value, ...patch })} />
            {data.map((w) => (
              <tr key={w.id} className={w.enabled ? undefined : "opacity-60"}>
                <Td>
                  <div className="flex items-center gap-2">
                    <span className={`size-2 rounded-full ${w.online ? "bg-ok" : "bg-border"}`} title={w.online ? t("online") : t("offline")} />
                    <span className="font-medium">{w.name}</span>
                  </div>
                  <div className="font-mono text-xs text-muted">
                    {w.prefix}… {w.version && `· ${w.version}`} {w.platform && `· ${w.platform}`} {w.lastIp && `· ${w.lastIp}`}
                  </div>
                </Td>
                <Td>
                  <div className="flex flex-wrap gap-1">
                    {roles.map((r) => {
                      const on = w.roles.includes(r.key);
                      return (
                        <RoleChip
                          key={r.key}
                          label={r.label}
                          help={r.help}
                          on={on}
                          onClick={() => update(w, { roles: on ? w.roles.filter((x) => x !== r.key) : [...w.roles, r.key] })}
                        />
                      );
                    })}
                  </div>
                </Td>
                <Td>
                  {w.roles.includes("upscale") ? (
                    <ModelSelect value={w.upscaleModel} models={modelsOf(w)} onChange={(upscaleModel) => update(w, { upscaleModel })} />
                  ) : (
                    <span className="text-xs text-muted">—</span>
                  )}
                </Td>
                <Td>
                  <DeferredNumber value={w.priority} onSave={(priority) => update(w, { priority })} title={t("Lower first")} />
                </Td>
                <Td>
                  <DeferredNumber value={w.concurrent} min={0} onSave={(concurrent) => update(w, { concurrent })} title={t("0 = default")} />
                </Td>
                <Td>
                  <DeferredNumber value={w.pageConcurrency} min={0} max={64} onSave={(pageConcurrency) => update(w, { pageConcurrency })} title={t("Pages one download fetches at once. 0 = the worker's own setting (4 unless set).")} />
                </Td>
                <Td className="text-muted">
                  {w.busy?.length ? (
                    <BusyList busy={w.busy} />
                  ) : (
                    <span className="text-xs">{t("idle · seen") + " "}{w.lastSeenAt ? relative(w.lastSeenAt) : t("never")}</span>
                  )}
                </Td>
                <Td className="text-muted">
                  <div className="text-xs">
                    {w.recent.tasks}{" " + t("tasks")}{w.recent.failed > 0 && <span className="text-err"> · {w.recent.failed}{" " + t("failed")}</span>}
                  </div>
                  <div className="text-xs">
                    {w.recent.pages}{" " + t("pages ·") + " "}{bytes(w.recent.bytesIn)}{" " + t("in ·") + " "}{bytes(w.recent.bytesOut)}{" " + t("out")}</div>
                  {w.recent.seconds > 0 && (
                    <div className="text-xs">
                      {((w.recent.bytesIn / w.recent.seconds) / (1 << 20)).toFixed(1)}{" " + t("MB/s while busy")}</div>
                  )}
                </Td>
                <Td className="text-muted">
                  <div className="text-xs">
                    {w.tasksDone}{" " + t("tasks")}{w.tasksFailed > 0 && <span className="text-err"> · {w.tasksFailed}{" " + t("failed")}</span>}
                  </div>
                  <div className="text-xs">
                    {w.pagesDone}{" " + t("pages ·") + " "}{bytes(w.bytesIn)}{" " + t("in")}</div>
                </Td>
                <Td>
                  <Switch checked={w.enabled} onChange={(v) => update(w, { enabled: v })} />
                </Td>
                <Td className="text-right">
                  <IconButton title={t("Remove")} onClick={() => setRemoving(w)}>
                    <Trash2 className="size-4" />
                  </IconButton>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      {data && data.length === 0 && (
        <p className="mt-3 text-sm text-muted">{t("A worker is the same mangarr image started with") + " "}<code>MANGARR_MODE=worker</code>{t(", a server address and a key from here. It dials in and asks for work, so it needs no port of its own.")}</p>
      )}
      <Card
        title={t("Worker concurrency")}
        className="mt-6"
        actions={<Button size="sm" loading={limits.saving} disabled={!limits.value} onClick={() => limits.save()}>{t("Save")}</Button>}
      >
        {limits.isLoading && <Loading />}
        {limits.error && <ErrorBox error={limits.error} />}
        {limits.value && (
          <div className="grid gap-4 md:grid-cols-3">
            <Field label={t("Chapter files processed at once")} help={t("Shared by this server and the remote workers.")}>
              <Input type="number" min={1} value={limits.value.maxConcurrentProcessing} onChange={(e) => limits.patch({ maxConcurrentProcessing: Number(e.target.value) })} />
            </Field>
            <Field label={t("Tasks across all remote workers")}>
              <Input type="number" min={1} value={limits.value.maxWorkerTasks} onChange={(e) => limits.patch({ maxWorkerTasks: Number(e.target.value) })} />
            </Field>
            <Field label={t("Default tasks per worker")} help={t("Per-worker overrides can be set above. 0 uses the default.")}>
              <Input type="number" min={1} value={limits.value.maxConcurrentPerWorker} onChange={(e) => limits.patch({ maxConcurrentPerWorker: Number(e.target.value) })} />
            </Field>
          </div>
        )}
      </Card>
      {adding && (
        <AddWorker
          onClose={() => setAdding(false)}
          onCreated={(name, key) => {
            setAdding(false);
            setIssued({ name, key });
            reload();
          }}
        />
      )}
      {issued && <IssuedKey name={issued.name} value={issued.key} onClose={() => setIssued(null)} />}
      {removing && (
        <Modal
          open
          onClose={() => setRemoving(null)}
          title={`Remove ${removing.name}?`}
          footer={
            <>
              <Button onClick={() => setRemoving(null)}>{t("Cancel")}</Button>
              <Button
                variant="danger"
                onClick={async () => {
                  try {
                    await unwrap(api.DELETE("/api/v1/workers/{id}", { params: { path: { id: removing.id } } }));
                    reload();
                  } catch (e) {
                    toast.fromError(e);
                  }
                  setRemoving(null);
                }}
              >{t("Remove")}</Button>
            </>
          }
        >
          <p className="text-sm text-muted">{t("Its key stops working at once. Anything it is doing now is given to another worker or run here.")}</p>
        </Modal>
      )}
    </>
  );
}

/**
 * ServerRow is this server in the list of workers. It downloads and encodes
 * what no worker takes unless its own work is switched off (maxLocalTasks
 * -1), when everything waits for the workers; it upscales when the image has
 * the built-in upscaler, and that is what its priority and model apply to.
 */
function ServerRow({ engine, onUpdate, limits, onLimits }: {
  engine?: ModuleResource;
  onUpdate: (engine: ModuleResource, patch: { enabled?: boolean; priority?: number; model?: string }) => Promise<void>;
  limits: Downloads | null;
  onLimits: (patch: Partial<Downloads>) => void;
}) {
  const { data: info } = useQuery({
    queryKey: ["upscaler-info", engine?.id],
    queryFn: () => unwrap(api.GET("/api/v1/modules/{id}/upscaler-info", { params: { path: { id: engine!.id } } })),
    enabled: !!engine?.enabled,
    retry: false,
  });
  const upscales = !!engine?.enabled;
  const model = typeof engine?.settings?.model === "string" ? engine.settings.model : "";
  const off = (limits?.maxLocalTasks ?? 0) < 0;
  const fallback = off ? t("Switched off: downloads and processing wait for the workers") : t("Always does the work no worker takes");
  return (
    <tr className={off ? "bg-panel-2/40 opacity-60" : "bg-panel-2/40"}>
      <Td>
        <div className="flex items-center gap-2">
          <span className={`size-2 rounded-full ${off ? "bg-border" : "bg-ok"}`} title={off ? t("switched off") : t("online")} />
          <span className="whitespace-nowrap font-medium">{t("This server")}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-1 text-xs text-muted">
          <Badge tone="accent">{t("Integrated")}</Badge>
          {info?.devices?.join(", ")}
        </div>
        {engine?.error && <div className="mt-1 text-xs text-err">{engine.error}</div>}
      </Td>
      <Td>
        <div className="flex flex-wrap gap-1">
          {roles.map((r) =>
            r.key === "upscale" ? (
              engine ? (
                <RoleChip key={r.key} label={r.label} help={r.help} on={upscales} onClick={() => onUpdate(engine, { enabled: !upscales })} />
              ) : (
                <RoleChip key={r.key} label={r.label} help={t("Needs the full image, which has the upscaling tools")} on={false} />
              )
            ) : (
              <RoleChip key={r.key} label={r.label} help={fallback} on={!off} />
            ),
          )}
        </div>
      </Td>
      <Td>
        {engine ? (
          <ModelSelect value={model} models={info?.models ?? []} disabled={!upscales} onChange={(m) => onUpdate(engine, { model: m })} />
        ) : (
          <span className="text-xs text-muted">—</span>
        )}
      </Td>
      <Td>
        {engine ? (
          <DeferredNumber value={engine.priority} onSave={(priority) => onUpdate(engine, { priority })} title={t("Lower first")} />
        ) : (
          <span className="text-xs text-muted">—</span>
        )}
      </Td>
      <Td>
        {limits && !off ? (
          <DeferredNumber value={limits.maxLocalTasks ?? 0} min={0} onSave={(maxLocalTasks) => onLimits({ maxLocalTasks })} title={t("Downloads and chapter files this server works on itself at once. 0 = no limit of its own.")} />
        ) : (
          <span className="text-xs text-muted">—</span>
        )}
      </Td>
      <Td>
        {limits ? (
          <DeferredNumber value={limits.pageConcurrency} min={1} max={64} onSave={(pageConcurrency) => onLimits({ pageConcurrency })} title={t("Pages fetched in parallel within a chapter.")} />
        ) : (
          <span className="text-xs text-muted">—</span>
        )}
      </Td>
      <Td className="text-xs text-muted">{fallback}</Td>
      <Td className="text-xs text-muted">—</Td>
      <Td className="text-xs text-muted">—</Td>
      <Td>
        {limits && (
          <span title={t("Off: this server downloads and processes nothing itself, and chapters wait for a worker")}>
            <Switch checked={!off} onChange={(v) => onLimits({ maxLocalTasks: v ? 0 : -1 })} />
          </span>
        )}
      </Td>
      <Td />
    </tr>
  );
}

type Busy = NonNullable<Worker["busy"]>[number];

/** shownBusy is how many tasks a row lists before it folds the rest. */
const shownBusy = 2;

/**
 * BusyList is what a worker is doing, one short line per task. A worker
 * with many tasks shows the first few and a count, unfolded on request.
 * Only downloads count pages as they go; the other tasks show their size.
 */
function BusyList({ busy }: { busy: Busy[] }) {
  const [open, setOpen] = useState(false);
  const shown = open ? busy : busy.slice(0, shownBusy);
  const kinds = Object.entries(busy.reduce<Record<string, number>>((n, b) => ({ ...n, [b.kind]: (n[b.kind] ?? 0) + 1 }), {}));
  return (
    <div className="flex w-56 flex-col gap-1">
      {busy.length > shownBusy && (
        <div className="text-xs text-fg">{t("{count} tasks", { count: busy.length })}: {kinds.map(([k, n]) => `${n} ${k}`).join(", ")}</div>
      )}
      {shown.map((b) => (
        <div key={b.taskId} className="flex items-center gap-2 text-xs" title={`${b.series || "?"}${b.chapter ? ` ch. ${b.chapter}` : ""} · ${bytes(b.bytesIn)}`}>
          <span className="w-16 shrink-0 text-muted">{b.kind}</span>
          <span className="min-w-0 flex-1 truncate text-fg">{b.series || "?"}{b.chapter && ` ${b.chapter}`}</span>
          {b.pagesDone > 0 && b.pagesTotal > 0 ? (
            <span className="flex shrink-0 items-center gap-1">
              <span className="h-1 w-10 rounded bg-border">
                <span className="block h-1 rounded bg-accent" style={{ width: `${(100 * b.pagesDone) / b.pagesTotal}%` }} />
              </span>
              {b.pagesDone}/{b.pagesTotal}
            </span>
          ) : (
            <span className="shrink-0">{b.pagesTotal ? t("{count} pages", { count: b.pagesTotal }) : ""}</span>
          )}
        </div>
      ))}
      {busy.length > shownBusy && (
        <button type="button" className="self-start text-xs text-accent hover:underline" onClick={() => setOpen(!open)}>
          {open ? t("Show less") : t("Show all {count}", { count: busy.length })}
        </button>
      )}
    </div>
  );
}

function RoleChip({ label, help, on, onClick }: { label: string; help: string; on: boolean; onClick?: () => void }) {
  return (
    <button
      type="button"
      title={help}
      disabled={!onClick}
      aria-pressed={on}
      onClick={onClick}
      className={`rounded border px-2 py-0.5 text-xs disabled:cursor-default ${on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted enabled:hover:text-fg"}`}
    >
      {label}
    </button>
  );
}

/**
 * ModelSelect picks the upscaling model a machine runs. Empty keeps the
 * profile's model; a model the machine doesn't have falls back to it too.
 */
function ModelSelect({ value, models, onChange, disabled }: { value: string; models: UpscaleModel[]; onChange: (value: string) => void; disabled?: boolean }) {
  const known = !value || models.some((m) => m.name === value);
  return (
    <Select className="w-44" value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} aria-label={t("Upscale model")}>
      <option value="">{t("Profile's model")}</option>
      {models.map((m) => (
        <option key={m.name} value={m.name} title={m.description}>{m.name}</option>
      ))}
      {!known && <option value={value}>{value}</option>}
    </Select>
  );
}

function DeferredNumber({ value, onSave, min, max, title }: { value: number; onSave: (value: number) => void; min?: number; max?: number; title: string }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const save = () => {
    if (draft !== value) onSave(draft);
  };
  return (
    <Input
      className="w-24"
      type="number"
      min={min}
      max={max}
      value={draft}
      onChange={(e) => setDraft(Number(e.target.value))}
      onBlur={save}
      onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); }}
      aria-label={title}
      title={title}
    />
  );
}

function AddWorker({ onClose, onCreated }: { onClose: () => void; onCreated: (name: string, key: string) => void }) {
  const toast = useToast();
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>(["upscale"]);
  const [busy, setBusy] = useState(false);
  const create = async () => {
    setBusy(true);
    try {
      const r = await unwrap(api.POST("/api/v1/workers", { body: { name, roles: picked } }));
      onCreated(r.worker.name, r.key);
    } catch (e) {
      toast.fromError(e, t("Could not add the worker"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Add worker")}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" disabled={!name.trim() || !picked.length || busy} onClick={create}>{t("Create key")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-sm font-medium">{t("Name")}</label>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="gpu-box" />
        </div>
        <div className="flex flex-col gap-1">
          <span className="text-sm font-medium">{t("What it may do")}</span>
          {roles.map((r) => (
            <label key={r.key} className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="mt-1"
                checked={picked.includes(r.key)}
                onChange={(e) => setPicked(e.target.checked ? [...picked, r.key] : picked.filter((x) => x !== r.key))}
              />
              <span>
                {r.label}
                <span className="block text-xs text-muted">{r.help}</span>
              </span>
            </label>
          ))}
        </div>
      </div>
    </Modal>
  );
}

function IssuedKey({ name, value, onClose }: { name: string; value: string; onClose: () => void }) {
  const toast = useToast();
  return (
    <Modal open onClose={onClose} title={`Key for ${name}`} footer={<Button variant="primary" onClick={onClose}>{t("Done")}</Button>}>
      <div className="flex flex-col gap-3">
        <p className="text-sm text-muted">{t("Copy it now: only its hash is kept here, so this is the one time it can be read.")}</p>
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded border border-border bg-bg px-2 py-1 font-mono text-xs break-all">{value}</code>
          <IconButton
            title={t("Copy")}
            onClick={async () => {
              await navigator.clipboard.writeText(value);
              toast.success(t("Key copied"));
            }}
          >
            <Copy className="size-4" />
          </IconButton>
        </div>
        <p className="text-xs text-muted">{t("Give it to the worker as") + " "}<code>MANGARR_WORKER_KEY</code>{t(", with") + " "}<code>MANGARR_SERVER_URL</code>{" " + t("pointing at this server.")}</p>
      </div>
    </Modal>
  );
}
