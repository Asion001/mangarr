import { t } from "../../lib/i18n/core";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import clsx from "clsx";
import { AlertTriangle, Check, Copy, ExternalLink, Music, Play, Plus, Search } from "lucide-react";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, Confirm, ErrorBox, Field, Input, Loading, Modal, PageHeader, SaveBar, Segmented, Switch, Tabs, TagInput } from "../../components/ui";
import { tint, type Mood } from "../../lib/soundtrack/core";
import { MoodIcon, moodColors, moodIcons, moodName } from "../../lib/soundtrack/moods";
import { connectSpotify, useMoods, useMySpotify, useSpotifyReturn } from "../../lib/soundtrack/spotify";
import { useToast } from "../../lib/toast";

type Tab = "moods" | "spotify";
type SearchType = "playlist" | "album" | "track";
type Item = S["SearchItem"];
type MoodDraft = { name: string; color: string; icon: string; genres: string[]; spotifyUri: string; spotifyName: string; spotifyImageUrl: string; shuffle: boolean };

const dashboard = "https://developer.spotify.com/dashboard";
const proxyDocs = "https://github.com/Asion001/mangarr/blob/main/docs/reverse-proxy.md";

/** ConnectPrompt asks to connect Spotify before searching it. */
function ConnectPrompt({ text }: { text: string }) {
  const toast = useToast();
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border bg-bg px-3 py-2.5 text-sm text-muted">
      <span className="min-w-0 flex-1">{text}</span>
      <Button size="sm" onClick={() => void connectSpotify().catch((e) => toast.fromError(e))}>{t("Connect Spotify")}</Button>
    </div>
  );
}

/** SpotifyPicker searches Spotify and picks a playlist, album or track, by cover. */
function SpotifyPicker({ value, onPick }: { value: string; onPick: (item: Item) => void }) {
  const [q, setQ] = useState("");
  const [query, setQuery] = useState("");
  const [type, setType] = useState<SearchType>("playlist");
  const [pages, setPages] = useState(1);
  useEffect(() => {
    const timer = window.setTimeout(() => (setQuery(q.trim()), setPages(1)), 350);
    return () => window.clearTimeout(timer);
  }, [q]);
  const { data, isFetching, error } = useQuery({
    queryKey: ["soundtrack", "search", type, query, pages],
    queryFn: async () => {
      const all: Item[] = [];
      for (let p = 0; p < pages; p++) {
        all.push(...(await unwrap(api.GET("/api/v1/spotify/search", { params: { query: { q: query, type: [type], offset: p * 10 } } }))));
      }
      return all;
    },
    enabled: query !== "",
    placeholderData: (prev) => prev,
  });
  const kind = (it: Item) => (it.type === "playlist" ? t("Playlist") : it.type === "album" ? t("Album") : t("Track"));
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative min-w-48 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("Search playlists, albums or tracks")} aria-label={t("Search Spotify")} className="w-full pl-9" />
        </div>
        <Segmented
          label={t("What to find")}
          value={type}
          onChange={(v) => (setType(v), setPages(1))}
          options={[
            { value: "playlist", label: t("Playlists") },
            { value: "album", label: t("Albums") },
            { value: "track", label: t("Tracks") },
          ]}
        />
      </div>
      {error && <ErrorBox error={error} />}
      {query !== "" && data && data.length === 0 && !isFetching && <p className="text-sm text-muted">{t("Nothing found on Spotify.")}</p>}
      {data && data.length > 0 && (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-2.5">
          {data.map((it) => (
            <button
              key={it.uri}
              type="button"
              aria-pressed={value === it.uri}
              onClick={() => onPick(it)}
              className={clsx(
                "flex flex-col gap-1.5 rounded-lg bg-panel-2 p-2 text-left text-[13px] font-medium",
                value === it.uri ? "border-2 border-accent" : "border border-border hover:border-muted/60",
              )}
            >
              {it.imageUrl ? (
                <img src={it.imageUrl} alt="" loading="lazy" className="aspect-square w-full rounded object-cover" />
              ) : (
                <span className="flex aspect-square w-full items-center justify-center rounded bg-border text-muted"><Music className="size-6" aria-hidden="true" /></span>
              )}
              <span className="line-clamp-2">{it.name}</span>
              <span className="truncate text-[11px] font-normal text-muted">
                {kind(it)}
                {it.subtitle ? " · " + it.subtitle : ""}
                {it.type === "playlist" && it.tracks ? " · " + t("{count} tracks", { count: it.tracks }) : ""}
              </span>
            </button>
          ))}
        </div>
      )}
      {data && data.length >= pages * 10 && (
        <Button size="sm" className="self-start" loading={isFetching} onClick={() => setPages(pages + 1)}>
          {t("More results")}
        </Button>
      )}
    </div>
  );
}

/** MoodEditor adds or changes a mood preset. */
function MoodEditor({ mood, onClose }: { mood: Mood | null; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const me = useMySpotify();
  const [d, setD] = useState<MoodDraft>(
    mood
      ? { name: mood.name, color: mood.color, icon: mood.icon, genres: mood.genres ?? [], spotifyUri: mood.spotifyUri, spotifyName: mood.spotifyName, spotifyImageUrl: mood.spotifyImageUrl, shuffle: mood.shuffle }
      : { name: "", color: moodColors[0], icon: "music", genres: [], spotifyUri: "", spotifyName: "", spotifyImageUrl: "", shuffle: true },
  );
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const patch = (p: Partial<MoodDraft>) => setD((v) => ({ ...v, ...p }));
  const done = () => {
    void qc.invalidateQueries({ queryKey: ["soundtrack"] });
    onClose();
  };
  const save = async () => {
    setSaving(true);
    try {
      if (mood) await unwrap(api.PUT("/api/v1/audio/moods/{id}", { params: { path: { id: mood.id } }, body: d }));
      else await unwrap(api.POST("/api/v1/audio/moods", { body: d }));
      toast.success(t("Mood saved"));
      done();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!mood) return;
    try {
      await unwrap(api.DELETE("/api/v1/audio/moods/{id}", { params: { path: { id: mood.id } } }));
      done();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const colorOK = /^#[0-9a-fA-F]{6}$/.test(d.color);
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={mood ? t("Edit mood") : t("New mood")}
      footer={
        <>
          {mood && (
            <Button variant="danger" className="mr-auto" onClick={() => setDeleting(true)}>
              {t("Delete")}
            </Button>
          )}
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!d.name.trim() || !colorOK} onClick={() => void save()}>
            {t("Save")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <div className="flex items-end gap-3">
          <span className="flex size-12 shrink-0 items-center justify-center rounded-xl" style={{ color: d.color, background: tint(d.color, "26") }}>
            <MoodIcon icon={d.icon} className="size-6" />
          </span>
          <Field label={t("Name")} className="flex-1">
            <Input value={d.name} onChange={(e) => patch({ name: e.target.value })} maxLength={60} />
          </Field>
        </div>
        <Field label={t("Colour")}>
          <div className="flex flex-wrap items-center gap-2">
            {moodColors.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => patch({ color: c })}
                aria-label={c}
                aria-pressed={d.color.toLowerCase() === c}
                className={clsx("size-8 rounded-full", d.color.toLowerCase() === c && "ring-2 ring-fg ring-offset-2 ring-offset-panel")}
                style={{ background: c }}
              />
            ))}
            <Input value={d.color} onChange={(e) => patch({ color: e.target.value })} className="w-28 font-mono text-xs" aria-label={t("Colour code")} />
          </div>
        </Field>
        <Field label={t("Icon")}>
          <div role="radiogroup" aria-label={t("Icon")} className="flex flex-wrap gap-1.5">
            {Object.keys(moodIcons).map((name) => (
              <button
                key={name}
                type="button"
                role="radio"
                aria-checked={d.icon === name}
                aria-label={name}
                onClick={() => patch({ icon: name })}
                className={clsx("flex size-10 items-center justify-center rounded-lg border", d.icon === name ? "border-2" : "border-border text-muted hover:text-fg")}
                style={d.icon === name ? { borderColor: d.color, color: d.color, background: tint(d.color, "1a") } : undefined}
              >
                <MoodIcon icon={name} className="size-5" />
              </button>
            ))}
          </div>
        </Field>
        <Field label={t("Genres")} help={t("Titles with these genres or tags suit the mood.")}>
          <TagInput value={d.genres} onChange={(genres) => patch({ genres })} placeholder={t("Drama")} />
        </Field>

        <div className="flex flex-col gap-3 border-t border-border pt-4">
          <h3 className="text-sm font-semibold">{t("Music · Spotify")}</h3>
          {d.spotifyUri ? (
            <div className="flex items-center gap-3 rounded-lg border border-border bg-bg p-2">
              {d.spotifyImageUrl ? <img src={d.spotifyImageUrl} alt="" className="size-12 rounded object-cover" /> : <span className="flex size-12 items-center justify-center rounded bg-border text-muted"><Music className="size-5" aria-hidden="true" /></span>}
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{d.spotifyName || d.spotifyUri}</div>
                <div className="truncate font-mono text-[11px] text-muted">{d.spotifyUri}</div>
              </div>
              <Button size="sm" variant="ghost" onClick={() => patch({ spotifyUri: "", spotifyName: "", spotifyImageUrl: "" })}>
                {t("Remove")}
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted">{t("No music yet: pick a playlist, album or track below.")}</p>
          )}
          {me.data?.connected ? (
            <SpotifyPicker value={d.spotifyUri} onPick={(it) => patch({ spotifyUri: it.uri, spotifyName: it.name, spotifyImageUrl: it.imageUrl ?? "" })} />
          ) : me.data?.available ? (
            <ConnectPrompt text={t("Connect your Spotify account to search it.")} />
          ) : (
            <p className="text-sm text-muted">{t("Add a Client ID on the Spotify tab to search Spotify.")}</p>
          )}
          <Switch checked={d.shuffle} onChange={(shuffle) => patch({ shuffle })} label={t("Shuffle")} />
        </div>
      </div>
      <Confirm
        open={deleting}
        danger
        title={t("Delete the mood?")}
        message={t("Titles that used it play no music there until someone picks another mood.")}
        confirmLabel={t("Delete")}
        onConfirm={() => void remove()}
        onClose={() => setDeleting(false)}
      />
    </Modal>
  );
}

/** MoodsTab lists the mood presets. */
function MoodsTab() {
  const { data: moods, isLoading, error } = useMoods();
  const me = useMySpotify();
  const toast = useToast();
  const [editing, setEditing] = useState<Mood | "new" | null>(null);
  const preview = async (m: Mood) => {
    try {
      await unwrap(api.PUT("/api/v1/me/spotify/play", { body: { uri: m.spotifyUri, shuffle: m.shuffle } }));
      toast.info(t("Playing on your Spotify"), m.spotifyName);
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  return (
    <div className="flex flex-col gap-5">
      <p className="max-w-3xl text-sm leading-relaxed text-muted">
        {t("Moods are ready-made presets: Spotify music with a colour and an icon. Readers pick a mood with one tap, and each title gets a mood that plays while it's read. Mangarr starts with these eight; change them or add more.")}
      </p>
      <div className="grid grid-cols-[repeat(auto-fill,minmax(16rem,1fr))] gap-3.5">
        {(moods ?? []).map((m) => (
          <div key={m.id} className="flex flex-col gap-3.5 rounded-xl border bg-panel p-4" style={{ borderColor: tint(m.color, "55"), borderTop: `4px solid ${m.color}` }}>
            <div className="flex items-center gap-3">
              <span className="flex size-12 shrink-0 items-center justify-center rounded-xl" style={{ color: m.color, background: tint(m.color, "26") }}>
                <MoodIcon icon={m.icon} className="size-6" />
              </span>
              <div className="min-w-0 flex-1">
                <div className="truncate font-semibold">{moodName(m)}</div>
                <div className="truncate text-xs text-muted">{(m.genres ?? []).join(", ")}</div>
              </div>
            </div>
            <div className="flex items-center gap-2.5">
              {m.spotifyImageUrl ? <img src={m.spotifyImageUrl} alt="" className="size-10 shrink-0 rounded object-cover" /> : <span className="flex size-10 shrink-0 items-center justify-center rounded bg-panel-2 text-muted"><Music className="size-4" aria-hidden="true" /></span>}
              <div className="min-w-0 truncate text-[13px] font-medium">{m.spotifyUri ? m.spotifyName || m.spotifyUri : <span className="font-normal text-muted">{t("No music yet")}</span>}</div>
            </div>
            <div className="mt-auto flex gap-2">
              {me.data?.connected && m.spotifyUri && (
                <Button className="flex-1" icon={<Play className="size-4" />} onClick={() => void preview(m)}>
                  {t("Preview")}
                </Button>
              )}
              <Button className="flex-1" onClick={() => setEditing(m)}>
                {t("Edit")}
              </Button>
            </div>
          </div>
        ))}
        <button type="button" onClick={() => setEditing("new")} className="flex min-h-44 items-center justify-center gap-2 rounded-xl border border-dashed border-border text-sm font-medium text-muted hover:border-muted/60 hover:text-fg">
          <Plus className="size-4" aria-hidden="true" /> {t("New mood")}
        </button>
      </div>
      {editing && <MoodEditor mood={editing === "new" ? null : editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

/** SpotifyTab is the admin's Spotify app: setup help, Client ID and who connected. */
function SpotifyTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const me = useMySpotify();
  const { data, isLoading, error } = useQuery({ queryKey: ["settings", "spotify"], queryFn: () => unwrap(api.GET("/api/v1/settings/spotify")) });
  const [clientId, setClientId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  if (isLoading) return <Loading />;
  if (error || !data) return <ErrorBox error={error} />;
  const value = clientId ?? data.clientId;
  const dirty = value !== data.clientId;
  const save = async () => {
    setSaving(true);
    try {
      qc.setQueryData(["settings", "spotify"], await unwrap(api.PUT("/api/v1/settings/spotify", { body: { clientId: value.trim() } })));
      setClientId(null);
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      toast.success(t("Settings saved"));
    } catch (e) {
      toast.fromError(e);
    } finally {
      setSaving(false);
    }
  };
  const disconnect = async () => {
    try {
      await unwrap(api.DELETE("/api/v1/me/spotify"));
      void qc.invalidateQueries({ queryKey: ["soundtrack"] });
      void qc.invalidateQueries({ queryKey: ["settings", "spotify"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  const users = data.users ?? [];
  return (
    <div className="flex flex-col gap-6">
      <Card
        title={t("Spotify")}
        actions={
          data.clientId ? <Badge tone={users.length >= data.maxUsers ? "warn" : "ok"}>{t("{count} of {max} users connected", { count: users.length, max: data.maxUsers })}</Badge> : <Badge>{t("Not set up")}</Badge>
        }
      >
        <div className="flex flex-col gap-4">
          <p className="text-sm leading-relaxed text-muted">
            {t("Music plays through each reader's own Spotify Premium account. Spotify allows up to {max} people per developer app, and each must be added by email in your Spotify dashboard.", { max: data.maxUsers })}
          </p>
          <ol className="ml-5 list-decimal text-sm leading-7">
            <li>
              {t("Create an app at")}{" "}
              <a href={dashboard} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-accent-2 hover:underline">
                {dashboard.replace("https://", "")} <ExternalLink className="size-3" aria-hidden="true" />
              </a>{" "}
              {t("(the account needs Premium) and tick Web API and Web Playback SDK.")}
            </li>
            <li>{t("Add this redirect URI to the app.")}</li>
            <li>{t("Paste the app's Client ID below.")}</li>
            <li>{t("Add each listener's Spotify email under User Management.")}</li>
          </ol>
          <Field label={t("Redirect URI")}>
            <div className="flex gap-2">
              <Input readOnly value={data.redirectUri} className="min-w-0 flex-1 font-mono text-xs" onFocus={(e) => e.target.select()} />
              <Button icon={<Copy className="size-4" />} onClick={() => void navigator.clipboard.writeText(data.redirectUri).then(() => toast.info(t("Copied")), () => toast.info(data.redirectUri))}>
                {t("Copy")}
              </Button>
            </div>
          </Field>
          {!data.secure && (
            <div className="flex gap-2.5 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2.5 text-sm">
              <AlertTriangle className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden="true" />
              <p>
                {t("Spotify sign-in needs HTTPS. Plain-HTTP servers need a reverse proxy with a certificate first, and the public URL in Settings › General set to its https address.")}{" "}
                <a href={proxyDocs} target="_blank" rel="noreferrer" className="text-accent-2 hover:underline">{t("How to set one up")}</a>
              </p>
            </div>
          )}
          <Field label={t("Client ID")}>
            <Input value={value} onChange={(e) => setClientId(e.target.value)} className="font-mono text-xs" autoComplete="off" spellCheck={false} />
          </Field>
        </div>
      </Card>

      <Card title={t("Connected people")}>
        <div className="flex flex-col gap-3">
          {users.length === 0 ? (
            <p className="text-sm text-muted">{t("Nobody connected Spotify yet. People connect from the reader's speaker button or a title's Soundtrack.")}</p>
          ) : (
            <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border">
              {users.map((u) => (
                <li key={u.userId} className="flex items-center gap-3 px-3 py-2.5 text-sm">
                  <span className="min-w-0 flex-1 truncate">
                    {u.username}
                    {u.spotifyName && <span className="text-muted"> · {u.spotifyName}</span>}
                  </span>
                  {u.premium ? (
                    <span className="inline-flex items-center gap-1 text-ok"><Check className="size-4" aria-hidden="true" /> {t("Connected")}</span>
                  ) : (
                    <span className="text-muted">{t("Not Premium: no music")}</span>
                  )}
                </li>
              ))}
            </ul>
          )}
          {data.clientId && me.data && (
            <div className="flex flex-wrap items-center gap-3 text-sm">
              <span className="min-w-0 flex-1 text-muted">{me.data.connected ? t("Your account: {name}", { name: me.data.displayName || "Spotify" }) : t("Connect your own account to search Spotify for moods.")}</span>
              {me.data.connected ? (
                <Button size="sm" variant="ghost" onClick={() => void disconnect()}>{t("Disconnect")}</Button>
              ) : (
                <Button size="sm" disabled={dirty || !data.secure} onClick={() => void connectSpotify().catch((e) => toast.fromError(e))}>{t("Connect Spotify")}</Button>
              )}
            </div>
          )}
        </div>
      </Card>
      <SaveBar dirty={dirty} saving={saving} onSave={() => void save()} onDiscard={() => setClientId(null)} />
    </div>
  );
}

/** SoundtracksPage is Settings › Soundtracks: mood presets and the Spotify app. */
export function SoundtracksPage() {
  const [search, setSearch] = useSearchParams();
  useSpotifyReturn();
  const tab: Tab = search.get("tab") === "spotify" ? "spotify" : "moods";
  return (
    <>
      <PageHeader title={t("Soundtracks")} subtitle={t("Music that plays while people read: mood presets, and the Spotify app it comes from.")} />
      <Tabs
        value={tab}
        onChange={(v) => setSearch(v === "moods" ? {} : { tab: v }, { replace: true })}
        tabs={[
          { value: "moods", label: t("Moods") },
          { value: "spotify", label: t("Spotify") },
        ]}
      />
      {tab === "moods" ? <MoodsTab /> : <SpotifyTab />}
    </>
  );
}
