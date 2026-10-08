import { useUIMode } from "../../lib/uiPreferences";
import { t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { BookOpen, Check, Clock, Inbox, Link2, PlusCircle, Trash2, Undo2, X } from "lucide-react";
import { api, unwrap, type LookupResult, type S } from "../../api/client";
import { Cover } from "../../components/Cover";
import { Badge, Button, Card, EmptyState, ErrorBox, Field, Input, Loading, Modal, PageHeader, Select, Tabs, Textarea } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { languageName, relative } from "../../lib/format";
import { LanguageSelect, useOfferedLanguages } from "../../components/LanguageSelect";
import { useToast } from "../../lib/toast";
import { useQueryParam } from "../../lib/urlState";
import { MetadataSearch } from "../series/AddSeries";
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
export function RequestSearch({ query, setQuery, onDone, hideInput }: { query: string; setQuery?: (q: string) => void; onDone?: () => void; hideInput?: boolean }) {
  const [asking, setAsking] = useState<LookupResult | null>(null);
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
            ) : r.request?.mine ? (
              <Badge tone={statusTone[r.request.status as Status] ?? "info"}>
                <Check className="size-3" />{" " + t("Requested")}</Badge>
            ) : (
              <span className="flex gap-2">
                <ReadButton result={r} />
                <Button size="sm" variant="primary" icon={<PlusCircle className="size-4" />} onClick={() => setAsking(r)}>
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

function AskModal({ result, onClose, onDone }: { result: LookupResult; onClose: () => void; onDone: () => void }) {
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

function ManageTab() {
  const [status, setStatus] = useQueryParam("status", "pending");
  const { data, isLoading, error } = useRequests(true, status === "all" ? "" : status);
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const [declining, setDeclining] = useState<Request | null>(null);
  const [linking, setLinking] = useState<Request | null>(null);
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
      <div className="mb-3 flex items-center gap-2">
        <Select value={status} onChange={(e) => setStatus(e.target.value)} className="w-44">
          <option value="pending">{t("Pending")}</option>
          <option value="approved">{t("Approved")}</option>
          <option value="available">{t("Available")}</option>
          <option value="declined">{t("Declined")}</option>
          <option value="all">{t("All")}</option>
        </Select>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && data.length === 0 && (
        <EmptyState title={status === "pending" ? tr("Nothing to handle") : tr("No requests")} icon={<Inbox className="size-8" />}>
          {status === "pending" && tr("New requests show up here.")}
        </EmptyState>
      )}
      <div className="flex flex-col gap-2">
        {data?.map((r) => (
          <RequestRow
            key={r.id}
            r={r}
            showRequesters
            actions={
              <>
                {(r.status === "pending" || r.status === "declined") && r.editionOf && r.language ? (
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
                {(r.status === "pending" || r.status === "declined") && (
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
      {declining && <DeclineModal r={declining} onClose={() => setDeclining(null)} />}
      {linking && <LinkModal r={linking} onClose={() => setLinking(null)} />}
    </>
  );
}

function RequestRow({ r, actions, showRequesters }: { r: Request; actions?: React.ReactNode; showRequesters?: boolean }) {
  const md = r.metadata;
  return (
    <div className="flex flex-wrap gap-3 rounded-lg border border-border bg-panel p-3 sm:flex-nowrap">
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

function DeclineModal({ r, onClose }: { r: Request; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const decline = async () => {
    setBusy(true);
    try {
      await unwrap(api.POST("/api/v1/requests/{id}/decline", { params: { path: { id: r.id } }, body: { reason: reason.trim() || undefined } }));
      qc.invalidateQueries({ queryKey: ["requests"] });
      onClose();
    } catch (e) {
      toast.fromError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={`Decline ${r.title}`}
      size="sm"
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="danger" loading={busy} onClick={decline}>{t("Decline")}</Button>
        </>
      }
    >
      <Field label={t("Reason (the requesters see it)")}>
        <Input value={reason} onChange={(e) => setReason(e.target.value)} maxLength={500} placeholder={t("e.g. no source has it")} />
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
