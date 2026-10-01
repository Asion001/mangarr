import { t } from "../../lib/i18n/core";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import clsx from "clsx";
import { GripVertical, X } from "lucide-react";
import { api, unwrap, type Catalog, type S } from "../../api/client";
import { useCatalogs, useProfiles, useRootFolders } from "../../api/queries";
import { ErrorBox, Field, Loading, SaveBar, Select } from "../../components/ui";
import { LanguageMenu } from "../../components/LanguageChooser";
import { languageName, sortLanguages } from "../../lib/format";
import { useSettingsDoc } from "../settings/useSettingsDoc";

type Sources = S["Sources"];
type Default = Sources["languageDefaults"][number];
const key = (c: Catalog) => `${c.moduleId}:${c.id}`;

/**
 * LanguageDefaults is the one ordered source list per language: adding a
 * series searches every source in it (the first becomes primary, the rest
 * fallbacks), and series that follow the defaults use the same order.
 */
export function LanguageDefaults() {
  const doc = useSettingsDoc<Sources>("sources");
  const { data: catalogs } = useCatalogs();
  const { data: roots } = useRootFolders();
  const { data: profiles } = useProfiles();
  const health = useQuery({ queryKey: ["catalogs-health"], queryFn: () => unwrap(api.GET("/api/v1/catalogs/health")), staleTime: 30_000 });
  const v = doc.value;
  const items = useMemo(() => (catalogs?.items ?? []).filter((c) => !c.hidden), [catalogs]);
  const byKey = useMemo(() => new Map(items.map((c) => [key(c), c])), [items]);
  const healthOf = useMemo(() => new Map((health.data ?? []).map((h) => [`${h.moduleId}:${h.sourceId}`, h])), [health.data]);
  const languages = useMemo(() => sortLanguages(items.map((c) => c.lang).filter((l) => l && l !== "all" && l !== "multi")), [items]);
  if (doc.isLoading) return <Loading />;
  if (doc.error) return <ErrorBox error={doc.error} />;
  if (!v) return null;
  const defaults = v.languageDefaults ?? [];
  const patch = (index: number, p: Partial<Default>) => doc.patch({ languageDefaults: defaults.map((d, i) => (i === index ? { ...d, ...p } : d)) });
  const folderFor = (lang: string) => {
    const own = roots?.find((r) => r.language.toLowerCase() === lang.toLowerCase());
    const auto = roots?.find((r) => r.language === "*");
    return own?.path ?? (auto && lang ? `${auto.path.replace(/\/+$/, "")}/${lang.toLowerCase()}` : "—");
  };
  return (
    <div className="flex flex-col gap-4">
      <p className="max-w-3xl text-sm text-muted">
        {t("Each language you read has one ordered list. Adding a series searches every source in it: the first becomes the primary source, the rest are fallbacks. Series that follow the defaults use the same order.")}
      </p>
      <div className="grid gap-4 xl:grid-cols-2">
        {defaults.map((d, index) => (
          <LanguageCard
            key={`${d.language}-${index}`}
            item={d}
            byKey={byKey}
            catalogs={items}
            folder={folderFor(d.language)}
            profiles={profiles ?? []}
            status={(k) => {
              const c = byKey.get(k);
              const h = healthOf.get(k);
              if (!c) return { tone: "err", text: t("Not installed") };
              if (c.cooldownUntil) return { tone: "warn", text: t("Paused for now") };
              if (h?.failing) return { tone: "err", text: t("{failing} of {n} links failing", { failing: h.failing, n: h.series }) };
              return { tone: h?.series ? "ok" : "muted", text: h?.series ? t("All links fine") : t("Not used yet") };
            }}
            onChange={(p) => patch(index, p)}
            onRemove={() => doc.patch({ languageDefaults: defaults.filter((_, i) => i !== index) })}
          />
        ))}
      </div>
      {!defaults.length && <p className="text-sm text-muted">{t("No language has a list yet: adding a series searches your catalogs in their global order.")}</p>}
      <LanguageMenu
        label={t("+ Add a language")}
        options={languages}
        exclude={defaults.map((d) => d.language)}
        onPick={(language) => doc.patch({ languageDefaults: [...defaults, { language, sources: [], profileId: 0, readingDirection: "" }] })}
        className="h-9 rounded-md border border-dashed border-border px-3 text-sm hover:border-muted"
      />
      <SaveBar dirty={doc.dirty} saving={doc.saving} onSave={() => void doc.save()} onDiscard={doc.reset} />
    </div>
  );
}

function LanguageCard({
  item,
  byKey,
  catalogs,
  folder,
  profiles,
  status,
  onChange,
  onRemove,
}: {
  item: Default;
  byKey: Map<string, Catalog>;
  catalogs: Catalog[];
  folder: string;
  profiles: { id: number; name: string }[];
  status: (key: string) => { tone: string; text: string };
  onChange: (p: Partial<Default>) => void;
  onRemove: () => void;
}) {
  const [dragging, setDragging] = useState<number | null>(null);
  const chosen = new Set(item.sources);
  // this language's catalogs and multi-language ones first
  const fits = (c: Catalog) => c.lang === item.language || c.lang === "all" || c.lang === "multi";
  const addable = catalogs.filter((c) => !chosen.has(key(c))).sort((a, b) => Number(fits(b)) - Number(fits(a)) || a.displayName.localeCompare(b.displayName));
  const move = (from: number, to: number) => {
    if (from === to) return;
    const next = [...item.sources];
    const [k] = next.splice(from, 1);
    next.splice(to, 0, k);
    onChange({ sources: next });
  };
  return (
    <section aria-label={languageName(item.language)} className="flex flex-col overflow-hidden rounded-xl border border-border bg-panel">
      <header className="flex items-center gap-3 border-b border-border px-4 py-3">
        <h3 className="flex-1 font-semibold">{languageName(item.language)}</h3>
        <span className="truncate font-mono text-xs text-muted" title={t("Set in Media management")}>{folder}</span>
        <button type="button" aria-label={t("Remove {name}", { name: languageName(item.language) })} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-panel-2 hover:text-fg" onClick={onRemove}>
          <X className="size-4" />
        </button>
      </header>
      <ol className="flex flex-col">
        {item.sources.map((k, i) => {
          const c = byKey.get(k);
          const st = status(k);
          return (
            <li
              key={k}
              draggable
              onDragStart={() => setDragging(i)}
              onDragEnd={() => setDragging(null)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => dragging !== null && move(dragging, i)}
              className={clsx("flex items-center gap-3 border-b border-border px-4 py-2.5 text-sm", dragging === i && "opacity-50")}
            >
              <GripVertical aria-hidden="true" className="hidden size-4 cursor-grab text-muted sm:block" />
              <span className="w-5 text-center font-semibold text-muted">{i + 1}</span>
              <span className="min-w-0 flex-1 truncate">
                {c?.displayName ?? k}
                {c && c.lang !== item.language && <span className="ml-2 text-xs text-muted">{languageName(c.lang)}</span>}
              </span>
              <span title={st.text} className="flex items-center gap-1.5 text-xs text-muted">
                <span aria-hidden="true" className={clsx("size-2 rounded-full", { ok: "bg-ok", warn: "bg-warn", err: "bg-err", muted: "bg-border" }[st.tone])} />
                <span className="hidden md:inline">{st.text}</span>
              </span>
              <span className="flex sm:hidden">
                <button type="button" aria-label={t("Move up")} disabled={i === 0} className="size-8 text-muted disabled:opacity-30" onClick={() => move(i, i - 1)}>↑</button>
                <button type="button" aria-label={t("Move down")} disabled={i === item.sources.length - 1} className="size-8 text-muted disabled:opacity-30" onClick={() => move(i, i + 1)}>↓</button>
              </span>
              <button type="button" aria-label={t("Remove {name}", { name: c?.displayName ?? k })} className="flex size-8 items-center justify-center rounded-md text-muted hover:bg-panel-2 hover:text-fg" onClick={() => onChange({ sources: item.sources.filter((x) => x !== k) })}>
                <X className="size-3.5" />
              </button>
            </li>
          );
        })}
        {!item.sources.length && <li className="border-b border-border px-4 py-3 text-sm text-muted">{t("No source yet: adding a series searches every catalog in this language, in the global order.")}</li>}
      </ol>
      <div className="grid gap-3 p-4 sm:grid-cols-3">
        <Field label={t("Add source")}>
          <Select value="" onChange={(e) => e.target.value && onChange({ sources: [...item.sources, e.target.value] })}>
            <option value="">{t("Add source…")}</option>
            {addable.map((c) => (
              <option key={key(c)} value={key(c)}>{c.displayName}{fits(c) ? "" : ` (${languageName(c.lang)})`}</option>
            ))}
          </Select>
        </Field>
        <Field label={t("Profile")}>
          <Select value={item.profileId || 0} onChange={(e) => onChange({ profileId: Number(e.target.value) })}>
            <option value={0}>{t("Default")}</option>
            {profiles.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
          </Select>
        </Field>
        <Field label={t("Reading direction")}>
          <Select value={item.readingDirection || ""} onChange={(e) => onChange({ readingDirection: e.target.value as Default["readingDirection"] })}>
            <option value="">{t("Automatic")}</option>
            <option value="rtl">{t("Right to left (manga)")}</option>
            <option value="ltr">{t("Left to right")}</option>
            <option value="webtoon">{t("Webtoon (long strip)")}</option>
          </Select>
        </Field>
      </div>
    </section>
  );
}

