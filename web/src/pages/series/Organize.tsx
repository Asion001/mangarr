import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { ChevronUp, X } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { useProfiles, useRootFolders, useTags } from "../../api/queries";
import {
  Button,
  ErrorBox,
  Field,
  Loading,
  Menu,
  Modal,
  Select,
  Switch,
} from "../../components/ui";
import { useToast } from "../../lib/toast";
import { BulkSourcesModal } from "./BulkSources";

/** RenameModal previews and applies renames to the current naming format. */
export function RenameModal({
  seriesIds,
  onClose,
}: {
  seriesIds: number[];
  onClose: () => void;
}) {
  const toast = useToast();
  const [folders, setFolders] = useState(false);
  const { data, isLoading, error } = useQuery({
    queryKey: ["rename-preview", seriesIds, folders],
    queryFn: () =>
      unwrap(
        api.POST("/api/v1/series/rename/preview", {
          body: { seriesIds, folders },
        }),
      ),
  });
  const [applying, setApplying] = useState(false);
  const files = (data ?? []).reduce((n, s) => n + s.files.length, 0);
  const dirs = (data ?? []).filter((s) => s.folderTo).length;
  const apply = async () => {
    setApplying(true);
    try {
      await unwrap(
        api.POST("/api/v1/series/rename", { body: { seriesIds, folders } }),
      );
      toast.success(
        tr("Renaming in the background"),
        tr("Reader progress is restored on library servers afterwards."),
      );
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setApplying(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Rename files")}
      size="xl"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button
            variant="primary"
            loading={applying}
            disabled={!files && !dirs}
            onClick={apply}
          >{t("Rename") + " "}{files}{" " + t("files")}{dirs ? ` and ${dirs} folders` : ""}
          </Button>
        </>
      }
    >
      <Switch
        checked={folders}
        onChange={setFolders}
        label={t("Also rename series folders to the folder format")}
      />
      <p className="mb-3 mt-2 text-xs text-muted">{t("Names follow Settings → Media management. Library servers may treat renamed files as new books; mangarr restores readers' progress afterwards.")}</p>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && data.length === 0 && (
        <p className="text-sm text-muted">{t("Everything already matches the naming format.")}</p>
      )}
      <div className="flex max-h-[55vh] flex-col gap-3 overflow-y-auto">
        {data?.map((s) => (
          <div key={s.seriesId}>
            <div className="text-sm font-medium">{s.title}</div>
            {s.folderTo && (
              <div className="font-mono text-xs">
                <span className="text-muted">{s.folderFrom}/</span> →{" "}
                <span className="text-accent-2">{s.folderTo}/</span>
              </div>
            )}
            {s.files.map((f) => (
              <div key={f.chapterId} className="font-mono text-xs break-all">
                <span className="text-muted">{f.from}</span> →{" "}
                <span className="text-accent-2">{f.to}</span>
              </div>
            ))}
          </div>
        ))}
      </div>
    </Modal>
  );
}

type Editor = {
  monitored?: boolean;
  monitorNew?: "all" | "none";
  profileId?: number;
  rootFolderId?: number;
  moveFiles?: boolean;
  tags?: number[];
  tagMode?: "add" | "remove" | "replace";
};

/**
 * MassEditBar edits the selected series. Picking a value from a menu only
 * stages the change; it runs when you press Apply.
 */
export function MassEditBar({
  selected,
  onRemove,
  onClear,
}: {
  /** selected maps series id to title. */
  selected: Map<number, string>;
  onRemove: (id: number) => void;
  onClear: () => void;
}) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: profiles } = useProfiles();
  const { data: roots } = useRootFolders();
  const { data: tags } = useTags();
  const ids = [...selected.keys()];
  const count = ids.length;
  const [moving, setMoving] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [sourcing, setSourcing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [busy, setBusy] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  // pending keeps the series it was confirmed for: Apply changes those, even
  // if the selection changed after the question was asked
  const [pending, setPending] = useState<{ text: string; body: Editor; ids: number[] } | null>(null);
  const [rootId, setRootId] = useState(0);
  const [moveFiles, setMoveFiles] = useState(true);
  const edit = async (body: Editor, done?: string, seriesIds = ids) => {
    setBusy(true);
    try {
      const r = await unwrap(api.POST("/api/v1/series/editor", { body: { seriesIds, ...body } }));
      qc.invalidateQueries({ queryKey: ["series"] });
      toast.success(done ?? t("{count} series updated", { count: r.updated }), r.moves ? t("{count} moving in the background", { count: r.moves }) : undefined);
      setPending(null);
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  const command = async (name: "SearchMissing" | "RefreshSeries", label: string) => {
    try {
      await unwrap(api.POST("/api/v1/commands", { body: { name, body: { seriesIds: ids } } }));
      toast.info(label);
    } catch (e) {
      toast.fromError(e);
    }
  };
  const removeAll = async () => {
    setBusy(true);
    let done = 0;
    try {
      for (const id of ids) {
        await unwrap(api.DELETE("/api/v1/series/{id}", { params: { path: { id }, query: { deleteFiles } } }));
        // a retry after a failure starts with the series that are left
        onRemove(id);
        done++;
      }
      toast.success(t("{count} series deleted", { count: done }));
      onClear();
      setDeleting(false);
    } catch (e) {
      toast.fromError(e, t("Stopped after {count} series", { count: done }));
    } finally {
      qc.invalidateQueries({ queryKey: ["series"] });
      setBusy(false);
    }
  };
  const stage = (text: string, body: Editor) => setPending({ text, body, ids });
  // dialogs render outside the bar: its backdrop-blur would clip fixed children
  return (
    <>
      <div role="toolbar" aria-label={t("Selection")} className="fixed inset-x-0 bottom-0 z-20 border-t border-border bg-panel/95 px-4 py-3 shadow-lg backdrop-blur md:left-(--nav-width)">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative">
            <Button variant="primary" aria-expanded={listOpen} onClick={() => setListOpen(!listOpen)}>
              {t("{count} selected", { count })}
              <ChevronUp className="size-3.5" />
            </Button>
            {listOpen && (
              <ul aria-label={t("Selected series")} className="absolute bottom-full left-0 mb-2 max-h-80 w-72 max-w-[calc(100vw-1rem)] overflow-y-auto rounded-lg border border-border bg-panel-2 p-1 shadow-xl">
                {[...selected].map(([id, title]) => (
                  <li key={id} className="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm hover:bg-border">
                    <span className="min-w-0 flex-1 truncate">{title || `#${id}`}</span>
                    <button type="button" aria-label={t("Remove {title} from the selection", { title })} className="text-muted hover:text-fg" onClick={() => onRemove(id)}>
                      <X className="size-3.5" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
          <Button variant="ghost" onClick={onClear}>{t("Clear")}</Button>
          <span aria-hidden className="mx-1 h-6 w-px bg-border" />
          {pending ? (
            <div role="status" className="flex min-w-0 flex-1 flex-wrap items-center gap-2 rounded-md border border-warn/40 bg-warn/10 px-3 py-1.5 text-sm">
              <span className="min-w-0 flex-1">{pending.text}</span>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => setPending(null)}>{t("Cancel")}</Button>
              <Button size="sm" variant="primary" loading={busy} onClick={() => void edit(pending.body, undefined, pending.ids)}>{t("Apply")}</Button>
            </div>
          ) : (
            <>
              <Menu
                up
                label={t("Monitor")}
                items={[
                  { label: t("Monitor"), onSelect: () => stage(t("Monitor {count} series?", { count }), { monitored: true }) },
                  { label: t("Unmonitor"), onSelect: () => stage(t("Stop monitoring {count} series?", { count }), { monitored: false }) },
                  { section: t("New chapters") },
                  { label: t("Download new chapters"), onSelect: () => stage(t("Download new chapters of {count} series?", { count }), { monitorNew: "all" }) },
                  { label: t("Don’t monitor new chapters"), onSelect: () => stage(t("Stop monitoring new chapters of {count} series?", { count }), { monitorNew: "none" }) },
                ]}
              />
              <Menu
                up
                label={t("Profile")}
                items={(profiles ?? []).map((p) => ({ label: p.name, onSelect: () => stage(t("Set profile {name} on {count} series?", { name: p.name, count }), { profileId: p.id }) }))}
              />
              <Menu
                up
                label={t("Tags")}
                items={
                  tags?.length
                    ? [
                        { section: t("Add") },
                        ...tags.map((tag) => ({ label: tag.label, onSelect: () => stage(t("Add tag {tag} to {count} series?", { tag: tag.label, count }), { tags: [tag.id], tagMode: "add" }) })),
                        { section: t("Remove") },
                        ...tags.map((tag) => ({ label: tag.label, onSelect: () => stage(t("Remove tag {tag} from {count} series?", { tag: tag.label, count }), { tags: [tag.id], tagMode: "remove" }) })),
                      ]
                    : [{ label: t("No tags yet — add them in Edit"), onSelect: () => undefined }]
                }
              />
              <Button onClick={() => setMoving(true)}>{t("Move…")}</Button>
              <Button onClick={() => setSourcing(true)}>{t("Sources…")}</Button>
              <Menu
                up
                label={t("More")}
                items={[
                  { label: t("Search missing chapters"), onSelect: () => void command("SearchMissing", t("Searching missing chapters of {count} series", { count })) },
                  { label: t("Refresh sources"), onSelect: () => void command("RefreshSeries", t("Refreshing {count} series", { count })) },
                  { label: t("Rename files…"), onSelect: () => setRenaming(true) },
                  { section: "" },
                  { label: t("Delete series…"), danger: true, onSelect: () => setDeleting(true) },
                ]}
              />
            </>
          )}
        </div>
      </div>
      {sourcing && <BulkSourcesModal ids={ids} onClose={() => setSourcing(false)} />}
      {deleting && (
        <Modal
          open
          onClose={() => setDeleting(false)}
          title={t("Delete {count} series", { count })}
          footer={
            <>
              <Button disabled={busy} onClick={() => setDeleting(false)}>{t("Cancel")}</Button>
              <Button variant="danger" loading={busy} onClick={() => void removeAll()}>{t("Delete")}</Button>
            </>
          }
        >
          <p className="mb-3 text-sm">{t("They leave the library and stop downloading. Read progress in Komga/Kavita is not touched.")}</p>
          <Switch checked={deleteFiles} onChange={setDeleteFiles} label={t("Also delete their files from disk")} />
        </Modal>
      )}
      {moving && (
        <Modal
          open
          onClose={() => setMoving(false)}
          title={`Move ${ids.length} series`}
          footer={
            <>
              <Button onClick={() => setMoving(false)}>{t("Cancel")}</Button>
              <Button
                variant="primary"
                disabled={!rootId}
                onClick={async () => {
                  await edit(
                    { rootFolderId: rootId, moveFiles },
                    "Moving in the background",
                  );
                  setMoving(false);
                }}
              >{t("Move")}</Button>
            </>
          }
        >
          <Field label={t("Root folder")}>
            <Select
              value={rootId}
              onChange={(e) => setRootId(Number(e.target.value))}
            >
              <option value={0}>{t("Choose…")}</option>
              {roots?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.path}
                </option>
              ))}
            </Select>
          </Field>
          <div className="mt-3">
            <Switch
              checked={moveFiles}
              onChange={setMoveFiles}
              label={t("Move the files (off: they were already moved by hand)")}
            />
          </div>
          <p className="mt-3 text-xs text-muted">{t("Moving to a root folder in another Komga/Kavita library resets read progress there; mangarr writes readers' progress back once the server has scanned the new location (readers need linked accounts).")}</p>
        </Modal>
      )}
      {renaming && <RenameModal seriesIds={ids} onClose={() => setRenaming(false)} />}
    </>
  );
}
