import { t as tr, t } from "../../lib/i18n/core";
import { useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArchiveRestore, Download, Plus, ShieldCheck, Trash2, Upload } from "lucide-react";
import { api, apiUrl, basePath, unwrap } from "../../api/client";
import { Badge, Button, Confirm, EmptyState, IconButton, Loading, PageHeader, Table, Td, Th } from "../../components/ui";
import { bytes, dateTime } from "../../lib/format";
import { useToast } from "../../lib/toast";

export function BackupsPage() {
  const qc = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [restoring, setRestoring] = useState<string | null>(null);
  const [verifying, setVerifying] = useState<string | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const { data, isLoading } = useQuery({ queryKey: ["backups"], queryFn: () => unwrap(api.GET("/api/v1/system/backups")) });
  const create = async () => {
    setCreating(true);
    try {
      await unwrap(api.POST("/api/v1/system/backups"));
      qc.invalidateQueries({ queryKey: ["backups"] });
      toast.success(tr("Backup created"));
    } catch (e) {
      toast.fromError(e);
    } finally {
      setCreating(false);
    }
  };
  const upload = async (f: File) => {
    setUploading(true);
    try {
      const r = await fetch(basePath + "/api/v1/system/backups/upload", { method: "POST", headers: { "Content-Type": "application/zip" }, body: f });
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        throw new Error(body.detail || `HTTP ${r.status}`);
      }
      qc.invalidateQueries({ queryKey: ["backups"] });
      toast.success(tr("Backup added"), tr("Restore it from the list"));
    } catch (e) {
      toast.fromError(e, tr("Couldn't add the backup"));
    } finally {
      setUploading(false);
      if (file.current) file.current.value = "";
    }
  };
  const restore = async () => {
    if (!restoring) return;
    try {
      await unwrap(api.POST("/api/v1/system/backups/{name}/restore", { params: { path: { name: restoring } } }));
      navigate("/system/database");
    } catch (e) {
      toast.fromError(e, tr("Couldn't restore"));
    }
    setRestoring(null);
  };
  const verify = async (name: string) => {
    setVerifying(name);
    try {
      const v = await unwrap(api.POST("/api/v1/system/backups/{name}/verify", { params: { path: { name } } }));
      qc.invalidateQueries({ queryKey: ["backups"] });
      toast.success(tr("Backup verified"), tr("{rows} rows from mangarr {version} on {database}", { rows: v.total, version: v.sourceVersion || "?", database: v.sourceDatabase || "?" }));
    } catch (e) {
      toast.fromError(e, tr("This backup can't be restored"));
    } finally {
      setVerifying(null);
    }
  };
  const remove = async (name: string) => {
    await unwrap(api.DELETE("/api/v1/system/backups/{name}", { params: { path: { name } } }));
    qc.invalidateQueries({ queryKey: ["backups"] });
  };
  return (
    <>
      <PageHeader
        title={t("Backups")}
        subtitle={t("The whole database, on SQLite and PostgreSQL alike. A backup restores into either, also on another install.")}
        actions={
          <>
            <input ref={file} type="file" accept=".zip,application/zip" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
            <Button icon={<Upload className="size-4" />} loading={uploading} onClick={() => file.current?.click()}>{t("Add a backup file")}</Button>
            <Button variant="primary" loading={creating} icon={<Plus className="size-4" />} onClick={create}>{t("Back up now")}</Button>
          </>
        }
      />
      {isLoading && <Loading />}
      {data?.length === 0 && <EmptyState title={t("No backups yet")} />}
      {data && data.length > 0 && (
        <Table>
          <thead>
            <tr>
              <Th>{t("Name")}</Th>
              <Th>{t("Type")}</Th>
              <Th>{t("Size")}</Th>
              <Th>{t("Created")}</Th>
              <Th>{t("Verified")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.map((b) => (
              <tr key={b.name}>
                <Td className="font-mono text-xs">{b.name}</Td>
                <Td>
                  <Badge>{b.type}</Badge>
                </Td>
                <Td>{bytes(b.size)}</Td>
                <Td className="text-muted">{dateTime(b.created)}</Td>
                <Td>
                  {b.verification ? (
                    <span
                      title={[
                        tr("Verified {when}", { when: dateTime(b.verification.verifiedAt) }),
                        `mangarr ${b.verification.sourceVersion || "?"} · ${b.verification.sourceDatabase || "?"}`,
                        tr("{rows} rows", { rows: b.verification.total }),
                        `SHA-256 ${b.verification.checksum}`,
                      ].join("\n")}
                    >
                      <Badge tone="ok">{t("Restorable")}</Badge>
                      <span className="ml-2 text-xs whitespace-nowrap text-muted">{dateTime(b.verification.verifiedAt)}</span>
                    </span>
                  ) : (
                    <Badge tone="warn">{t("Not verified")}</Badge>
                  )}
                </Td>
                <Td className="text-right">
                  <div className="flex justify-end">
                    <IconButton title={t("Verify")} disabled={verifying === b.name} onClick={() => verify(b.name)}>
                      <ShieldCheck className={verifying === b.name ? "size-4 animate-pulse" : "size-4"} />
                    </IconButton>
                    <a href={apiUrl(`api/v1/system/backups/${encodeURIComponent(b.name)}`)} download>
                      <IconButton title={t("Download")}>
                        <Download className="size-4" />
                      </IconButton>
                    </a>
                    <IconButton title={t("Restore")} onClick={() => setRestoring(b.name)}>
                      <ArchiveRestore className="size-4" />
                    </IconButton>
                    <IconButton title={t("Delete")} onClick={() => remove(b.name)}>
                      <Trash2 className="size-4" />
                    </IconButton>
                  </div>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
      <Confirm
        open={!!restoring}
        title={t("Restore backup")}
        danger
        confirmLabel={t("Replace data and restart")}
        message={`All of mangarr's data is replaced with ${restoring}'s, then mangarr restarts. Files in your library aren't touched. Make a backup first if you might want today's data back.`}
        onConfirm={restore}
        onClose={() => setRestoring(null)}
      />
    </>
  );
}
