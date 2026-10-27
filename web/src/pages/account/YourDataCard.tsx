import { label, t as tr, t } from "../../lib/i18n/core";
import { useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, FileUp, Upload } from "lucide-react";
import clsx from "clsx";
import { api, apiUrl, unwrap } from "../../api/client";
import type { components } from "../../api/schema";
import { Badge, Button, Card, ErrorBox, Field, Input, Modal, SecretInput, Select } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { useToast } from "../../lib/toast";
import { readingStatusName, type ReadingStatus } from "../../lib/readingLists";

type Export = components["schemas"]["Export"];
type Options = components["schemas"]["DataImportOptions"];

const n = (v: number) => v.toLocaleString();

/** YourDataCard exports the account's own data and imports one exported on another server. */
export function YourDataCard() {
  const { data: counts } = useQuery({ queryKey: ["me", "data"], queryFn: () => unwrap(api.GET("/api/v1/me/data/summary")) });
  // a missing or odd answer just leaves the counts out
  const sum = typeof counts?.titles === "number" ? counts : null;
  const file = useRef<HTMLInputElement>(null);
  const [data, setData] = useState<Export | null>(null);
  // from names the app or site an export was read from (not another server)
  const [from, setFrom] = useState("");
  const [error, setError] = useState<unknown>(null);
  const open = async (f: File) => {
    setError(null);
    try {
      const parsed = JSON.parse(await f.text()) as Export;
      if (parsed?.format !== "mangarr-user-data") throw new Error(tr("This isn't a mangarr data export."));
      setFrom("");
      setData(parsed);
    } catch (e) {
      setError(e instanceof SyntaxError ? new Error(tr("This isn't a mangarr data export.")) : e);
    } finally {
      if (file.current) file.current.value = "";
    }
  };
  return (
    <Card title={t("Your data")}>
      <div className="flex flex-col gap-3 text-sm">
        <p className="text-muted">{t("Take your reading with you to another mangarr server, or bring it here from one. Only your own data: nothing from other users, no password and no library files.")}</p>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="flex flex-col gap-2 rounded-md border border-border p-3">
            <div className="font-medium">{t("Export")}</div>
            <ul className="list-disc pl-5 text-muted">
              <li>{sum ? t("Progress in {count} titles, down to the page", { count: n(sum.titles) }) : t("Progress, down to the page")}</li>
              <li>{sum ? t("Reading history ({count} entries) and reading time", { count: n(sum.history) }) : t("Reading history and reading time")}</li>
              <li>{sum ? t("{count} followed titles", { count: n(sum.follows) }) : t("Followed titles")}</li>
              <li>{t("Reader and interface settings")}</li>
            </ul>
            <a href={apiUrl("api/v1/me/data")} download className="self-start">
              <Button variant="primary" icon={<Download className="size-4" />}>{t("Export my data")}</Button>
            </a>
          </div>
          <div className="flex flex-col gap-2 rounded-md border border-border p-3">
            <div className="font-medium">{t("Import")}</div>
            <p className="text-muted">{t("Upload a file exported from another server. You'll see which titles match this library before anything changes. Progress that's already further here is kept.")}</p>
            <input ref={file} type="file" accept=".json,application/json" hidden onChange={(e) => e.target.files?.[0] && void open(e.target.files[0])} />
            <Button className="self-start" icon={<Upload className="size-4" />} onClick={() => file.current?.click()}>{t("Import a file…")}</Button>
          </div>
        </div>
        <BringLibrary onRead={(d, name) => (setFrom(name), setData(d))} />
        {error ? <ErrorBox error={error} /> : null}
      </div>
      {data && <ImportReview data={data} from={from} onClose={() => setData(null)} />}
    </Card>
  );
}

type Filter = "all" | "title" | "missing";

/** BringLibrary reads another app's backup or a manga site account as the user's own data. */
function BringLibrary({ onRead }: { onRead: (d: Export, from: string) => void }) {
  const toast = useToast();
  const file = useRef<HTMLInputElement>(null);
  const sources = useQuery({ queryKey: ["me", "data", "accounts"], queryFn: () => unwrap(api.GET("/api/v1/me/data/accounts")) });
  const list = Array.isArray(sources.data) ? sources.data : [];
  const [picked, setPicked] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [reading, setReading] = useState(false);
  const key = (a: (typeof list)[number]) => `${a.moduleId}:${a.sourceId}`;
  const site = list.find((a) => key(a) === picked) ?? list[0];
  const backup = async (f: File) => {
    setBusy(true);
    try {
      const d = await unwrap(api.POST("/api/v1/me/data/from-backup", {
        body: f as unknown as string,
        bodySerializer: (b: unknown) => b as BodyInit,
        headers: { "Content-Type": "application/octet-stream" },
      }));
      onRead(d, f.name);
    } catch (e) {
      toast.fromError(e, tr("Couldn't read the backup"));
    } finally {
      setBusy(false);
      if (file.current) file.current.value = "";
    }
  };
  const account = async () => {
    if (!site || !value.trim()) return;
    setReading(true);
    try {
      const d = await unwrap(api.POST("/api/v1/me/data/from-account", { body: { moduleId: site.moduleId, sourceId: site.sourceId, token: value.trim() } }));
      setValue("");
      onRead(d, tr("{site} account", { site: site.name }));
    } catch (e) {
      toast.fromError(e, tr("Couldn't read the account"));
    } finally {
      setReading(false);
    }
  };
  return (
    <div className="flex flex-col gap-3 rounded-md border border-border p-3">
      <div className="font-medium">{t("Bring my library from another app or site")}</div>
      <p className="text-muted">{t("Titles on this server get your progress right away. The ones that aren't can be asked for: each becomes a request, and your progress is written once it's added.")}</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="flex flex-col gap-2">
          <div className="text-xs font-medium uppercase tracking-wide text-muted">{t("From an app backup")}</div>
          <p className="text-xs text-muted">{t("Mihon, Tachiyomi and forks, Suwayomi:") + " "}<code>.tachibk</code> / <code>.proto.gz</code>{". Aidoku: "}<code>.aib</code></p>
          <input ref={file} type="file" hidden accept=".tachibk,.gz,.proto,.aib,.json,.plist" onChange={(e) => e.target.files?.[0] && void backup(e.target.files[0])} />
          <Button className="self-start" loading={busy} icon={<FileUp className="size-4" />} onClick={() => file.current?.click()}>{t("Choose a backup…")}</Button>
        </div>
        {site && (
          <form className="flex flex-col gap-2" onSubmit={(e) => (e.preventDefault(), void account())}>
            <div className="text-xs font-medium uppercase tracking-wide text-muted">{t("From a site account")}</div>
            <Field label={t("Site")}>
              <Select value={key(site)} onChange={(e) => (setPicked(e.target.value), setValue(""))}>
                {list.map((a) => (
                  <option key={key(a)} value={key(a)}>{a.name}</option>
                ))}
              </Select>
            </Field>
            <Field label={label(site.field)} help={site.secret ? t("Used once to read your lists and progress, then forgotten. It isn't saved on this server.") : undefined}>
              {site.secret ? <SecretInput value={value} onChange={setValue} /> : <Input value={value} onChange={(e) => setValue(e.target.value)} />}
            </Field>
            {site.steps.length > 0 && (
              <details className="text-xs text-muted">
                <summary className="cursor-pointer font-medium text-fg">{t("How to find it")}</summary>
                <ol className="mt-1 list-decimal space-y-0.5 pl-5">
                  {site.steps.map((step) => (
                    <li key={step}>{label(step)}</li>
                  ))}
                </ol>
              </details>
            )}
            <Button type="submit" className="self-start" loading={reading} disabled={!value.trim()}>{t("Read my lists")}</Button>
          </form>
        )}
      </div>
    </div>
  );
}

function ImportReview({ data, from, onClose }: { data: Export; from: string; onClose: () => void }) {
  const { account, can } = useAccount();
  const qc = useQueryClient();
  const toast = useToast();
  const [picked, setPicked] = useState<Record<string, number>>({});
  const [changing, setChanging] = useState<number | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [opts, setOpts] = useState({ progress: true, history: true, follows: true, lists: true, settings: true });
  const [importing, setImporting] = useState(false);
  // titles not on this server the user doesn't want asked for
  const [noAsk, setNoAsk] = useState<Record<number, boolean>>({});
  const preview = useQuery({
    queryKey: ["me", "data", "preview", data.exported, picked],
    queryFn: () => unwrap(api.POST("/api/v1/me/data/preview", { body: { data, options: { ...opts, picked } as Options } })),
    placeholderData: (prev) => prev,
  });
  const series = useQuery({ queryKey: ["series", "pick"], queryFn: () => unwrap(api.GET("/api/v1/series")), enabled: changing !== null });
  const matches = preview.data ?? [];
  const count = (by: string) => matches.filter((m) => m.by === by).length;
  const missing = matches.filter((m) => !m.seriesId).length;
  const going = matches.length - missing;
  const inFile = useMemo(() => data.titles.reduce((s, ti) => s + (ti.progress ?? []).length, 0), [data]);
  // an app or site keeps how far each title was read: count it in the titles here
  const read = from ? matches.reduce((s, m) => s + (m.seriesId ? m.read : 0), 0) : inFile;
  const follows = data.titles.filter((ti) => ti.following).length;
  const listed = data.titles.filter((ti) => ti.status || ti.favorite || (ti.lists ?? []).length > 0).length;
  // requests are a person's: none without logins
  const canAsk = !!from && account?.kind === "user" && can(["requests.create"]);
  const ask = canAsk ? matches.filter((m) => !m.seriesId && !(m.index in picked) && !noAsk[m.index]).map((m) => m.index) : [];
  const shown = matches.filter((m) => (filter === "title" ? m.by === "title" : filter === "missing" ? !m.seriesId : true));
  const run = async () => {
    setImporting(true);
    try {
      const res = await unwrap(api.POST("/api/v1/me/data/import", { body: { data, options: { ...opts, picked, request: ask } as Options } }));
      qc.invalidateQueries();
      const asked = (res.requested ? " · " + tr("{count} titles asked for", { count: res.requested }) : "") +
        (res.pending ? " · " + tr("{count} titles get their progress in a few minutes, once the site says which chapter you reached", { count: res.pending }) : "");
      toast.success(tr("Your data was imported"), tr("{titles} titles · {progress} chapters · {history} history entries · {follows} follows", res) + asked);
      onClose();
    } catch (e) {
      toast.fromError(e, tr("Couldn't import your data"));
    } finally {
      setImporting(false);
    }
  };
  const tile = (value: number, label: string, warn = false) => (
    <div className={clsx("rounded-md border p-2.5", warn ? "border-warn/60 bg-warn/10" : "border-border")}>
      <div className="text-xl font-semibold tabular-nums">{n(value)}</div>
      <div className="text-xs text-muted">{label}</div>
    </div>
  );
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={t("Import your data")}
      footer={
        <div className="flex w-full flex-wrap items-center justify-between gap-3">
          <span className="text-xs text-muted">
            {t("Progress already further here is kept. Importing again adds nothing twice.")}
            {ask.length > 0 && " " + t("{count} titles not here will be asked for.", { count: n(ask.length) })}
          </span>
          <div className="flex gap-2">
            <Button onClick={onClose}>{t("Cancel")}</Button>
            <Button variant="primary" loading={importing} disabled={(!going && !ask.length) || !Object.values(opts).some(Boolean)} onClick={() => void run()}>
              {t("Import {count} titles", { count: n(going) })}
            </Button>
          </div>
        </div>
      }
    >
      <div className="flex flex-col gap-4 text-sm">
        <p className="text-muted">
          {from
            ? t("From {from} · {count} titles", { from, count: n(data.titles.length) })
            : t("Exported by {user} on {date} from another server · {count} titles", { user: data.user || "—", date: new Date(data.exported).toLocaleDateString(), count: n(data.titles.length) })}
        </p>
        {preview.error ? <ErrorBox error={preview.error} /> : null}
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          {tile(count("source") + count("picked"), t("same source"))}
          {tile(count("id"), t("same AniList / MAL id"))}
          {tile(count("title"), t("same title"))}
          {tile(missing, t("not on this server"), missing > 0)}
        </div>
        <div className="overflow-hidden rounded-md border border-border">
          <div role="tablist" aria-label={t("Show")} className="flex gap-1 border-b border-border bg-panel-2 p-1.5">
            {(
              [
                ["all", t("All {count}", { count: n(matches.length) })],
                ["title", t("Matched by title {count}", { count: n(count("title")) })],
                ["missing", t("Not here {count}", { count: n(missing) })],
              ] as [Filter, string][]
            ).map(([v, l]) => (
              <button key={v} type="button" role="tab" aria-selected={filter === v} onClick={() => setFilter(v)} className={clsx("rounded px-2.5 py-1 text-xs", filter === v ? "border border-border bg-panel text-fg" : "text-muted hover:text-fg")}>
                {l}
              </button>
            ))}
          </div>
          <div className="max-h-72 overflow-y-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted">
                  <th className="px-3 py-1.5 font-medium">{t("In the file")}</th>
                  <th className="px-3 py-1.5 font-medium">{t("On this server")}</th>
                  <th className="px-3 py-1.5 text-right font-medium">{t("Read")}</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((m) => (
                  <tr key={m.index} className="border-t border-border">
                    <td className="px-3 py-2">
                      {m.title}
                      {(m.status || m.favorite || (m.lists ?? []).length > 0) && (
                        <div className="text-xs text-muted">
                          {[m.status && readingStatusName(m.status as ReadingStatus), m.favorite && t("Favorites"), ...(m.lists ?? [])].filter(Boolean).join(", ")}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2">
                      {changing === m.index ? (
                        <Select
                          aria-label={t("Series on this server")}
                          value={String(m.seriesId)}
                          onChange={(e) => {
                            setPicked({ ...picked, [m.index]: Number(e.target.value) });
                            setChanging(null);
                          }}
                        >
                          <option value="0">{t("Don't import")}</option>
                          {(series.data ?? []).map((s) => (
                            <option key={s.id} value={s.id}>{s.title}</option>
                          ))}
                        </Select>
                      ) : m.seriesId ? (
                        <span className="flex flex-wrap items-center gap-1.5">
                          {m.seriesTitle}
                          <Badge tone={m.by === "title" ? "default" : m.by === "id" ? "info" : "ok"}>
                            {m.by === "source" ? t("source") : m.by === "id" ? t("id") : m.by === "picked" ? t("picked") : t("title")}
                          </Badge>
                          <button type="button" className="text-xs text-accent-2 hover:underline" onClick={() => setChanging(m.index)}>{t("Change")}</button>
                        </span>
                      ) : (
                        <span className="flex flex-wrap items-center gap-1.5 text-warn">
                          {m.index in picked ? t("Not imported") : t("Not on this server")}
                          <button type="button" className="text-xs text-accent-2 hover:underline" onClick={() => setChanging(m.index)}>{t("Pick")}</button>
                          {canAsk && !(m.index in picked) ? (
                            <label className="flex items-center gap-1 text-xs text-fg">
                              <input type="checkbox" className="size-3.5 accent-primary" checked={!noAsk[m.index]} onChange={(e) => setNoAsk({ ...noAsk, [m.index]: !e.target.checked })} />
                              {t("Ask for it")}
                            </label>
                          ) : can(["requests.create", "requests.manage"]) && (
                            <Link to="/requests" className="text-xs text-accent-2 hover:underline">{t("Request it")}</Link>
                          )}
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">
                      {m.reached && m.reached.chapter >= 0 ? t("ch. {n}", { n: m.reached.chapter }) + (m.reached.page ? ", " + t("p. {n}", { n: m.reached.page }) : "") : m.reached ? t("started") : n(m.read)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
        <fieldset className="flex flex-col gap-2">
          <legend className="pb-1.5 font-medium">{t("Import")}</legend>
          <div className="grid gap-x-4 gap-y-1.5 sm:grid-cols-2">
            {(
              [
                ["progress", t("Progress"), t("{count} chapters", { count: n(read) })],
                ["history", t("Reading history and time"), ""],
                ["follows", t("Followed titles"), follows ? n(follows) : ""],
                ["lists", t("Reading statuses and lists"), listed ? n(listed) : ""],
                ["settings", t("Reader and interface settings"), ""],
              ] as [keyof typeof opts, string, string][]
            ).filter(([k]) => !from || k === "progress" || k === "follows" || k === "lists").map(([k, l, hint]) => (
              <label key={k} className="flex items-center gap-2">
                <input type="checkbox" className="size-4 accent-primary" checked={opts[k]} onChange={(e) => setOpts({ ...opts, [k]: e.target.checked })} />
                {l}
                {hint && <span className="text-muted">· {hint}</span>}
              </label>
            ))}
          </div>
        </fieldset>
      </div>
    </Modal>
  );
}
