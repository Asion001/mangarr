import { t } from "../../lib/i18n/core";
import { type S } from "../../api/client";
import { useCatalogs, useProfiles, useRootFolders } from "../../api/queries";
import { Badge, Button, Card, ErrorBox, Field, Input, Loading, PageHeader, SaveBar, Select, Switch } from "../../components/ui";
import { LanguageSelect } from "../../components/LanguageSelect";
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
  const { data: roots } = useRootFolders();
  // the language's own folder, else the one the automatic folder would make
  const folderFor = (lang: string) => {
    const own = roots?.find((root) => root.language.toLowerCase() === lang.toLowerCase());
    const auto = roots?.find((root) => root.language === "*");
    return own?.path ?? (auto && lang ? `${auto.path.replace(/\/+$/, "")}/${lang.toLowerCase()}` : "—");
  };
  const { data: profiles } = useProfiles();
  const v = doc.value;
  const catalogLangs = Array.from(new Set((catalogs?.items ?? []).filter((c) => !c.hidden).map((c) => c.lang))).filter((l) => l && l !== "all" && l !== "multi").sort((a, b) => languageName(a).localeCompare(languageName(b)));
  const qs = v?.quickSearch;
  const setQS = (p: Partial<Sources["quickSearch"]>) => v && doc.patch({ quickSearch: { ...v.quickSearch, ...p } });
  const setT = (p: Partial<Throttle>) => v && doc.patch({ throttle: { ...v.throttle, ...p } });
  const preset = presets[v?.throttle.preset || "normal"];
  const languageDefaults = v?.languageDefaults ?? [];
  const patchLanguage = (index: number, patch: Partial<Sources["languageDefaults"][number]>) => {
    if (!v) return;
    doc.patch({ languageDefaults: languageDefaults.map((item, i) => i === index ? { ...item, ...patch } : item) });
  };
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
              <div className="flex flex-wrap items-center gap-1.5 text-sm">
                <span className="mr-1 text-muted">{t("Search languages by default:")}</span>
                {catalogLangs.map((l) => {
                  const on = (v.defaultLanguages ?? []).includes(l);
                  return (
                    <button
                      key={l}
                      type="button"
                      aria-pressed={on}
                      disabled={!!doc.lock("defaultLanguages")}
                      onClick={() => doc.patch({ defaultLanguages: on ? v.defaultLanguages.filter((x) => x !== l) : [...(v.defaultLanguages ?? []), l] })}
                      className={`rounded border px-2 py-0.5 text-xs ${on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted hover:text-fg"}`}
                    >
                      {languageName(l)}
                    </button>
                  );
                })}
                {!(v.defaultLanguages ?? []).length && <span className="text-xs text-muted">{t("(none selected = all languages)")}</span>}
              </div>
            </div>
          </Card>
          <Card title={t("Language defaults")} className="mb-6">
            <p className="mb-4 text-sm text-muted">{t("Choose the source order, profile and reading direction used for each language.")}</p>
            <div className="flex flex-col gap-4">
              {languageDefaults.map((item, index) => {
                const chosen = new Set(item.sources);
                return (
                  <div key={`${item.language}-${index}`} className="rounded-lg border border-border p-3">
                    <div className="mb-3 grid gap-3 md:grid-cols-4">
                      <Field label={t("Language")}>
                        <LanguageSelect value={item.language} onChange={(language) => patchLanguage(index, { language })} />
                      </Field>
                      <Field label={t("Folder")} help={t("Set in Media management")}>
                        <span className="block truncate py-2 font-mono text-xs text-muted">{folderFor(item.language)}</span>
                      </Field>
                      <Field label={t("Profile")}>
                        <Select value={item.profileId || 0} onChange={(e) => patchLanguage(index, { profileId: Number(e.target.value) })}>
                          <option value={0}>{t("Default")}</option>
                          {profiles?.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}
                        </Select>
                      </Field>
                      <Field label={t("Reading direction")}>
                        <Select value={item.readingDirection || ""} onChange={(e) => patchLanguage(index, { readingDirection: e.target.value as typeof item.readingDirection })}>
                          <option value="">{t("Automatic")}</option>
                          <option value="rtl">{t("Right to left (manga)")}</option>
                          <option value="ltr">{t("Left to right")}</option>
                          <option value="webtoon">{t("Webtoon (long strip)")}</option>
                        </Select>
                      </Field>
                    </div>
                    <div className="mb-2 text-sm font-medium">{t("Source priority")}</div>
                    <div className="mb-2 flex flex-col gap-1">
                      {item.sources.map((key, sourceIndex) => {
                        const catalog = catalogs?.items.find((candidate) => `${candidate.moduleId}:${candidate.id}` === key);
                        return (
                          <div key={key} className="flex items-center gap-2 rounded bg-panel-2 px-2 py-1 text-sm">
                            <span className="w-5 text-muted">{sourceIndex + 1}.</span>
                            <span className="flex-1">{catalog?.displayName ?? key}</span>
                            {catalog?.lang && <Badge>{languageName(catalog.lang)}</Badge>}
                            <Button size="sm" disabled={sourceIndex === 0} onClick={() => patchLanguage(index, { sources: swap(item.sources, sourceIndex, sourceIndex - 1) })}>{t("Up")}</Button>
                            <Button size="sm" disabled={sourceIndex === item.sources.length - 1} onClick={() => patchLanguage(index, { sources: swap(item.sources, sourceIndex, sourceIndex + 1) })}>{t("Down")}</Button>
                            <Button size="sm" onClick={() => patchLanguage(index, { sources: item.sources.filter((value) => value !== key) })}>{t("Remove")}</Button>
                          </div>
                        );
                      })}
                    </div>
                    <div className="flex gap-2">
                      <Select value="" onChange={(e) => e.target.value && patchLanguage(index, { sources: [...item.sources, e.target.value] })}>
                        <option value="">{t("Add source…")}</option>
                        {catalogs?.items.filter((catalog) => !catalog.hidden && !chosen.has(`${catalog.moduleId}:${catalog.id}`)).map((catalog) => (
                          <option key={`${catalog.moduleId}:${catalog.id}`} value={`${catalog.moduleId}:${catalog.id}`}>{catalog.displayName} ({languageName(catalog.lang)})</option>
                        ))}
                      </Select>
                      <Button onClick={() => doc.patch({ languageDefaults: languageDefaults.filter((_, i) => i !== index) })}>{t("Remove")}</Button>
                    </div>
                  </div>
                );
              })}
              <Button
                onClick={() => {
                  const known = Array.from(new Set(["en", "ru", ...(catalogs?.items ?? []).map((catalog) => catalog.lang)])).filter((language) => language !== "all" && language !== "multi");
                  const language = known.find((candidate) => !languageDefaults.some((item) => item.language === candidate)) ?? "";
                  doc.patch({ languageDefaults: [...languageDefaults, { language, sources: [], profileId: 0, readingDirection: "" }] });
                }}
              >{t("Add language")}</Button>
            </div>
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

function swap<T>(values: T[], a: number, b: number): T[] {
  const copy = [...values];
  [copy[a], copy[b]] = [copy[b], copy[a]];
  return copy;
}
