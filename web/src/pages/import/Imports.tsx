import { label, t as tr, t } from "../../lib/i18n/core";
import { useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FileUp, Trash2 } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, Confirm, EmptyState, ErrorBox, Field, IconButton, Loading, PageHeader, SecretInput, Select, Spinner, Table, Td, Th } from "../../components/ui";
import { date, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";

export type ImportResource = S["ImportResource"];

export const useImports = () => useQuery({ queryKey: ["imports"], queryFn: () => unwrap(api.GET("/api/v1/imports")) });

export const formatLabel = (imp: Pick<ImportResource, "format" | "info">) =>
  imp.format === "account" ? Object.values(imp.info.sources ?? {})[0] ?? tr("Site account") : imp.format === "aidoku" ? "Aidoku" : "Mihon / Tachiyomi";

export function statusTone(s: string) {
  return s === "done" ? "ok" : s === "failed" ? "err" : s === "review" ? "accent" : "info";
}

export function statusLabel(s: string) {
  return { mapping: "matching", review: "ready to import", running: "importing", done: "done", failed: "failed" }[s] ?? s;
}

/** ImportsPage uploads backups of other apps and lists earlier imports. */
export function ImportsPage() {
  const { data, isLoading, error } = useImports();
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [del, setDel] = useState<ImportResource | null>(null);

  const upload = useMutation({
    mutationFn: (file: File) =>
      unwrap(
        api.POST("/api/v1/imports", {
          params: { query: { fileName: file.name } },
          body: file as unknown as string,
          bodySerializer: (b: unknown) => b as BodyInit,
          headers: { "Content-Type": "application/octet-stream" },
        }),
      ),
    onSuccess: (imp) => {
      qc.invalidateQueries({ queryKey: ["imports"] });
      nav(`/import/${imp.id}`);
    },
    onError: (e) => toast.fromError(e, tr("Couldn't read the backup")),
  });

  const pick = (files: FileList | null) => {
    const f = files?.[0];
    if (f) upload.mutate(f);
  };

  return (
    <>
      <PageHeader title={t("Import library")} subtitle={t("Restore your library, read chapters and lists from another app's backup or a manga site account")} />
      <div className="grid items-start gap-4 lg:grid-cols-2">
        <Card title={t("From an app backup")}>
          <div
            onDragOver={(e) => (e.preventDefault(), setDragging(true))}
            onDragLeave={() => setDragging(false)}
            onDrop={(e) => (e.preventDefault(), setDragging(false), pick(e.dataTransfer.files))}
            className={`flex flex-col items-center gap-3 rounded-lg border-2 border-dashed p-8 text-center transition ${dragging ? "border-accent bg-accent/5" : "border-border"}`}
          >
            {upload.isPending ? <Spinner className="size-8" /> : <FileUp className="size-8 text-muted" />}
            <div className="text-sm">{t("Drop a backup here or")}{" "}
              <button type="button" className="font-medium text-accent-2 hover:underline" onClick={() => input.current?.click()}>{t("choose a file")}</button>
            </div>
            <div className="max-w-xl text-xs text-muted">{t("Mihon, Tachiyomi and forks, and Suwayomi:") + " "}<code>.tachibk</code> / <code>.proto.gz</code>{" " + t("(Mihon: More → Backup and restore → Create backup). Aidoku:") + " "}<code>.aib</code>{" " + t("(Settings → Backups). Nothing is added until you review the matches and start the import.")}</div>
            <input ref={input} type="file" hidden accept=".tachibk,.gz,.proto,.aib,.json,.plist" onChange={(e) => pick(e.target.files)} />
          </div>
        </Card>
        <AccountCard />
      </div>

      <h2 className="mb-2 mt-6 text-sm font-semibold uppercase tracking-wide text-muted">{t("Earlier imports")}</h2>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && data.length === 0 && <EmptyState title={t("No imports yet")}>{t("Upload a backup to see how its manga map to your catalogs.")}</EmptyState>}
      {data && data.length > 0 && (
        <Table>
          <thead>
            <tr>
              <Th>{t("Import")}</Th>
              <Th>{t("From")}</Th>
              <Th>{t("Status")}</Th>
              <Th>{t("Manga")}</Th>
              <Th>{t("Uploaded")}</Th>
              <Th className="w-10" />
            </tr>
          </thead>
          <tbody>
            {data.map((imp) => (
              <tr key={imp.id} className="hover:bg-panel-2/60">
                <Td>
                  <Link to={`/import/${imp.id}`} className="font-medium hover:text-accent-2">
                    {imp.fileName}
                  </Link>
                  {imp.format === "account"
                    ? imp.info.categories.length > 0 && <div className="text-xs text-muted">{t("Lists") + ": " + imp.info.categories.length}</div>
                    : imp.info.backupDate && <div className="text-xs text-muted">{t("made") + " "}{date(imp.info.backupDate)}</div>}
                </Td>
                <Td>{formatLabel(imp)}</Td>
                <Td>
                  <Badge tone={statusTone(imp.status)}>{statusLabel(imp.status)}</Badge>
                  {imp.progress && <div className="mt-1 text-xs text-muted">{imp.progress}</div>}
                </Td>
                <Td className="text-xs text-muted">
                  {imp.counts.all ?? 0} · {imp.counts.imported ?? 0}{" " + t("imported")}</Td>
                <Td className="text-xs text-muted">{relative(imp.createdAt)}</Td>
                <Td>
                  <IconButton title={t("Delete import")} onClick={() => setDel(imp)} disabled={imp.busy}>
                    <Trash2 className="size-4" />
                  </IconButton>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Confirm
        open={!!del}
        title={t("Delete import?")}
        message="The import and its matches are removed. Series it already added stay in the library."
        confirmLabel={t("Delete")}
        danger
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          try {
            await unwrap(api.DELETE("/api/v1/imports/{id}", { params: { path: { id: del.id } } }));
            qc.invalidateQueries({ queryKey: ["imports"] });
          } catch (e) {
            toast.fromError(e, tr("Delete failed"));
          }
          setDel(null);
        }}
      />
    </>
  );
}

/** AccountCard reads a manga site account's lists and progress into a new import. */
function AccountCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const sources = useQuery({ queryKey: ["imports", "accounts"], queryFn: () => unwrap(api.GET("/api/v1/imports/accounts")) });
  const [picked, setPicked] = useState("");
  const [token, setToken] = useState("");
  const list = sources.data ?? [];
  const key = (a: (typeof list)[number]) => `${a.moduleId}:${a.sourceId}`;
  const site = list.find((a) => key(a) === picked) ?? list[0];

  const read = useMutation({
    mutationFn: () => unwrap(api.POST("/api/v1/imports/accounts", { body: { moduleId: site!.moduleId, sourceId: site!.sourceId, token: token.trim() } })),
    onSuccess: (imp) => {
      setToken("");
      qc.invalidateQueries({ queryKey: ["imports"] });
      nav(`/import/${imp.id}`);
    },
    onError: (e) => toast.fromError(e, tr("Couldn't read the account")),
  });

  return (
    <Card title={t("From a site account")}>
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">{t("Bring over the manga lists of your account on a manga site and how far you read each title.")}</p>
        {sources.error && <ErrorBox error={sources.error} />}
        {sources.data && list.length === 0 && <p className="text-sm text-muted">{t("None of the enabled sources can read site accounts.")}</p>}
        {site && (
          <form
            className="flex flex-col gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              if (token.trim()) read.mutate();
            }}
          >
            <Field label={t("Site")}>
              <Select value={key(site)} onChange={(e) => setPicked(e.target.value)}>
                {list.map((a) => (
                  <option key={key(a)} value={key(a)}>
                    {a.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={label(site.field)} help={t("Used once to read your lists and progress, then forgotten. It isn't saved on this server.")}>
              <SecretInput value={token} onChange={setToken} />
            </Field>
            {site.steps.length > 0 && (
              <details className="rounded-md border border-border bg-panel-2/40 px-3 py-2 text-sm">
                <summary className="cursor-pointer font-medium">{t("How to find it")}</summary>
                <ol className="mt-2 list-decimal space-y-1 pl-5 text-muted">
                  {site.steps.map((step) => (
                    <li key={step}>{label(step)}</li>
                  ))}
                </ol>
              </details>
            )}
            <Button type="submit" variant="primary" className="self-start" loading={read.isPending} disabled={!token.trim()}>
              {t("Read my lists")}
            </Button>
          </form>
        )}
      </div>
    </Card>
  );
}
