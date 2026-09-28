import { t } from "../../lib/i18n/core";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Clock, History, Pause, Play } from "lucide-react";
import { Link } from "react-router";
import { api, unwrap, type S } from "../../api/client";
import { usePushCommand, useTasks } from "../../api/queries";
import { Badge, Button, ErrorBox, IconButton, Loading, PageHeader, Spinner, Switch, Table, Td, Th } from "../../components/ui";
import { useToast } from "../../lib/toast";
import { EditScheduleModal, TaskHistoryDrawer } from "./TaskDialogs";
import { runDuration, runTime, scheduleText } from "./taskSchedule";

type Task = S["TaskInfo"];

// commands that only make sense with a body (a series, a chapter list)
const needsBody = new Set(["RefreshSeries", "UpscaleExisting"]);

// a line under a schedule that says what else decides when the work happens
const scheduleNotes: Record<string, () => string> = {
  RefreshSources: () => t("Each series still follows its own check interval"),
  SyncReadProgress: () => t("Same setting as Settings › Reader sync"),
};

/** useTaskActions pauses, resumes and runs tasks, refreshing the list afterwards. */
export function useTaskActions() {
  const qc = useQueryClient();
  const toast = useToast();
  const push = usePushCommand();
  const pause = useMutation({
    mutationFn: ({ name, paused }: { name?: string; paused: boolean }) => {
      if (!name) return unwrap(paused ? api.POST("/api/v1/system/tasks/pause-all") : api.POST("/api/v1/system/tasks/resume-all"));
      const path = { params: { path: { name } } };
      return unwrap(paused ? api.POST("/api/v1/system/tasks/{name}/pause", path) : api.POST("/api/v1/system/tasks/{name}/resume", path));
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["tasks"] }),
    onError: (e) => toast.fromError(e, t("Could not change the task")),
  });
  const run = (name: string) =>
    push.mutate({ name, label: t("{task} queued", { task: name }) }, { onSettled: () => qc.invalidateQueries({ queryKey: ["tasks"] }) });
  return { pause, run };
}

function LastRun({ task }: { task: Task }) {
  if (task.running) {
    return (
      <div className="flex max-w-60 items-center gap-2 text-xs text-info">
        <Spinner className="size-3.5 shrink-0 text-info" />
        <span className="truncate" title={task.running.message}>
          {task.running.status === "queued" ? t("Queued") : t("Running")}
          {task.running.message ? ` · ${task.running.message}` : ""}
        </span>
      </div>
    );
  }
  const last = task.lastRun;
  if (!last) return <span className="text-muted">{t("never")}</span>;
  const failed = last.status !== "completed";
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-2">
        <span className={`size-2 shrink-0 rounded-full ${failed ? "bg-err" : "bg-ok"}`} />
        <span className="whitespace-nowrap text-muted" title={last.endedAt ?? undefined}>{runTime(last.endedAt, task.timezone)}</span>
      </div>
      <div className={`ml-4 max-w-56 truncate text-xs ${failed ? "text-err" : "text-muted"}`} title={last.error || last.message}>
        {failed ? t("Failed: {error}", { error: last.error || last.message || last.status }) : t("Succeeded in {duration}", { duration: runDuration(last.durationMs) })}
      </div>
    </div>
  );
}

function NextRun({ task }: { task: Task }) {
  if (task.paused) return <span className="text-warn">{t("Paused")}</span>;
  if (task.running) return <span className="text-muted">{t("After this run")}</span>;
  const next = task.nextRuns?.[0];
  return <span className="text-muted" title={next}>{runTime(next, task.timezone)}</span>;
}

export function TasksPage() {
  const { data, isLoading, error } = useTasks();
  const { pause, run } = useTaskActions();
  const [editing, setEditing] = useState<string | null>(null);
  const [history, setHistory] = useState<string | null>(null);
  const scheduled = (data ?? []).filter((x) => x.scheduled);
  const onDemand = (data ?? []).filter((x) => !x.scheduled && !needsBody.has(x.name));
  const allPaused = scheduled.length > 0 && scheduled.every((x) => x.paused);
  const pausedCount = scheduled.filter((x) => x.paused).length;
  const runningCount = scheduled.filter((x) => x.running).length;
  const housekeeping = scheduled.find((x) => x.name === "Housekeeping");
  const zone = data?.[0]?.timezone || t("the server's time zone");
  const editTask = data?.find((x) => x.name === editing);
  const historyTask = data?.find((x) => x.name === history);

  return (
    <>
      <PageHeader
        title={t("Tasks")}
        subtitle={
          <>
            {t("Scheduled and on-demand commands. Times use {zone} from", { zone }) + " "}
            <Link to="/settings/schedule" className="text-accent-2 hover:underline">{t("Settings › Schedule")}</Link>
            {"; " + t("quiet hours there still hold back downloads and processing.")}
          </>
        }
        actions={
          scheduled.length > 0 && (
            <Button icon={allPaused ? <Play className="size-4" /> : <Pause className="size-4" />} loading={pause.isPending && !pause.variables?.name} onClick={() => pause.mutate({ paused: !allPaused })}>
              {allPaused ? t("Resume all") : t("Pause all scheduled")}
            </Button>
          )
        }
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {housekeeping?.paused && (
        <div role="status" className="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-warn/40 bg-warn/10 px-3 py-2.5 text-sm">
          <Pause className="size-4 shrink-0 text-warn" />
          <span className="min-w-0 flex-1">{t("Housekeeping is paused, so the recycle bin, old commands and caches are not purged.")}</span>
          <Button size="sm" onClick={() => pause.mutate({ name: "Housekeeping", paused: false })}>{t("Resume")}</Button>
        </div>
      )}
      {data && (
        <>
          <div className="mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <h2 className="font-semibold">{t("Scheduled")}</h2>
            <span className="text-xs text-muted">
              {[
                t("{count} tasks", { count: scheduled.length }),
                pausedCount > 0 && t("{count} paused", { count: pausedCount }),
                runningCount > 0 && t("{count} running", { count: runningCount }),
              ]
                .filter(Boolean)
                .join(" · ")}
            </span>
          </div>
          <Table className="mb-6">
            <thead>
              <tr>
                <Th className="w-14">{t("On")}</Th>
                <Th>{t("Task")}</Th>
                <Th>{t("Schedule")}</Th>
                <Th>{t("Last run")}</Th>
                <Th>{t("Next run")}</Th>
                <Th />
              </tr>
            </thead>
            <tbody>
              {scheduled.map((task) => (
                <tr key={task.name} className={task.paused ? "bg-warn/5" : undefined}>
                  <Td>
                    <Switch checked={!task.paused} onChange={(on) => pause.mutate({ name: task.name, paused: !on })} label={<span className="sr-only">{t("Run {task} on schedule", { task: task.name })}</span>} />
                  </Td>
                  <Td className="min-w-48">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={task.paused ? "font-medium text-muted" : "font-medium"}>{task.name}</span>
                      {task.paused && <Badge tone="warn">{t("Paused")}</Badge>}
                    </div>
                    <div className="text-xs text-muted">{task.description}</div>
                  </Td>
                  <Td className="whitespace-nowrap">
                    <div className="flex items-center gap-2">
                      <span className={task.paused ? "text-muted" : undefined}>{scheduleText(task.schedule)}</span>
                      {task.custom && <Badge tone="info">{t("Custom")}</Badge>}
                    </div>
                    {scheduleNotes[task.name] && <div className="text-xs text-muted">{scheduleNotes[task.name]()}</div>}
                  </Td>
                  <Td>
                    <LastRun task={task} />
                  </Td>
                  <Td className="whitespace-nowrap">
                    <NextRun task={task} />
                  </Td>
                  <Td>
                    <div className="flex items-center justify-end gap-1">
                      <Button size="sm" className="whitespace-nowrap" icon={<Play className="size-3.5" />} disabled={!!task.running} onClick={() => run(task.name)}>
                        {task.running ? t("Running") : t("Run now")}
                      </Button>
                      <IconButton title={t("Edit schedule")} onClick={() => setEditing(task.name)}>
                        <Clock className="size-4" />
                      </IconButton>
                      <IconButton title={t("Run history")} onClick={() => setHistory(task.name)}>
                        <History className="size-4" />
                      </IconButton>
                    </div>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>

          {onDemand.length > 0 && (
            <>
              <div className="mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <h2 className="font-semibold">{t("On demand")}</h2>
                <span className="text-xs text-muted">{t("Run when you or Mangarr start them")}</span>
              </div>
              <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                {onDemand.map((task) => (
                  <div key={task.name} className="flex items-center gap-3 rounded-lg border border-border bg-panel px-3 py-2.5">
                    <div className="min-w-0 flex-1">
                      <div className="text-sm font-medium">{task.name}</div>
                      <div className="truncate text-xs text-muted" title={task.description}>{task.description}</div>
                    </div>
                    <Button size="sm" icon={<Play className="size-3.5" />} disabled={!!task.running} onClick={() => run(task.name)}>
                      {task.running ? t("Running") : t("Run")}
                    </Button>
                  </div>
                ))}
              </div>
            </>
          )}
        </>
      )}
      {editTask && <EditScheduleModal task={editTask} onClose={() => setEditing(null)} />}
      {historyTask && (
        <TaskHistoryDrawer
          task={historyTask}
          onClose={() => setHistory(null)}
          onEdit={() => {
            setEditing(historyTask.name);
            setHistory(null);
          }}
          onRun={() => run(historyTask.name)}
        />
      )}
    </>
  );
}
