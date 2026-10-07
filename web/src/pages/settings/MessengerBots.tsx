import { t } from "../../lib/i18n/core";
import { useState, type ReactNode } from "react";
import { CheckCircle2 } from "lucide-react";
import { basePath, type S } from "../../api/client";
import { Badge, Button, ErrorBox, Field, Input, Loading, SaveBar, SecretInput, Select, Switch } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { useSettingsDoc } from "./useSettingsDoc";

type Messenger = S["MessengerSettings"];
type Bot = "telegram" | "discord";
type Identity = S["Identity"];

const telegramAPI = "https://api.telegram.org";

function TelegramIcon() {
  return (
    <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-info text-white" aria-hidden>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 4 3 11l6 2 2 6 3-4 5 4z" /></svg>
    </span>
  );
}

function DiscordIcon() {
  return (
    <span className="flex size-7 shrink-0 items-center justify-center rounded-full bg-accent text-white" aria-hidden>
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M7 7c3-1.5 7-1.5 10 0l2 9c-2 1.5-4 2-4 2l-1-2m-4 0-1 2s-2-.5-4-2z" /></svg>
    </span>
  );
}

function BotCard({ icon, name, subtitle, enabled, onEnabled, env, children }: { icon: ReactNode; name: string; subtitle: ReactNode; enabled: boolean; onEnabled: (v: boolean) => void; env?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4 rounded-lg border border-border bg-panel p-4">
      <header className="flex items-center gap-3">
        {icon}
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-semibold">{name}</h3>
          <div className="text-xs text-muted">{subtitle}</div>
        </div>
        <Switch checked={enabled} onChange={onEnabled} env={env} label={<span className="sr-only">{t("Enabled")}</span>} />
      </header>
      {children}
    </section>
  );
}

/**
 * MessengerBots is the admin's Telegram and Discord bots (Settings ›
 * Notifications). People link their own account to them; the admin only
 * enters the bots' credentials here.
 */
export function MessengerBots() {
  const { value: m, patch, save, saving, isLoading, error, dirty, reset, lock } = useSettingsDoc<Messenger>("messenger");
  const toast = useToast();
  const [testing, setTesting] = useState<Bot | null>(null);
  const [bots, setBots] = useState<Partial<Record<Bot, Identity>>>({});

  const test = async (bot: Bot) => {
    if (!m) return;
    setTesting(bot);
    try {
      const r = await fetch(`${basePath}/api/v1/settings/messenger/test`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ bot, settings: m }),
      });
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.detail || body.title || `HTTP ${r.status}`);
      setBots((b) => ({ ...b, [bot]: body as Identity }));
      toast.success(t("The bot answered"), body.username ? "@" + body.username : body.name);
    } catch (e) {
      setBots((b) => ({ ...b, [bot]: undefined }));
      toast.fromError(e, t("The bot didn't answer"));
    } finally {
      setTesting(null);
    }
  };

  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  if (!m) return null;
  const tg = m.telegram;
  const dc = m.discord;
  const setTg = (p: Partial<Messenger["telegram"]>) => patch({ telegram: { ...tg, ...p } });
  const setDc = (p: Partial<Messenger["discord"]>) => patch({ discord: { ...dc, ...p } });
  const botLine = (bot: Bot, on: boolean) => {
    const id = bots[bot];
    if (id) return <span className="inline-flex items-center gap-1 text-ok"><CheckCircle2 className="size-3.5" />{id.username ? "@" + id.username : id.name}</span>;
    return on ? t("On") : t("Not set up");
  };

  return (
    <section className="mb-8 flex flex-col gap-3">
      <div>
        <h2 className="text-base font-semibold">{t("Messenger bots")}</h2>
        <p className="text-sm text-muted">{t("Your own Telegram and Discord bots send people their updates in private messages. People link their account under My account › Notifications; they never enter tokens or addresses.")}</p>
      </div>
      <div className="grid gap-3 xl:grid-cols-2">
        <BotCard icon={<TelegramIcon />} name="Telegram" subtitle={botLine("telegram", tg.enabled)} enabled={tg.enabled} onEnabled={(enabled) => setTg({ enabled })} env={lock("telegram.enabled")}>
          <Field label={t("Bot token")} env={lock("telegram.botToken")} help={t("From @BotFather. Test checks it and sends nothing.")}>
            <div className="flex gap-2">
              <SecretInput value={tg.botToken} onChange={(botToken) => setTg({ botToken })} className="flex-1" />
              <Button loading={testing === "telegram"} onClick={() => void test("telegram")}>{t("Test")}</Button>
            </div>
          </Field>
          <Field label={t("Bot API server")} env={lock("telegram.apiUrl")} help={t("Leave as it is unless you run your own Bot API server.")}>
            <Input value={tg.apiUrl} onChange={(e) => setTg({ apiUrl: e.target.value })} placeholder={telegramAPI} />
          </Field>
        </BotCard>

        <BotCard icon={<DiscordIcon />} name="Discord" subtitle={botLine("discord", dc.enabled)} enabled={dc.enabled} onEnabled={(enabled) => setDc({ enabled })} env={lock("discord.enabled")}>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label={t("Application (client) ID")} env={lock("discord.clientId")}>
              <Input value={dc.clientId} onChange={(e) => setDc({ clientId: e.target.value })} autoComplete="off" />
            </Field>
            <Field label={t("Client secret")} env={lock("discord.clientSecret")}>
              <SecretInput value={dc.clientSecret} onChange={(clientSecret) => setDc({ clientSecret })} />
            </Field>
          </div>
          <Field label={t("Bot token")} env={lock("discord.botToken")} help={t("From the Discord developer portal. Test checks it and sends nothing.")}>
            <div className="flex gap-2">
              <SecretInput value={dc.botToken} onChange={(botToken) => setDc({ botToken })} className="flex-1" />
              <Button loading={testing === "discord"} onClick={() => void test("discord")}>{t("Test")}</Button>
            </div>
          </Field>
          <Field label={t("Redirect URL")} help={m.discordRedirectUrl ? t("Add exactly this address under OAuth2 › Redirects in the Discord application. It's built from Settings › General › Public URL.") : undefined}>
            {m.discordRedirectUrl ? (
              <Input readOnly value={m.discordRedirectUrl} className="font-mono text-xs" />
            ) : (
              <p className="text-sm text-warn">{t("Set Settings › General › Public URL first: Discord sends people back to that address after they link.")}</p>
            )}
          </Field>
        </BotCard>
      </div>
      <div className="flex flex-wrap items-center gap-x-8 gap-y-3 rounded-lg border border-border bg-panel p-4">
        <div>
          <Switch checked={m.allowInstant} onChange={(allowInstant) => patch({ allowInstant })} env={lock("allowInstant")} label={t("Allow instant messages")} />
          <p className="mt-1 text-xs text-muted">{t("Off: people can only pick the daily digest.")}</p>
        </div>
        <label className="flex items-center gap-2 text-sm">
          {t("Daily digest at")}
          <Select value={m.digestHour} onChange={(e) => patch({ digestHour: Number(e.target.value) })} disabled={!!lock("digestHour")} className="w-24">
            {Array.from({ length: 24 }, (_, h) => (
              <option key={h} value={h}>{String(h).padStart(2, "0")}:00</option>
            ))}
          </Select>
          <span className="text-xs text-muted">{t("server time")}</span>
          {lock("digestHour") && <Badge tone="warn">env</Badge>}
        </label>
      </div>
      <SaveBar dirty={dirty} saving={saving} onSave={() => void save()} onDiscard={reset} />
    </section>
  );
}
