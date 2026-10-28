import { useUIMode } from "../../lib/uiPreferences";
import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Check, Clock, Inbox, Languages, Link2, PlusCircle, Search, Trash2, Undo2, X } from "lucide-react";
import { api, unwrap, type LookupResult, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, ButtonLink, Card, EmptyState, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Tabs, Textarea } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { languageName, relative } from "../../lib/format";
import { LanguageSelect, useOfferedLanguages } from "../../components/LanguageSelect";
import { useToast } from "../../lib/toast";
import { useQueryParam } from "../../lib/urlState";
import { isNovel, MetadataSearch } from "../series/MetadataSearch";
import { ReadButton } from "../series/Preview";

type Request = S["Request"];
type Status = Request["status"];

const statusTone: Record<Status, "info" | "accent" | "ok" | "err"> = { pending: "info", approved: "accent", available: "ok", declined: "err" };
const statusLabel: Record<Status, string> = { pending: "Pending", approved: "Approved", available: "Available", declined: "Declined" };

function useRequests(all: boolean, status: string) {
  return useQuery({
    queryKey: ["requests", all ? "all" : "mine", status],
    queryFn: () => unwrap(api.GET("/api/v1/requests", { params: { query: { all, status: (status || undefined) as Status | undefined } } })),
  });
}

/** RequestsPage: ask for series (Jellyseerr style), and handle requests. */
export function RequestsPage() {
  const { can } = useAccount();
  const { editing } = useUIMode();
  const manager = editing && can(["requests.manage", "library.manage"]);
  const asker = can("requests.create");
  const [requestedTab, setTab] = useQueryParam("tab", manager ? "manage" : "ask");
  const tab = requestedTab === "manage" && !manager ? "ask" : requestedTab;
  return (
    <>
      <PageHeader title={t("Requests")} subtitle={t("Ask for series to be added to the library; you'll be told when they arrive.")} />
      <Tabs
        value={tab}
        onChange={setTab}
        tabs={[
          ...(manager ? [{ value: "manage", label: "To handle" }] : []),
          ...(asker ? [{ value: "ask", label: "Request a series" }] : []),
          ...(asker ? [{ value: "mine", label: "My requests" }] : []),
        ]}
      />
      {tab === "manage" && manager && <ManageTab />}
      {tab === "ask" && asker && <AskTab onDone={() => setTab("mine")} />}
      {tab === "mine" && asker && <MineTab />}
    </>
  );
}

function AskTab({ onDone }: { onDone: () => void }) {
  const [q, setQ] = useQueryParam("q");
  return (
    <Card>
      <RequestSearch query={q} setQuery={(v) => setQ(v, { replace: false })} onDone={onDone} />
    </Card>
  );
}

/**
 * RequestSearch finds titles to request with the metadata search; with
 * hideInput the query comes from a search box elsewhere (the library's).
 */
export function RequestSearch({ query, setQuery, onDone, hideInput, add }: {
  query: string;
  setQuery?: (q: string) => void;
  onDone?: () => void;
  hideInput?: boolean;
  /** add offers "Add…" (the add flow) instead of Request, for people who add titles. */
  add?: boolean;
}) {
  const [asking, setAsking] = useState<LookupResult | null>(null);
  const nav = useNavigate();
  return (
    <>
        <MetadataSearch
          query={query}
          setQuery={setQuery}
          hideInput={hideInput}
          hideErrors={hideInput}
          placeholder={t("Search for a series to request")}
          action={(r) =>
            r.existingSeriesId ? (
              <Link to={`/series/${r.existingSeriesId}`}>
                <Button size="sm" icon={<BookOpen className="size-4" />}>{t("In library")}</Button>
              </Link>
            ) : add ? (
              <span className="flex gap-2">
                <ReadButton result={r} />
                <Button size="sm" variant="primary" icon={<PlusCircle className="size-4" />} onClick={() => nav(`/add/${r.moduleId}/${encodeURIComponent(r.id)}/sources`)}>
                  {t("Add…")}
                </Button>
              </span>
            ) : r.request?.mine ? (
              <Badge tone={statusTone[r.request.status as Status] ?? "info"}>
                <Check className="size-3" />{" " + t("Requested")}</Badge>
            ) : (
              <span className="flex gap-2">
                <ReadButton result={r} />
                <Button size="sm" variant="primary" icon={<PlusCircle className="size-4" />} onClick={() => setAsking(r)} disabled={isNovel(r.format)}
                  title={isNovel(r.format) ? tr("Manga sources have no chapters of novels") : undefined}>
                  {r.request ? tr("Request too") : tr("Request")}
                </Button>
              </span>
            )
          }
        />
      {asking && <AskModal result={asking} onClose={() => setAsking(null)} onDone={onDone ?? (() => undefined)} />}
    </>
  );
}

/** AskModal asks for a title found by the metadata search, in one language. */
export function AskModal({ result, onClose, onDone }: { result: LookupResult; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  // one language, so the title isn't downloaded in every language the server has
  const offered = useOfferedLanguages();
  const [chosen, setLanguage] = useState<string | null>(null);
  const language = chosen ?? offered.defaults[0] ?? offered.languages[0] ?? "";
  const send = async () => {
    setBusy(true);
    try {
      const res = await unwrap(api.POST("/api/v1/requests", { body: { moduleId: result.moduleId, id: result.id, language: language || undefined, note: note.trim() || undefined } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      qc.invalidateQueries({ queryKey: ["lookup"] });
      toast.success(res.joined ? "Added you to the request" : "Requested", res.joined ? "Someone asked for it already; you'll be told too." : "You'll be told when it's added.");
      onClose();
      onDone();
    } catch (e) {
      toast.fromError(e, tr("Couldn't request it"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={`Request ${result.title}`}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={busy} onClick={send}>{t("Request")}</Button>
        </>
      }
    >
      <div className="flex gap-3">
        <Cover src={result.coverUrl} alt={result.title} className="aspect-[2/3] w-20 shrink-0" />
        <div className="min-w-0 text-sm">
          <div className="font-medium">{result.title}</div>
          <div className="text-muted">{[result.year, result.format].filter(Boolean).join(" · ")}</div>
          {result.request && <p className="mt-2 text-muted">{t("Someone asked for this already; you'll be added to their request.")}</p>}
        </div>
      </div>
      {offered.languages.length > 0 && (
        <Field label={t("Language")} className="mt-4">
          <LanguageSelect offered value={language} onChange={setLanguage} />
        </Field>
      )}
      <Field label={t("Note (optional)")} className="mt-4">
        <Textarea value={note} onChange={(e) => setNote(e.target.value)} maxLength={500} placeholder={t("e.g. the official English translation")} />
      </Field>
    </Modal>
  );
}

function MineTab() {
  const { data, isLoading, error } = useRequests(false, "");
  const qc = useQueryClient();
  const toast = useToast();
  const withdraw = async (r: Request) => {
    try {
      await unwrap(api.DELETE("/api/v1/requests/{id}/mine", { params: { path: { id: r.id } } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  if (isLoading) return <Loading />;
  if (error) return <ErrorBox error={error} />;
  if (!data?.length)
    return (
      <EmptyState title={t("No requests yet")} icon={<Inbox className="size-8" />}>{t("Find a series under Request a series.")}</EmptyState>
    );
  return (
    <div className="flex flex-col gap-2">
      {data.map((r) => (
        <RequestRow
          key={r.id}
          r={r}
          actions={
            r.status === "pending" && (
              <Button size="sm" icon={<Undo2 className="size-4" />} onClick={() => withdraw(r)}>{t("Withdraw")}</Button>
            )
          }
        />
      ))}
    </div>
  );
}

type Kind = "title" | "language" | "monitor" | "import";
const kindOf = (r: Request): Kind => (r.kind === "monitor" ? "monitor" : r.kind === "import" ? "import" : r.workId ? "language" : "title");

/** sorters for the manager's list; the server answers newest first */
const sorters: Record<string, (a: Request, b: Request) => number> = {
  oldest: (a, b) => a.createdAt.localeCompare(b.createdAt) || a.id - b.id,
  asked: (a, b) => b.count - a.count,
  title: (a, b) => a.title.localeCompare(b.title),
  year: (a, b) => (b.metadata.year ?? 0) - (a.metadata.year ?? 0),
};

/** counted lists the values of a field with how many requests have each, most first. */
function counted(list: Request[], keys: (r: Request) => string[]) {
  const n = new Map<string, number>();
  for (const r of list) for (const k of keys(r)) n.set(k, (n.get(k) ?? 0) + 1);
  return [...n.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
}

function ManageTab() {
  const { isAdmin } = useAccount();
  const [status, setStatus] = useQueryParam("status", "pending");
  const [q, setQ] = useQueryParam("q");
  const [kind, setKind] = useQueryParam("type");
  const [by, setBy] = useQueryParam("by");
  const [lang, setLang] = useQueryParam("lang");
  const [sort, setSort] = useQueryParam("sort");
  const { data, isLoading, error } = useRequests(true, status === "all" ? "" : status);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [bulk, setBulk] = useState<"decline" | "language" | null>(null);
  const [bulkBusy, setBulkBusy] = useState(false);
  const all = data ?? [];
  const words = norm(q).split(" ").filter(Boolean);
  const shown = all
    .filter((r) => !kind || kindOf(r) === kind)
    .filter((r) => !by || r.requesters.some((x) => String(x.userId) === by))
    .filter((r) => !lang || (r.language || "-") === lang)
    .filter((r) => words.every((w) => norm([r.title, r.seriesTitle ?? "", ...(r.metadata.altTitles ?? [])].join(" ")).includes(w)));
  if (sorters[sort]) shown.sort(sorters[sort]);
  const people = counted(all, (r) => r.requesters.map((x) => String(x.userId)));
  const nameOf = (id: string) => all.flatMap((r) => r.requesters).find((x) => String(x.userId) === id)?.name ?? id;
  const langs = counted(all, (r) => [r.language || "-"]);
  const kinds = counted(all, (r) => [kindOf(r)]);
  const picked = shown.filter((r) => selected.has(r.id));
  const toggle = (id: number, on: boolean) => {
    const next = new Set(selected);
    if (on) next.add(id);
    else next.delete(id);
    setSelected(next);
  };
  const runBulk = async (action: "add" | "decline" | "language" | "delete", extra: { reason?: string; language?: string } = {}) => {
    const ids = (action === "add" ? addable : picked.filter((r) => (action === "delete" ? r.status !== "pending" : r.status === "pending"))).map((r) => r.id);
    if (!ids.length) return;
    setBulkBusy(true);
    try {
      const res = await unwrap(api.POST("/api/v1/requests/bulk", { body: { ids, action, ...extra } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      setSelected(new Set());
      setBulk(null);
      if (action === "add" && res.importId) nav(`/import/${res.importId}`);
      else toast.success(tr("{count} requests changed", { count: res.changed }));
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBulkBusy(false);
    }
  };
  const pending = picked.filter((r) => r.status === "pending").length;
  // titles and downloads waiting for a manager, not ones already in an import
  const addable = picked.filter((r) => r.status === "pending" && r.kind !== "import" && !r.importId);
  const handled = picked.length - pending;
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const [declining, setDeclining] = useState<Request | null>(null);
  const [linking, setLinking] = useState<Request | null>(null);
  const [monitoring, setMonitoring] = useState(0);
  const monitor = async (r: Request) => {
    setMonitoring(r.id);
    try {
      await unwrap(api.POST("/api/v1/requests/{id}/monitor", { params: { path: { id: r.id } } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      qc.invalidateQueries({ queryKey: ["series"] });
      toast.success(tr("Downloading"), tr("The requesters were told."));
    } catch (e) {
      toast.fromError(e);
    } finally {
      setMonitoring(0);
    }
  };
  const remove = async (r: Request) => {
    try {
      await unwrap(api.DELETE("/api/v1/requests/{id}", { params: { path: { id: r.id } } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <>
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative w-full sm:w-64">
          <Search className="absolute left-2.5 top-2.5 size-4 text-muted" />
          <Input className="pl-8" placeholder={t("Filter by title…")} defaultValue={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Select aria-label={t("Status")} value={status} onChange={(e) => (setStatus(e.target.value), setSelected(new Set()))} className="w-auto">
          <option value="pending">{t("Pending")}</option>
          <option value="approved">{t("Approved")}</option>
          <option value="available">{t("Available")}</option>
          <option value="declined">{t("Declined")}</option>
          <option value="all">{t("All")}</option>
        </Select>
        {(kinds.length > 1 || kind) && (
          <Select aria-label={t("Type")} value={kind} onChange={(e) => setKind(e.target.value)} className="w-auto">
            <option value="">{t("Type: any")}</option>
            {kinds.map(([k, n]) => (
              <option key={k} value={k}>
                {(k === "monitor" ? tr("Download (in library)") : k === "language" ? tr("Another language") : k === "import" ? tr("Library import") : tr("New title")) + ` (${n})`}
              </option>
            ))}
          </Select>
        )}
        {(people.length > 1 || by) && (
          <Select aria-label={t("Asked by")} value={by} onChange={(e) => setBy(e.target.value)} className="w-auto">
            <option value="">{t("Asked by: anyone")}</option>
            {people.map(([id, n]) => (
              <option key={id} value={id}>
                {nameOf(id)} ({n})
              </option>
            ))}
          </Select>
        )}
        {(langs.length > 1 || lang) && (
          <Select aria-label={t("Language")} value={lang} onChange={(e) => setLang(e.target.value)} className="w-auto">
            <option value="">{t("Language: any")}</option>
            {langs.map(([l, n]) => (
              <option key={l} value={l}>
                {(l === "-" ? tr("Default languages") : languageName(l)) + ` (${n})`}
              </option>
            ))}
          </Select>
        )}
        <Select aria-label={t("Sort")} value={sort} onChange={(e) => setSort(e.target.value)} className="w-auto">
          <option value="">{t("Sort: newest")}</option>
          <option value="oldest">{t("Sort: oldest")}</option>
          <option value="asked">{t("Sort: most asked for")}</option>
          <option value="title">{t("Sort: title A–Z")}</option>
          <option value="year">{t("Sort: year")}</option>
        </Select>
      </div>
      {shown.length > 0 && (
        <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-muted">
          <input
            type="checkbox"
            aria-label={t("Select all shown")}
            className="size-4 accent-primary"
            checked={picked.length > 0 && picked.length === shown.length}
            ref={(el) => {
              if (el) el.indeterminate = picked.length > 0 && picked.length < shown.length;
            }}
            onChange={(e) => setSelected(e.target.checked ? new Set(shown.map((r) => r.id)) : new Set())}
          />
          <span>{picked.length ? t("{count} selected", { count: picked.length }) : t("{count} requests", { count: shown.length })}</span>
          {picked.length > 0 && picked.length < shown.length && (
            <button type="button" className="text-accent-2 hover:underline" onClick={() => setSelected(new Set(shown.map((r) => r.id)))}>
              {t("Select all {count} matching", { count: shown.length })}
            </button>
          )}
          {picked.length > 0 && (
            <button type="button" className="text-accent-2 hover:underline" onClick={() => setSelected(new Set())}>
              {t("Clear")}
            </button>
          )}
        </div>
      )}
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && data.length === 0 && (
        <EmptyState title={status === "pending" ? tr("Nothing to handle") : tr("No requests")} icon={<Inbox className="size-8" />}>
          {status === "pending" && tr("New requests show up here.")}
        </EmptyState>
      )}
      <div className="flex flex-col gap-2">
        {data && data.length > 0 && shown.length === 0 && <p className="text-sm text-muted">{t("No requests match.")}</p>}
        {shown.map((r) => (
          <RequestRow
            key={r.id}
            r={r}
            showRequesters
            selected={selected.has(r.id)}
            onSelect={(on) => toggle(r.id, on)}
            actions={
              <>
                {r.kind === "import" ? (
                  r.importId && (
                    <ButtonLink size="sm" variant="primary" to={`/import/${r.importId}`}>{t("Review in Import →")}</ButtonLink>
                  )
                ) : r.kind === "monitor" ? (
                  (r.status === "pending" || r.status === "declined") && (
                    <Button size="sm" variant="primary" icon={<PlusCircle className="size-4" />} loading={monitoring === r.id} onClick={() => void monitor(r)}>{t("Monitor and download")}</Button>
                  )
                ) : (r.status === "pending" || r.status === "declined") && r.editionOf && r.language ? (
                  <Button
                    size="sm"
                    variant="primary"
                    icon={<PlusCircle className="size-4" />}
                    onClick={() => nav(`/series/${r.editionOf}?addLanguage=${encodeURIComponent(r.language!)}&request=${r.id}`)}
                  >{t("Add language")}</Button>
                ) : (r.status === "pending" || r.status === "declined") && r.metadata.moduleId && r.metadata.id && (
                  <Button
                    size="sm"
                    variant="primary"
                    icon={<PlusCircle className="size-4" />}
                    onClick={() => nav(`/add/${r.metadata.moduleId}/${encodeURIComponent(r.metadata.id!)}/sources?request=${r.id}${r.language ? `&lang=${encodeURIComponent(r.language)}` : ""}`)}
                  >{t("Add series")}</Button>
                )}
                {(r.status === "pending" || r.status === "declined") && r.kind !== "monitor" && r.kind !== "import" && (
                  <Button size="sm" icon={<Link2 className="size-4" />} onClick={() => setLinking(r)}>{t("Link")}</Button>
                )}
                {r.status === "pending" && (
                  <Button size="sm" icon={<X className="size-4" />} onClick={() => setDeclining(r)}>{t("Decline")}</Button>
                )}
                {r.status !== "pending" && (
                  <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} onClick={() => remove(r)} title={t("Delete the request")}>{t("Delete")}</Button>
                )}
              </>
            }
          />
        ))}
      </div>
      {picked.length > 0 && (
        <div className="sticky bottom-[calc(0.75rem+var(--tabbar-h))] z-20 mt-3 flex flex-wrap items-center gap-2 rounded-lg border border-accent bg-panel-2 px-3 py-2 shadow-lg">
          <span className="mr-1 text-sm font-semibold">{t("{count} selected", { count: picked.length })}</span>
          {pending > 0 && (
            <>
              {isAdmin && addable.length > 0 && (
                <Button size="sm" variant="primary" icon={<Search className="size-4" />} loading={bulkBusy} onClick={() => void runBulk("add")}>
                  {t("Find sources and add {count}…", { count: addable.length })}
                </Button>
              )}
              <Button size="sm" icon={<X className="size-4" />} disabled={bulkBusy} onClick={() => setBulk("decline")}>{t("Decline…")}</Button>
              <Button size="sm" icon={<Languages className="size-4" />} disabled={bulkBusy} onClick={() => setBulk("language")}>{t("Language…")}</Button>
            </>
          )}
          {handled > 0 && (
            <Button size="sm" variant="ghost" icon={<Trash2 className="size-4" />} disabled={bulkBusy} onClick={() => void runBulk("delete")}>
              {t("Delete {count}", { count: handled })}
            </Button>
          )}
        </div>
      )}
      {bulk === "decline" && <DeclineModal title={t("Decline {count} requests", { count: pending })} busy={bulkBusy} onClose={() => setBulk(null)} onDecline={(reason) => runBulk("decline", { reason })} />}
      {bulk === "language" && <LanguageModal count={pending} busy={bulkBusy} onClose={() => setBulk(null)} onSave={(language) => runBulk("language", { language })} />}
      {declining && (
        <DeclineModal
          title={`Decline ${declining.title}`}
          onClose={() => setDeclining(null)}
          onDecline={async (reason) => {
            try {
              await unwrap(api.POST("/api/v1/requests/{id}/decline", { params: { path: { id: declining.id } }, body: { reason: reason || undefined } }));
              qc.invalidateQueries({ queryKey: ["requests"] });
              setDeclining(null);
            } catch (e) {
              toast.fromError(e);
            }
          }}
        />
      )}
      {linking && <LinkModal r={linking} onClose={() => setLinking(null)} />}
    </>
  );
}

function RequestRow({ r, actions, showRequesters, selected, onSelect }: {
  r: Request;
  actions?: React.ReactNode;
  showRequesters?: boolean;
  selected?: boolean;
  onSelect?: (on: boolean) => void;
}) {
  const md = r.metadata;
  return (
    <div className={`flex flex-wrap gap-3 rounded-lg border bg-panel p-3 sm:flex-nowrap ${selected ? "border-accent/60" : "border-border"}`}>
      {onSelect && (
        <input type="checkbox" className="size-4 shrink-0 self-center accent-primary" aria-label={`${t("Select")} ${r.title}`} checked={!!selected} onChange={(e) => onSelect(e.target.checked)} />
      )}
      <Cover src={md.coverUrl} alt={r.title} className="aspect-[2/3] w-14 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          {r.seriesId ? (
            <Link to={`/series/${r.seriesId}`} className="font-medium hover:text-accent-2">
              {r.seriesTitle || r.title}
            </Link>
          ) : (
            <span className="font-medium">{r.title}</span>
          )}
          {md.year ? <Badge>{md.year}</Badge> : null}
          {md.format && <Badge>{md.format}</Badge>}
          {r.kind === "monitor" && <Badge tone="accent">{t("Download")}</Badge>}
          {r.import && <Badge tone="accent">{t("Import")}</Badge>}
          {!r.import && r.importId && r.status === "pending" && (
            <Link to={`/import/${r.importId}`}>
              <Badge tone="accent">{t("in review")}</Badge>
            </Link>
          )}
          {r.import && <Badge>{r.import.imported > 0 ? t("{done} of {count} added", { done: r.import.imported, count: r.import.titles }) : t("{count} titles", { count: r.import.titles })}</Badge>}
          {r.language && <Badge tone="info">{languageName(r.language)}</Badge>}
          <Badge tone={statusTone[r.status]}>{statusLabel[r.status]}</Badge>
          {r.count > 1 && <Badge title={t("People who asked for it")}>{r.count}{" " + t("people")}</Badge>}
        </div>
        <div className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-muted">
          <span className="flex items-center gap-1">
            <Clock className="size-3" />{" " + t("asked") + " "}{relative(r.createdAt)}
            {showRequesters && r.requesters.length > 0 && <>{" " + t("by") + " "}{r.requesters.map((x) => x.name).join(", ")}</>}
          </span>
          {r.handledAt && r.status !== "pending" && (
            <span>
              {r.status === "declined" ? tr("declined") : tr("added")} {relative(r.handledAt)}
              {r.handledBy && ` by ${r.handledBy}`}
            </span>
          )}
        </div>
        {r.import && Object.keys(r.import.lists).length > 0 && (
          <div className="mt-0.5 text-xs text-muted">
            {Object.entries(r.import.lists)
              .sort((a, b) => b[1] - a[1])
              .map(([name, n]) => `${name} ${n}`)
              .join(" · ")}
          </div>
        )}
        {r.requesters
          .filter((x) => x.note)
          .map((x) => (
            <p key={x.userId} className="mt-1 text-sm text-fg/80">
              “{x.note}”{showRequesters && <span className="text-muted"> — {x.name}</span>}
            </p>
          ))}
        {r.reason && (
          <p className="mt-1 text-sm text-err">
            {t(r.status === "declined" ? "Declined:" : "Last add attempt:") + " "}{r.reason}
          </p>
        )}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2 self-center">{actions}</div>}
    </div>
  );
}

function DeclineModal({ title, busy, onClose, onDecline }: { title: string; busy?: boolean; onClose: () => void; onDecline: (reason: string) => Promise<void> | void }) {
  const [reason, setReason] = useState("");
  const [sending, setSending] = useState(false);
  const decline = async () => {
    setSending(true);
    try {
      await onDecline(reason.trim());
    } finally {
      setSending(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="danger" loading={busy || sending} onClick={() => void decline()}>{t("Decline")}</Button>
        </>
      }
    >
      <Field label={t("Reason (the requesters see it)")}>
        <Input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} placeholder={t("e.g. no source has it")} />
      </Field>
    </Modal>
  );
}

/** LanguageModal picks the language selected requests ask for. */
function LanguageModal({ count, busy, onClose, onSave }: { count: number; busy: boolean; onClose: () => void; onSave: (language: string) => void }) {
  const [language, setLanguage] = useState("");
  return (
    <Modal
      open
      onClose={onClose}
      title={t("Language of {count} requests", { count })}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={busy} onClick={() => onSave(language)}>{t("Save")}</Button>
        </>
      }
    >
      <Field label={t("Language")} help={t("Download requests keep theirs.")}>
        <LanguageSelect offered value={language} onChange={setLanguage} placeholder={t("The server's default languages")} />
      </Field>
    </Modal>
  );
}

/** norm compares titles by their letters and digits ("Journey’s" = "Journey's"). */
const norm = (s: string) =>
  s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/['’`]/g, "")
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim();

function LinkModal({ r, onClose }: { r: Request; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [filter, setFilter] = useState(r.title);
  const [busy, setBusy] = useState(false);
  const { data: series } = useQuery({ queryKey: ["series", "pick"], queryFn: () => unwrap(api.GET("/api/v1/series")) });
  const words = norm(filter).split(" ").filter(Boolean);
  const list = (series ?? []).filter((s) => words.every((w) => norm(s.title).includes(w))).slice(0, 30);
  const link = async (seriesId: number) => {
    setBusy(true);
    try {
      await unwrap(api.POST("/api/v1/requests/{id}/link", { params: { path: { id: r.id } }, body: { seriesId } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      toast.success(tr("Linked"), tr("The requesters were told."));
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal open onClose={onClose} title={`Link ${r.title} to a series`}>
      <p className="mb-3 text-sm text-muted">{t("Already in the library under another name? Pick it; the request is marked as added.")}</p>
      <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={t("Filter series")} autoFocus />
      <div className="mt-3 flex flex-col gap-1">
        {list.map((s) => (
          <button
            key={s.id}
            type="button"
            disabled={busy}
            onClick={() => link(s.id)}
            className="flex items-center justify-between rounded-md px-3 py-2 text-left text-sm hover:bg-panel-2 disabled:opacity-50"
          >
            <span className="truncate">{s.title}</span>
            <span className="text-xs text-muted">{s.metadata.year || ""}</span>
          </button>
        ))}
        {series && list.length === 0 && <p className="text-sm text-muted">{t("No series match.")}</p>}
      </div>
    </Modal>
  );
}
