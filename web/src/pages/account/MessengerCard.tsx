import { t } from "../../lib/i18n/core";
import { useEffect, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertCircle, Loader2 } from "lucide-react";
import { api, basePath, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, ErrorBox, Segmented } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { DiscordIcon, TelegramIcon } from "../settings/MessengerBots";

type Kind = "telegram" | "discord";
type Link = S["MessengerLink"];
type Mode = S["MyMessenger"]["mode"];

const myMessenger = ["me-messenger"];

function Row({ icon, name, sub, children, note }: { icon: ReactNode; name: string; sub: ReactNode; children: ReactNode; note?: ReactNode }) {
  return (
    <div className="flex flex-col gap-2.5 px-3.5 py-3">
      <div className="flex flex-wrap items-center gap-3">
        {icon}
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">{name}</div>
          <div className="text-xs text-muted">{sub}</div>
        </div>
        <div className="flex flex-wrap items-center gap-2">{children}</div>
      </div>
      {note}
    </div>
  );
}

/**
 * MessengerCard is My account › Notifications: link a Telegram or Discord
 * account to the server's bots, and choose what you hear about and how
 * often.
 */
export function MessengerCard() {
  const qc = useQueryClient();
  const toast = useToast();
  const [pending, setPending] = useState<{ kind: Kind; url: string; until: number } | null>(null);
  const { data: m, error } = useQuery({
    queryKey: myMessenger,
    queryFn: () => unwrap(api.GET("/api/v1/me/messenger")),
    refetchInterval: pending ? 3000 : false,
  });
  const link = (kind: Kind) => m?.links?.find((l) => l.kind === kind);

  // the bot links the account: stop waiting once it shows up, or the link expires
  useEffect(() => {
    if (!pending) return;
    if (m?.links?.some((l) => l.kind === pending.kind)) {
      setPending(null);
      toast.success(t("Telegram is linked"));
      return;
    }
    const left = pending.until - Date.now();
    const timer = window.setTimeout(() => setPending(null), Math.max(left, 0));
    return () => clearTimeout(timer);
  }, [m, pending, toast]);

  const startTelegram = async () => {
    try {
      const r = await unwrap(api.POST("/api/v1/me/messenger/telegram/link"));
      setPending({ kind: "telegram", url: r.url, until: new Date(r.expiresAt).getTime() });
      window.open(r.url, "_blank", "noopener");
    } catch (e) {
      toast.fromError(e);
    }
  };
  const unlink = async (kind: Kind) => {
    try {
      await unwrap(api.DELETE("/api/v1/me/messenger/{kind}", { params: { path: { kind } } }));
      qc.invalidateQueries({ queryKey: myMessenger });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const [testing, setTesting] = useState<Kind | null>(null);
  const test = async (kind: Kind) => {
    setTesting(kind);
    try {
      await unwrap(api.POST("/api/v1/me/messenger/{kind}/test", { params: { path: { kind } } }));
      toast.success(t("Test message sent"));
    } catch (e) {
      toast.fromError(e, t("The message didn't go through"));
    } finally {
      setTesting(null);
      qc.invalidateQueries({ queryKey: myMessenger });
    }
  };
  const setPreferences = async (mode: Mode, events: string[]) => {
    try {
      const r = await unwrap(api.PUT("/api/v1/me/messenger/preferences", { body: { mode, events } }));
      qc.setQueryData(myMessenger, r);
    } catch (e) {
      toast.fromError(e);
    }
  };

  if (error) return <Card title={t("Notifications")}><ErrorBox error={error} /></Card>;
  if (!m?.links) return null;
  const none = !m.telegram.available && !m.discord.available;
  const linked = m.links.length > 0;

  const linkedRow = (kind: Kind, l: Link, icon: ReactNode, name: string, problem: string) => (
    <Row
      icon={icon}
      name={name}
      sub={t("Linked as {name}", { name: l.displayName || "?" })}
      note={
        l.status === "broken" && (
          <div className="ml-10 flex flex-wrap items-center gap-3 rounded-md border border-warn/40 bg-warn/10 px-3 py-2.5">
            <AlertCircle className="size-4 shrink-0 text-warn" />
            <p className="min-w-0 flex-1 text-sm">{problem}</p>
            <Button size="sm" loading={testing === kind} onClick={() => void test(kind)}>{t("Try again")}</Button>
          </div>
        )
      }
    >
      {l.status === "broken" ? <Badge tone="warn">{t("Paused")}</Badge> : <Badge tone="ok">{t("Linked")}</Badge>}
      {l.status !== "broken" && <Button size="sm" loading={testing === kind} onClick={() => void test(kind)}>{t("Send a test")}</Button>}
      <Button size="sm" onClick={() => void unlink(kind)}>{t("Unlink")}</Button>
    </Row>
  );

  const tg = link("telegram");
  const dc = link("discord");
  const modes: { value: Mode; label: string }[] = [
    { value: "off", label: t("Off") },
    ...(m.allowInstant ? [{ value: "instant" as Mode, label: t("Instant") }] : []),
    { value: "daily_digest", label: t("Daily digest") },
    ...(m.allowInstant ? [{ value: "instant_and_digest" as Mode, label: t("Both") }] : []),
  ];
  const digestAt = `${String(m.digestHour).padStart(2, "0")}:00`;
  const event = (key: string, label: string, help: string) => (
    <label className="flex items-start gap-2.5 text-sm">
      <input
        type="checkbox"
        className="mt-0.5 size-4 accent-accent"
        checked={m.events.includes(key)}
        onChange={(e) => void setPreferences(m.mode, e.target.checked ? [...m.events, key] : m.events.filter((x) => x !== key))}
      />
      <span>
        {label}
        <span className="block text-xs text-muted">{help}</span>
      </span>
    </label>
  );

  return (
    <Card title={t("Notifications")}>
      <p className="mb-4 text-sm text-muted">{t("Private messages from this server's bot about the series you follow and your requests. Link an account once; there's nothing else to set up. Follow a series with the bell on its page.")}</p>
      {none && <p className="text-sm text-muted">{t("The admin hasn't set up a Telegram or Discord bot on this server yet.")}</p>}
      {!none && (
        <div className="flex flex-col divide-y divide-border rounded-lg border border-border">
          {m.telegram.available &&
            (tg ? (
              linkedRow("telegram", tg, <TelegramIcon />, "Telegram", t("The bot can't message you: you may have blocked it or deleted the chat. Open the chat with the bot, press Start, then try again."))
            ) : pending?.kind === "telegram" ? (
              <Row
                icon={<TelegramIcon />}
                name="Telegram"
                sub={<span className="inline-flex items-center gap-1.5"><Loader2 className="size-3.5 animate-spin" />{t("Waiting for you in Telegram…")}</span>}
                note={<p className="ml-10 text-xs text-muted">{t("Press Start in the chat with the bot; this page links itself as soon as the bot hears from you. The link works once and for 10 minutes.")}</p>}
              >
                <a href={pending.url} target="_blank" rel="noopener noreferrer" className="inline-flex h-7 items-center rounded-md bg-primary px-2.5 text-xs font-medium text-white hover:bg-primary-hover">{t("Open Telegram again")}</a>
                <Button size="sm" onClick={() => setPending(null)}>{t("Cancel")}</Button>
              </Row>
            ) : (
              <Row icon={<TelegramIcon />} name="Telegram" sub={t("Opens a chat with the bot. Press Start there and this page links itself.")}>
                <Button size="sm" variant="primary" onClick={() => void startTelegram()}>{t("Link Telegram")}</Button>
              </Row>
            ))}
          {m.discord.available &&
            (dc ? (
              linkedRow("discord", dc, <DiscordIcon />, "Discord", t("Discord won't let the bot message you yet. Join a server the bot is in and allow direct messages from its members."))
            ) : (
              <Row icon={<DiscordIcon />} name="Discord" sub={t("Sign in with Discord. Only your Discord user id is kept.")}>
                <a href={`${basePath}/api/v1/me/messenger/discord/link`} className="inline-flex h-7 items-center rounded-md bg-primary px-2.5 text-xs font-medium text-white hover:bg-primary-hover">{t("Link Discord")}</a>
              </Row>
            ))}
        </div>
      )}
      {linked && (
        <div className="mt-5 grid gap-6 md:grid-cols-2">
          <div className="flex flex-col gap-2.5">
            <h3 className="text-sm font-semibold">{t("Tell me about")}</h3>
            {event("chapter.imported", t("New chapters of series I follow"), t("Only series you can see. One message per series, not per chapter."))}
            {event("request.updated", t("My requests"), t("Approved, declined, added to the library."))}
          </div>
          <div className="flex flex-col gap-2.5">
            <h3 className="text-sm font-semibold">{t("How often")}</h3>
            <Segmented label={t("How often")} value={m.mode} options={modes} onChange={(mode) => void setPreferences(mode, m.events)} />
            <p className="text-xs text-muted">
              {m.allowInstant
                ? t("Instant sends new chapters as they arrive; the digest collects the day at {time}. Request updates always go out right away.", { time: digestAt })
                : t("New chapters come in one digest a day at {time}. Request updates always go out right away.", { time: digestAt })}
            </p>
          </div>
        </div>
      )}
    </Card>
  );
}
