import { t } from "../../lib/i18n/core";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import clsx from "clsx";
import { Trash2 } from "lucide-react";
import { Button, Card, Confirm, PageHeader, Progress, Switch } from "../../components/ui";
import { DeviceIcon, useOfflineReady } from "../../components/OnDevice";
import { bytes } from "../../lib/format";
import { removeAll, removeChapter, saving, setRemoveRead, useRemoveRead, useSavedChapters, type SavedChapter } from "../../lib/offline/store";

type Usage = { usage: number; quota: number; persisted: boolean };

function useStorage(saved: unknown) {
  const [usage, setUsage] = useState<Usage | null>(null);
  useEffect(() => {
    let live = true;
    void (async () => {
      const est = await navigator.storage?.estimate?.().catch(() => undefined);
      const persisted = (await navigator.storage?.persisted?.().catch(() => false)) ?? false;
      if (live && est) setUsage({ usage: est.usage ?? 0, quota: est.quota ?? 0, persisted });
    })();
    return () => {
      live = false;
    };
  }, [saved]);
  return usage;
}

type Show = "all" | "saving" | "unread" | "read";
const shows: Record<Show, (c: SavedChapter) => boolean> = { all: () => true, saving, unread: (c) => !c.readAt, read: (c) => !!c.readAt };

/** SavingNow is how far the chapters still saving have got, all together. */
function SavingNow({ chapters }: { chapters: SavedChapter[] }) {
  const pages = chapters.reduce((n, c) => n + c.pages, 0);
  const done = chapters.reduce((n, c) => n + c.saved, 0);
  return (
    <Card>
      <div className="flex flex-col gap-2" role="status">
        <div className="flex items-baseline justify-between gap-3">
          <span className="font-medium">{t("Saving {count} chapters", { count: chapters.length })}</span>
          <span className="text-xs text-muted tabular-nums">{t("{saved} of {pages} pages", { saved: done, pages })}</span>
        </div>
        <Progress value={pages ? (100 * done) / pages : 0} />
      </div>
    </Card>
  );
}

/** OnDevicePage lists the chapters saved in this browser for reading offline (/on-device). */
export function OnDevicePage() {
  const ready = useOfflineReady();
  const saved = useSavedChapters();
  const removeRead = useRemoveRead();
  const usage = useStorage(saved);
  const [confirmAll, setConfirmAll] = useState(false);
  const [show, setShow] = useState<Show>("all");
  const all = Object.values(saved);
  const inProgress = all.filter(saving);
  const counts: Record<Show, number> = { all: all.length, saving: inProgress.length, unread: all.filter((c) => !c.readAt).length, read: all.filter((c) => c.readAt).length };
  const titles = useMemo(() => {
    const by = new Map<number, SavedChapter[]>();
    for (const c of Object.values(saved).filter(shows[show])) by.set(c.seriesId, [...(by.get(c.seriesId) ?? []), c]);
    return [...by.values()]
      .map((list) => list.sort((a, b) => Number(a.number) - Number(b.number) || a.id - b.id))
      .sort((a, b) => Math.max(...b.map((c) => c.at)) - Math.max(...a.map((c) => c.at)));
  }, [saved, show]);
  const total = Object.values(saved).reduce((n, c) => n + c.bytes, 0);

  return (
    <>
      <PageHeader
        title={t("On this device")}
        subtitle={t("Chapters saved in this browser, to read when the server is away or there's no network.")}
        actions={titles.length > 0 && <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={() => setConfirmAll(true)}>{t("Remove all")}</Button>}
      />
      <div className="flex flex-col gap-6">
        {!ready && (
          <Card>
            <p className="text-sm text-muted">{t("Saving chapters needs the app opened from this server over HTTPS (or on this computer). Open Mangarr again and it will be ready.")}</p>
          </Card>
        )}
        {inProgress.length > 0 && <SavingNow chapters={inProgress} />}
        <Card>
          <div className="flex flex-col gap-3">
            {usage && usage.quota > 0 && (
              <>
                <Progress value={(100 * usage.usage) / usage.quota} />
                <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted tabular-nums">
                  <span>{t("Saved chapters")} {bytes(total)}</span>
                  <span>{t("App and other data")} {bytes(Math.max(0, usage.usage - total))}</span>
                  <span>{t("Free about")} {bytes(Math.max(0, usage.quota - usage.usage))}</span>
                  {!usage.persisted && <span>{t("The browser may clear these when it runs low on space")}</span>}
                </div>
              </>
            )}
            <label className="flex items-center justify-between gap-3 text-sm">
              {t("Remove chapters I saved once I've read them")}
              <Switch checked={removeRead} onChange={setRemoveRead} />
            </label>
            <p className="text-xs text-muted">{t("Saved chapters are a copy in this browser. iOS can still clear them when the device runs low on space, or if you remove the app from the home screen. Chapters the reader saved ahead are removed after you read them.")}</p>
          </div>
        </Card>
        {all.length > 0 && (
          <div role="group" aria-label={t("Show")} className="flex flex-wrap gap-1.5">
            {(Object.keys(shows) as Show[]).map((key) => (
              <button
                key={key}
                type="button"
                aria-pressed={show === key}
                onClick={() => setShow(key)}
                className={clsx("rounded-full border px-3 py-1.5 text-xs font-medium", show === key ? "border-accent bg-accent text-white" : "border-border bg-panel-2 text-fg hover:border-accent")}
              >
                {{ all: t("All"), saving: t("Saving"), unread: t("Not read"), read: t("Read") }[key]} {counts[key]}
              </button>
            ))}
          </div>
        )}
        {all.length > 0 && titles.length === 0 ? (
          <p className="text-sm text-muted">{t("No saved chapters match.")}</p>
        ) : titles.length === 0 ? (
          <p className="text-sm text-muted">{t("Nothing saved yet. The reader saves the next chapters as you read; you can also save chapters from a title's chapter list.")}</p>
        ) : (
          <Card>
            <div className="divide-y divide-border">
              {titles.map((list) => {
                const size = list.reduce((n, c) => n + c.bytes, 0);
                const ahead = list.filter((c) => c.kind === "ahead").length;
                return (
                  <section key={list[0].seriesId} className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0">
                    <div className="flex items-center gap-3">
                      <div className="min-w-0 flex-1">
                        <Link to={`/series/${list[0].seriesId}`} className="font-medium hover:text-accent-2">{list[0].seriesTitle}</Link>
                        <div className="text-xs text-muted tabular-nums">
                          {t("{count} chapters", { count: list.length })} · {bytes(size)}
                          {ahead > 0 && ` · ${t("{count} saved ahead", { count: ahead })}`}
                        </div>
                      </div>
                      <Button size="sm" onClick={() => void Promise.all(list.map((c) => removeChapter(c.id)))}>{t("Remove")}</Button>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {list.map((c) => (
                        <Link key={c.id} to={`/read/${c.id}`} title={saving(c) ? t("Saving {saved} of {pages} pages", { saved: c.saved, pages: c.pages }) : undefined} className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs hover:border-accent">
                          <DeviceIcon chapter={c} className="size-3.5" />
                          {t("Ch.")} {c.number}
                        </Link>
                      ))}
                    </div>
                  </section>
                );
              })}
            </div>
          </Card>
        )}
      </div>
      <Confirm
        open={confirmAll}
        title={t("Remove every saved chapter from this device?")}
        message={t("You can save them again while the server is reachable.")}
        confirmLabel={t("Remove all")}
        danger
        onConfirm={() => {
          setConfirmAll(false);
          void removeAll();
        }}
        onClose={() => setConfirmAll(false)}
      />
    </>
  );
}
