import { t } from "../../lib/i18n/core";
import { Link } from "react-router";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Eraser, RefreshCw } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { usePushCommand, useTags, useUsers } from "../../api/queries";
import { Badge, Button, Card, Confirm, ErrorBox, Field, Input, Loading, PageHeader, SaveBar, Switch, Table, Td, Th } from "../../components/ui";
import { bytes, relative } from "../../lib/format";
import { useSettingsDoc } from "./useSettingsDoc";

type CleanupSettings = S["Cleanup"];
type Skip = S["CleanupSkip"];

// the preview lists at most this many chapters
const previewRows = 200;

const statusName = (s: string) =>
  ({ ongoing: t("Ongoing"), completed: t("Completed"), hiatus: t("Hiatus"), cancelled: t("Cancelled"), unknown: t("Unknown") })[s] ?? s;

/** skipReason says why a series has nothing to clean, in the UI's language. */
function skipReason(s: Skip): string {
  const p = s.params ?? {};
  switch (s.code) {
    case "status": return t("its status ({status}) isn't in scope", { status: statusName(p.status ?? "") });
    case "no-reader": return t("no required reader has started it in this language");
    case "none-finished": return t("no chapter is finished by every required reader yet");
    case "keep": return t("keeping the last {count} read chapters", { count: p.count ?? "" });
    case "grace": return t("finished chapters are still in the {days} day grace period", { days: p.days ?? "" });
    case "enough-space": return t("{path} has more than {gb} GB free", { path: p.path ?? "", gb: p.gb ?? "" });
    case "bin-pending": return t("{path} gets back above {gb} GB free once the recycle bin empties", { path: p.path ?? "", gb: p.gb ?? "" });
    case "tagged": return t("tagged {tag}", { tag: p.tag ?? "" });
  }
  return s.reason;
}

export function CleanupPage() {
  const { value: c, patch, save, saving, isLoading, error, lock, dirty, reset } = useSettingsDoc<CleanupSettings>("cleanup");
  const { data: users } = useUsers();
  const { data: tags } = useTags();
  const push = usePushCommand();
  const [confirmRun, setConfirmRun] = useState(false);
  const preview = useQuery({ queryKey: ["cleanup-preview"], queryFn: () => unwrap(api.GET("/api/v1/cleanup/preview")) });
  const toggleStatus = (s: string) => {
    if (!c) return;
    const cur = c.statuses ?? [];
    patch({ statuses: cur.includes(s) ? cur.filter((x) => x !== s) : [...cur, s] });
  };
  const toggleUser = (id: number) => {
    if (!c) return;
    const cur = c.userIds ?? [];
    patch({ userIds: cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id] });
  };
  return (
    <>
      <PageHeader
        title={t("Read-based cleanup")}
        subtitle={t("Delete chapters everyone counted has finished to save space. Cleaned chapters are never downloaded again unless you restore them.")}
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {c && (
        <Card title={t("Rules")} className="mb-6">
          <div className="grid gap-5 md:grid-cols-2">
            <div className="flex flex-col gap-3">
              <Switch env={lock("enabled")} checked={c.enabled} onChange={(v) => patch({ enabled: v })} label={<b>{t("Enable cleanup")}</b>} />
              <Switch env={lock("dryRun")} checked={c.dryRun} onChange={(v) => patch({ dryRun: v })} label={t("Dry run (only preview, never delete)")} />
              <Switch env={lock("ignoreReadersNotStarted")} checked={c.ignoreReadersNotStarted} onChange={(v) => patch({ ignoreReadersNotStarted: v })} label={t("Ignore readers who never started a series")} />
              <Switch env={lock("useRecycleBin")} checked={c.useRecycleBin} onChange={(v) => patch({ useRecycleBin: v })} label={t("Move to recycle bin instead of deleting")} />
            </div>
            <div className="grid grid-cols-2 gap-4">
              <Field env={lock("keepLastRead")} label={t("Keep last read chapters")} help={t("Keeps apps' progress anchored")}>
                <Input type="number" min={0} value={c.keepLastRead} onChange={(e) => patch({ keepLastRead: Number(e.target.value) })} />
              </Field>
              <Field env={lock("graceDays")} label={t("Grace period (days)")} help={t("After the last reader finished")}>
                <Input type="number" min={0} value={c.graceDays} onChange={(e) => patch({ graceDays: Number(e.target.value) })} />
              </Field>
              <Field env={lock("minFreeSpaceGb")} label={t("Only when free space below (GB)")} help={t("0 = always")}>
                <Input type="number" min={0} value={c.minFreeSpaceGb} onChange={(e) => patch({ minFreeSpaceGb: Number(e.target.value) })} />
              </Field>
            </div>
            <Field env={lock("statuses")} label={t("Series status in scope")}>
              <div className="flex flex-wrap gap-3">
                {["ongoing", "completed", "hiatus", "cancelled", "unknown"].map((s) => (
                  <label key={s} className="flex items-center gap-1.5 text-sm">
                    <input type="checkbox" checked={(c.statuses ?? []).includes(s)} onChange={() => toggleStatus(s)} /> {statusName(s)}
                  </label>
                ))}
              </div>
            </Field>
            <Field env={lock("userIds")} label={t("Required users")} help={t("None selected = every user counting for cleanup (Settings → Users), or everyone when nobody is marked to count.")}>
              <div className="flex flex-wrap gap-3">
                {users?.map((u) => (
                  <label key={u.id} className="flex items-center gap-1.5 text-sm">
                    <input type="checkbox" checked={(c.userIds ?? []).includes(u.id)} onChange={() => toggleUser(u.id)} /> {u.displayName || u.username}
                  </label>
                ))}
              </div>
            </Field>
            <Field env={lock("excludeTags")} label={t("Excluded tags")} help={t("Series with any of these tags are never cleaned.")}>
              <div className="flex flex-wrap gap-3">
                {Array.from(new Set([...(c.excludeTags ?? []), ...(tags ?? []).map((t) => t.label)])).map((t) => (
                  <label key={t} className="flex items-center gap-1.5 text-sm">
                    <input
                      type="checkbox"
                      checked={(c.excludeTags ?? []).includes(t)}
                      onChange={(e) => patch({ excludeTags: e.target.checked ? [...(c.excludeTags ?? []), t] : (c.excludeTags ?? []).filter((x) => x !== t) })}
                    />
                    {t}
                  </label>
                ))}
              </div>
            </Field>
          </div>
        </Card>
      )}
      <Card
        title={t("Preview")}
        actions={
          <>
            <Button size="sm" icon={<RefreshCw className="size-3.5" />} onClick={() => preview.refetch()}>{t("Refresh")}</Button>
            <Button
              size="sm"
              variant="danger"
              icon={<Eraser className="size-3.5" />}
              disabled={!preview.data?.candidates.length}
              onClick={() => setConfirmRun(true)}
            >{t("Run now")}</Button>
          </>
        }
      >
        {preview.data && (
          <Confirm
            open={confirmRun}
            danger
            title={t("Run cleanup now?")}
            confirmLabel={t("Remove chapters")}
            loading={push.isPending}
            message={(c?.useRecycleBin ? t("{count} chapters ({size}) move to the recycle bin.", { count: preview.data.candidates.length, size: bytes(preview.data.totalSize) })
              : t("{count} chapters ({size}) are deleted for good.", { count: preview.data.candidates.length, size: bytes(preview.data.totalSize) }))
              + (preview.data.dryRun ? " " + t("This runs even though dry run is on.") : "")
              + " " + t("They won't be downloaded again unless you restore them.")}
            onConfirm={() => push.mutate({ name: "Cleanup", body: { force: true }, label: t("Cleanup started") }, { onSettled: () => setConfirmRun(false) })}
            onClose={() => setConfirmRun(false)}
          />
        )}
        {preview.isLoading && <Loading />}
        {preview.error && <ErrorBox error={preview.error} />}
        {preview.data && (
          <>
            <p className="mb-3 text-sm">
              {preview.data.candidates.length}{" " + t("chapters ·") + " "}<b>{bytes(preview.data.totalSize)}</b>{" " + t("would be freed")}{!preview.data.enabled && <span className="ml-2"><Badge tone="warn">{t("cleanup disabled")}</Badge></span>}
              {preview.data.dryRun && <span className="ml-2"><Badge tone="info">{t("dry run")}</Badge></span>}
            </p>
            {preview.data.candidates.length > 0 && (
              <Table className="mb-4">
                <thead>
                  <tr>
                    <Th>{t("Series")}</Th>
                    <Th>{t("Chapter")}</Th>
                    <Th>{t("Size")}</Th>
                    <Th>{t("Last read")}</Th>
                  </tr>
                </thead>
                <tbody>
                  {preview.data.candidates.slice(0, previewRows).map((cd) => (
                    <tr key={cd.chapterId}>
                      <Td>
                        <Link to={`/series/${cd.seriesId}`} className="hover:text-accent-2">
                          {cd.seriesTitle}
                        </Link>
                      </Td>
                      <Td>{cd.chapter}</Td>
                      <Td>{bytes(cd.size)}</Td>
                      <Td className="text-muted">{relative(cd.lastReadAt)}</Td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            )}
            {preview.data.candidates.length > previewRows && (
              <p className="mb-3 text-sm text-muted">{t("Showing the first {shown} of {count} chapters.", { shown: previewRows, count: preview.data.candidates.length })}</p>
            )}
            {preview.data.held > 0 && (
              <p className="mb-3 text-sm text-muted">
                {t("{count} more finished chapters ({size}) are kept: removing the ones above already brings the drive back above the free space limit.", { count: preview.data.held, size: bytes(preview.data.heldSize) })}
              </p>
            )}
            {preview.data.skipped.length > 0 && (
              <details className="text-sm">
                <summary className="cursor-pointer text-muted">{preview.data.skipped.length}{" " + t("series skipped")}</summary>
                <ul className="mt-2 flex flex-col gap-1">
                  {preview.data.skipped.map((s) => (
                    <li key={s.seriesId} className="text-xs">
                      <span className="text-fg">{s.seriesTitle}</span> <span className="text-muted">— {skipReason(s)}</span>
                    </li>
                  ))}
                </ul>
              </details>
            )}
          </>
        )}
      </Card>
      <SaveBar dirty={dirty} saving={saving} onSave={async () => (await save(), preview.refetch())} onDiscard={reset} />
    </>
  );
}
