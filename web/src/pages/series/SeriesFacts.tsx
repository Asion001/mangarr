import { useState, type ReactNode } from "react";
import { Bell, BellOff, ExternalLink } from "lucide-react";
import { type Series } from "../../api/client";
import { Button } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { bytes, relative } from "../../lib/format";
import { label, t as tr, t } from "../../lib/i18n/core";
import { useSeriesActions } from "./useSeriesActions";

export const statusClass: Record<string, string> = { ongoing: "text-ok", completed: "text-info", hiatus: "text-warn", cancelled: "text-err" };
const directionNames: Record<string, string> = { rtl: "Right to left", ltr: "Left to right", vertical: "Vertical" };
export const capital = (v: string) => v.charAt(0).toUpperCase() + v.slice(1);

/** Dot is one item of a quiet "a · b · c" line. */
export function Dot({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2.5 ${className ?? ""}`}>
      <span className="text-muted/60" aria-hidden="true">·</span>
      <span className="inline-flex items-center">{children}</span>
    </span>
  );
}

/** Facts is what the title page keeps behind "Details": the other names,
 * reading direction, credits, files, who reads it and links. */
export function Facts({ series: s, manage }: { series: Series; manage: boolean }) {
  const { account } = useAccount();
  const md = s.metadata;
  const links = Object.entries(md.links ?? {});
  const authors = md.authors ?? [];
  const artists = md.artists ?? [];
  const sameCredits = authors.length > 0 && authors.join() === artists.join();
  const row = (name: string, value: ReactNode, cls?: string) => (
    <>
      <dt className="text-muted">{name}</dt>
      <dd className={cls ?? "text-fg"}>{value}</dd>
    </>
  );
  const readers = account?.kind !== "user" ? (s.reading?.readers ?? []) : [];
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] content-start gap-x-4 gap-y-1.5 text-sm">
      {md.altTitles && md.altTitles.length > 0 && row(t("Also known as"), md.altTitles.slice(0, 6).join(" · "))}
      {s.readingDirection && row(t("Direction"), label(directionNames[s.readingDirection] ?? s.readingDirection))}
      {md.ageRating && row(t("Age rating"), md.ageRating, "text-warn")}
      {sameCredits ? row(t("Story & art"), authors.join(", ")) : (
        <>
          {authors.length > 0 && row(t("Story"), authors.join(", "))}
          {artists.length > 0 && row(t("Art"), artists.join(", "))}
        </>
      )}
      {md.publisher && row(t("Publisher"), md.publisher)}
      {row(t("Downloaded"), `${s.stats.fileCount} / ${s.stats.chapterCount}` + (manage && s.stats.cleanedCount > 0 ? ` · ${t("{count} cleaned", { count: s.stats.cleanedCount })}` : ""))}
      {manage && row(t("On disk"), bytes(s.stats.sizeOnDisk) + (s.stats.spaceSaved > 0 ? ` · ${t("saved {size}", { size: bytes(s.stats.spaceSaved) })}` : ""))}
      {readers.length > 0 && row(t("Readers"), (
        <span className="flex flex-col gap-0.5">
          {readers.map((r) => (
            <span key={r.userId} title={r.lastReadAt ? `last read ${relative(r.lastReadAt)}` : undefined}>
              {`${r.name}: `}{r.read}/{s.stats.chapterCount}{" " + t("read")}{r.inProgress > 0 && `, ${r.inProgress} started`}
            </span>
          ))}
        </span>
      ))}
      {(links.length > 0 || s.reading?.webUrl) && row(t("Links"), (
        <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
          {links.map(([k, v]) => (
            <a key={k} href={v} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent-2 hover:underline">
              {k}<ExternalLink className="size-3" />
            </a>
          ))}
          {s.reading?.webUrl && (
            <a href={s.reading.webUrl} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent-2 hover:underline">
              {t("Open in") + " "}{s.reading.webName || tr("library")}<ExternalLink className="size-3" />
            </a>
          )}
        </span>
      ))}
    </dl>
  );
}

/** FollowButton: follow a series to get its new chapters on your notification targets. */
export function FollowButton({ seriesId, following }: { seriesId: number; following: boolean }) {
  const { setFollowing } = useSeriesActions(seriesId);
  const [busy, setBusy] = useState(false);
  const toggle = async () => {
    setBusy(true);
    await setFollowing(!following);
    setBusy(false);
  };
  return (
    <Button
      className="h-11 whitespace-nowrap px-4"
      variant={following ? "secondary" : undefined}
      loading={busy}
      icon={following ? <BellOff className="size-4" /> : <Bell className="size-4" />}
      onClick={toggle}
      title={following ? tr("Stop getting its new chapters") : tr("Get its new chapters on your notifications (set them up under My account)")}
    >
      {following ? tr("Following") : tr("Follow")}
    </Button>
  );
}
