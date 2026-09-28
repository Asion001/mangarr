# Scheduled task controls

Administrators can pause, resume and override the schedule of registered tasks.
Pausing prevents new scheduled submissions; queued and running commands finish,
and manual runs are still allowed. Pause Housekeeping to prevent future scheduled
purges of the recycle bin, old commands and caches. An already queued or running
Housekeeping command is not cancelled.

System → Tasks shows every scheduled task with an on/off switch, its schedule,
last and next run, and Run now, Edit schedule and Run history buttons. Pause
all scheduled (Resume all) switches every task at once, and a banner warns while
Housekeeping is paused. Commands with no schedule are listed under On demand.

## Scheduling rules

- Interval schedules count from completion, including manual and failed runs.
  Defaults remain in code; user overrides and pause state survive restarts.
- Daily schedules accept one or more `HH:MM` times and optional weekdays
  (`mon`, `tue`, `wed`, `thu`, `fri`, `sat`, `sun`). No weekdays, or all seven,
  means every day. Times use Settings → Schedule's timezone, falling back to
  the server timezone. Changes to that timezone also affect task schedules.
- Slots are constructed with Go's `time.Date`. A skipped local time is normalized
  across the DST transition; a repeated local time produces one run, not two.
- A missed slot runs once after startup or resumption. Multiple missed slots
  coalesce into one run. The scheduler checks every 30 seconds; execution can
  be delayed by command queue capacity or exclusivity.
- Minimum intervals are 1 minute for HealthCheck, 5 for RefreshSources and
  SyncReadProgress, and 15 for other tasks. The maximum is 525600 minutes.
  Daily schedules accept at most 24 distinct times.
- Quiet hours continue to apply to downloads and processing. They do not pause
  the scheduler or introduce per-task restrictions.

## API

All `/api/v1/system/tasks` routes require administrator access.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/system/tasks` | Definitions, effective/default schedules, minimum intervals, timezone, pause/custom state, next three runs, active command and last completed attempt |
| PUT | `/api/v1/system/tasks/{name}/schedule` | Set a schedule override |
| DELETE | `/api/v1/system/tasks/{name}/schedule` | Reset to the code default, preserving pause state |
| POST | `/api/v1/system/tasks/{name}/pause` | Pause future scheduled submissions |
| POST | `/api/v1/system/tasks/{name}/resume` | Resume scheduled submissions |
| POST | `/api/v1/system/tasks/pause-all` | Pause all registered scheduled tasks |
| POST | `/api/v1/system/tasks/resume-all` | Resume all registered scheduled tasks |
| POST | `/api/v1/system/tasks/preview` | Preview three runs without changing a schedule |

Schedule examples:

```json
{"kind":"interval","intervalMinutes":30}
```

```json
{"kind":"daily","timesOfDay":["03:30","05:00"],"weekdays":["mon","thu"]}
```

Preview accepts the same body and an optional `name` to enforce that task's
minimum interval and use its completion time as the anchor. Without a name,
preview starts at the current time. Subsequent interval predictions assume
negligible execution time; actual intervals start after completion.

Invalid schedules return HTTP 400; unregistered task names return HTTP 404.
Paused tasks have `nextExecution: null` and an empty `nextRuns`. Active tasks
also have no predicted next execution until their current command finishes.
The active command includes its ID, queued/started status and live message.
The last run includes completion status, duration, message, error and end time.

Run commands with `POST /api/v1/commands` as before. Identical queued/running
commands return the existing command, including concurrent requests. Manual
requests record the authenticated username in their trigger when available.
`GET /api/v1/commands?name=Housekeeping&limit=20` provides recent attempts in
reverse insertion order, including their triggers and durations. These command
routes retain their existing permission checks.

Reader sync's settings route reads the task's interval and writes an interval
override. Saving there replaces a daily schedule. While a daily schedule is
active, the legacy interval field reports the code default; the task resource
contains the actual daily schedule. Reset restores the 30-minute default.
Environment-pinned reader intervals still take precedence, and changing or
resetting their schedules returns HTTP 409 until the environment pin is removed.

Schedule, pause and command lifecycle changes publish `tasks` events. The web
client invalidates task and Reader sync settings queries through SSE.
