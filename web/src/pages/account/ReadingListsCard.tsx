import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Pencil, Plus, Trash2, X } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Card, Confirm, IconButton, Input } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { readingListName, readingStatusName, useReadingLists, type ReadingList, type ReadingStatus } from "../../lib/readingLists";

/** ReadingListsCard shows your statuses and lists, and renames and deletes the lists you made. */
export function ReadingListsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data } = useReadingLists();
  const [editing, setEditing] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<ReadingList | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["me", "lists"] });
  const run = async (fn: () => Promise<unknown>) => {
    try {
      await fn();
      setEditing(null);
      setAdding(false);
      setName("");
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (!data?.lists) return null;
  const link = (key: string) => `/?list=${encodeURIComponent(key)}`;
  return (
    <Card title={t("Your lists")} actions={!adding && <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => { setAdding(true); setEditing(null); setName(""); }}>{t("New list…")}</Button>}>
      <div className="flex flex-col gap-3 text-sm">
        <div className="flex flex-wrap gap-2">
          {data.statuses.map((s) => (
            <Link key={s.status} to={link(s.status)} className="rounded-full border border-border bg-panel px-3 py-1 hover:border-accent-2">
              {readingStatusName(s.status as ReadingStatus)} <span className="text-muted">{s.count}</span>
            </Link>
          ))}
        </div>
        <ul className="flex flex-col divide-y divide-border">
          {data.lists.map((l) => (
            <li key={l.id} className="flex min-h-10 items-center gap-2 py-1">
              {editing === l.id ? (
                <form className="flex flex-1 items-center gap-1" onSubmit={(e) => { e.preventDefault(); void run(() => unwrap(api.PUT("/api/v1/me/lists/{id}", { params: { path: { id: l.id } }, body: { name } }))); }}>
                  <Input autoFocus className="h-8" value={name} maxLength={100} aria-label={t("List name")} onChange={(e) => setName(e.target.value)} />
                  <IconButton title={t("Save")} type="submit" disabled={!name.trim()}><Check className="size-4" /></IconButton>
                  <IconButton title={t("Cancel")} type="button" onClick={() => setEditing(null)}><X className="size-4" /></IconButton>
                </form>
              ) : (
                <>
                  <Link to={link(String(l.id))} className="flex-1 truncate hover:text-accent-2">{readingListName(l)}</Link>
                  <span className="text-muted">{l.count}</span>
                  {l.kind !== "favorites" && (
                    <>
                      <IconButton title={t("Rename")} onClick={() => { setEditing(l.id); setName(l.name); setAdding(false); }}><Pencil className="size-4" /></IconButton>
                      <IconButton title={t("Delete")} onClick={() => setDeleting(l)}><Trash2 className="size-4" /></IconButton>
                    </>
                  )}
                </>
              )}
            </li>
          ))}
        </ul>
        {adding && (
          <form className="flex items-center gap-1" onSubmit={(e) => { e.preventDefault(); void run(() => unwrap(api.POST("/api/v1/me/lists", { body: { name } }))); }}>
            <Input autoFocus className="h-8" placeholder={t("List name")} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
            <Button size="sm" type="submit" disabled={!name.trim()}>{t("Add")}</Button>
            <Button size="sm" variant="ghost" type="button" onClick={() => setAdding(false)}>{t("Cancel")}</Button>
          </form>
        )}
        <p className="text-xs text-muted">{t("Put a title in a status or a list from its page. Lists brought in from another app or site land here too.")}</p>
      </div>
      <Confirm
        open={!!deleting}
        title={t("Delete {name}?", { name: deleting?.name ?? "" })}
        message={t("The titles stay in the library.")}
        confirmLabel={t("Delete")}
        danger
        onConfirm={() => { const id = deleting?.id ?? 0; setDeleting(null); void run(() => unwrap(api.DELETE("/api/v1/me/lists/{id}", { params: { path: { id } } }))); }}
        onClose={() => setDeleting(null)}
      />
    </Card>
  );
}
