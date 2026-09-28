import { t } from "../../lib/i18n/core";
import { useEffect, useId, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Play, Plus, X } from "lucide-react";
import { Link } from "react-router";
import clsx from "clsx";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, ErrorBox, IconButton, Input, Modal, Select, Spinner, Switch } from "../../components/ui";
import { useToast } from "../../lib/toast";
import {
  WEEKDAYS,
  allDays,
  runDuration,
  runTime,
  sameSchedule,
  scheduleProblem,
  scheduleText,
  splitInterval,
  toMinutes,
  triggerText,
  weekdayName,
  type IntervalUnit,
  type TaskSchedule,
} from "./taskSchedule";

type Task = S["TaskInfo"];
type Command = S["Command"];

type Draft = { kind: "interval" | "daily"; value: number; unit: IntervalUnit; times: string[]; days: string[] };

function draftOf(s?: TaskSchedule | null): Draft {
  const interval = splitInterval(s?.kind === "interval" ? (s.intervalMinutes ?? 60) : 1440);
  if (s?.kind === "daily") return { kind: "daily", ...interval, times: s.timesOfDay?.length ? [...s.timesOfDay] : ["03:30"], days: allDays(s.weekdays) ? [] : [...(s.weekdays ?? [])] };
  return { kind: "interval", ...interval, times: ["03:30"], days: [] };
}

function scheduleOf(d: Draft): TaskSchedule {
  if (d.kind === "interval") return { kind: "interval", intervalMinutes: toMinutes(d.value, d.unit) };
  return { kind: "daily", timesOfDay: d.times, weekdays: allDays(d.days) ? [] : WEEKDAYS.filter((x) => d.days.includes(x)) };
}

function KindOption({ checked, onSelect, title, help, children }: { checked: boolean; onSelect: () => void; title: string; help: string; children: React.ReactNode }) {
  return (
    <div className={clsx("flex gap-3 rounded-lg border p-3", checked ? "border-primary bg-accent/5" : "border-border")}>
      <input type="radio" name="schedule-kind" checked={checked} onChange={onSelect} aria-label={title} className="mt-0.5 size-4 accent-accent" />
      <div className="flex min-w-0 flex-1 flex-col gap-2.5">
        <button type="button" className="text-left" onClick={onSelect}>
          <div className="text-sm font-medium">{title}</div>
          <div className="text-xs text-muted">{help}</div>
        </button>
        <fieldset disabled={!checked} className={clsx("flex flex-col gap-2", !checked && "opacity-50")}>
          {children}
        </fieldset>
      </div>
    </div>
  );
}

/** EditScheduleModal changes when a scheduled task runs, or pauses it. */
export function EditScheduleModal({ task, onClose }: { task: Task; onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [draft, setDraft] = useState<Draft>(() => draftOf(task.schedule));
  const [enabled, setEnabled] = useState(!task.paused);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const schedule = scheduleOf(draft);
  const problem = scheduleProblem(schedule, task.minIntervalMinutes || 1);
  const patch = (p: Partial<Draft>) => setDraft((d) => ({ ...d, ...p }));

  const preview = useQuery({
    queryKey: ["tasks", "preview", task.name, schedule],
    queryFn: () => unwrap(api.POST("/api/v1/system/tasks/preview", { body: { ...schedule, name: task.name } })),
    enabled: !problem,
    staleTime: 30_000,
  });

  const finish = () => {
    qc.invalidateQueries({ queryKey: ["tasks"] });
    onClose();
  };
  const path = { params: { path: { name: task.name } } };
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (!sameSchedule(schedule, task.schedule)) {
        if (sameSchedule(schedule, task.defaultSchedule)) await unwrap(api.DELETE("/api/v1/system/tasks/{name}/schedule", path));
        else await unwrap(api.PUT("/api/v1/system/tasks/{name}/schedule", { ...path, body: schedule }));
      }
      if (enabled === task.paused) await unwrap(enabled ? api.POST("/api/v1/system/tasks/{name}/resume", path) : api.POST("/api/v1/system/tasks/{name}/pause", path));
      toast.success(t("Schedule saved"));
      finish();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  const reset = async () => {
    setSaving(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/api/v1/system/tasks/{name}/schedule", path));
      toast.success(t("Schedule reset"));
      finish();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  const units: { value: IntervalUnit; label: string }[] = [
    { value: "minute", label: t("minutes") },
    { value: "hour", label: t("hours") },
    { value: "day", label: t("days") },
  ];

  return (
    <Modal
      open
      onClose={onClose}
      title={t("{task} schedule", { task: task.name })}
      footer={
        <div className="flex w-full flex-wrap items-center gap-2">
          {task.custom && (
            <Button variant="ghost" disabled={saving} onClick={reset}>
              {t("Reset to default ({schedule})", { schedule: scheduleText(task.defaultSchedule).toLocaleLowerCase() })}
            </Button>
          )}
          <span className="flex-1" />
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!!problem} onClick={save}>{t("Save")}</Button>
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <p className="min-w-0 flex-1 text-sm text-muted">{task.description}</p>
          <Switch checked={enabled} onChange={setEnabled} label={t("Run on schedule")} />
        </div>

        <div className="flex flex-col gap-2">
          <div className="text-sm font-medium">{t("Repeat")}</div>
          <KindOption checked={draft.kind === "interval"} onSelect={() => patch({ kind: "interval" })} title={t("Every set amount of time")} help={t("Counted from when the last run finished.")}>
            <div className="flex gap-2">
              <Input
                type="number"
                min={1}
                aria-label={t("Every")}
                className="w-24"
                value={Number.isFinite(draft.value) ? draft.value : ""}
                onChange={(e) => patch({ value: e.target.valueAsNumber })}
              />
              <Select aria-label={t("Unit")} className="w-32" value={draft.unit} onChange={(e) => patch({ unit: e.target.value as IntervalUnit })}>
                {units.map((u) => (
                  <option key={u.value} value={u.value}>{u.label}</option>
                ))}
              </Select>
            </div>
          </KindOption>
          <KindOption checked={draft.kind === "daily"} onSelect={() => patch({ kind: "daily" })} title={t("At a time of day")} help={t("Good for heavy work you want at night.")}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-10 text-sm text-muted">{t("At")}</span>
              {draft.times.map((time, i) => (
                <span key={i} className="inline-flex items-center gap-1">
                  <Input type="time" aria-label={t("Time")} className="w-28" value={time} onChange={(e) => patch({ times: draft.times.map((x, j) => (j === i ? e.target.value : x)) })} />
                  {draft.times.length > 1 && (
                    <IconButton title={t("Remove time")} onClick={() => patch({ times: draft.times.filter((_, j) => j !== i) })}>
                      <X className="size-3.5" />
                    </IconButton>
                  )}
                </span>
              ))}
              <Button size="sm" variant="ghost" icon={<Plus className="size-3.5" />} onClick={() => patch({ times: [...draft.times, "12:00"] })}>{t("Add time")}</Button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <span className="w-10 text-sm text-muted">{t("On")}</span>
              <div className="flex flex-wrap gap-1">
                {WEEKDAYS.map((d) => {
                  const on = draft.days.length === 0 || draft.days.includes(d);
                  const pick = () => {
                    const current = draft.days.length === 0 ? [...WEEKDAYS] : draft.days;
                    const next = on ? current.filter((x) => x !== d) : [...current, d];
                    patch({ days: next.length === 0 || allDays(next) ? [] : next });
                  };
                  return (
                    <button
                      key={d}
                      type="button"
                      aria-pressed={on}
                      onClick={pick}
                      className={clsx("h-7 min-w-10 rounded border px-2 text-xs", on ? "border-accent bg-accent/15 text-fg" : "border-border text-muted")}
                    >
                      {weekdayName(d)}
                    </button>
                  );
                })}
              </div>
              {draft.days.length === 0 && <span className="text-xs text-muted">{t("every day")}</span>}
            </div>
          </KindOption>
        </div>

        {problem && <p className="text-sm text-err">{problem}</p>}

        <div className="flex flex-col gap-2 rounded-lg border border-border bg-bg px-3.5 py-3">
          <div className="text-xs font-semibold uppercase tracking-wide text-muted">{t("Next runs")} · {task.timezone || t("server time")}</div>
          <div className="flex min-h-7 flex-wrap gap-2 text-sm">
            {problem ? (
              <span className="text-muted">—</span>
            ) : preview.isLoading ? (
              <Spinner className="size-4" />
            ) : (
              preview.data?.nextRuns.map((r) => (
                <span key={r} className="rounded bg-panel-2 px-2 py-1">{runTime(r, task.timezone)}</span>
              ))
            )}
          </div>
          {preview.error && <ErrorBox error={preview.error} />}
          <p className="text-xs text-muted">{t("If Mangarr is off at that time, the task runs once soon after it starts.")}</p>
        </div>
        {error != null && <ErrorBox error={error} />}
      </div>
    </Modal>
  );
}

function median(xs: number[]): number {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  const mid = Math.floor(s.length / 2);
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2;
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-border px-3 py-2.5">
      <div className="text-xs font-semibold uppercase tracking-wide text-muted">{label}</div>
      <div className="mt-1 text-base font-semibold">{value}</div>
    </div>
  );
}

function RunRow({ run, zone }: { run: Command; zone: string }) {
  const failed = run.status === "failed" || run.status === "orphaned";
  const active = run.status === "queued" || run.status === "started";
  return (
    <li className={clsx("flex flex-col gap-1.5 rounded-lg border px-3 py-2.5", failed ? "border-err/40 bg-err/5" : "border-border")}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        <span className={clsx("size-2 shrink-0 rounded-full", failed ? "bg-err" : active ? "bg-info" : "bg-ok")} />
        <span className="font-medium" title={run.startedAt ?? run.queuedAt}>{runTime(run.startedAt ?? run.queuedAt, zone)}</span>
        <Badge>{triggerText(run.trigger)}</Badge>
        <span className="flex-1" />
        <span className="text-muted">{active ? (run.status === "queued" ? t("Queued") : t("Running")) : runDuration(run.durationMs)}</span>
      </div>
      {(run.error || run.message) && <div className={clsx("pl-5 text-xs break-words", failed ? "text-err" : "text-muted")}>{failed ? run.error || run.message : run.message}</div>}
      {failed && (
        <Link to="/system/logs" className="pl-5 text-xs text-accent-2 hover:underline">{t("Open in Logs")}</Link>
      )}
    </li>
  );
}

/** TaskHistoryDrawer lists a task's recent runs from the command history. */
export function TaskHistoryDrawer({ task, onClose, onEdit, onRun }: { task: Task; onClose: () => void; onEdit: () => void; onRun: () => void }) {
  const titleId = useId();
  const { data, isLoading, error } = useQuery({
    queryKey: ["commands", "task", task.name],
    queryFn: () => unwrap(api.GET("/api/v1/commands", { params: { query: { name: task.name, limit: 20 } } })),
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const runs = data ?? [];
  const ended = runs.filter((r) => r.endedAt);
  const ok = ended.filter((r) => r.status === "completed");
  const lastOk = ok[0];
  return (
    <div className="fixed inset-0 z-50 bg-black/50" onMouseDown={onClose}>
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onMouseDown={(e) => e.stopPropagation()}
        className="absolute inset-y-0 right-0 flex w-full max-w-xl flex-col border-l border-border bg-panel shadow-2xl"
      >
        <header className="flex flex-col gap-3 border-b border-border px-5 py-4">
          <div className="flex items-start gap-3">
            <div className="min-w-0 flex-1">
              <h2 id={titleId} className="font-semibold">{task.name}</h2>
              <p className="mt-0.5 text-sm text-muted">{task.description}</p>
            </div>
            <IconButton title={t("Close")} onClick={onClose}>
              <X className="size-4" />
            </IconButton>
          </div>
          {task.scheduled && (
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span>{scheduleText(task.schedule)}</span>
              {task.custom && <Badge tone="info">{t("Custom")}</Badge>}
              <span className="text-muted">· {task.paused ? t("Paused") : t("Next run: {time}", { time: runTime(task.nextRuns?.[0], task.timezone) })}</span>
              <span className="flex-1" />
              <Button size="sm" onClick={onEdit}>{t("Edit schedule")}</Button>
              <Button size="sm" variant="primary" icon={<Play className="size-3.5" />} disabled={!!task.running} onClick={onRun}>
                {task.running ? t("Running") : t("Run now")}
              </Button>
            </div>
          )}
        </header>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4">
          {isLoading && <Spinner />}
          {error && <ErrorBox error={error} />}
          {data && (
            <>
              <div className="grid grid-cols-1 gap-2.5 sm:grid-cols-3">
                <Stat label={ended.length === 1 ? t("Last run") : t("Last {count} runs", { count: ended.length })} value={t("{ok} ok · {failed} failed", { ok: ok.length, failed: ended.length - ok.length })} />
                <Stat label={t("Typical run")} value={ended.length ? runDuration(median(ended.map((r) => r.durationMs))) : "—"} />
                <Stat label={t("Last success")} value={lastOk ? runTime(lastOk.endedAt, task.timezone) : "—"} />
              </div>
              <div className="text-xs font-semibold uppercase tracking-wide text-muted">{t("Recent runs")}</div>
              {runs.length === 0 ? (
                <p className="text-sm text-muted">{t("This task has not run yet.")}</p>
              ) : (
                <ul className="flex flex-col gap-2">
                  {runs.map((r) => (
                    <RunRow key={r.id} run={r} zone={task.timezone} />
                  ))}
                </ul>
              )}
            </>
          )}
        </div>
      </section>
    </div>
  );
}
