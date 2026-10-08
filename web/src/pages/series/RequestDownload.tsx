import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Download } from "lucide-react";
import { api, unwrap, type Chapter } from "../../api/client";
import { Badge, Button } from "../../components/ui";
import { useToast } from "../../lib/toast";

/** waitingChapters are the chapters here that aren't monitored or downloaded. */
export const waitingChapters = (chapters?: Chapter[]) => (chapters ?? []).filter((c) => !c.monitored && !c.file && c.state !== "cleaned").length;

/**
 * RequestDownloadButton asks for a title's chapters that are listed but not
 * monitored to be downloaded, for people who can request but not manage.
 * Once asked, it says so.
 */
export function RequestDownloadButton({ seriesId, waiting }: { seriesId: number; waiting: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const { data: mine } = useQuery({ queryKey: ["requests", "mine", ""], queryFn: () => unwrap(api.GET("/api/v1/requests", { params: { query: { all: false } } })) });
  const asked = (mine ?? []).some((r) => r.kind === "monitor" && r.seriesId === seriesId && r.status === "pending");
  if (asked) return <Badge tone="info"><Check className="size-3" />{" " + t("Download requested")}</Badge>;
  const send = async () => {
    setBusy(true);
    try {
      const res = await unwrap(api.POST("/api/v1/requests", { body: { seriesId, monitor: true } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      toast.success(res.joined ? t("Added you to the request") : t("Requested"), t("You'll be told when the chapters arrive."));
    } catch (err) {
      toast.fromError(err, t("Couldn't request it"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button icon={<Download className="size-4" />} loading={busy} onClick={() => void send()} title={t("These chapters are listed but not downloaded; ask for them to be downloaded.")}>
      {t("Request download ({count} chapters)", { count: waiting })}
    </Button>
  );
}
