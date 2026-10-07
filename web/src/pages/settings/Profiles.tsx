import { t as tr, t } from "../../lib/i18n/core";
import { useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { ArrowRight, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import { Link } from "react-router";
import { api, ApiError, apiUrl, unwrap, type Profile, type S } from "../../api/client";
import { useChapters, useProfiles, useSeriesList } from "../../api/queries";
import { Badge, Button, Card, Confirm, ErrorBox, Field, IconButton, Input, Loading, Modal, PageHeader, Segmented, Select, Switch, TagInput } from "../../components/ui";
import { bytes } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "./useSettingsDoc";

type Cfg = Profile["config"];
type Tab = "releases" | "processing" | "cleanup";
type Preset = Cfg["encode"]["preset"];
type UpscalerModel = S["UpscalerModel"];

const emptyConfig: Cfg = {
  preferredScanlators: [],
  blockedScanlators: [],
  allowUpgrades: false,
  minPages: 0,
  upscale: { enabled: false, upscalerId: 0, minWidth: 1400, maxWidth: 2048, model: "waifu2x-cunet", noise: 1, format: "source", quality: 90 },
  encode: { format: "keep", preset: "balanced", quality: 0, speed: 0, grayscale: true, progressive: false, minSavingsPct: 10, recycleOriginals: true },
  pages: { junkUnder: 0, removeJunk: false, maxWidth: 0, splitTall: false, splitRatio: 0, segmentRatio: 0 },
  lowRes: { width: 0, action: "retry" },
  processTiming: "background",
  processExisting: false,
  cleanup: {},
};

// server defaults (model.DefaultJunkUnder, model.DefaultLowResWidth)
const defaultJunk = 300;
const defaultLowRes = 720;

// quality each speed preset uses when Quality is left empty (imageenc.Resolve)
const presetQuality: Record<"avif" | "jxl", Record<Preset, number>> = { avif: { fast: 60, balanced: 55, max: 48 }, jxl: { fast: 85, balanced: 80, max: 75 } };

// reader support for re-encoded pages (see docs/setup.md)
const compat: Record<string, { yes: string; no: string; note?: string }> = {
  avif: {
    yes: "Mihon 0.17+, Tachimanga, Panels (iOS 17+), Paperback (iOS 16+), Komga, Kavita",
    no: "Chunky",
    note: "KOReader through mangarr's OPDS catalog, which converts pages to JPEG; Chunky through Komga's OPDS; 32-bit ARM Komga can't read AVIF.",
  },
  jxl: { yes: "Mihon 0.17+, Tachimanga, Panels (iOS 17+), Komga", no: "Kavita", note: "KOReader through mangarr's OPDS catalog, which converts pages to JPEG." },
};

/** normalize fills defaults and maps older upscale formats onto "Save pages as". */
function normalize(profile: Profile): Profile {
  const upscale = { ...emptyConfig.upscale, ...profile.config.upscale };
  // upscaled pages keep their own format unless they're re-encoded; the
  // separate WebP/JPEG/PNG choice is gone
  upscale.format = "source";
  const encode = { ...emptyConfig.encode, ...profile.config.encode };
  // the width limit moved from the upscale step to every page
  const pages = { ...emptyConfig.pages, ...(profile.config.pages ?? { maxWidth: upscale.enabled ? upscale.maxWidth : 0 }) };
  const lowRes = profile.config.lowRes ?? { width: 0, action: "" };
  return { ...profile, config: { ...emptyConfig, ...profile.config, upscale, encode, pages, lowRes } };
}

const formatName = (e: Pick<Cfg["encode"], "format" | "lossy">) => (e.format === "avif" ? "AVIF" : e.format === "jxl" ? (e.lossy ? tr("JPEG XL, lossy") : "JPEG XL") : "");

// "Save pages as" choices: a format, and lossless or lossy for JPEG XL
type SaveAs = Cfg["encode"]["format"] | "jxl-lossy";
const saveAsOf = (e: Cfg["encode"]): SaveAs => (e.format === "jxl" && e.lossy ? "jxl-lossy" : e.format);
const presetName = (p: Preset) => (p === "fast" ? tr("Fast") : p === "max" ? tr("Smallest") : tr("Balanced"));

/** processingSummary is the one-line pipeline of a profile, e.g. "Upscale under 1400 px → AVIF". */
function processingSummary(c: Cfg) {
  const steps = [];
  if (c.pages?.maxWidth) steps.push(tr("shrink over {px} px", { px: c.pages.maxWidth }));
  if (c.upscale.enabled) steps.push(tr("Upscale under {px} px", { px: c.upscale.minWidth }));
  if (c.pages?.splitTall) steps.push(tr("Split strips over {ratio}× width", { ratio: c.pages.splitRatio || 3 }));
  if (c.encode?.format && c.encode.format !== "keep") steps.push(formatName(c.encode));
  return steps.length ? steps.join(" → ") : tr("Pages as downloaded");
}

function profileSummary(c: Cfg) {
  return [
    processingSummary(c),
    c.allowUpgrades ? tr("upgrades on") : tr("upgrades off"),
    c.preferredScanlators?.length ? tr("prefers {names}", { names: c.preferredScanlators.join(", ") }) : "",
    c.blockedScanlators?.length ? tr("blocks {names}", { names: c.blockedScanlators.join(", ") }) : "",
    c.cleanup?.enabled === false ? tr("no cleanup") : c.cleanup?.enabled ? tr("own cleanup") : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

export function ProfilesPage() {
  const { data, isLoading } = useProfiles();
  const qc = useQueryClient();
  const toast = useToast();
  const [editing, setEditing] = useState<Profile | null>(null);
  const [deleting, setDeleting] = useState<Profile | null>(null);

  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/profiles/{id}", { params: { path: { id: deleting.id } } }));
      qc.invalidateQueries({ queryKey: ["profiles"] });
      setDeleting(null);
    } catch (e) {
      toast.fromError(e);
    }
  };

  return (
    <>
      <PageHeader
        title={t("Profiles")}
        subtitle={t("How releases are chosen, upgraded, upscaled and cleaned for the series using a profile.")}
        actions={
          <Button
            variant="primary"
            icon={<Plus className="size-4" />}
            onClick={() => setEditing({ id: 0, name: "", isDefault: false, config: structuredClone(emptyConfig), createdAt: "", updatedAt: "" })}
          >{t("Add profile")}</Button>
        }
      />
      {isLoading && <Loading />}
      <div className="grid gap-3 md:grid-cols-2">
        {data?.map((p) => (
          <Card
            key={p.id}
            title={
              <span className="flex items-center gap-2">
                {p.name} {p.isDefault && <Badge tone="accent">{t("default")}</Badge>}
              </span>
            }
            actions={
              <>
                <IconButton title={t("Edit")} onClick={() => setEditing(p)}>
                  <Pencil className="size-4" />
                </IconButton>
                {!p.isDefault && (
                  <IconButton title={t("Delete")} onClick={() => setDeleting(p)}>
                    <Trash2 className="size-4" />
                  </IconButton>
                )}
              </>
            }
          >
            <p className="text-sm text-muted">{profileSummary(normalize(p).config)}</p>
          </Card>
        ))}
      </div>
      {editing && <ProfileEditor profile={editing} onClose={() => setEditing(null)} />}
      <Confirm
        open={!!deleting}
        title={t("Delete profile")}
        danger
        confirmLabel={t("Delete")}
        message={t("Delete {name}?", { name: deleting?.name ?? "" })}
        onConfirm={remove}
        onClose={() => setDeleting(null)}
      />
    </>
  );
}

/** Step is one numbered stage of page processing. */
function Step({ n, title, hint, action, children }: { n: number; title: string; hint?: string; action?: ReactNode; children?: ReactNode }) {
  return (
    <section className="rounded-lg border border-border">
      <header className="flex items-center gap-3 px-3.5 py-3">
        <span className="flex size-5.5 shrink-0 items-center justify-center rounded-full bg-panel-2 text-xs font-bold text-muted">{n}</span>
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-semibold">{title}</h3>
          {hint && <p className="text-xs text-muted">{hint}</p>}
        </div>
        {action}
      </header>
      {children && <div className="px-3.5 pb-3.5 sm:pl-12">{children}</div>}
    </section>
  );
}

function ProfileEditor({ profile, onClose }: { profile: Profile; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const globalCleanup = useSettingsDoc<S["Cleanup"]>("cleanup").value;
  const [base] = useState(() => normalize(profile));
  const [p, setP] = useState<Profile>(base);
  const [tab, setTab] = useState<Tab>("releases");
  const [previewing, setPreviewing] = useState(false);
  const [applyTo, setApplyTo] = useState<{ id: number; files: number; bytes: number } | null>(null);
  const [saving, setSaving] = useState(false);
  const cfg = p.config;
  const setCfg = (c: Partial<Cfg>) => setP({ ...p, config: { ...cfg, ...c } });
  const up = cfg.upscale;
  const setUp = (u: Partial<Cfg["upscale"]>) => setCfg({ upscale: { ...up, ...u } });
  const enc = cfg.encode;
  const setEnc = (e: Partial<Cfg["encode"]>) => setCfg({ encode: { ...enc, ...e } });
  const { data: modelCatalog, error: modelError } = useQuery({
    queryKey: ["upscaler-models"],
    queryFn: () => unwrap(api.GET("/api/v1/upscalers/models")),
    enabled: up.enabled,
    retry: false,
  });
  const modelOptions: UpscalerModel[] = modelCatalog?.models.some((model) => model.name === up.model)
    ? modelCatalog.models
    : [...(modelCatalog?.models ?? []), { name: up.model, description: "", scales: [], sources: [] }];
  const selectedModel = modelOptions.find((model) => model.name === up.model);
  const modelLocations = selectedModel?.sources.map((source) => source.available ? source.name : `${source.name} (${t("offline")})`).join(", ");
  const hasAvailableModel = modelCatalog?.models.some((model) => model.sources.some((source) => source.available));
  const modelHelp = modelError
    ? t("Could not load upscaler models.")
    : modelCatalog && !hasAvailableModel
      ? t("No upscaler is available right now. Remembered worker models are still listed.")
      : modelLocations || t("This saved model is unavailable.");

  const pg = cfg.pages;
  const setPages = (v: Partial<Cfg["pages"]>) => setCfg({ pages: { ...pg, ...v } });
  const junkSize = pg.junkUnder < 0 ? 0 : pg.junkUnder || defaultJunk;
  const encoding = enc.format !== "keep";
  const processing = up.enabled || encoding || pg.maxWidth > 0 || pg.splitTall;
  const changedProcessing =
    JSON.stringify([base.config.upscale, base.config.encode, base.config.pages.maxWidth, base.config.pages.splitTall, base.config.pages.splitRatio, base.config.pages.segmentRatio]) !==
    JSON.stringify([cfg.upscale, cfg.encode, pg.maxWidth, pg.splitTall, pg.splitRatio, pg.segmentRatio]);

  const save = async () => {
    setSaving(true);
    // the upscaler caps what it writes with the same limit
    const body = { ...p, config: { ...cfg, upscale: { ...up, maxWidth: pg.maxWidth } } };
    try {
      const saved = p.id
        ? await unwrap(api.PUT("/api/v1/profiles/{id}", { params: { path: { id: p.id } }, body }))
        : await unwrap(api.POST("/api/v1/profiles", { body }));
      qc.invalidateQueries({ queryKey: ["profiles"] });
      toast.success(tr("Profile saved"));
      // chapters already downloaded wait until asked; the server clears an
      // earlier opt-in when the processing settings change
      if (processing && changedProcessing && !saved.config.processExisting && p.id) {
        const est = await unwrap(api.GET("/api/v1/profiles/{id}/process-estimate", { params: { path: { id: saved.id } } }));
        if (est.files > 0) {
          setApplyTo({ id: saved.id, files: est.files, bytes: est.bytes });
          return;
        }
      }
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };

  const cleanupMode = cfg.cleanup?.enabled === undefined || cfg.cleanup?.enabled === null ? "inherit" : cfg.cleanup.enabled ? "custom" : "never";
  const tabs: { value: Tab; label: string; hint: string }[] = [
    { value: "releases", label: tr("Releases"), hint: cfg.allowUpgrades ? tr("upgrades on") : tr("upgrades off") },
    { value: "processing", label: tr("Page processing"), hint: processingSummary(cfg) },
    { value: "cleanup", label: tr("Cleanup"), hint: cleanupMode === "inherit" ? tr("Library default") : cleanupMode === "never" ? tr("Never") : tr("Custom") },
  ];
  const saveAs: { value: SaveAs; name: string; desc: string }[] = [
    { value: "keep", name: tr("As downloaded"), desc: up.enabled ? tr("Upscaled pages keep their format; the rest stay untouched.") : tr("Pages stay exactly as the source sent them.") },
    { value: "avif", name: "AVIF", desc: tr("Smallest files, typically 40–70% less. Lossy.") },
    { value: "jxl", name: "JPEG XL", desc: tr("Lossless. About 20% less for JPEG pages.") },
    { value: "jxl-lossy", name: tr("JPEG XL, lossy"), desc: tr("Smaller than lossless at a quality you choose. Keeps fine lines well.") },
  ];
  const lossy = enc.format === "avif" || (enc.format === "jxl" && enc.lossy);
  const c = compat[enc.format];

  return (
    <Modal
      open
      onClose={onClose}
      title={p.id ? t("Edit {name}", { name: profile.name }) : t("New profile")}
      size="xl"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <div className="mb-4 flex flex-wrap items-end gap-4">
        <Field label={t("Name")} className="min-w-56 flex-1">
          <Input value={p.name} onChange={(e) => setP({ ...p, name: e.target.value })} />
        </Field>
        <div className="pb-2">
          <Switch checked={p.isDefault} onChange={(v) => setP({ ...p, isDefault: v })} label={t("Default profile")} />
        </div>
      </div>
      <div className="flex flex-col gap-4 md:flex-row">
        <nav aria-label={t("Profile sections")} className="-mx-1 flex shrink-0 gap-1 overflow-x-auto md:mx-0 md:w-44 md:flex-col md:self-start">
          {tabs.map((x) => (
            <button
              key={x.value}
              type="button"
              aria-current={tab === x.value ? "page" : undefined}
              onClick={() => setTab(x.value)}
              className={clsx("flex min-w-0 flex-col rounded-md px-2.5 py-2 text-left", tab === x.value ? "bg-panel-2 text-fg" : "text-muted hover:text-fg")}
            >
              <span className={clsx("text-sm", tab === x.value ? "font-semibold" : "font-medium")}>{x.label}</span>
              <span className="truncate text-xs text-muted">{x.hint}</span>
            </button>
          ))}
        </nav>
        <div className="min-w-0 flex-1">
          {tab === "releases" && (
            <div className="flex flex-col gap-4">
              <Field label={<>{t("Preferred scanlators")} <span className="font-normal text-muted">· {t("first wins")}</span></>} help={t("Names or regular expressions. Sources are ranked by series priority before scanlators.")}>
                <TagInput value={cfg.preferredScanlators ?? []} onChange={(v) => setCfg({ preferredScanlators: v })} placeholder={t("e.g. ^Official$ or TCB")} />
              </Field>
              <Field label={t("Never download from")} help={t("Releases matching these are never downloaded.")}>
                <TagInput value={cfg.blockedScanlators ?? []} onChange={(v) => setCfg({ blockedScanlators: v })} placeholder={t("Add a name or pattern…")} />
              </Field>
              <Switch checked={cfg.allowUpgrades} onChange={(v) => setCfg({ allowUpgrades: v })} label={t("Replace a chapter when a better release appears")} />
              <label className="flex flex-wrap items-center gap-2 text-sm">
                {t("Skip chapters under")}
                <Input
                  className="w-20"
                  type="number"
                  min={0}
                  placeholder={t("any")}
                  value={cfg.minPages || ""}
                  onChange={(e) => setCfg({ minPages: Number(e.target.value) || 0 })}
                />
                {t("real pages")}
              </label>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                {t("If most pages are narrower than")}
                <Input
                  className="w-20"
                  type="number"
                  min={1}
                  aria-label={t("Low-resolution width (px)")}
                  placeholder={String(defaultLowRes)}
                  value={cfg.lowRes.width || ""}
                  onChange={(e) => setCfg({ lowRes: { ...cfg.lowRes, width: Number(e.target.value) || 0 } })}
                />
                px
                <Select
                  className="w-auto"
                  aria-label={t("What to do with low-resolution releases")}
                  value={cfg.lowRes.action || "keep"}
                  onChange={(e) => setCfg({ lowRes: { ...cfg.lowRes, action: e.target.value as Cfg["lowRes"]["action"] } })}
                >
                  <option value="retry">{t("try another source, else keep it")}</option>
                  <option value="keep">{t("keep it")}</option>
                  <option value="reject">{t("reject it")}</option>
                </Select>
              </div>
              <p className="rounded-md bg-bg px-3 py-2.5 text-xs text-muted">
                {t("Junk images don't count as pages. A chapter that is only junk (a “not available” banner, blank pages) is blocklisted on that source and the next source is tried. If none is left, the chapter stays wanted and History says why.")}
              </p>
            </div>
          )}

          {tab === "processing" && (
            <div className="flex flex-col gap-4">
              <div role="img" aria-label={t("What happens to each downloaded page")} className="flex flex-wrap items-center gap-2 rounded-lg border border-border bg-bg px-3.5 py-3 text-sm">
                <span className="rounded bg-panel-2 px-2.5 py-1 text-fg/80">{t("Downloaded page")}</span>
                {(junkSize > 0 || pg.maxWidth > 0) && (
                  <>
                    <ArrowRight className="size-4 text-muted" />
                    <span className="rounded bg-panel-2 px-2.5 py-1 text-fg/80">
                      {[junkSize > 0 && t("skip tiny"), pg.maxWidth > 0 && t("shrink over {px} px", { px: pg.maxWidth })].filter(Boolean).join(" · ")}
                    </span>
                  </>
                )}
                {up.enabled && (
                  <>
                    <ArrowRight className="size-4 text-muted" />
                    <span className="rounded bg-info/15 px-2.5 py-1 text-info">{t("Upscale if narrower than {px} px", { px: up.minWidth })}</span>
                  </>
                )}
                {pg.splitTall && (
                  <>
                    <ArrowRight className="size-4 text-muted" />
                    <span className="rounded bg-warn/15 px-2.5 py-1 text-warn">{t("Split strips over {ratio}× width", { ratio: pg.splitRatio || 3 })}</span>
                  </>
                )}
                <ArrowRight className="size-4 text-muted" />
                <span className={clsx("rounded px-2.5 py-1", encoding ? "bg-accent/15 text-accent-2" : "bg-panel-2 text-fg/80")}>
                  {encoding
                    ? t("Save as {format} · {speed}", { format: formatName(enc), speed: presetName(enc.preset).toLowerCase() })
                    : up.enabled || pg.splitTall
                      ? t("Keep each page's format")
                      : t("Saved as downloaded")}
                </span>
                <span className="flex-1" />
                {processing && <span className="text-xs text-muted">{cfg.processTiming === "inline" ? t("before import") : t("in the background")}</span>}
                {processing && <Button size="sm" onClick={() => setPreviewing(true)}>{t("Preview on a chapter…")}</Button>}
              </div>

              <Step n={1} title={t("Page size")} hint={t("Applies to every page, with or without upscaling.")}>
                <div className="grid gap-4 md:grid-cols-2">
                  <Field label={t("Treat images as junk under (px)")} help={t("Spacers, logos and tracking pixels: never upscaled or re-encoded. Empty = off.")}>
                    <Input
                      type="number"
                      min={1}
                      placeholder={t("off")}
                      value={junkSize || ""}
                      onChange={(e) => setPages({ junkUnder: Number(e.target.value) > 0 ? Number(e.target.value) : -1 })}
                    />
                  </Field>
                  <Field label={t("Shrink pages wider than (px)")} help={t("Two-page spreads may be twice as wide. Empty keeps full size.")}>
                    <Input type="number" min={0} placeholder={t("no limit")} value={pg.maxWidth || ""} onChange={(e) => setPages({ maxWidth: Number(e.target.value) || 0 })} />
                  </Field>
                  <div className="md:col-span-2">
                    <Switch
                      checked={pg.removeJunk}
                      disabled={!junkSize}
                      onChange={(v) => setPages({ removeJunk: v })}
                      label={<>{t("Remove junk images from the chapter")} <span className="text-xs text-muted">{t("off: they stay in the file, untouched")}</span></>}
                    />
                  </div>
                </div>
              </Step>

              <Step
                n={2}
                title={t("Upscale small pages")}
                hint={up.enabled ? t("Wider pages skip this step.") : t("Off. Turn on to sharpen low-resolution scans.")}
                action={<Switch checked={up.enabled} onChange={(v) => setUp({ enabled: v })} label={<span className="sr-only">{t("Upscale small pages")}</span>} />}
              >
                {up.enabled && (
                  <div className="grid gap-4 md:grid-cols-2">
                    <Field label={t("Model")} help={modelHelp}>
                      <Select value={up.model} onChange={(e) => setUp({ model: e.target.value })}>
                        {modelOptions.map((m) => {
                          const locations = m.sources.map((source) => source.available ? source.name : `${source.name} (${t("offline")})`).join(", ");
                          return (
                            <option key={m.name} value={m.name} title={m.description}>
                              {m.name}{" — "}{locations || t("unavailable")}
                            </option>
                          );
                        })}
                      </Select>
                    </Field>
                    <Field label={t("When narrower than (px)")} help={t("iPad portrait: ~1600–2000. Two-page spreads count each half.")}>
                      <Input type="number" min={1} value={up.minWidth} onChange={(e) => setUp({ minWidth: Number(e.target.value) })} />
                    </Field>
                    <div className="flex flex-wrap items-center gap-3 md:col-span-2">
                      <span className="text-sm font-medium">{t("Noise reduction")}</span>
                      <Segmented
                        label={t("Noise reduction")}
                        value={up.noise}
                        onChange={(noise) => setUp({ noise })}
                        options={[{ value: -1, label: t("Off") }, ...[0, 1, 2, 3].map((n) => ({ value: n, label: String(n) }))]}
                      />
                      <span className="text-xs text-muted">{t("Removes JPEG artefacts from scans")}</span>
                    </div>
                  </div>
                )}
              </Step>

              <Step
                n={3}
                title={t("Split tall pages")}
                hint={pg.splitTall ? t("Cuts near quiet rows after upscaling, before saving.") : t("Off. Long webtoon strips stay as one image.")}
                action={<Switch checked={pg.splitTall} onChange={(v) => setPages({ splitTall: v })} label={<span className="sr-only">{t("Split tall pages")}</span>} />}
              >
                {pg.splitTall && (
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label={t("Split pages taller than (× width)")} help={t("Only webtoon strips. Manga pages are about 1.4× as tall as wide, even after upscaling.")}>
                      <Input type="number" min={1} step={0.1} placeholder="3" value={pg.splitRatio || ""} onChange={(e) => setPages({ splitRatio: Number(e.target.value) || 0 })} />
                    </Field>
                    <Field label={t("Segment height (× width)")} help={t("Pieces keep full width and cut near quiet rows. A phone screen is about 2.2.")}>
                      <Input type="number" min={0.5} step={0.1} placeholder="2" value={pg.segmentRatio || ""} onChange={(e) => setPages({ segmentRatio: Number(e.target.value) || 0 })} />
                    </Field>
                  </div>
                )}
              </Step>

              <Step n={4} title={t("Save pages as")} hint={encoding && (up.enabled || pg.splitTall) ? t("Changed pages go straight into it, lossless.") : undefined}>
                <div role="radiogroup" aria-label={t("Save pages as")} className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
                  {saveAs.map((f) => (
                    <label key={f.value} className={clsx("flex cursor-pointer flex-col gap-1 rounded-md border p-2.5", saveAsOf(enc) === f.value ? "border-accent bg-accent/8" : "border-border hover:border-muted")}>
                      <span className="flex items-center gap-2 text-sm font-semibold">
                        <input
                          type="radio"
                          name="save-as"
                          className="accent-accent"
                          checked={saveAsOf(enc) === f.value}
                          onChange={() => setEnc({ format: f.value === "jxl-lossy" ? "jxl" : f.value, lossy: f.value === "jxl-lossy", progressive: false })}
                        />
                        {f.name}
                      </span>
                      <span className="text-xs text-muted">{f.desc}</span>
                    </label>
                  ))}
                </div>
                {!encoding && up.enabled && (
                  <Field label={t("Upscaled page quality")} help={t("For JPEG and WebP pages; PNG stays lossless.")} className="mt-4 max-w-56">
                    <Input type="number" min={1} max={100} value={up.quality} onChange={(e) => setUp({ quality: Number(e.target.value) })} />
                  </Field>
                )}
                {encoding && (
                  <div className="mt-4 grid gap-4 rounded-md bg-bg p-3.5 md:grid-cols-2">
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 md:col-span-2">
                      <span className="text-sm font-medium">{t("Speed")}</span>
                      <Segmented
                        label={t("Speed")}
                        value={enc.preset}
                        onChange={(preset) => setEnc({ preset })}
                        options={(["fast", "balanced", "max"] as const).map((v) => ({ value: v, label: presetName(v) }))}
                      />
                      <span className="text-xs text-muted">{t("Smallest is much slower")}</span>
                    </div>
                    {lossy && (
                      <Field label={t("Quality")} help={t("1–100, empty follows speed")}>
                        <Input
                          type="number"
                          min={0}
                          max={100}
                          placeholder={t("Auto ({q})", { q: presetQuality[enc.format === "jxl" ? "jxl" : "avif"][enc.preset] })}
                          value={enc.quality || ""}
                          onChange={(e) => setEnc({ quality: Number(e.target.value) || 0 })}
                        />
                      </Field>
                    )}
                    <Field label={t("Keep the original unless it saves (%)")} help={up.enabled ? t("Upscaled pages are always converted") : undefined}>
                      <Input type="number" min={0} max={90} value={enc.minSavingsPct} onChange={(e) => setEnc({ minSavingsPct: Number(e.target.value) })} />
                    </Field>
                    <div className="flex flex-col gap-2.5 md:col-span-2">
                      {enc.format === "avif" && (
                        <Switch checked={enc.grayscale} onChange={(v) => setEnc({ grayscale: v })} label={t("Store black-and-white pages without colour (smaller)")} />
                      )}
                      <Switch checked={enc.recycleOriginals} onChange={(v) => setEnc({ recycleOriginals: v })} label={t("Keep replaced originals in the recycle bin")} />
                    </div>
                    {c && (
                      <p className="border-t border-border pt-2.5 text-xs text-muted md:col-span-2">
                        <span className="font-semibold text-ok">{t("Opens in")}</span> {c.yes} · <span className="font-semibold text-warn">{t("Not in")}</span> {c.no}
                        {c.note && <> · {c.note}</>} · {t("mangarr checks Komga after the first chapter and pauses if it can't read it.")}
                      </p>
                    )}
                  </div>
                )}
              </Step>

              {processing && (
                <Step
                  n={5}
                  title={t("When")}
                  hint={t("In the background, chapters are readable right away; night hours are in Settings → Schedule.")}
                  action={
                    <Segmented
                      label={t("When")}
                      value={cfg.processTiming || "background"}
                      onChange={(processTiming) => setCfg({ processTiming })}
                      options={[
                        { value: "background", label: t("After import, in the background") },
                        { value: "inline", label: t("Before import") },
                      ]}
                    />
                  }
                />
              )}
            </div>
          )}

          {tab === "cleanup" && (
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-center gap-3">
                <h3 className="flex-1 text-sm font-semibold">{t("Cleanup of read chapters")}</h3>
                <Segmented
                  label={t("Cleanup of read chapters")}
                  value={cleanupMode}
                  onChange={(m) => setCfg({ cleanup: m === "inherit" ? {} : m === "never" ? { enabled: false } : { ...cfg.cleanup, enabled: true } })}
                  options={[
                    { value: "inherit", label: globalCleanup ? (globalCleanup.enabled ? t("Library default (on)") : t("Library default (off)")) : t("Library default") },
                    { value: "custom", label: t("Custom") },
                    { value: "never", label: t("Never") },
                  ]}
                />
              </div>
              {cleanupMode === "custom" && (
                <div className="grid gap-4 md:grid-cols-2">
                  <Field label={t("Keep the last read chapters")} help={globalCleanup ? t("Empty uses the library default ({n})", { n: globalCleanup.keepLastRead }) : undefined}>
                    <Input
                      type="number"
                      min={0}
                      placeholder={globalCleanup ? String(globalCleanup.keepLastRead) : ""}
                      value={cfg.cleanup?.keepLastRead ?? ""}
                      onChange={(e) => setCfg({ cleanup: { ...cfg.cleanup, keepLastRead: e.target.value === "" ? undefined : Number(e.target.value) } })}
                    />
                  </Field>
                  <Field label={t("Delete after (days)")} help={globalCleanup ? t("Empty uses the library default ({n})", { n: globalCleanup.graceDays }) : undefined}>
                    <Input
                      type="number"
                      min={0}
                      placeholder={globalCleanup ? String(globalCleanup.graceDays) : ""}
                      value={cfg.cleanup?.graceDays ?? ""}
                      onChange={(e) => setCfg({ cleanup: { ...cfg.cleanup, graceDays: e.target.value === "" ? undefined : Number(e.target.value) } })}
                    />
                  </Field>
                </div>
              )}
              {cleanupMode === "never" && <p className="text-sm text-muted">{t("Series using this profile keep every downloaded chapter.")}</p>}
            </div>
          )}
        </div>
      </div>
      {previewing && <PipelinePreview upscale={up} encode={enc} pages={pg} onClose={() => setPreviewing(false)} />}
      <Confirm
        open={!!applyTo}
        title={t("Process existing chapters?")}
        confirmLabel={applyTo ? t("Queue {n} chapters", { n: applyTo.files }) : ""}
        cancelLabel={t("Not now")}
        message={
          applyTo
            ? t("{files} chapters ({size}) already downloaded with this profile weren't processed with these settings. Queue them for processing in the background? New chapters are processed automatically either way.", {
                files: applyTo.files,
                size: bytes(applyTo.bytes),
              })
            : ""
        }
        onConfirm={async () => {
          if (!applyTo) return;
          try {
            await unwrap(
              api.PUT("/api/v1/profiles/{id}", {
                params: { path: { id: applyTo.id } },
                body: { ...p, id: applyTo.id, config: { ...cfg, upscale: { ...up, maxWidth: pg.maxWidth }, processExisting: true } },
              }),
            );
            qc.invalidateQueries({ queryKey: ["profiles"] });
            toast.success(tr("Existing chapters will be processed in the background"));
          } catch (e) {
            toast.fromError(e);
          }
          setApplyTo(null);
          onClose();
        }}
        onClose={() => (setApplyTo(null), onClose())}
      />
    </Modal>
  );
}

/** PipelinePreview runs the unsaved upscale and encode settings on three pages of a chapter. */
function PipelinePreview({ upscale, encode, pages, onClose }: { upscale: Cfg["upscale"]; encode: Cfg["encode"]; pages: Cfg["pages"]; onClose: () => void }) {
  const { data: series } = useSeriesList();
  const [seriesId, setSeriesId] = useState(0);
  const { data: chapters } = useChapters(seriesId);
  const withFiles = (chapters ?? []).filter((c) => c.file);
  const [chapterId, setChapterId] = useState(0);
  // encodeOnly retries without upscaling when no upscaler is online
  const [encodeOnly, setEncodeOnly] = useState(false);
  const encoding = encode.format !== "keep";
  const upscaling = upscale.enabled && !encodeOnly;
  const run = useMutation({
    mutationFn: () =>
      unwrap(api.POST("/api/v1/processing/preview", { body: { chapterId: chapterId || withFiles[0]?.id, encode, pages, upscale: upscaling ? upscale : undefined } })),
  });
  const res = run.data;
  const noUpscaler = run.error instanceof ApiError && run.error.status === 409;
  const img = (i: number, v: "original" | "encoded") => apiUrl(`api/v1/processing/preview/${res!.token}/${i}/${v}`);
  const steps = [pages.maxWidth > 0 && t("shrink"), upscaling && t("upscale"), pages.splitTall && t("split"), encoding && formatName(encode)].filter(Boolean).join(" → ");
  return (
    <Modal open onClose={onClose} title={<span className="flex flex-wrap items-baseline gap-x-3">{t("Preview: {steps}", { steps })}<span className="text-xs font-normal text-muted">{t("Uses the unsaved settings")}</span></span>} size="xl">
      <div className="mb-4 flex flex-wrap items-end gap-2">
        <Field label={t("Series")} className="min-w-48 flex-1">
          <Select value={seriesId} onChange={(e) => (setSeriesId(Number(e.target.value)), setChapterId(0))}>
            <option value={0}>{t("Pick a series…")}</option>
            {series?.map((s) => (
              <option key={s.id} value={s.id}>
                {s.title}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Chapter")} className="w-40">
          <Select value={chapterId || withFiles[0]?.id || 0} onChange={(e) => setChapterId(Number(e.target.value))}>
            {withFiles.map((c) => (
              <option key={c.id} value={c.id}>
                {c.number}
              </option>
            ))}
          </Select>
        </Field>
        <Button variant="primary" disabled={!withFiles.length} loading={run.isPending} onClick={() => run.mutate()}>{t("Run on 3 pages")}</Button>
      </div>
      {noUpscaler ? (
        <div role="alert" className="flex gap-3 rounded-lg border border-warn/40 bg-warn/10 p-3.5">
          <TriangleAlert className="mt-0.5 size-4.5 shrink-0 text-warn" />
          <div className="flex flex-col gap-1 text-sm">
            <strong className="font-semibold">{t("No upscaler is available right now")}</strong>
            <span className="text-fg/80">{(run.error as ApiError).message}</span>
            <span className="mt-1 flex flex-wrap gap-3">
              <Link to="/system/workers" className="font-semibold text-accent-2 hover:underline">{t("Open Workers")}</Link>
              {encoding && (
                <button type="button" className="font-semibold text-accent-2 hover:underline" onClick={() => (setEncodeOnly(true), run.reset())}>
                  {t("Preview {format} only", { format: formatName(encode) })}
                </button>
              )}
            </span>
          </div>
        </div>
      ) : (
        run.error && <ErrorBox error={run.error} />
      )}
      {res && (
        <>
          <p className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-bg px-3 py-2 text-sm text-muted">
            {res.upscaler && <span><b className="font-semibold text-fg">{res.upscaler}</b> · {t("upscale {s} s", { s: res.upscaleSeconds.toFixed(1) })}</span>}
            {res.engine && <span><b className="font-semibold text-fg">{res.engine}</b> · {t("encode {s} s", { s: res.encodeSeconds.toFixed(1) })}</span>}
            <span className="flex-1" />
            <a className="text-accent-2 hover:underline" href={apiUrl(`api/v1/processing/preview/${res.token}/sample.cbz`)}>{t("Download sample CBZ")}</a>
          </p>
          <div className="flex flex-col gap-4">
            {res.pages.map((pg) => {
              const smaller = Math.round(100 - (100 * pg.encodedSize) / Math.max(pg.originalSize, 1));
              const scale = pg.width ? pg.resultWidth / pg.width : 1;
              const chips = pg.junk
                ? [t("junk image, left alone")]
                : [
                    pg.upscaled ? t("upscaled {x}×", { x: scale.toFixed(1) }) : pg.shrunk ? t("shrunk from {px} px", { px: pg.width }) : upscaling ? t("wide enough, not upscaled") : "",
                    pg.split ? t("split to {px} px tall", { px: pg.resultHeight }) : "",
                    smaller >= 0 ? t("{n}% smaller", { n: smaller }) : t("{n}% larger", { n: -smaller }),
                  ].filter(Boolean);
              return (
                <div key={pg.index} className="grid grid-cols-2 gap-3">
                  {(["original", "encoded"] as const).map((v) => (
                    <figure key={v} className="flex flex-col gap-1.5">
                      <a href={img(pg.index, v)} target="_blank" rel="noreferrer">
                        <img src={img(pg.index, v)} alt={`${v} ${pg.name}`} className="w-full rounded border border-border" loading="lazy" />
                      </a>
                      <figcaption className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
                        {v === "original" ? (
                          <>
                            <span className="font-semibold text-fg/80">{t("Original")}</span>
                            {pg.width} × {pg.height} · {pg.originalFormat.toUpperCase()} · {bytes(pg.originalSize)}
                          </>
                        ) : (
                          <>
                            <span className="font-semibold text-fg/80">{t("Result")}</span>
                            {pg.resultWidth} × {pg.resultHeight} · {pg.encodedFormat.toUpperCase()} · {bytes(pg.encodedSize)}
                            <Badge tone={pg.upscaled ? "info" : "default"}>{chips.join(" · ")}</Badge>
                          </>
                        )}
                      </figcaption>
                    </figure>
                  ))}
                </div>
              );
            })}
          </div>
        </>
      )}
    </Modal>
  );
}
