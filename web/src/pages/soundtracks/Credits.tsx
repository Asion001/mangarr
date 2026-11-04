import { t } from "../../lib/i18n/core";
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../../api/client";
import { packsOf } from "./credits";
import { Badge, Card, EmptyState, ErrorBox, Loading, PageHeader } from "../../components/ui";

/** CreditsPage lists every installed sound with who made it and its license. */
export function CreditsPage() {
  const { data, isLoading, error } = useQuery({ queryKey: ["soundtrack", "tracks", "all"], queryFn: () => unwrap(api.GET("/api/v1/audio/tracks")) });
  const packs = useMemo(() => packsOf(data ?? []), [data]);
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title={t("Sound credits")} subtitle={t("The sounds installed on this server, who made them and under which license. Music comes from Spotify and is credited there.")} />
      {isLoading ? (
        <Loading />
      ) : error ? (
        <ErrorBox error={error} />
      ) : packs.length === 0 ? (
        <EmptyState title={t("No sound packs are installed.")} />
      ) : (
        packs.map((p) => (
          <Card
            key={p.name}
            title={p.name}
            actions={
              <span className="text-xs text-muted">
                {t("{count} sounds", { count: p.sounds.length })}
                {p.licenses.length > 0 ? " · " + p.licenses.join(" / ") : ""}
              </span>
            }
          >
            {p.sounds.map((s) => (
              <div key={s.id} className="flex justify-between gap-2.5 border-t border-border py-2 first:border-t-0 first:pt-0 last:pb-0">
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="truncate text-[13px] font-medium">{s.title}</span>
                  <span className="truncate text-xs text-muted">{s.attribution || t("Author not given")}</span>
                </div>
                <div className="flex shrink-0 flex-col items-end gap-0.5">
                  {s.license && <Badge>{s.license}</Badge>}
                  {s.sourceUrl && /^https?:\/\//.test(s.sourceUrl) && (
                    <a href={s.sourceUrl} target="_blank" rel="noreferrer noopener" className="text-xs text-accent hover:underline">
                      {t("Source")}
                    </a>
                  )}
                </div>
              </div>
            ))}
          </Card>
        ))
      )}
    </div>
  );
}
