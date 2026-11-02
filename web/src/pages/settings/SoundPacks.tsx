import { t } from "../../lib/i18n/core";
import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, FolderSearch, Trash2, Upload } from "lucide-react";
import { api, basePath, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, Confirm, ErrorBox, Field, Input, Loading, SaveBar, Switch } from "../../components/ui";
import { bytes } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "./useSettingsDoc";

type Sound = S["Sound"];
type Pack = S["SoundtrackPackView"];

const folderExample = "/data/sounds/my-pack";
const catalogExample = "https://…/catalog.json";

/** usePacks lists the installed sound packs and the room they take. */
export function usePacks() {
  return useQuery({ queryKey: ["soundtrack", "packs"], queryFn: () => unwrap(api.GET("/api/v1/audio/packs")) });
}

/** Catalog lists the packs the catalog offers, to install or update. */
function Catalog({ url }: { url: string }) {
  const toast = useToast();
  const { data, isLoading, error } = useQuery({ queryKey: ["soundtrack", "catalog", url], queryFn: () => unwrap(api.GET("/api/v1/audio/catalog")), enabled: url !== "" });
  const [queued, setQueued] = useState<string[]>([]);
  if (url === "") return <p className="text-sm text-muted">{t("Add a catalog URL below to install packs from it.")}</p>;
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  const install = async (id: string, name: string) => {
    try {
      await unwrap(api.POST("/api/v1/audio/packs/install", { body: { id } }));
      setQueued((q) => [...q, id]);
      toast.info(t("Installing {name}", { name }), t("It shows here when it's done (System › Tasks has the progress)."));
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <div className="flex flex-col gap-2">
      {(data ?? []).length === 0 && <p className="text-sm text-muted">{t("The catalog offers no packs.")}</p>}
      {(data ?? []).map((p) => {
        const current = p.installed === p.version;
        return (
          <div key={p.id} className="flex items-center gap-3 rounded-lg bg-panel-2 p-3">
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">{p.name}</div>
              <div className="text-xs text-muted">
                {[p.description, p.sounds ? t("{count} sounds", { count: p.sounds }) : "", p.license].filter(Boolean).join(" · ")}
              </div>
            </div>
            {current ? (
              <span className="text-xs text-ok">{t("Installed")}</span>
            ) : (
              <Button size="sm" variant="primary" disabled={queued.includes(p.id)} onClick={() => void install(p.id, p.name)}>
                {p.installed ? t("Update") : t("Install")}
                {p.size ? " · " + bytes(p.size) : ""}
              </Button>
            )}
          </div>
        );
      })}
    </div>
  );
}

/**
 * SoundPacksCard is Settings › Soundtracks › Spotify & packs: ambience and
 * sound effects from a catalog, an uploaded zip or a folder on the server.
 */
export function SoundPacksCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { value: cfg, patch, save, saving, dirty, reset } = useSettingsDoc<Sound>("sound");
  const { data, isLoading, error } = usePacks();
  const [folder, setFolder] = useState("");
  const [uploading, setUploading] = useState(false);
  const [removing, setRemoving] = useState<Pack | null>(null);
  const file = useRef<HTMLInputElement>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["soundtrack"] });
  const upload = async (f: File) => {
    setUploading(true);
    try {
      const r = await fetch(`${basePath}/api/v1/audio/packs/upload`, { method: "POST", headers: { "Content-Type": "application/zip" }, body: f });
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.detail || body.title || `HTTP ${r.status}`);
      toast.success(t("Pack installed"), body.name);
      refresh();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setUploading(false);
    }
  };
  const detectAll = async () => {
    try {
      await unwrap(api.POST("/api/v1/soundtracks/detect"));
      toast.info(t("Finding moods"), t("It shows here when it's done (System › Tasks has the progress)."));
    } catch (e) {
      toast.fromError(e);
    }
  };
  const scan = async () => {
    try {
      await unwrap(api.POST("/api/v1/audio/packs/import-folder", { body: { path: folder.trim() } }));
      toast.info(t("Scanning the folder"), t("It shows here when it's done (System › Tasks has the progress)."));
      setFolder("");
    } catch (e) {
      toast.fromError(e);
    }
  };
  const remove = async () => {
    if (!removing) return;
    try {
      await unwrap(api.DELETE("/api/v1/audio/packs/{id}", { params: { path: { id: removing.id } } }));
      setRemoving(null);
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const usage = data?.usage;
  return (
    <Card title={t("Sound packs")} actions={usage && <span className="text-xs text-muted">{t("{size} used", { size: bytes(usage.size) })}</span>}>
      <div className="flex flex-col gap-4">
        <p className="text-sm leading-relaxed text-muted">{t("Ambience and sound effects that play over the music, also for readers without Spotify. Scenes layer them over a mood.")}</p>
        {usage?.over && (
          <div className="flex gap-2.5 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2.5 text-sm">
            <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden="true" />
            <p>{t("Sound packs take {size}, more than the {quota} set below. Remove packs you don't use.", { size: bytes(usage.size), quota: bytes(usage.quotaSize) })}</p>
          </div>
        )}
        {isLoading && <Loading />}
        {error && <ErrorBox error={error} />}
        {data && data.packs.length > 0 && (
          <ul className="flex flex-col gap-2">
            {data.packs.map((p) => (
              <li key={p.id} className="flex items-center gap-3 rounded-lg bg-panel-2 p-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2 text-sm font-medium">
                    {p.name}
                    {p.version && <span className="text-xs font-normal text-muted">{p.version}</span>}
                    {p.origin === "folder" && <Badge>{t("Imported folder")}</Badge>}
                  </div>
                  <div className="text-xs text-muted">
                    {[t("{count} ambience", { count: p.ambience }), t("{count} effects", { count: p.sfx }), bytes(p.size), p.license].filter(Boolean).join(" · ")}
                  </div>
                </div>
                <button type="button" onClick={() => setRemoving(p)} className="flex size-10 items-center justify-center rounded text-muted hover:bg-border hover:text-fg" aria-label={t("Remove the pack")}>
                  <Trash2 className="size-4" />
                </button>
              </li>
            ))}
          </ul>
        )}
        {data && data.packs.length === 0 && <p className="text-sm text-muted">{t("No sound packs yet.")}</p>}

        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <h3 className="text-sm font-semibold">{t("From the catalog")}</h3>
          <Catalog url={cfg?.catalogUrl ?? ""} />
        </div>

        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <h3 className="text-sm font-semibold">{t("Upload a pack")}</h3>
          <p className="text-xs text-muted">{t("A zip with pack.json and its sounds (Ogg/Opus, MP3, M4A or WAV).")}</p>
          <Button className="self-start" icon={<Upload className="size-4" />} loading={uploading} onClick={() => file.current?.click()}>
            {t("Choose a zip")}
          </Button>
          <input
            ref={file}
            type="file"
            accept=".zip,application/zip"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) void upload(f);
            }}
          />
        </div>

        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <h3 className="text-sm font-semibold">{t("Import a folder")}</h3>
          <p className="text-xs text-muted">{t("For packs you downloaded yourself. Files are sorted into ambience and effects from their names; music is skipped.")}</p>
          <div className="flex gap-2">
            <Input value={folder} onChange={(e) => setFolder(e.target.value)} placeholder={folderExample} aria-label={t("Folder path")} className="min-w-0 flex-1 font-mono text-xs" />
            <Button icon={<FolderSearch className="size-4" />} disabled={!folder.trim().startsWith("/")} onClick={() => void scan()}>
              {t("Scan")}
            </Button>
          </div>
        </div>

        {cfg && (
          <div className="flex flex-col gap-4 border-t border-border pt-4">
            <Field label={t("Catalog URL")} help={t("A catalog.json listing packs to install. Empty: none.")}>
              <Input value={cfg.catalogUrl} onChange={(e) => patch({ catalogUrl: e.target.value })} placeholder={catalogExample} className="font-mono text-xs" />
            </Field>
            <Field label={t("Warn above (MB)")} help={t("0: never.")}>
              <Input type="number" min={0} value={cfg.quotaMb} onChange={(e) => patch({ quotaMb: Math.max(0, Number(e.target.value) || 0) })} className="w-32" />
            </Field>
            <Switch checked={cfg.suggestFromGenres} onChange={(suggestFromGenres) => patch({ suggestFromGenres })} label={t("Suggest a mood from a title's genres when it has none")} />
            <div className="flex flex-wrap items-center gap-3">
              <Switch checked={!!cfg.autoMoods} onChange={(autoMoods) => patch({ autoMoods })} label={t("Find moods from chapter titles (after new chapters arrive)")} />
              <Button size="sm" onClick={() => void detectAll()}>
                {t("Look through the library now")}
              </Button>
            </div>
          </div>
        )}
      </div>
      <SaveBar dirty={dirty} saving={saving} onSave={() => void save().then(refresh)} onDiscard={reset} />
      <Confirm
        open={!!removing}
        danger
        title={t("Remove the pack?")}
        message={t("Its sounds are deleted, and scenes lose the layers and effects that used them.")}
        confirmLabel={t("Remove")}
        onConfirm={() => void remove()}
        onClose={() => setRemoving(null)}
      />
    </Card>
  );
}
