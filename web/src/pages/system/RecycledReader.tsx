import { t } from "../../lib/i18n/core";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft } from "lucide-react";
import { useNavigate, useParams } from "react-router";
import { api, apiUrl, unwrap } from "../../api/client";
import { ErrorBox, IconButton, Loading } from "../../components/ui";

/**
 * RecycledReaderPage shows a recycled chapter file page by page, read-only:
 * nothing here touches reading progress.
 */
export function RecycledReaderPage() {
  const id = Number(useParams().id);
  const navigate = useNavigate();
  const { data, isLoading, error } = useQuery({
    queryKey: ["recycle-bin", "read", id],
    queryFn: () => unwrap(api.GET("/api/v1/recycle-bin/{id}/read", { params: { path: { id } } })),
  });
  return (
    <div className="min-h-dvh bg-black text-fg">
      <header className="sticky top-0 z-10 flex items-center gap-3 border-b border-border bg-panel/95 px-3 py-2 backdrop-blur" style={{ paddingTop: "max(0.5rem, env(safe-area-inset-top))" }}>
        <IconButton title={t("Back")} onClick={() => navigate(-1)}>
          <ArrowLeft className="size-4" />
        </IconButton>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium">
            {[data?.seriesTitle, data?.number && t("Ch. {number}", { number: data.number })].filter(Boolean).join(" · ") || t("Recycle bin")}
          </div>
          <div className="text-xs text-muted">{t("Recycled version, read-only")}{data ? ` · ${t("{count} pages", { count: data.pages.length })}` : ""}</div>
        </div>
      </header>
      {isLoading && <Loading />}
      {error && <div className="p-4"><ErrorBox error={error} /></div>}
      <div className="mx-auto flex max-w-3xl flex-col">
        {data?.pages.map((p) => (
          <img key={p.number} src={apiUrl(`/api/v1/recycle-bin/${id}/pages/${p.number}`)} alt={t("Page {page}", { page: p.number })} loading={p.number > 3 ? "lazy" : "eager"} className="block h-auto w-full" />
        ))}
      </div>
    </div>
  );
}
