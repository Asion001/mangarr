import { t } from "../../lib/i18n/core";
import { Plus, X } from "lucide-react";
import { type S } from "../../api/client";
import { Button, Card, IconButton, Input, Select } from "../../components/ui";

export type UpscaleRoute = S["UpscaleRoute"];
type UpscaleModel = { name: string; description?: string };

/** An upscaler a rule can send pages to: a worker's id, -1 for this server. */
export type RouteTarget = { id: number; name: string; models: UpscaleModel[] };

/** routeAnchor is where a worker row's "gets these pages" link leads. */
export const routeAnchor = "upscale-routing";

const scales = [2, 3, 4];

/** describeRoute is a rule in a few words, for the row it sends pages to. */
export function describeRoute(r: UpscaleRoute): string {
  return r.match === "width" ? t("pages under {width}px", { width: r.belowWidth ?? 0 }) : (r.scales ?? []).map((s) => `×${s}`).join(" ");
}

/**
 * UpscaleRoutes sends the pages of a given scale or width to a chosen
 * upscaler and model, ahead of the priority order. The first rule that
 * matches a page wins.
 */
export function UpscaleRoutes({ routes, targets, onChange, onSave, saving, disabled }: {
  routes: UpscaleRoute[];
  targets: RouteTarget[];
  onChange: (routes: UpscaleRoute[]) => void;
  onSave: () => void;
  saving: boolean;
  disabled: boolean;
}) {
  const set = (i: number, patch: Partial<UpscaleRoute>) => onChange(routes.map((r, k) => (k === i ? { ...r, ...patch } : r)));
  const allModels = [...new Map(targets.flatMap((x) => x.models).map((m) => [m.name, m])).values()];
  return (
    <div id={routeAnchor} className="mt-6 scroll-mt-4">
      <Card
        title={t("Upscale routing")}
        actions={<Button size="sm" loading={saving} disabled={disabled} onClick={onSave}>{t("Save")}</Button>}
      >
        <p className="mb-3 text-sm text-muted">{t("Send pages of a given scale or size to a chosen upscaler and model, ahead of the priority order. The first rule that matches a page wins; pages no rule matches go by priority as before.")}</p>
        {routes.length > 0 && (
          <div className="flex flex-col gap-2">
            {routes.map((r, i) => {
              const target = targets.find((x) => x.id === r.target);
              const models = r.target === 0 ? allModels : (target?.models ?? []);
              const known = !r.model || models.some((m) => m.name === r.model);
              return (
                <div key={i} className="flex flex-wrap items-center gap-2 rounded-md bg-panel-2 px-2 py-2">
                  <span className="w-5 text-center text-xs text-muted">{i + 1}</span>
                  <Select className="w-36" aria-label={t("When")} value={r.match}
                    onChange={(e) => set(i, e.target.value === "width" ? { match: "width", belowWidth: r.belowWidth || 600, scales: undefined } : { match: "scale", scales: r.scales?.length ? r.scales : [4], belowWidth: undefined })}>
                    <option value="scale">{t("Scale")}</option>
                    <option value="width">{t("Page width")}</option>
                  </Select>
                  {r.match === "width" ? (
                    <span className="flex items-center gap-1 text-sm">
                      {t("under")}
                      <Input className="w-24" type="number" min={1} aria-label={t("Page width")} value={r.belowWidth ?? 0}
                        onChange={(e) => set(i, { belowWidth: Number(e.target.value) })} />
                      <span className="text-muted">px</span>
                    </span>
                  ) : (
                    <span className="flex gap-1">
                      {scales.map((s) => {
                        const on = (r.scales ?? []).includes(s);
                        return (
                          <button key={s} type="button" aria-pressed={on}
                            onClick={() => set(i, { scales: on ? (r.scales ?? []).filter((x) => x !== s) : [...(r.scales ?? []), s].sort((a, b) => a - b) })}
                            className={`rounded border px-2.5 py-1 text-sm ${on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg"}`}>
                            ×{s}
                          </button>
                        );
                      })}
                    </span>
                  )}
                  <span className="text-muted">→</span>
                  <Select className="w-44" aria-label={t("Send to")} value={r.target} onChange={(e) => set(i, { target: Number(e.target.value), model: undefined })}>
                    <option value={0}>{t("Any upscaler")}</option>
                    {targets.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                    {!target && r.target !== 0 && <option value={r.target}>{t("Removed upscaler")}</option>}
                  </Select>
                  <Select className="w-52" aria-label={t("With model")} value={r.model ?? ""} onChange={(e) => set(i, { model: e.target.value || undefined })}>
                    <option value="">{t("Its usual model")}</option>
                    {models.map((m) => <option key={m.name} value={m.name} title={m.description}>{m.name}</option>)}
                    {!known && <option value={r.model}>{r.model}</option>}
                  </Select>
                  <Select className="w-52" aria-label={t("If it is offline")} value={r.wait ? "wait" : "priority"} disabled={r.target === 0}
                    onChange={(e) => set(i, { wait: e.target.value === "wait" })}>
                    <option value="wait">{t("Wait for it")}</option>
                    <option value="priority">{t("Use the priority order")}</option>
                  </Select>
                  <IconButton title={t("Remove rule")} onClick={() => onChange(routes.filter((_, k) => k !== i))}>
                    <X className="size-4" />
                  </IconButton>
                </div>
              );
            })}
          </div>
        )}
        <Button size="sm" className="mt-3" icon={<Plus className="size-4" />} disabled={disabled}
          onClick={() => onChange([...routes, { match: "scale", scales: [4], target: 0, wait: false }])}>
          {t("Add rule")}
        </Button>
        <div className="mt-4 grid gap-3 border-t border-border pt-3 text-xs text-muted md:grid-cols-3">
          <p><span className="block font-medium text-fg">{t("Scale")}</span>{t("The scale a page needs to reach the profile's minimum width.")}</p>
          <p><span className="block font-medium text-fg">{t("Page width and model")}</span>{t("Width is the page before upscaling. A rule's model replaces the profile's and the upscaler's own; an upscaler without it uses its usual one.")}</p>
          <p><span className="block font-medium text-fg">{t("If it is offline")}</span>{t("Wait for it: the chapter waits for that upscaler. Use the priority order: any upscaler may take the pages meanwhile.")}</p>
        </div>
        <p className="mt-3 text-xs text-muted">{t("When the workers process pages (MANGARR_PROCESSING=workers), one worker processes a whole chapter: it goes to the worker of the rule most of its pages match, when that worker also has the encode role.")}</p>
      </Card>
    </div>
  );
}
