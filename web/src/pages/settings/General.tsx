import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Copy, Eye, EyeOff, KeyRound, Plus, Trash2 } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { useTags } from "../../api/queries";
import { Badge, Button, Card, EnvLock, ErrorBox, Field, IconButton, Input, Loading, PageHeader, SaveBar, Select, Textarea } from "../../components/ui";
import { AccentPicker, StartPageOptions } from "../../components/AccentPicker";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "./useSettingsDoc";

type General = S["GeneralSettingsResource"];

export function GeneralPage() {
  const { value: g, patch, save, saving, isLoading, error, setValue, lock, dirty, reset } = useSettingsDoc<General>("general");
  const toast = useToast();
  const [showKey, setShowKey] = useState(false);
  const regen = async () => {
    try {
      const v = await unwrap(api.POST("/api/v1/settings/general/apikey"));
      setValue(v);
      toast.success(tr("New API key generated"));
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <>
      <PageHeader
        title={t("General")}
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {g && (
        <Card title={t("Server")} className="mb-6">
          <div className="grid gap-4 md:grid-cols-2">
            <Field env={lock("instanceName")} label={t("Instance name")}>
              <Input value={g.instanceName} onChange={(e) => patch({ instanceName: e.target.value })} />
            </Field>
            <Field env={lock("publicUrl")} label={t("Public URL")} help={t("Used for links in notifications, e.g. https://mangarr.example.com")}>
              <Input value={g.publicUrl} onChange={(e) => patch({ publicUrl: e.target.value })} />
            </Field>
            <Field env={lock("imageCacheMaxMb")} label={t("Image cache limit (MB)")} help={t("Thumbnails and covers; oldest files are removed first. 0 = unlimited.")}>
              <Input type="number" min={0} value={g.imageCacheMaxMb} onChange={(e) => patch({ imageCacheMaxMb: Number(e.target.value) })} />
            </Field>
            <Field env={lock("backupRetention")} label={t("Keep scheduled backups")}>
              <Input type="number" min={1} value={g.backupRetention} onChange={(e) => patch({ backupRetention: Number(e.target.value) })} />
            </Field>
            <Field
              label={
                <>{t("API key") + " "}<EnvLock env={lock("apiKey")} />
                </>
              }
              help={t("Send as X-Api-Key header. API docs: /api/docs")}
            >
              <div className="flex gap-2">
                <Input readOnly autoComplete="off" type={showKey ? "text" : "password"} value={g.apiKey} aria-label={t("API key")} className="font-mono text-xs" />
                <IconButton title={showKey ? t("Hide API key") : t("Show API key")} aria-pressed={showKey} onClick={() => setShowKey(!showKey)}>
                  {showKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </IconButton>
                <IconButton title={t("Copy")} onClick={() => (navigator.clipboard.writeText(g.apiKey), toast.info(tr("Copied")))}>
                  <Copy className="size-4" />
                </IconButton>
                <Button onClick={regen} disabled={!!lock("apiKey")} icon={<KeyRound className="size-4" />}>{t("Regenerate")}</Button>
              </div>
            </Field>
          </div>
        </Card>
      )}
      <Appearance />
      <Tags />
      <Password />
      <SaveBar dirty={dirty} saving={saving} onSave={() => void save()} onDiscard={reset} />
    </>
  );
}

/** Appearance is how the UI looks for everyone, and new users' defaults. */
function Appearance() {
  const doc = useSettingsDoc<S["Appearance"]>("appearance");
  const qc = useQueryClient();
  const v = doc.value;
  // a document without its fields (an older server) shows nothing
  if (!v?.theme) return null;
  const save = async () => {
    await doc.save();
    await qc.invalidateQueries({ queryKey: ["auth"] });
  };
  return (
    <Card title={t("Appearance")} className="mb-6">
      <div className="flex flex-col gap-5">
        <Field env={doc.lock("accent")} label={t("Accent colour")} help={t("Text on the accent keeps 4.5:1 contrast; the shade adjusts for light and dark themes.")}>
          <AccentPicker value={v.accent} onChange={(accent) => doc.patch({ accent })} defaultLabel={t("mangarr orange")} />
        </Field>
        <Field env={doc.lock("loginMessage")} label={t("Sign-in page message")} help={t("Shown under the sign-in form, e.g. who to ask for an account.")}>
          <Textarea value={v.loginMessage} maxLength={500} onChange={(e) => doc.patch({ loginMessage: e.target.value })} />
        </Field>
        <div>
          <h3 className="mb-2 text-sm font-semibold">{t("Defaults for new users")}</h3>
          <div className="grid gap-4 md:grid-cols-3">
            <Field label={t("Theme")}>
              <Select value={v.theme} onChange={(e) => doc.patch({ theme: e.target.value as S["Appearance"]["theme"] })}>
                <option value="dark">{t("Dark")}</option>
                <option value="light">{t("Light")}</option>
                <option value="system">{t("Follow system")}</option>
              </Select>
            </Field>
            <Field label={t("Start page")}>
              <Select value={v.startPage} onChange={(e) => doc.patch({ startPage: e.target.value as S["Appearance"]["startPage"] })}>
                <StartPageOptions />
              </Select>
            </Field>
            <Field label={t("Interface language")}>
              <Select value={v.locale} onChange={(e) => doc.patch({ locale: e.target.value as S["Appearance"]["locale"] })}>
                <option value="auto">{t("Automatic")}</option>
                <option value="en">English</option>
                <option value="ru">Русский</option>
                <option value="uk">Українська</option>
              </Select>
            </Field>
          </div>
        </div>
        {doc.dirty && (
          <div className="flex gap-2">
            <Button variant="primary" loading={doc.saving} onClick={() => void save()}>{t("Save appearance")}</Button>
            <Button onClick={doc.reset}>{t("Discard")}</Button>
          </div>
        )}
      </div>
    </Card>
  );
}

function Tags() {
  const { data } = useTags();
  const qc = useQueryClient();
  const toast = useToast();
  const [label, setLabel] = useState("");
  const add = async () => {
    try {
      await unwrap(api.POST("/api/v1/tags", { body: { label } }));
      setLabel("");
      qc.invalidateQueries({ queryKey: ["tags"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const remove = async (id: number) => {
    await unwrap(api.DELETE("/api/v1/tags/{id}", { params: { path: { id } } }));
    qc.invalidateQueries({ queryKey: ["tags"] });
  };
  return (
    <Card title={t("Tags")} className="mb-6">
      <p className="mb-3 text-sm text-muted">{t("Tags limit notifications to some series, and the “keep” tag excludes series from cleanup.")}</p>
      <div className="mb-3 flex flex-wrap gap-2">
        {data?.map((t) => (
          <Badge key={t.id}>
            {t.label}
            <button className="ml-1 hover:text-err" onClick={() => remove(t.id)}>
              <Trash2 className="size-3" />
            </button>
          </Badge>
        ))}
      </div>
      <div className="flex max-w-sm gap-2">
        <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="keep" />
        <Button icon={<Plus className="size-4" />} disabled={!label} onClick={add}>{t("Add")}</Button>
      </div>
    </Card>
  );
}

function Password() {
  const toast = useToast();
  const [pw, setPw] = useState("");
  const [saving, setSaving] = useState(false);
  const change = async () => {
    setSaving(true);
    try {
      await unwrap(api.POST("/api/v1/auth/password", { body: { password: pw } }));
      setPw("");
      toast.success(tr("Password changed"));
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Card title={t("Password")}>
      <div className="flex max-w-sm gap-2">
        <Input type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} placeholder={t("New password")} />
        <Button disabled={pw.length < 6} loading={saving} onClick={change}>{t("Change")}</Button>
      </div>
    </Card>
  );
}
