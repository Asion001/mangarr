import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { BookOpen, Eye, Plus, PlusCircle } from "lucide-react";
import { api, unwrap, type LookupResult, type S } from "../../api/client";
import { useProfiles } from "../../api/queries";
import { Button, Field, Modal, Select } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { useToast } from "../../lib/toast";

type Series = S["SeriesResource"];

/** useOpenPreview opens a title without adding it, then shows its page. */
export function useOpenPreview() {
  const nav = useNavigate();
  const toast = useToast();
  const [opening, setOpening] = useState("");
  const open = async (key: string, body: S["TitlePreviewInput"]) => {
    setOpening(key);
    try {
      const res = await unwrap(api.POST("/api/v1/previews", { body }));
      nav(`/series/${res.seriesId}`);
    } catch (e) {
      toast.fromError(e, t("Couldn't open the title"));
    } finally {
      setOpening("");
    }
  };
  return { open, opening };
}

/** ReadButton opens a metadata search result as a preview. */
export function ReadButton({ result, language }: { result: LookupResult; language?: string }) {
  const { open, opening } = useOpenPreview();
  const key = `${result.moduleId}:${result.id}`;
  return (
    <Button
      size="sm"
      icon={<BookOpen className="size-3.5" />}
      loading={opening === key}
      title={t("Read without adding it: pages stream from the source and nothing is downloaded")}
      onClick={() =>
        void open(key, {
          metadata: { moduleId: result.moduleId, provider: result.provider, id: result.id },
          title: result.title,
          titles: (result.altTitles ?? []).slice(0, 10),
          language: language || undefined,
        })
      }
    >
      {t("Read")}
    </Button>
  );
}

/** PreviewBanner heads a preview's page: it isn't in the library, and how to add it. */
export function PreviewBanner({ series }: { series: Series }) {
  const { can } = useAccount();
  const nav = useNavigate();
  const [adding, setAdding] = useState(false);
  const source = series.sources?.[0]?.sourceName;
  return (
    <div role="status" className="mb-5 flex flex-wrap items-center gap-3 rounded-lg border border-info/40 bg-info/10 px-3.5 py-2.5 text-sm">
      <Eye className="size-4 shrink-0 text-info" />
      <span className="min-w-0 flex-1">
        <b>{t("Preview.")}</b>{" "}
        {source
          ? t("This title isn't in your library. Pages stream from {source} and nothing is downloaded. Your progress is kept and carries over if you add it.", { source })
          : t("This title isn't in your library. Pages stream from the source and nothing is downloaded. Your progress is kept and carries over if you add it.")}
      </span>
      {can("library.manage") ? (
        <Button variant="primary" size="sm" icon={<Plus className="size-3.5" />} onClick={() => setAdding(true)}>{t("Add to library…")}</Button>
      ) : can("requests.create") ? (
        <Button variant="primary" size="sm" icon={<PlusCircle className="size-3.5" />} onClick={() => nav(`/requests?q=${encodeURIComponent(series.title)}`)}>{t("Request")}</Button>
      ) : null}
      {adding && <AddPreviewModal series={series} onClose={() => setAdding(false)} />}
    </div>
  );
}

/** AddPreviewModal adds a preview to the library, keeping its chapters and progress. */
function AddPreviewModal({ series, onClose }: { series: Series; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: profiles } = useProfiles();
  const [monitor, setMonitor] = useState("all");
  const [profileId, setProfileId] = useState(0);
  const [saving, setSaving] = useState<"" | "download" | "later">("");
  const add = async (searchMissing: boolean) => {
    setSaving(searchMissing ? "download" : "later");
    try {
      await unwrap(
        api.POST("/api/v1/series", {
          body: {
            title: series.title,
            language: series.language || undefined,
            profileId: profileId || undefined,
            monitor: monitor as "all",
            searchMissing,
            sources: (series.sources ?? []).map((l) => ({ moduleId: l.moduleId, sourceId: l.sourceId, url: l.mangaUrl, title: l.title, sourceName: l.sourceName, lang: l.lang })),
          },
        }),
      );
      toast.success(t("{title} added", { title: series.title }));
      await qc.invalidateQueries({ queryKey: ["series"] });
      onClose();
    } catch (e) {
      toast.fromError(e, t("Couldn't add the title"));
    } finally {
      setSaving("");
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Add {title} to the library", { title: series.title })}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button loading={saving === "later"} disabled={!!saving} onClick={() => void add(false)}>{t("Add without downloading")}</Button>
          <Button variant="primary" loading={saving === "download"} disabled={!!saving} onClick={() => void add(true)}>{t("Add and download")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <p className="text-sm text-muted">{t("Its chapters and your reading progress stay as they are.")}</p>
        <Field label={t("Monitor")}>
          <Select value={monitor} onChange={(e) => setMonitor(e.target.value)}>
            <option value="all">{t("All chapters")}</option>
            <option value="future">{t("Future chapters only")}</option>
            <option value="none">{t("Nothing, just track it")}</option>
          </Select>
        </Field>
        <Field label={t("Profile")}>
          <Select value={profileId} onChange={(e) => setProfileId(Number(e.target.value))}>
            <option value={0}>{t("Default for the language")}</option>
            {profiles?.map((p) => (
              <option key={p.id} value={p.id}>{p.name}</option>
            ))}
          </Select>
        </Field>
      </div>
    </Modal>
  );
}
