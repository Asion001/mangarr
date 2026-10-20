import { t as tr, t } from "../../lib/i18n/core";
import { useMemo, useRef, useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Upload } from "lucide-react";
import clsx from "clsx";
import { api, apiUrl, unwrap } from "../../api/client";
import type { components } from "../../api/schema";
import { Badge, Button, Card, ErrorBox, Modal, Select } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { useToast } from "../../lib/toast";

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
  const [error, setError] = useState<unknown>(null);
  const open = async (f: File) => {
    setError(null);
    try {
      const parsed = JSON.parse(await f.text()) as Export;
      if (parsed?.format !== "mangarr-user-data") throw new Error(tr("This isn't a mangarr data export."));
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
        {error ? <ErrorBox error={error} /> : null}
      </div>
      {data && <ImportReview data={data} onClose={() => setData(null)} />}
    </Card>
  );
}

type Filter = "all" | "title" | "missing";

function ImportReview({ data, onClose }: { data: Export; onClose: () => void }) {
  const { can } = useAccount();
  const qc = useQueryClient();
  const toast = useToast();
  const [picked, setPicked] = useState<Record<string, number>>({});
  const [changing, setChanging] = useState<number | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [opts, setOpts] = useState({ progress: true, history: true, follows: true, settings: true });
  const [importing, setImporting] = useState(false);
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
  const read = useMemo(() => data.titles.reduce((s, ti) => s + (ti.progress ?? []).length, 0), [data]);
  const follows = data.titles.filter((ti) => ti.following).length;
  const shown = matches.filter((m) => (filter === "title" ? m.by === "title" : filter === "missing" ? !m.seriesId : true));
  const run = async () => {
    setImporting(true);
    try {
      const res = await unwrap(api.POST("/api/v1/me/data/import", { body: { data, options: { ...opts, picked } as Options } }));
      qc.invalidateQueries();
      toast.success(tr("Your data was imported"), tr("{titles} titles · {progress} chapters · {history} history entries · {follows} follows", res));
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
          <span className="text-xs text-muted">{t("Progress already further here is kept. Importing again adds nothing twice.")}</span>
          <div className="flex gap-2">
            <Button onClick={onClose}>{t("Cancel")}</Button>
            <Button variant="primary" loading={importing} disabled={!going || !Object.values(opts).some(Boolean)} onClick={() => void run()}>
              {t("Import {count} titles", { count: n(going) })}
            </Button>
          </div>
        </div>
      }
    >
      <div className="flex flex-col gap-4 text-sm">
        <p className="text-muted">
          {t("Exported by {user} on {date} from another server · {count} titles", { user: data.user || "—", date: new Date(data.exported).toLocaleDateString(), count: n(data.titles.length) })}
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
                    <td className="px-3 py-2">{m.title}</td>
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
                          {can(["requests.create", "requests.manage"]) && (
                            <Link to="/requests" className="text-xs text-accent-2 hover:underline">{t("Request it")}</Link>
                          )}
                        </span>
                      )}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums">{n(m.read)}</td>
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
                ["settings", t("Reader and interface settings"), ""],
              ] as [keyof typeof opts, string, string][]
            ).map(([k, l, hint]) => (
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
