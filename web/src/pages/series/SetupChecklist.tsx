import { t } from "../../lib/i18n/core";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { Check } from "lucide-react";
import { api, basePath, unwrap } from "../../api/client";
import { useCatalogs, useHealth, useModules } from "../../api/queries";
import { Button, Input, Progress } from "../../components/ui";
import { LanguageSelect } from "../../components/LanguageSelect";
import { LanguageList, useSourcesSettings } from "../../components/LanguageChooser";
import { useToast } from "../../lib/toast";
import { browserLanguages } from "../../lib/format";

const skippedKey = "mangarr:setup-skipped";
const readSkipped = (): string[] => {
  try {
    return JSON.parse(localStorage.getItem(skippedKey) ?? "[]");
  } catch {
    return [];
  }
};

type Step = { id: string; title: string; kind: "required" | "recommended" | "optional"; body: string; done: boolean; action?: ReactNode };

/**
 * SetupChecklist walks an admin through a fresh install from the Series
 * page: the health checks say what's missing, and each step can be done
 * right here or links to its settings page.
 */
export function SetupChecklist() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: health } = useHealth(true);
  const { data: modules } = useModules();
  const { data: catalogs } = useCatalogs();
  const sources = useSourcesSettings();
  const catalogLangs = [...new Set((catalogs?.items ?? []).filter((c) => !c.hidden).map((c) => c.lang).filter((l) => l && l !== "all" && l !== "multi"))];
  const [langs, setLangs] = useState<string[] | null>(null);
  const reading = langs ?? browserLanguages(catalogLangs);
  const [skipped, setSkipped] = useState(readSkipped);
  const [busy, setBusy] = useState("");
  const [path, setPath] = useState("");
  const [folderLang, setLang] = useState("");
  const lang = folderLang || (sources.data?.defaultLanguages ?? [])[0] || reading[0] || "en";
  if (!health || !modules || !sources.data) return null;
  // optional modules are a choice, so Health says nothing about them
  const has = (kind: string) => modules.some((m) => m.kind === kind && m.enabled);
  // "No … is configured" checks mean setup is missing; the same sources also
  // report failing sites or full disks, which aren't setup steps
  const missing = (source: string) => health.checks.some((c) => c.source === source && c.message.startsWith("No "));
  const skip = (id: string) => {
    const next = [...skipped, id];
    setSkipped(next);
    try {
      localStorage.setItem(skippedKey, JSON.stringify(next));
    } catch {
      /* private mode: skipped for this visit only */
    }
  };
  const run = async (id: string, fn: () => Promise<unknown>) => {
    setBusy(id);
    try {
      await fn();
      await unwrap(api.POST("/api/v1/health/check"));
      await qc.invalidateQueries({ queryKey: ["health"] });
      qc.invalidateQueries({ queryKey: ["rootfolders"] });
      await qc.invalidateQueries({ queryKey: ["modules"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy("");
    }
  };
  const addModule = (kind: "source" | "metadata", implementation: string, name: string) =>
    unwrap(api.POST("/api/v1/modules", { body: { kind, implementation, name, enabled: true, priority: 1, settings: {} } }));

  const steps: Step[] = [
    {
      id: "source",
      title: t("Add a source"),
      kind: "required",
      body: t("Where chapters come from. mangarr sources covers 23 sites, including MangaDex, MANGA Plus, Weeb Central and MangaFire, with nothing else to run."),
      done: !missing("Sources"),
      action: (
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="primary" size="sm" loading={busy === "source"} onClick={() => run("source", () => addModule("source", "native", "mangarr sources"))}>{t("Add mangarr sources")}</Button>
          <Link to="/settings/sources" className="text-sm text-accent-2 hover:underline">{t("Other engines")}</Link>
        </div>
      ),
    },
    {
      id: "languages",
      title: t("Which languages do you read?"),
      kind: "required",
      body: t("Picked from your browser. Only catalogs in these languages (and multi-language ones) are turned on, and Add series searches them."),
      done: (sources.data.defaultLanguages ?? []).length > 0,
      action: missing("Sources") ? (
        <span className="text-sm text-muted">{t("Add a source first.")}</span>
      ) : (
        <div className="flex max-w-md flex-col gap-3">
          <LanguageList value={reading} onChange={setLangs} options={catalogLangs} />
          <Button
            variant="primary"
            size="sm"
            className="self-start"
            disabled={!reading.length}
            loading={busy === "languages"}
            onClick={() => run("languages", async () => {
              await fetch(`${basePath}/api/v1/settings/sources`, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...sources.data, defaultLanguages: reading }) }).then((r) => {
                if (!r.ok) throw new Error(`HTTP ${r.status}`);
              });
              await Promise.all([qc.invalidateQueries({ queryKey: ["settings"] }), qc.invalidateQueries({ queryKey: ["catalogs"] })]);
            })}
          >
            {t("Save languages")}
          </Button>
        </div>
      ),
    },
    {
      id: "root",
      title: t("Choose a root folder"),
      kind: "required",
      body: t("Downloaded chapters are saved here as CBZ. Mount the same folder read-only into Komga or Kavita."),
      done: !missing("Root folders"),
      action: (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (path.trim()) void run("root", () => unwrap(api.POST("/api/v1/rootfolders", { body: { path: path.trim(), language: lang.trim() || "en" } })));
          }}
        >
          <Input className="min-w-0 flex-1" aria-label={t("Folder path")} placeholder="/data/manga/en" value={path} onChange={(e) => setPath(e.target.value)} />
          <LanguageSelect className="w-40" aria-label={t("Language")} value={lang} onChange={setLang} />
          <Button type="submit" variant="primary" size="sm" className="h-auto" loading={busy === "root"} disabled={!path.trim()}>{t("Add folder")}</Button>
        </form>
      ),
    },
    {
      id: "metadata",
      title: t("Add a metadata provider"),
      kind: "recommended",
      body: t("Titles, synopsis, covers and genres from AniList. Without one, Add series can only add by title."),
      done: has("metadata"),
      action: <Button size="sm" loading={busy === "metadata"} onClick={() => run("metadata", () => addModule("metadata", "anilist", "AniList"))}>{t("Add AniList")}</Button>,
    },
    {
      id: "library",
      title: t("Connect Komga or Kavita"),
      kind: "optional",
      body: t("Reading apps can connect to mangarr directly (Komga-compatible API, OPDS, KOReader sync). Connect a library server only if you already run one: it is rescanned after every import and shares read progress."),
      done: has("library"),
      action: (
        <Link to="/settings/library" className="text-sm text-accent-2 hover:underline">{t("Set up")}</Link>
      ),
    },
  ];
  const required = steps.filter((s) => s.kind === "required");
  const requiredDone = required.filter((s) => s.done).length;
  const ready = requiredDone === required.length;
  const kindLabel = { required: t("Required"), recommended: t("Recommended"), optional: t("Optional") };
  return (
    <section aria-labelledby="setup-title" className="mb-6 max-w-3xl overflow-hidden rounded-xl border border-border bg-panel">
      <header className="flex flex-col gap-2 border-b border-border px-5 py-4">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h2 id="setup-title" className="text-lg font-semibold">{t("Set up mangarr")}</h2>
          <span className="text-sm text-muted">{t("{done} of {total} required steps done", { done: requiredDone, total: required.length })}</span>
        </div>
        <Progress value={(100 * requiredDone) / required.length} tone="ok" />
      </header>
      <ol>
        {steps.map((s, i) => {
          const isSkipped = !s.done && s.kind !== "required" && skipped.includes(s.id);
          const finished = s.done || isSkipped;
          return (
            <li key={s.id} className={clsx("flex gap-4 border-b border-border px-5 py-4", !finished && s.kind === "required" && "bg-panel-2/40")}>
              <span
                aria-hidden
                className={clsx(
                  "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full text-xs font-bold",
                  s.done ? "bg-ok/15 text-ok" : isSkipped ? "border-2 border-dashed border-border text-muted" : "border-2 border-accent text-accent-2",
                )}
              >
                {s.done ? <Check className="size-4" /> : i + 1}
              </span>
              <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                <div className="flex flex-wrap items-center gap-2">
                  <span className={clsx("font-semibold", finished && "text-muted")}>{s.title}</span>
                  <span className={clsx("text-[11px] font-semibold uppercase tracking-wide", s.kind === "required" ? "text-accent-2" : s.kind === "recommended" ? "text-info" : "text-muted")}>{kindLabel[s.kind]}</span>
                  {s.done && <span className="sr-only">{t("Done")}</span>}
                  {isSkipped && <span className="text-xs text-muted">· {t("Skipped")}</span>}
                </div>
                {!finished && <p className="text-sm text-muted">{s.body}</p>}
                {!finished && (
                  <div className="mt-1 flex flex-wrap items-center gap-3">
                    <div className="min-w-0 flex-1">{s.action}</div>
                    {s.kind !== "required" && (
                      <button type="button" className="text-sm text-muted hover:text-fg" onClick={() => skip(s.id)}>{t("Skip")}</button>
                    )}
                  </div>
                )}
              </div>
            </li>
          );
        })}
        <li className={clsx("flex items-center gap-4 px-5 py-4", !ready && "opacity-60")}>
          <span aria-hidden className="flex size-7 shrink-0 items-center justify-center rounded-full border-2 border-border text-xs font-bold text-muted">{steps.length + 1}</span>
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="font-semibold">{t("Add your first series")}</span>
            <span className="text-sm text-muted">
              {ready ? t("All set.") : t("Available once the required steps are done.")}{" "}
              <Link to="/import" className="text-accent-2 hover:underline">{t("Or import a Mihon, Tachiyomi or Aidoku backup")}</Link>
            </span>
          </div>
          {ready ? (
            <Link to="/add"><Button variant="primary">{t("Add series")}</Button></Link>
          ) : (
            <Button variant="primary" disabled>{t("Add series")}</Button>
          )}
        </li>
      </ol>
    </section>
  );
}
