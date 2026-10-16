import { t as tr, t } from "../../lib/i18n/core";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap, type Chapter } from "../../api/client";
import { Badge, ErrorBox, Loading, Modal } from "../../components/ui";

export function DecisionModal({ seriesId, chapter, onClose }: { seriesId: number; chapter: Chapter; onClose: () => void }) {
  const { data, error } = useQuery({
    queryKey: ["decision", seriesId, chapter.id],
    queryFn: () => unwrap(api.GET("/api/v1/series/{id}/chapters/{chapterId}/decision", { params: { path: { id: seriesId, chapterId: chapter.id } } })),
  });
  const relName = (id?: number) => chapter.releases.find((r) => r.id === id);
  return (
    <Modal open onClose={onClose} title={`Chapter ${chapter.number}: download decision`}>
      {error && <ErrorBox error={error} />}
      {!data && !error && <Loading />}
      {data && (
        <div className="flex flex-col gap-3 text-sm">
          {data.approved ? (
            <p>{t("Would download") + " "}<b>{data.approved.name}</b>{" " + t("from") + " "}<b>{data.approved.sourceName}</b>
              {data.approved.scanlator ? ` (${data.approved.scanlator})` : ""}
              {data.decision.isUpgrade ? tr(" as an upgrade") : ""}.
            </p>
          ) : (
            <p className="text-muted">{t("Nothing would be downloaded right now.")}</p>
          )}
          {data.decision.rejections.length > 0 && (
            <ul className="flex flex-col gap-1">
              {data.decision.rejections.map((r, i) => (
                <li key={i} className="flex items-start gap-2">
                  <Badge tone={r.temporary ? "warn" : "err"}>{r.temporary ? tr("temporary") : tr("rejected")}</Badge>
                  <span>
                    {r.releaseId ? <span className="text-muted">{relName(r.releaseId)?.sourceName ?? tr("release")}: </span> : null}
                    {r.reason}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Modal>
  );
}
