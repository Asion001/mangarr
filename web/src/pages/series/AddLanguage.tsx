import { t } from "../../lib/i18n/core";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { X } from "lucide-react";
import { api, unwrap, type S, type SourceManga } from "../../api/client";
import { Badge, Button, IconButton, Modal, Select, Spinner, Textarea } from "../../components/ui";
import { LanguageSelect, useLanguageFolders, useOfferedLanguages } from "../../components/LanguageSelect";
import { languageName } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { SourceSearch, pickKey, useCatalogTargets, useDefaultsSearch, type PickGroup, type Picked, type Scope } from "./SourceSearch";

type Monitor = "all" | "future" | "none";

/**
 * AddLanguageModal adds an edition of this title in another language: pick a
 * language and every source of its language default that has the title is
 * prefilled (the first primary, the rest fallbacks). Review, choose what to
 * download, add. A language the title already has gets the sources instead.
 */
export function AddLanguageModal({ series, onClose, initialLang, requestId }: {
  series: S["SeriesResource"];
  onClose: () => void;
  initialLang?: string;
  /** requestId is the request this fulfils (linked once added). */
  requestId?: number;
}) {
  const have = new Set((series.editions ?? []).map((e) => e.language));
  const langs = useOfferedLanguages().languages;
  const [chosen, setLang] = useState<string | null>(initialLang || null);
  // until you pick, the first language this title doesn't have yet
  const lang = chosen ?? langs.find((l) => !have.has(l)) ?? "";
  const titles = [...new Set([series.workTitle, series.title, ...(series.metadata.altTitles ?? [])].filter((x): x is string => !!x))];
  const [query, setQuery] = useState(series.workTitle || series.title);
  const [scope, setScope] = useState<Scope>("active");
  const [keys, setKeys] = useState<string[]>([]);
  const [more, setMore] = useState(false);
  const [searchingMore, setSearchingMore] = useState(false);
  const [picked, setPicked] = useState<Picked[]>([]);
  const [monitor, setMonitor] = useState<Monitor>("all");
  const [busy, setBusy] = useState(false);
  const folder = useLanguageFolders(lang ? [lang] : []).get(lang);
  const { gen } = useCatalogTargets("active", lang, []);
  const found = useDefaultsSearch({ query, titles, lang, enabled: !!lang && !folder?.error, gen });
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();

  // a new language starts from what its default sources found
  useEffect(() => {
    const ed = found.data?.editions.find((e) => e.lang === lang);
    setPicked(
      (ed?.sources ?? []).flatMap((src) =>
        src.match ? [{ manga: src.match.manga, group: { moduleId: src.match.moduleId, sourceId: src.match.sourceId, sourceName: src.match.sourceName, lang: src.match.lang } }] : [],
      ),
    );
  }, [found.data, lang]);

  const addPick = (m: SourceManga, g: PickGroup) => {
    setPicked((cur) => (cur.some((p) => pickKey(p) === pickKey({ manga: m, group: g })) ? cur : [...cur, { manga: m, group: g }]));
    setSearchingMore(false);
  };

  const add = async () => {
    if (busy || !picked.length) return;
    setBusy(true);
    try {
      const res = await unwrap(
        api.POST("/api/v1/series/editions", {
          body: {
            workId: series.workId,
            sources: picked.map((p) => ({ moduleId: p.group.moduleId, sourceId: p.group.sourceId, url: p.manga.url, engineRef: p.manga.engineRef, title: p.manga.title, sourceName: p.group.sourceName, lang })),
            monitor,
            monitorNew: monitor === "none" ? "none" : "all",
            searchMissing: monitor === "all",
            requestId: requestId || undefined,
          },
        }),
      );
      qc.invalidateQueries({ queryKey: ["series"] });
      qc.invalidateQueries({ queryKey: ["rootfolders"] });
      qc.invalidateQueries({ queryKey: ["requests"] });
      const e = res.editions[0];
      toast.success(t("{lang} edition added", { lang: languageName(lang) }), monitor === "all" ? t("Fetching chapters…") : undefined);
      onClose();
      if (e) nav(`/series/${e.id}`);
    } catch (err) {
      toast.fromError(err);
    } finally {
      setBusy(false);
    }
  };

  const ed = found.data?.editions.find((e) => e.lang === lang);
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Add a language to {title}", { title: series.workTitle || series.title })}
      size="xl"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={busy} disabled={!picked.length || !!folder?.error} onClick={() => void add()}>
            {monitor === "all" ? t("Add and download") : t("Add series")}
          </Button>
        </>
      }
    >
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1.5 text-sm font-medium">
          {t("Language")}
          <LanguageSelect offered value={lang} onChange={setLang} placeholder={t("Choose…")} className="w-44" />
        </label>
        {lang && (
          <div className="flex min-w-0 flex-col gap-1.5 text-sm">
            <span className="font-medium">{t("Saved to")}</span>
            {have.has(lang) ? (
              <span className="text-muted">{t("Joins the {lang} edition this title already has", { lang: languageName(lang) })}</span>
            ) : folder?.error ? (
              <span className="text-warn">{folder.error}</span>
            ) : folder ? (
              <span className="font-mono text-xs text-muted">
                {folder.path}
                {!folder.exists && " · " + t("new folder")}
              </span>
            ) : null}
          </div>
        )}
      </div>
      {lang && !folder?.error && (
        <div className="flex flex-col gap-4">
          {found.isFetching && <p className="flex items-center gap-2 text-sm text-muted"><Spinner />{t("Searching every source in your language defaults…")}</p>}
          {ed && (
            <p className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
              {ed.sources.map((src) => (
                <span key={src.key} className={src.match ? "text-ok" : ""}>
                  {src.match ? "✓ " : ""}{src.sourceName}{src.match ? "" : src.empty ? ` · ${t("0 chapters")}` : ` · ${t("not found")}`}
                </span>
              ))}
            </p>
          )}
          {picked.length > 0 && (
            <ol className="flex flex-col gap-2">
              {picked.map((p, i) => (
                <li key={pickKey(p)} className={clsx("flex items-center gap-3 rounded-lg border p-3", i === 0 ? "border-accent/40 bg-accent/5" : "border-border")}>
                  <span className="w-4 text-center font-semibold text-muted">{i + 1}</span>
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="flex items-center gap-2 font-semibold">{p.group.sourceName}{i === 0 ? <Badge tone="accent">{t("Primary")}</Badge> : <span className="text-xs font-normal text-muted">{t("fallback")}</span>}</span>
                    <span className="truncate text-sm text-muted">“{p.manga.title}”{p.manga.chapterCount ? ` · ${t("{count} chapters", { count: p.manga.chapterCount })}` : ""}</span>
                  </span>
                  <IconButton title={t("Remove")} onClick={() => setPicked((cur) => cur.filter((x) => pickKey(x) !== pickKey(p)))}><X className="size-3.5" /></IconButton>
                </li>
              ))}
            </ol>
          )}
          {!found.isFetching && !picked.length && <p className="text-sm text-muted">{t("No confident match at your sources. Search them to pick one.")}</p>}
          <fieldset className="flex flex-wrap gap-2 text-sm">
            <legend className="mb-1.5 font-medium">{t("What to download")}</legend>
            {([["all", t("All chapters")], ["future", t("Only new chapters")], ["none", t("Nothing, just track it")]] as const).map(([v, label]) => (
              <label key={v} className={clsx("flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2", monitor === v ? "border-accent/60 bg-accent/5" : "border-border")}>
                <input type="radio" name="add-language-monitor" checked={monitor === v} onChange={() => setMonitor(v)} className="accent-accent" />
                {label}
              </label>
            ))}
          </fieldset>
          {searchingMore ? (
            <SourceSearch
              query={query}
              setQuery={setQuery}
              titles={titles}
              scope={scope}
              setScope={setScope}
              lang={lang}
              setLang={setLang}
              keys={keys}
              setKeys={setKeys}
              more={more}
              setMore={setMore}
              selected={picked}
              onPick={(m, g) => addPick(m, g)}
            />
          ) : (
            <Button className="self-start" onClick={() => setSearchingMore(true)}>{t("Search sources")}</Button>
          )}
        </div>
      )}
    </Modal>
  );
}

/**
 * RequestLanguageModal asks for this title in another language, for people
 * who can't add one themselves: a manager adds it (or it is added
 * automatically for their group) and they hear when it arrives.
 */
export function RequestLanguageModal({ series, onClose }: { series: S["SeriesResource"]; onClose: () => void }) {
  const have = new Set((series.editions ?? []).map((e) => e.language));
  const langs = useOfferedLanguages().languages.filter((l) => !have.has(l));
  const [chosen, setLang] = useState<string | null>(null);
  const lang = chosen ?? langs[0] ?? "";
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const toast = useToast();
  const send = async () => {
    if (busy || !lang) return;
    setBusy(true);
    try {
      const res = await unwrap(api.POST("/api/v1/requests", { body: { seriesId: series.id, language: lang, note: note.trim() || undefined } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      toast.success(
        res.joined ? t("Added you to the request") : t("Requested"),
        res.joined ? t("Someone asked for it already; you'll be told too.") : t("You'll be told when it's added."),
      );
      onClose();
    } catch (err) {
      toast.fromError(err, t("Couldn't request it"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Request {title} in another language", { title: series.workTitle || series.title })}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={busy} disabled={!lang} onClick={() => void send()}>{t("Request")}</Button>
        </>
      }
    >
      {langs.length ? (
        <div className="flex flex-col gap-4">
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            {t("Language")}
            <Select value={lang} onChange={(e) => setLang(e.target.value)} className="w-52">
              {langs.map((code) => (
                <option key={code} value={code}>{languageName(code)}</option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1.5 text-sm font-medium">
            {t("Note (optional)")}
            <Textarea value={note} onChange={(e) => setNote(e.target.value)} maxLength={500} />
          </label>
        </div>
      ) : (
        <p className="text-sm text-muted">{t("This title is here in every language the server is set up for.")}</p>
      )}
    </Modal>
  );
}
