import { t } from "../../lib/i18n/core";
import { LanguageList } from "../../components/LanguageChooser";
import { Link } from "react-router";
import { type S } from "../../api/client";
import { useCatalogs } from "../../api/queries";
import { Card, ErrorBox, Field, Input, Loading, PageHeader, SaveBar, Select, Switch } from "../../components/ui";
import { languageName } from "../../lib/format";
import { useSettingsDoc } from "./useSettingsDoc";

type Sources = S["Sources"];
type Throttle = S["ThrottleConfig"];

// mirrors sourcegov.Presets (shown as placeholders)
const presets: Record<string, Partial<Throttle>> = {
  fast: { maxConcurrent: 5 },
  normal: { requestsPerMinute: 120, burst: 10, jitterMs: 250, maxConcurrent: 3, chapterGapMinSec: 2, chapterGapMaxSec: 6, refreshGapMinSec: 1, refreshGapMaxSec: 4 },
  gentle: { requestsPerMinute: 30, burst: 3, minDelayMs: 500, jitterMs: 1500, maxConcurrent: 1, chapterGapMinSec: 10, chapterGapMaxSec: 30, refreshGapMinSec: 5, refreshGapMaxSec: 15 },
};

const throttleFields: { key: keyof Throttle; label: string; help?: string }[] = [
  { key: "requestsPerMinute", label: "Requests per minute", help: "Per catalog, on top of the extension's own limit" },
  { key: "burst", label: "Burst" },
  { key: "maxConcurrent", label: "Parallel requests" },
  { key: "minDelayMs", label: "Minimum gap (ms)" },
  { key: "jitterMs", label: "Random extra delay (ms)" },
  { key: "chapterGapMinSec", label: "Pause between chapters, min (s)" },
  { key: "chapterGapMaxSec", label: "Pause between chapters, max (s)" },
  { key: "refreshGapMinSec", label: "Pause between series checks, min (s)" },
  { key: "refreshGapMaxSec", label: "Pause between series checks, max (s)" },
];

export function SearchSettingsPage() {
  const doc = useSettingsDoc<Sources>("sources");
  const { data: catalogs } = useCatalogs();
  const v = doc.value;
  const catalogLangs = Array.from(new Set((catalogs?.items ?? []).filter((c) => !c.hidden).map((c) => c.lang))).filter((l) => l && l !== "all" && l !== "multi").sort((a, b) => languageName(a).localeCompare(languageName(b)));
  const qs = v?.quickSearch;
  const setQS = (p: Partial<Sources["quickSearch"]>) => v && doc.patch({ quickSearch: { ...v.quickSearch, ...p } });
  const setT = (p: Partial<Throttle>) => v && doc.patch({ throttle: { ...v.throttle, ...p } });
  const preset = presets[v?.throttle.preset || "normal"];
  return (
    <>
      <PageHeader
        title={t("Search & throttling")}
        subtitle={t("How catalogs are searched when adding series, and how gently mangarr talks to sites.")}
      />
      {doc.isLoading && <Loading />}
      {doc.error && <ErrorBox error={doc.error} />}
      {v && qs && (
        <>
          <Card title={t("Catalogs")} className="mb-6">
            <div className="flex flex-col gap-3">
              <Switch
                checked={v.hideNsfw}
                env={doc.lock("hideNsfw")}
                onChange={(hideNsfw) => doc.patch({ hideNsfw })}
                label={t("Hide NSFW catalogs everywhere (search, browse, add series)")}
              />
              <div className="flex max-w-md flex-col gap-2 text-sm">
                <span className="font-medium">{t("Search languages by default")}</span>
                <span className="text-muted">{t("Add series and source search look in these languages first.")}</span>
                <LanguageList
                  value={v.defaultLanguages ?? []}
                  onChange={(defaultLanguages) => doc.patch({ defaultLanguages })}
                  options={catalogLangs}
                  disabled={!!doc.lock("defaultLanguages")}
                  empty={t("None chosen: every language is searched.")}
                />
              </div>
            </div>
          </Card>
          <Card title={t("Language defaults")} className="mb-6">
            <p className="text-sm text-muted">
              {t("Each language's ordered source list, profile and reading direction now live in one place.")}{" "}
              <Link to="/sources/defaults" className="text-accent-2 hover:underline">{t("Edit them in Sources › Language defaults")}</Link>
            </p>
          </Card>
          <Card title={t("Quick search")} className="mb-6">
            <div className="flex flex-col gap-4">
              <Switch
                checked={qs.enabled}
                env={doc.lock("quickSearch.enabled")}
                onChange={(x) => setQS({ enabled: x })}
                label={t("Search catalogs one by one (by priority) and stop at the first confident match")}
              />
              <div className="grid gap-4 md:grid-cols-2">
                <Field label={t("Match threshold")} help={t("Title similarity from 0 to 1 that counts as the same series")} env={doc.lock("quickSearch.threshold")}>
                  <Input type="number" step={0.01} min={0.5} max={1} value={qs.threshold} onChange={(e) => setQS({ threshold: Number(e.target.value) })} />
                </Field>
                <Field label={t("Time limit (s)")} help={t("Stop searching one by one after this")} env={doc.lock("quickSearch.budgetSeconds")}>
                  <Input type="number" min={5} value={qs.budgetSeconds} onChange={(e) => setQS({ budgetSeconds: Number(e.target.value) })} />
                </Field>
                <Field label={t("Chapter counts")} help={t("Each count is one extra request to the site")} env={doc.lock("quickSearch.details")}>
                  <Select value={qs.details} onChange={(e) => setQS({ details: e.target.value as typeof qs.details })}>
                    <option value="none">{t("Don't fetch")}</option>
                    <option value="best">{t("Best match only")}</option>
                    <option value="top">{t("Top results")}</option>
                  </Select>
                </Field>
                {qs.details === "top" && (
                  <Field label={t("Results with chapter counts")} env={doc.lock("quickSearch.topN")}>
                    <Input type="number" min={1} max={5} value={qs.topN} onChange={(e) => setQS({ topN: Number(e.target.value) })} />
                  </Field>
                )}
              </div>
            </div>
          </Card>
          <Card title={t("Throttling")}>
            <p className="mb-4 text-sm text-muted">{t("Applies to every catalog (override per catalog in Sources → Catalogs).") + " "}<b>{t("Fast")}</b>{" " + t("behaves like Mihon: no extra pauses.") + " "}<b>{t("Normal")}</b>{" " + t("adds short random pauses between chapters and checks.") + " "}<b>{t("Gentle")}</b>{" " + t("is for sites that block easily. Sites that answer with 429 or Cloudflare errors are paused automatically (5 minutes, doubling up to 2 hours).")}</p>
            <div className="grid gap-4 md:grid-cols-3">
              <Field label={t("Preset")} env={doc.lock("throttle.preset")}>
                <Select value={v.throttle.preset || "normal"} onChange={(e) => doc.patch({ throttle: { preset: e.target.value as Throttle["preset"] } })}>
                  <option value="gentle">{t("Gentle")}</option>
                  <option value="normal">{t("Normal")}</option>
                  <option value="fast">{t("Fast (like Mihon)")}</option>
                </Select>
              </Field>
              {throttleFields.map((f) => (
                <Field key={f.key} label={f.label} help={f.help} env={doc.lock(`throttle.${f.key}`)}>
                  <Input
                    type="number"
                    min={0}
                    placeholder={String(preset[f.key] ?? 0)}
                    value={(v.throttle[f.key] as number | undefined) || ""}
                    onChange={(e) => setT({ [f.key]: e.target.value === "" ? 0 : Number(e.target.value) })}
                  />
                </Field>
              ))}
            </div>
          </Card>
        </>
      )}
      <SaveBar dirty={doc.dirty} saving={doc.saving} onSave={() => void doc.save()} onDiscard={doc.reset} />
    </>
  );
}
