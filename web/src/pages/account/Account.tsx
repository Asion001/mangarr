import { t as tr, t, label } from "../../lib/i18n/core";
import { useUIPreferences } from "../../lib/uiPreferences";
import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, Download, KeyRound, Link2, LogOut, Monitor, Pencil, Plus, Trash2, Unlink } from "lucide-react";
import { api, apiUrl, unwrap, type Implementation, type ModuleResource } from "../../api/client";
import { DynamicForm, defaultsOf } from "../../components/DynamicForm";
import { Badge, Button, Card, Confirm, ErrorBox, Field, IconButton, Input, Modal, PageHeader, Select, Switch, Tabs } from "../../components/ui";
import { ModuleEditor, type Draft } from "../settings/Modules";
import { appAddress, appTabs, DevicesCard, Guide, type App } from "../settings/ReadingApps";
import { useAccount } from "../../lib/account";
import { useAuthStatus } from "../../api/queries";
import { AccentPicker, StartPageOptions } from "../../components/AccentPicker";
import { relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { ReadingStatsCard } from "./ReadingStats";
import { MessengerCard } from "./MessengerCard";

const permLabel: Record<string, string> = {
  admin: "Administrator",
  "library.manage": "Manage the library",
  "requests.manage": "Handle requests",
  "requests.create": "Request series",
  apps: "Reading apps",
  download: "Download files",
};

/** browser names a session's device from its user agent. */
function browser(ua: string) {
  const b = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /iPad/.test(ua) ? "iPad" : /iPhone/.test(ua) ? "iPhone" : /Android/.test(ua) ? "Android" : /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${b} on ${os}` : b;
}

export function AccountPage() {
  const { account, name, can } = useAccount();
  return (
    <>
      <PageHeader title={t("My account")} subtitle={
          account?.kind === "user" ? `Signed in as ${account.username}` : account?.kind === "anonymous" ? tr("Logins are turned off (MANGARR_AUTH_DISABLED)") : tr("Signed in with the API key")
        } />
      <div className="flex flex-col gap-6">
        <InterfaceCard />
        <Card title={name}>
          <div className="flex flex-col gap-2 text-sm">
            <div>{t("Group") + " "}<Badge tone="info">{account?.group || "—"}</Badge>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-muted">{t("You can:")}</span>{" " + t("read the library")}{(account?.permissions ?? []).map((p) => (
                <Badge key={p}>{label(permLabel[p] ?? p)}</Badge>
              ))}
            </div>
          </div>
        </Card>
        <ReadingStatsCard />
        {account?.kind === "user" && <MessengerCard />}
        {account?.kind === "user" && <NotificationsCard />}
        {account?.kind === "user" && <LibraryAccountsCard />}
        {can("apps") && <ReadingAppsCard />}
        {account?.kind === "user" && <PasswordCard />}
        {account?.kind === "user" && <SessionsCard />}
      </div>
    </>
  );
}

function ReadingAppsCard() {
  const { data: st } = useQuery({ queryKey: ["reading", "status"], queryFn: () => unwrap(api.GET("/api/v1/reading/status")) });
  const [app, setApp] = useState<App>("mihon");
  const [exporting, setExporting] = useState(false);
  const qc = useQueryClient();
  const toast = useToast();
  if (!st) return null;
  if (!st.enabled) {
    return (
      <Card title={t("Reading apps")}>
        <p className="text-sm text-muted">{t("Mihon, KMReader, Paperback and KOReader can read this library once an administrator turns on reading apps.")}</p>
      </Card>
    );
  }
  const address = appAddress(st.publicUrl, st.address);
  const exportMihon = async () => {
    setExporting(true);
    try {
      const response = await fetch(apiUrl("api/v1/reading/mihon-backup"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ address }),
      });
      if (!response.ok) {
        const body = await response.json().catch(() => ({})) as { detail?: string; title?: string };
        throw new Error(body.detail || body.title || response.statusText);
      }
      const blob = await response.blob();
      const file = response.headers.get("Content-Disposition")?.match(/filename="([^"]+)"/)?.[1] || "mangarr-mihon.tachibk";
      const href = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = href;
      link.download = file;
      link.click();
      setTimeout(() => URL.revokeObjectURL(href), 0);
      qc.invalidateQueries({ queryKey: ["reading", "keys"] });
      toast.success(tr("Mihon setup backup downloaded"));
    } catch (error) {
      toast.fromError(error, tr("Could not export Mihon backup"));
    } finally {
      setExporting(false);
    }
  };
  return (
    <>
      <Card title={t("Reading apps")}>
        <p className="mb-3 text-sm text-muted">{t("Read in Mihon, KMReader, Paperback or KOReader: they connect as your account and sync your progress.")}</p>
        <Tabs
          tabs={appTabs}
          value={app}
          onChange={setApp}
        />
        <div className="mt-4 text-sm">
          <Guide app={app} address={address} />
        </div>
        {app === "mihon" && (
          <div className="mt-5 rounded-lg border border-border bg-panel-2 p-4">
            <h3 className="font-medium">{t("Set up Mihon from a backup")}</h3>
            <p className="mt-1 text-sm text-muted">{t("Download your visible library and current progress with this server address already configured. The file contains a new device key; revoke that device below if the file is lost or replaced.")}</p>
            <Button className="mt-3" icon={<Download className="size-4" />} loading={exporting} onClick={exportMihon}>{t("Download Mihon setup backup")}</Button>
            <p className="mt-2 text-xs text-muted">{t("Install the Komga extension, then restore this file in Mihon under More → Backup and restore. Select library entries and source settings during restore.")}</p>
          </div>
        )}
      </Card>
      <DevicesCard />
    </>
  );
}

function PasswordCard() {
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError(new Error("The new passwords don't match"));
      return;
    }
    setSaving(true);
    try {
      await unwrap(api.POST("/api/v1/auth/password", { body: { current, password } }));
      toast.success(tr("Password changed"), tr("Your other sessions were signed out"));
      setCurrent("");
      setPassword("");
      setConfirm("");
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Card title={t("Password")}>
      <form onSubmit={submit} className="grid max-w-xl gap-4 sm:grid-cols-2">
        <Field label={t("Current password")} className="sm:col-span-2">
          <Input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
        </Field>
        <Field label={t("New password")} help={t("At least 8 characters.")}>
          <Input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={8} />
        </Field>
        <Field label={t("Confirm new password")}>
          <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </Field>
        {error !== null && (
          <div className="sm:col-span-2">
            <ErrorBox error={error} />
          </div>
        )}
        <div>
          <Button type="submit" icon={<KeyRound className="size-4" />} loading={saving}>{t("Change password")}</Button>
        </div>
      </form>
    </Card>
  );
}

function SessionsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data, error } = useQuery({ queryKey: ["me", "sessions"], queryFn: () => unwrap(api.GET("/api/v1/me/sessions")) });
  const revoke = async (id: string) => {
    try {
      await unwrap(api.DELETE("/api/v1/me/sessions/{id}", { params: { path: { id } } }));
      qc.invalidateQueries({ queryKey: ["me", "sessions"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const others = async () => {
    try {
      await unwrap(api.POST("/api/v1/me/sessions/revoke-others"));
      qc.invalidateQueries({ queryKey: ["me", "sessions"] });
      toast.success(tr("Signed out everywhere else"));
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <Card
      title={t("Where you're signed in")}
      actions={
        (data?.length ?? 0) > 1 && (
          <Button size="sm" icon={<LogOut className="size-3.5" />} onClick={others}>{t("Sign out everywhere else")}</Button>
        )
      }
    >
      {error && <ErrorBox error={error} />}
      <div className="flex flex-col gap-2">
        {data?.map((s) => (
          <div key={s.id} className="flex items-center gap-3 rounded bg-panel-2 px-3 py-2 text-sm">
            <Monitor className="size-4 shrink-0 text-muted" />
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="font-medium">
                {browser(s.userAgent)} {s.current && <Badge tone="ok">{t("this browser")}</Badge>}
              </span>
              <span className="truncate text-xs text-muted">
                {s.ip || tr("unknown address")}{" " + t("· active") + " "}{relative(s.lastSeenAt)}{" " + t("· signed in") + " "}{relative(s.createdAt)}
              </span>
            </div>
            {!s.current && (
              <IconButton title={t("Sign out")} onClick={() => revoke(s.id)}>
                <LogOut className="size-4" />
              </IconButton>
            )}
          </div>
        ))}
      </div>
    </Card>
  );
}

/** NotificationsCard: your own notification targets (new chapters of series you follow, your requests). */
function NotificationsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: targets, error } = useQuery({ queryKey: ["me-notifications"], queryFn: () => unwrap(api.GET("/api/v1/me/notifications")) });
  const { data: impls } = useQuery({
    queryKey: ["me-notifications", "schema"],
    queryFn: () => unwrap(api.GET("/api/v1/me/notifications/schema")),
    staleTime: Infinity,
  });
  const [picking, setPicking] = useState(false);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [deleting, setDeleting] = useState<ModuleResource | null>(null);
  const implOf = (name: string) => impls?.find((i) => i.name === name);
  const startNew = (impl: Implementation) => {
    setPicking(false);
    setDraft({ implementation: impl.name, name: impl.displayName, enabled: true, priority: 25, events: impl.events ?? [], settings: defaultsOf(impl.fields) });
  };
  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/me/notifications/{id}", { params: { path: { id: deleting.id } } }));
      qc.invalidateQueries({ queryKey: ["me-notifications"] });
      setDeleting(null);
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <Card
      title={t("Notifications")}
      actions={
        <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setPicking(true)}>{t("Add")}</Button>
      }
    >
      <p className="mb-3 text-sm text-muted">{t("Get new chapters of the series you follow, and news about your requests, on your phone or chat. Follow a series with the bell on its page.")}</p>
      {error && <ErrorBox error={error} />}
      {targets?.length === 0 && <p className="text-sm text-muted">{t("No notifications set up yet.")}</p>}
      <div className="flex flex-col gap-2">
        {targets?.map((m) => (
          <div key={m.id} className="flex items-center gap-3 rounded-md bg-panel-2 px-3 py-2 text-sm">
            <Bell className="size-4 text-muted" />
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium">{m.name}</div>
              <div className="text-xs text-muted">
                {implOf(m.implementation)?.displayName ?? m.implementation}
                {(m.events ?? []).length > 0 && ` · ${m.events.length === 1 ? (m.events[0] === "chapter.imported" ? "new chapters only" : "requests only") : "everything"}`}
              </div>
              {m.error && <div className="text-xs text-err">{m.error}</div>}
            </div>
            {!m.enabled && <Badge>{t("off")}</Badge>}
            <IconButton
              title={t("Edit")}
              onClick={() =>
                setDraft({ id: m.id, implementation: m.implementation, name: m.name, enabled: m.enabled, priority: m.priority, events: m.events ?? [], settings: { ...m.settings } })
              }
            >
              <Pencil className="size-4" />
            </IconButton>
            <IconButton title={t("Delete")} onClick={() => setDeleting(m)}>
              <Trash2 className="size-4" />
            </IconButton>
          </div>
        ))}
      </div>
      <Modal open={picking} onClose={() => setPicking(false)} title={t("Send my notifications to")}>
        <div className="flex flex-col gap-2">
          {impls?.map((i) => (
            <button key={i.name} onClick={() => startNew(i)} className="rounded-lg border border-border p-3 text-left hover:border-accent hover:bg-panel-2">
              <div className="font-medium">{i.displayName}</div>
              <div className="mt-0.5 text-sm text-muted">{i.description}</div>
            </button>
          ))}
        </div>
      </Modal>
      {draft && <ModuleEditor kind="notify" personal draft={draft} impl={implOf(draft.implementation)} onClose={() => setDraft(null)} />}
      <Confirm open={!!deleting} title={t("Delete notification")} danger confirmLabel={t("Delete")} message={`Delete ${deleting?.name}?`} onConfirm={remove} onClose={() => setDeleting(null)} />
    </Card>
  );
}

/** LibraryAccountsCard: link your own Komga/Kavita account so your progress syncs with it. */
function LibraryAccountsCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: servers } = useQuery({ queryKey: ["me-library-accounts"], queryFn: () => unwrap(api.GET("/api/v1/me/library-accounts")) });
  const [linking, setLinking] = useState<number | null>(null);
  const [creds, setCreds] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  if (!servers?.length) return null;
  const refresh = () => qc.invalidateQueries({ queryKey: ["me-library-accounts"] });
  const link = async (moduleId: number) => {
    setBusy(true);
    try {
      const credentials = Object.fromEntries(Object.entries(creds).map(([k, v]) => [k, String(v ?? "")]));
      await unwrap(api.POST("/api/v1/me/library-accounts", { body: { moduleId, credentials } }));
      toast.success(tr("Linked"), tr("Your progress there syncs with mangarr now."));
      setLinking(null);
      setCreds({});
      refresh();
    } catch (e) {
      toast.fromError(e, tr("Couldn't link it"));
    } finally {
      setBusy(false);
    }
  };
  const unlink = async (moduleId: number) => {
    try {
      await unwrap(api.DELETE("/api/v1/me/library-accounts/{moduleId}", { params: { path: { moduleId } } }));
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <Card title={t("Library servers")}>
      <p className="mb-3 text-sm text-muted">{t("Read in Komga or Kavita with your own account? Link it and your progress there syncs with mangarr (and the other way).")}</p>
      <div className="flex flex-col gap-2">
        {servers.map((srv) => (
          <div key={srv.moduleId} className="rounded-md bg-panel-2 px-3 py-2 text-sm">
            <div className="flex items-center gap-3">
              <div className="min-w-0 flex-1">
                <div className="font-medium">{srv.name}</div>
                <div className="text-xs text-muted">
                  {srv.linked ? (
                    <>{t("Linked as") + " "}{srv.linked.externalUser || tr("you")}
                      {srv.linked.lastSyncAt && ` · synced ${relative(srv.linked.lastSyncAt)}`}
                    </>
                  ) : (
                    tr("Not linked")
                  )}
                </div>
                {srv.linked?.lastError && <div className="text-xs text-err">{srv.linked.lastError}</div>}
              </div>
              {srv.linked ? (
                <Button size="sm" icon={<Unlink className="size-4" />} onClick={() => unlink(srv.moduleId)}>{t("Unlink")}</Button>
              ) : (
                linking !== srv.moduleId && (
                  <Button
                    size="sm"
                    icon={<Link2 className="size-4" />}
                    onClick={() => {
                      setLinking(srv.moduleId);
                      setCreds(defaultsOf(srv.fields));
                    }}
                  >{t("Link")}</Button>
                )
              )}
            </div>
            {linking === srv.moduleId && (
              <div className="mt-3 flex flex-col gap-3 border-t border-border pt-3">
                <DynamicForm fields={srv.fields} values={creds} onChange={setCreds} />
                <div className="flex justify-end gap-2">
                  <Button size="sm" onClick={() => setLinking(null)}>{t("Cancel")}</Button>
                  <Button size="sm" variant="primary" loading={busy} onClick={() => link(srv.moduleId)}>{t("Link")}</Button>
                </div>
              </div>
            )}
          </div>
        ))}
      </div>
    </Card>
  );
}

function InterfaceCard() {
  const {preferences,save,saving,error} = useUIPreferences();
  const toast=useToast();
  const { data: status } = useAuthStatus();
  const o = preferences.options ?? {};
  const set = (options: Partial<typeof o>) => void save({ options }).catch((e) => toast.fromError(e));
  const serverTheme = { dark: t("Dark"), light: t("Light"), system: t("Follow system") }[status?.appearance?.theme ?? "dark"];
  // sidebar items you can hide (Series and your account always stay)
  const hideable = [{ to: "/discover", label: t("Discover") }, { to: "/updates", label: t("Updates") }, { to: "/requests", label: t("Requests") }];
  const hidden = o.hiddenNav ?? [];
  return (
    <Card title={t("Interface")}>
      <div className="flex flex-col gap-5">
        <div className="grid gap-4 md:grid-cols-3">
          <Field label={t("Interface language")}>
            <Select disabled={saving} value={preferences.locale} onChange={(e) => { void save({ locale: e.target.value as typeof preferences.locale }).catch((e) => toast.fromError(e)); }}>
              <option value="auto">{t("Automatic")}</option><option value="en">English</option><option value="ru">Русский</option><option value="uk">Українська</option>
            </Select>
          </Field>
          <Field label={t("Theme")}>
            <Select value={o.theme ?? ""} onChange={(e) => set({ theme: e.target.value as typeof o.theme })}>
              <option value="">{t("Server's ({theme})", { theme: serverTheme })}</option>
              <option value="dark">{t("Dark")}</option>
              <option value="light">{t("Light")}</option>
              <option value="system">{t("Follow system")}</option>
            </Select>
          </Field>
          <Field label={t("Start page")}>
            <Select value={o.startPage ?? ""} onChange={(e) => set({ startPage: e.target.value as typeof o.startPage })}>
              <option value="">{t("Server's default")}</option>
              <StartPageOptions />
            </Select>
          </Field>
        </div>
        <Field label={t("Accent colour")}>
          <AccentPicker value={o.accent ?? ""} onChange={(accent) => set({ accent })} defaultLabel={t("Server's")} />
        </Field>
        <div className="flex flex-col gap-2">
          <span className="text-sm font-medium">{t("Sidebar")}</span>
          {hideable.map((item) => (
            <Switch key={item.to} checked={!hidden.includes(item.to)} onChange={(on) => set({ hiddenNav: on ? hidden.filter((x) => x !== item.to) : [...hidden, item.to] })} label={item.label} />
          ))}
          <Switch checked={!!o.sidebarCollapsed} onChange={(v) => { try { localStorage.removeItem("mangarr:nav-collapsed"); } catch { /* private mode */ } set({ sidebarCollapsed: v }); }} label={t("Start collapsed")} />
          <p className="text-sm text-muted">{t("Saved to your account, so it follows you to other devices.")}</p>
        </div>
        <div>
          <Switch disabled={saving} checked={o.otherLanguageChapters ?? true} onChange={(v) => set({ otherLanguageChapters: v })} label={t("Show chapters from other languages")} />
          <p className="mt-1 text-sm text-muted">{t("When a title has several language editions, chapters only another edition has appear in the list with a language tag.")}</p>
        </div>
        {error ? <p role="alert">{t("Could not save preferences")}</p> : null}
      </div>
    </Card>
  );
}
