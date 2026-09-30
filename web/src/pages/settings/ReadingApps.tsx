import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUp, Copy, KeyRound, Languages, Plus, Trash2, X } from "lucide-react";
import { Link } from "react-router";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, Confirm, ErrorBox, Field, IconButton, Input, Loading, Modal, PageHeader, SaveBar, Switch, Table, Tabs, Td, Th } from "../../components/ui";
import { LanguageSelect } from "../../components/LanguageSelect";
import { languageName, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "./useSettingsDoc";

type ReadingSettings = S["Reading"];
type Key = S["ReadingKey"];
export type App = "mihon" | "kmreader" | "paperback" | "koreader";

/** The apps the guides cover, in the order the tabs show them. */
export const appTabs: { value: App; label: string }[] = [
  { value: "mihon", label: "Mihon (Android)" },
  { value: "kmreader", label: "KMReader (iPhone, iPad)" },
  { value: "paperback", label: "Paperback (iPhone, iPad)" },
  { value: "koreader", label: "KOReader (e-readers)" },
];

/** The address apps should use: the configured one, else this host on the API's port. */
export function appAddress(publicUrl: string, listen?: string) {
  if (publicUrl) return publicUrl.replace(/\/+$/, "");
  const port = listen?.split(":").pop() || "25600";
  return `${window.location.protocol}//${window.location.hostname}:${port}`;
}

export function ReadingAppsPage() {
  const { value: r, patch, save, saving, isLoading, error, lock, dirty, reset } = useSettingsDoc<ReadingSettings>("reading");
  const status = useQuery({ queryKey: ["reading", "status"], queryFn: () => unwrap(api.GET("/api/v1/reading/status")), refetchInterval: 10_000 });
  const [app, setApp] = useState<App>("mihon");
  const st = status.data;
  const address = appAddress(r?.publicUrl ?? "", st?.address);
  const saveAndRefresh = async () => {
    await save();
    setTimeout(() => status.refetch(), 300);
  };
  return (
    <>
      <PageHeader
        title={t("Reading apps")}
        subtitle={t("Read your whole mangarr library in Mihon, KMReader, Paperback or KOReader through a Komga-compatible API and an OPDS catalog, with progress synced both ways.")}
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {r && (
        <div className="flex flex-col gap-6">
          <Card
            title={
              <span className="flex items-center gap-2">{t("Komga-compatible API")}{st?.listening ? (
                  <Badge tone="ok">{t("listening on") + " "}{st.address}</Badge>
                ) : st?.error ? (
                  <Badge tone="err" title={st.error}>{t("can't listen")}</Badge>
                ) : (
                  <Badge>{t("off")}</Badge>
                )}
              </span>
            }
          >
            <div className="grid gap-5 md:grid-cols-2">
              <div className="flex flex-col gap-3">
                <Switch env={lock("enabled")} checked={r.enabled} onChange={(v) => patch({ enabled: v })} label={<b>{t("Allow Komga apps to connect")}</b>} />
                <p className="text-xs text-muted">{t("Apps see every series and every chapter mangarr knows, downloaded or not. The API runs on its own port (like Komga's, 25600), so apps connect to it as they would to a Komga server.")}</p>
                {st?.error && <ErrorBox error={`The API can't listen: ${st.error}`} />}
                <Switch env={lock("downloadOnOpen")} checked={r.downloadOnOpen} onChange={(v) => patch({ downloadOnOpen: v })} label={t("Download chapters opened before they're downloaded")} />
                <div>
                  <Switch env={lock("resizePages")} checked={r.resizePages} onChange={(v) => patch({ resizePages: v })} label={t("Send phones a page at their screen size")} />
                  <p className="mt-1 text-xs text-muted">{t("A copy of each page is made once and kept in the image cache; turn it off to always send the full scan.")}</p>
                </div>
                <p className="-mt-2 text-xs text-muted">{t("Chapters that aren't downloaded are streamed from the source meanwhile.")}</p>
                <Switch
                  env={lock("readAhead.enabled")}
                  checked={r.readAhead.enabled}
                  onChange={(v) => patch({ readAhead: { ...r.readAhead, enabled: v } })}
                  label={t("Download the next chapters while someone reads")}
                />
              </div>
              <div className="flex flex-col gap-4">
                <Field env={lock("publicUrl")} label={t("Address apps should use")} help={`Shown in the guides below. Empty: ${appAddress("", st?.address)}`}>
                  <Input value={r.publicUrl} placeholder="https://manga.example.com:25600" onChange={(e) => patch({ publicUrl: e.target.value })} />
                </Field>
                <Field env={lock("readAhead.chapters")} label={t("Chapters to download ahead")} help={t("After the furthest chapter a reader has started, in any app or library server.")}>
                  <Input
                    type="number"
                    min={1}
                    max={50}
                    value={r.readAhead.chapters}
                    disabled={!r.readAhead.enabled}
                    onChange={(e) => patch({ readAhead: { ...r.readAhead, chapters: Number(e.target.value) } })}
                  />
                </Field>
              </div>
            </div>
          </Card>

          <DevicesCard all />

          <Card title={t("Connect an app")}>
            <Tabs
              tabs={appTabs}
              value={app}
              onChange={setApp}
            />
            <div className="mt-4 text-sm">
              <Guide app={app} address={address} />
            </div>
            {!r.enabled && <p className="mt-4 text-sm text-warn">{t("Turn on \"Allow Komga apps to connect\" first.")}</p>}
          </Card>
        </div>
      )}
      <SaveBar dirty={dirty} saving={saving} onSave={() => void saveAndRefresh()} onDiscard={reset} />
    </>
  );
}

function Code({ children }: { children: string }) {
  return <code className="rounded bg-panel-2 px-1.5 py-0.5 text-xs break-all">{children}</code>;
}

export function Guide({ app, address }: { app: App; address: string }) {
  switch (app) {
    case "mihon":
      return (
        <ol className="flex list-decimal flex-col gap-2 pl-5">
          <li>{t("In Mihon, open") + " "}<b>{t("Browse → Extensions")}</b>{" " + t("and install the") + " "}<b>Komga</b>{" " + t("extension (Keiyoushi repository).")}</li>
          <li>{t("Open the extension's settings and set the address to") + " "}<Code>{address}</Code>{t(". For the login, add a device under Devices and paste its API key (or use your mangarr username and password).")}</li>
          <li>{t("The Komga source now lists every mangarr series, downloaded or not. Chapters that aren't downloaded are streamed from the source.")}</li>
          <li>{t("For progress sync, open") + " "}<b>{t("Settings → Tracking")}</b>{" " + t("and turn on") + " "}<b>Komga</b>{" " + t("under enhanced services. Mihon then tells mangarr the chapters you finish, and picks up what you read elsewhere when it refreshes tracking. It syncs finished chapters, not pages.")}</li>
        </ol>
      );
    case "kmreader":
      return (
        <ol className="flex list-decimal flex-col gap-2 pl-5">
          <li>{t("In KMReader, add a server with the address") + " "}<Code>{address}</Code>.
          </li>
          <li>{t("Sign in with an API key from Devices, or with your mangarr username and password (KMReader then creates its own device key).")}</li>
          <li>{t("Progress syncs page by page, and changes made elsewhere show up live. Downloaded chapters can be saved for offline reading; the others are streamed.")}</li>
        </ol>
      );
    case "paperback":
      return (
        <ol className="flex list-decimal flex-col gap-2 pl-5">
          <li>{t("In Paperback, add the") + " "}<b>Komga</b>{" " + t("extension from the default extensions repository.")}</li>
          <li>{t("Set the server address to") + " "}<Code>{address}</Code>{t(". Log in with any username and a device key from Devices as the password (or your mangarr username and password).")}</li>
          <li>{t("Enable the Komga tracker in Paperback to send finished chapters to mangarr.")}</li>
        </ol>
      );
    case "koreader":
      return (
        <ol className="flex list-decimal flex-col gap-2 pl-5">
          <li>{t("Add a device under Devices and keep its key: KOReader uses it as the password for both the catalog and progress sync.")}</li>
          <li>{t("In KOReader, open") + " "}<b>{t("Search → OPDS catalog")}</b>{t(", tap +, and add") + " "}<Code>{`${address}/opds`}</Code>{t(". Sign in with your mangarr username and the device key.")}</li>
          <li>{t("Browse or search the catalog and tap a chapter to download it. AVIF and JPEG XL pages are converted to JPEG in the downloaded copy, since KOReader can't show them.")}</li>
          <li>{t("For progress sync, open") + " "}<b>{t("Tools → Progress sync → Custom sync server")}</b>{", " + t("enter") + " "}<Code>{address}</Code>{t(", then log in with your mangarr username and the device key. Keep document matching on Binary.")}</li>
          <li>{t("Only chapters downloaded from this catalog sync. KOReader sends its page and percentage, and they become your reading progress in mangarr.")}</li>
        </ol>
      );
  }
}

export function DevicesCard({ all = false }: { all?: boolean }) {
  const qc = useQueryClient();
  const toast = useToast();
  const keys = useQuery({ queryKey: ["reading", "keys", all], queryFn: () => unwrap(api.GET("/api/v1/reading/keys", { params: { query: { all } } })) });
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<Key | null>(null);
  const [languagesOf, setLanguagesOf] = useState<Key | null>(null);
  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/reading/keys/{id}", { params: { path: { id: deleting.id } } }));
      qc.invalidateQueries({ queryKey: ["reading", "keys"] });
    } catch (e) {
      toast.fromError(e);
    }
    setDeleting(null);
  };
  return (
    <Card
      title={t("Devices")}
      actions={
        <Button size="sm" icon={<Plus className="size-3.5" />} onClick={() => setAdding(true)}>{t("Add device")}</Button>
      }
    >
      <p className="mb-3 text-sm text-muted">
        {all ? (
          <>{t("Every account's app keys. Apps act as the key's account, with its progress and the series its group sees. Sync history per device is under Settings →")}{" "}
            <Link to="/settings/readers" className="text-accent-2 hover:underline">{t("Readers")}</Link>
            .
          </>
        ) : (
          tr("Each of your apps gets its own key: it reads and writes your progress, and you can revoke one without the others.")
        )}
      </p>
      {keys.error && <ErrorBox error={keys.error} />}
      {keys.data?.length === 0 && <p className="text-sm text-muted">{t("No devices yet.")}</p>}
      {!!keys.data?.length && (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <Th>{t("Device")}</Th>
                {all && <Th>{t("Account")}</Th>}
                <Th>{t("Key")}</Th>
                <Th>{t("Languages")}</Th>
                <Th>{t("Last used")}</Th>
                <Th>{t("Added")}</Th>
                <Th />
              </tr>
            </thead>
            <tbody>
              {keys.data.map((k) => (
                <tr key={k.id}>
                  <Td className="font-medium">{k.comment || tr("Unnamed")}</Td>
                  {all && <Td className="text-muted">{k.user || "—"}</Td>}
                  <Td>
                    <Code>{`${k.prefix}…`}</Code>
                  </Td>
                  <Td className="text-muted">
                    {k.languages?.length ? k.languages.map(languageName).join(" → ") : t("Each language separately")}
                  </Td>
                  <Td className="text-muted">{k.lastUsedAt ? `${relative(k.lastUsedAt)}${k.lastClient ? ` · ${k.lastClient}` : ""}` : tr("never")}</Td>
                  <Td className="text-muted">{relative(k.createdAt)}</Td>
                  <Td className="whitespace-nowrap">
                    <IconButton title={t("Reading languages")} onClick={() => setLanguagesOf(k)}>
                      <Languages className="size-4" />
                    </IconButton>
                    <IconButton title={t("Revoke key")} onClick={() => setDeleting(k)}>
                      <Trash2 className="size-4" />
                    </IconButton>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
      {adding && <AddDeviceModal onClose={() => setAdding(false)} />}
      {languagesOf && <DeviceLanguagesModal device={languagesOf} onClose={() => setLanguagesOf(null)} />}
      <Confirm
        open={!!deleting}
        title={t("Revoke key")}
        danger
        confirmLabel={t("Revoke")}
        message={`${deleting?.comment || "This device"} won't be able to connect with this key any more.`}
        onConfirm={remove}
        onClose={() => setDeleting(null)}
      />
    </Card>
  );
}

/** DeviceLanguagesModal sets a device's language order: with one, a title in
 * several languages shows once on the device. */
function DeviceLanguagesModal({ device, onClose }: { device: Key; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [order, setOrder] = useState<string[]>(device.languages ?? []);
  const [saving, setSaving] = useState(false);
  const save = async () => {
    setSaving(true);
    try {
      await unwrap(api.PUT("/api/v1/reading/keys/{id}", { params: { path: { id: device.id } }, body: { languages: order } }));
      qc.invalidateQueries({ queryKey: ["reading", "keys"] });
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const moveUp = (i: number) => setOrder((o) => o.map((code, j) => (j === i - 1 ? o[i] : j === i ? o[i - 1] : code)));
  return (
    <Modal
      open
      onClose={onClose}
      title={`${t("Reading languages")}: ${device.comment || tr("Unnamed")}`}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <p className="mb-3 text-sm text-muted">
        {t("A title you have in several languages shows once on this device. Each chapter comes in the first of these languages that has it, and reading progress is shared between languages. With no languages here, each language shows as its own series.")}
      </p>
      {order.length > 0 && (
        <ol className="mb-3 flex flex-col gap-1">
          {order.map((code, i) => (
            <li key={code} className="flex items-center gap-2 rounded-md bg-panel-2 px-3 py-1.5 text-sm">
              <span className="w-5 text-muted">{i + 1}.</span>
              <span className="flex-1">{languageName(code)}</span>
              {i > 0 && (
                <IconButton title={t("Move up")} onClick={() => moveUp(i)}>
                  <ArrowUp className="size-4" />
                </IconButton>
              )}
              <IconButton title={t("Remove")} onClick={() => setOrder((o) => o.filter((c) => c !== code))}>
                <X className="size-4" />
              </IconButton>
            </li>
          ))}
        </ol>
      )}
      <LanguageSelect value="" placeholder={t("Add a language…")} onChange={(code) => code && !order.includes(code) && setOrder((o) => [...o, code])} />
    </Modal>
  );
}

function AddDeviceModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [key, setKey] = useState("");
  const [saving, setSaving] = useState(false);
  const create = async () => {
    setSaving(true);
    try {
      const k = await unwrap(api.POST("/api/v1/reading/keys", { body: { comment: name.trim() } }));
      setKey(k.key);
      qc.invalidateQueries({ queryKey: ["reading", "keys"] });
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(key);
      toast.success(tr("Key copied"));
    } catch {
      toast.error(tr("Couldn't copy: select the key and copy it by hand"));
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={key ? tr("Device added") : tr("Add a device")}
      footer={
        key ? (
          <Button variant="primary" onClick={onClose}>{t("Done")}</Button>
        ) : (
          <>
            <Button onClick={onClose}>{t("Cancel")}</Button>
            <Button variant="primary" icon={<KeyRound className="size-4" />} loading={saving} disabled={!name.trim()} onClick={create}>{t("Create key")}</Button>
          </>
        )
      }
    >
      {key ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm">{t("Enter this API key in the app. It's only shown now.")}</p>
          <div className="flex gap-2">
            <Input readOnly value={key} onFocus={(e) => e.currentTarget.select()} className="font-mono text-xs" />
            <Button icon={<Copy className="size-4" />} onClick={copy}>{t("Copy")}</Button>
          </div>
        </div>
      ) : (
        <Field label={t("Device name")} help={t("For example Mihon phone or KMReader iPad.")}>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && name.trim() && create()} />
        </Field>
      )}
    </Modal>
  );
}
