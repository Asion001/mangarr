import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Lock, Unlock } from "lucide-react";
import { api, unwrap, type Series, type UpdateRequest } from "../../api/client";
import { useProfiles, useRootFolders, useTags } from "../../api/queries";
import { Button, Field, Input, Modal, Select, Switch, TagInput, Textarea } from "../../components/ui";
import { useToast } from "../../lib/toast";

export function EditSeriesModal({ series, onClose, onMetadata }: { series: Series; onClose: () => void; onMetadata: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: profiles } = useProfiles();
  const { data: tags } = useTags();
  const [title, setTitle] = useState(series.title);
  const [monitored, setMonitored] = useState(series.monitored);
  const [monitorNew, setMonitorNew] = useState(series.monitorNew);
  const [profileId, setProfileId] = useState(series.profileId);
  const [direction, setDirection] = useState(series.readingDirection);
  const [language, setLanguage] = useState(series.language);
  const [status, setStatus] = useState(series.status);
  const [description, setDescription] = useState(series.metadata.description ?? "");
  const [tagIds, setTagIds] = useState<number[]>(series.tags ?? []);
  const [blocked, setBlocked] = useState<string[]>(series.blockedScanlators ?? []);
  const [locks, setLocks] = useState<string[]>(series.metadata.locks ?? []);
  const { data: roots } = useRootFolders();
  const [rootId, setRootId] = useState(series.rootFolderId);
  const [folder, setFolder] = useState(series.path);
  const [moveFiles, setMoveFiles] = useState(true);
  const moving = rootId !== series.rootFolderId || folder.trim() !== series.path;
  const [saving, setSaving] = useState(false);

  const save = async () => {
    setSaving(true);
    try {
      await unwrap(
        api.PUT("/api/v1/series/{id}", {
          params: { path: { id: series.id } },
          body: {
            title: title !== series.title ? title : undefined,
            monitored,
            monitorNew: monitorNew as UpdateRequest["monitorNew"],
            profileId,
            readingDirection: (direction !== series.readingDirection ? direction : undefined) as UpdateRequest["readingDirection"],
            language,
            status: (status !== series.status ? status : undefined) as UpdateRequest["status"],
            description: description !== (series.metadata.description ?? "") ? description : undefined,
            tags: tagIds,
            blockedScanlators: blocked,
            locks,
            rootFolderId: rootId !== series.rootFolderId ? rootId : undefined,
            path: folder.trim() !== series.path ? folder.trim() : undefined,
            moveFiles: moving ? moveFiles : undefined,
          },
        }),
      );
      qc.invalidateQueries({ queryKey: ["series"] });
      toast.success(tr("Series saved"), moving ? (moveFiles ? "Moving the files in the background" : "Location updated") : undefined);
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={`Edit ${series.title}`}
      size="lg"
      footer={
        <>
          <Button onClick={onMetadata} className="mr-auto">{t("Metadata…")}</Button>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Field label={t("Title")} help={t("Editing locks the field against metadata refreshes.")} className="md:col-span-2">
          <Input value={title} onChange={(e) => setTitle(e.target.value)} />
        </Field>
        <Field label={t("Profile")}>
          <Select value={profileId} onChange={(e) => setProfileId(Number(e.target.value))}>
            {profiles?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("New chapters")}>
          <Select value={monitorNew} onChange={(e) => setMonitorNew(e.target.value)}>
            <option value="all">{t("Monitor automatically")}</option>
            <option value="none">{t("Don't monitor")}</option>
          </Select>
        </Field>
        <Field label={t("Reading direction")} help={t("Written to ComicInfo (Manga field).")}>
          <Select value={direction} onChange={(e) => setDirection(e.target.value)}>
            <option value="rtl">{t("Right to left (manga)")}</option>
            <option value="ltr">{t("Left to right")}</option>
            <option value="vertical">{t("Vertical")}</option>
            <option value="webtoon">{t("Webtoon (long strip)")}</option>
          </Select>
        </Field>
        <Field label={t("Status")}>
          <Select value={status} onChange={(e) => setStatus(e.target.value)}>
            {["unknown", "ongoing", "completed", "hiatus", "cancelled"].map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Language (BCP-47)")}>
          <Input value={language} onChange={(e) => setLanguage(e.target.value)} placeholder="en" />
        </Field>
        <Field label={t("Tags")}>
          <div className="flex flex-wrap gap-2">
            {tags?.map((t) => (
              <label key={t.id} className="flex items-center gap-1 text-sm">
                <input type="checkbox" checked={tagIds.includes(t.id)} onChange={(e) => setTagIds(e.target.checked ? [...tagIds, t.id] : tagIds.filter((x) => x !== t.id))} />
                {t.label}
              </label>
            ))}
            {!tags?.length && <span className="text-xs text-muted">{t("Create tags in Settings → General.")}</span>}
          </div>
        </Field>
        <Field label={t("Blocked scanlators")} help={t("Releases by these groups are never downloaded for this series (on top of the profile's list).")}>
          <TagInput value={blocked} onChange={setBlocked} placeholder={t("Group name, Enter")} />
        </Field>
        <Field label={t("Root folder")}>
          <Select value={rootId} onChange={(e) => setRootId(Number(e.target.value))}>
            {roots?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.path}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("Folder")}>
          <Input value={folder} onChange={(e) => setFolder(e.target.value)} />
        </Field>
        {moving && (
          <div className="md:col-span-2">
            <Switch checked={moveFiles} onChange={setMoveFiles} label={t("Move the files (off: they were already moved by hand)")} />
            <p className="mt-1 text-xs text-muted">{t("Reader progress on Komga/Kavita is restored after the move.")}</p>
          </div>
        )}
        <Field label={t("Description")} className="md:col-span-2">
          <Textarea rows={5} value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <div className="md:col-span-2">
          <Switch checked={monitored} onChange={setMonitored} label={t("Monitored")} />
        </div>
        {locks.length > 0 && (
          <Field label={t("Locked fields")} help={t("Click to unlock; unlocked fields follow metadata refreshes again.")} className="md:col-span-2">
            <div className="flex flex-wrap gap-1.5">
              {locks.map((l) => (
                <button key={l} type="button" className="inline-flex items-center gap-1 rounded bg-panel-2 px-2 py-1 text-xs hover:bg-border" onClick={() => setLocks(locks.filter((x) => x !== l))}>
                  <Lock className="size-3" /> {l} <Unlock className="size-3 text-muted" />
                </button>
              ))}
            </div>
          </Field>
        )}
      </div>
    </Modal>
  );
}
