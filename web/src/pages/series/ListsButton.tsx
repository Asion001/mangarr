import { t } from "../../lib/i18n/core";
import { useEffect, useRef, useState } from "react";
import clsx from "clsx";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bookmark, BookmarkCheck, Check, Plus } from "lucide-react";
import { api, unwrap } from "../../api/client";
import { Button, Input } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { useReadingLists, readingListName, readingStatusName, readingStatuses, type ReadingStatus } from "../../lib/readingLists";

/**
 * ListsButton puts a title in one of your statuses (one at a time) and in
 * any of your lists, or makes a new list. Every edition of the title shares
 * them.
 */
export function ListsButton({ seriesId }: { seriesId: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [open, setOpen] = useState(false);
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState("");
  const ref = useRef<HTMLDivElement>(null);
  const key = ["series", seriesId, "lists"];
  const { data: entry } = useQuery({ queryKey: key, queryFn: () => unwrap(api.GET("/api/v1/series/{id}/lists", { params: { path: { id: seriesId } } })) });
  const { data: mine } = useReadingLists();

  useEffect(() => {
    if (!open) return;
    const outside = (e: PointerEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const escape = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  useEffect(() => {
    if (!open) setNaming(false);
  }, [open]);

  const status = (entry?.status ?? "") as ReadingStatus | "";
  const lists = entry?.lists ?? [];
  const save = async (next: { status: string; lists: number[] }) => {
    // the menu answers at once; the server's answer replaces it
    qc.setQueryData(key, next);
    try {
      const saved = await unwrap(api.PUT("/api/v1/series/{id}/lists", { params: { path: { id: seriesId } }, body: next as never }));
      qc.setQueryData(key, saved);
    } catch (e) {
      toast.fromError(e);
    }
    qc.invalidateQueries({ queryKey: ["me", "lists"] });
  };
  const create = async () => {
    const n = name.trim();
    if (!n) return;
    try {
      const made = await unwrap(api.POST("/api/v1/me/lists", { body: { name: n } }));
      setName("");
      setNaming(false);
      await save({ status, lists: lists.includes(made.id) ? lists : [...lists, made.id] });
    } catch (e) {
      toast.fromError(e);
    }
  };

  const favorite = mine?.lists?.find((l) => l.kind === "favorites");
  const inFavorites = !!favorite && lists.includes(favorite.id);
  const label = status ? readingStatusName(status) : inFavorites ? readingListName(favorite) : t("Add to list");
  const row = "flex w-full items-center gap-2 whitespace-nowrap rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-border focus:bg-border focus:outline-none";
  const section = "px-2.5 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wide text-muted";
  return (
    <div ref={ref} className="relative inline-block">
      <Button
        className="h-11 whitespace-nowrap px-4"
        variant={status || lists.length ? "secondary" : undefined}
        icon={status || lists.length ? <BookmarkCheck className="size-4" /> : <Bookmark className="size-4" />}
        aria-haspopup="menu"
        aria-expanded={open}
        title={t("Your status and lists for this title")}
        onClick={() => setOpen(!open)}
      >
        {label}
      </Button>
      {open && (
        <div role="menu" aria-label={t("Lists")} className="absolute left-0 z-30 mt-1 max-h-[60vh] w-64 max-w-[calc(100vw-2rem)] overflow-y-auto rounded-lg border border-border bg-panel-2 p-1 shadow-xl">
          <div role="presentation" className={section}>{t("Status (one at a time)")}</div>
          {readingStatuses.map((st) => (
            <button key={st} type="button" role="menuitemradio" aria-checked={status === st} className={clsx(row, status === st ? "text-accent-2" : "text-fg")}
              onClick={() => save({ status: status === st ? "" : st, lists })}>
              <span className={clsx("inline-flex size-4 items-center justify-center rounded-full border", status === st ? "border-accent-2" : "border-muted")}>
                {status === st && <span className="size-2 rounded-full bg-accent-2" />}
              </span>
              {readingStatusName(st)}
            </button>
          ))}
          <div role="presentation" className={clsx(section, "mt-1 border-t border-border")}>{t("My lists")}</div>
          {mine?.lists?.map((l) => {
            const on = lists.includes(l.id);
            return (
              <button key={l.id} type="button" role="menuitemcheckbox" aria-checked={on} className={clsx(row, "text-fg")}
                onClick={() => save({ status, lists: on ? lists.filter((x) => x !== l.id) : [...lists, l.id] })}>
                <span className={clsx("inline-flex size-4 items-center justify-center rounded border", on ? "border-accent-2 bg-accent-2 text-white" : "border-muted")}>
                  {on && <Check className="size-3" />}
                </span>
                <span className="truncate">{readingListName(l)}</span>
              </button>
            );
          })}
          {naming ? (
            <form className="flex gap-1 p-1" onSubmit={(e) => { e.preventDefault(); void create(); }}>
              <Input autoFocus className="h-8" placeholder={t("List name")} value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
              <Button size="sm" type="submit" disabled={!name.trim()}>{t("Add")}</Button>
            </form>
          ) : (
            <button type="button" role="menuitem" className={clsx(row, "text-accent-2")} onClick={() => setNaming(true)}>
              <Plus className="size-4" />
              {t("New list…")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}
