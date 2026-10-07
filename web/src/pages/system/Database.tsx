import { t as tr, t } from "../../lib/i18n/core";
import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft, Database, PlugZap, RotateCcw } from "lucide-react";
import { api, basePath, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, Confirm, ErrorBox, Field, Input, Loading, PageHeader, Progress, SecretInput, Select, Switch } from "../../components/ui";
import { bytes } from "../../lib/format";
import { useToast } from "../../lib/toast";

type Test = S["DBTest"];
type Move = S["MoveState"];

const stageLabel: Record<string, string> = {
  preparing: "Pausing downloads and tasks",
  snapshot: "Taking a snapshot of the current database",
  copying: "Copying data",
  switching: "Switching to the new database",
  done: "Done",
  failed: "Failed",
};

/** postgresDSN builds a postgres:// address from form fields. */
function postgresDSN(f: { host: string; port: string; database: string; user: string; password: string; sslmode: string }) {
  const auth = f.user ? `${encodeURIComponent(f.user)}${f.password ? ":" + encodeURIComponent(f.password) : ""}@` : "";
  return `postgres://${auth}${f.host}:${f.port || "5432"}/${encodeURIComponent(f.database)}?sslmode=${f.sslmode}`;
}

export function DatabasePage() {
  const qc = useQueryClient();
  const { data, isLoading, error } = useQuery({
    queryKey: ["database"],
    queryFn: () => unwrap(api.GET("/api/v1/system/database")),
    refetchInterval: (q) => (q.state.data?.move.running || q.state.data?.move.restarting ? 700 : false),
  });
  const move = data?.move;
  return (
    <>
      <PageHeader title={t("Database")} subtitle={t("Where mangarr keeps its data. Move it to PostgreSQL (or back) with a button: mangarr copies everything and restarts.")} />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <div className="flex flex-col gap-6">
          <Card title={t("Current database")}>
            <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
              <dt className="text-muted">{t("Type")}</dt>
              <dd>
                <Badge tone="info">{data.kind === "postgres" ? "PostgreSQL" : "SQLite"}</Badge>
              </dd>
              <dt className="text-muted">{t("Address")}</dt>
              <dd className="break-all font-mono text-xs">{data.dsn}</dd>
              {data.kind === "sqlite" && (
                <>
                  <dt className="text-muted">{t("Size")}</dt>
                  <dd>{bytes(data.sizeBytes)}</dd>
                </>
              )}
              <dt className="text-muted">{t("Set by")}</dt>
              <dd>
                {data.source === "env" ? (
                  <>
                    <code>MANGARR_DB</code>{" " + t("(moving copies the data; you then change the variable)")}</>
                ) : data.source === "file" ? (
                  tr("this page (a previous move)")
                ) : (
                  tr("default (SQLite in the data folder)")
                )}
              </dd>
            </dl>
          </Card>
          {move && move.stage ? (
            <MoveProgress move={move} onDismiss={() => qc.invalidateQueries({ queryKey: ["database"] })} />
          ) : (
            <MoveForm info={data} />
          )}
        </div>
      )}
    </>
  );
}

function MoveForm({ info }: { info: S["DatabaseInfo"] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [paste, setPaste] = useState(false);
  const [f, setF] = useState({ host: "postgres", port: "5432", database: "mangarr", user: "mangarr", password: "", sslmode: "disable" });
  const [raw, setRaw] = useState("");
  const [test, setTest] = useState<Test | null>(null);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState<unknown>(null);
  const [overwrite, setOverwrite] = useState(false);
  const [confirm, setConfirm] = useState<string | null>(null);
  const dsn = paste ? raw.trim() : postgresDSN(f);
  const patch = (p: Partial<typeof f>) => (setF({ ...f, ...p }), setTest(null));

  const runTest = async () => {
    setTesting(true);
    setTestError(null);
    setTest(null);
    try {
      setTest(await unwrap(api.POST("/api/v1/system/database/test", { body: { dsn } })));
    } catch (e) {
      setTestError(e);
    } finally {
      setTesting(false);
    }
  };
  const start = async (target: string, ow: boolean) => {
    try {
      await unwrap(api.POST("/api/v1/system/database/move", { body: { dsn: target, overwrite: ow } }));
      qc.invalidateQueries({ queryKey: ["database"] });
    } catch (e) {
      toast.fromError(e, tr("Couldn't start the move"));
    }
    setConfirm(null);
  };

  return (
    <>
      <Card title={info.kind === "sqlite" ? tr("Move to PostgreSQL") : tr("Move to another PostgreSQL server")}>
        <div className="flex flex-col gap-4">
          <p className="text-sm text-muted">{t("Create an empty database and a user for mangarr on your PostgreSQL server (13 or newer), enter its details, test the connection, then move. Downloads and tasks pause while the data is copied (usually seconds), then mangarr restarts on the new database.")}{info.kind === "sqlite" && tr(" The SQLite file stays in the data folder, so you can move back.")}
          </p>
          <Switch checked={paste} onChange={(v) => (setPaste(v), setTest(null))} label={t("Paste a connection address instead")} />
          {paste ? (
            <Field label={t("Address")} help="postgres://user:password@host:5432/database?sslmode=disable">
              <Input value={raw} onChange={(e) => (setRaw(e.target.value), setTest(null))} placeholder="postgres://mangarr:secret@postgres:5432/mangarr?sslmode=disable" />
            </Field>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label={t("Host")}>
                <Input value={f.host} onChange={(e) => patch({ host: e.target.value })} />
              </Field>
              <Field label={t("Port")}>
                <Input value={f.port} inputMode="numeric" onChange={(e) => patch({ port: e.target.value })} />
              </Field>
              <Field label={t("Database")}>
                <Input value={f.database} onChange={(e) => patch({ database: e.target.value })} />
              </Field>
              <Field label="SSL">
                <Select value={f.sslmode} onChange={(e) => patch({ sslmode: e.target.value })}>
                  <option value="disable">{t("Off (same host or private network)")}</option>
                  <option value="require">{t("Required")}</option>
                  <option value="verify-full">{t("Required, verify the certificate")}</option>
                </Select>
              </Field>
              <Field label={t("User")}>
                <Input value={f.user} autoComplete="off" onChange={(e) => patch({ user: e.target.value })} />
              </Field>
              <Field label={t("Password")}>
                <SecretInput value={f.password} onChange={(password) => patch({ password })} />
              </Field>
            </div>
          )}
          <div className="flex flex-wrap items-center gap-3">
            <Button icon={<PlugZap className="size-4" />} loading={testing} disabled={!dsn} onClick={runTest}>{t("Test connection")}</Button>
            {test && (
              <span className="text-sm">
                <Badge tone="ok">{t("connected")}</Badge> {test.kind === "postgres" ? `PostgreSQL ${test.version}` : `SQLite ${test.version}`}
                {test.rows > 0 ? <span className="text-warn">{" " + t("· already has") + " "}{test.rows}{" " + t("rows of mangarr data")}</span> : <span className="text-muted">{" " + t("· empty")}</span>}
              </span>
            )}
          </div>
          {testError !== null && <ErrorBox error={testError} />}
          {test && test.rows > 0 && <Switch checked={overwrite} onChange={setOverwrite} label={t("Replace the data that's already there")} />}
          <div>
            <Button variant="primary" icon={<ArrowRightLeft className="size-4" />} disabled={!test || (test.rows > 0 && !overwrite)} onClick={() => setConfirm(dsn)}>{t("Move data and switch")}</Button>
          </div>
        </div>
      </Card>
      {info.kind === "postgres" && (
        <Card title={t("Move back to SQLite")}>
          <div className="flex flex-col gap-3">
            <p className="text-sm text-muted">{t("Copies the current data into a new SQLite file in the data folder (an older file there is kept next to it with a date in its name) and restarts on it.")}</p>
            <div>
              <Button icon={<Database className="size-4" />} onClick={() => setConfirm(info.defaultDsn)}>{t("Move to SQLite")}</Button>
            </div>
          </div>
        </Card>
      )}
      <Confirm
        open={confirm !== null}
        title={t("Move the database")}
        confirmLabel={t("Move and restart")}
        message={
          info.source === "env"
            ? tr("mangarr pauses downloads and tasks and copies all data. MANGARR_DB sets the database, so you then change it and restart mangarr yourself.")
            : tr("mangarr pauses downloads and tasks, copies all data, switches to the new database and restarts. This page reloads when it's back.")
        }
        onConfirm={() => confirm && start(confirm, confirm === info.defaultDsn || overwrite)}
        onClose={() => setConfirm(null)}
      />
    </>
  );
}

function MoveProgress({ move, onDismiss }: { move: Move; onDismiss: () => void }) {
  const toast = useToast();
  const [back, setBack] = useState(false);
  // after the switch mangarr restarts: wait for it and reload
  useEffect(() => {
    if (!move.restarting) return;
    let down = false;
    const t = window.setInterval(async () => {
      try {
        const r = await fetch(basePath + "/ping", { cache: "no-store" });
        if (r.ok && down) {
          setBack(true);
          window.clearInterval(t);
          window.setTimeout(() => window.location.reload(), 800);
        }
        if (!r.ok) down = true;
      } catch {
        down = true;
      }
    }, 1000);
    return () => window.clearInterval(t);
  }, [move.restarting]);
  const resume = async () => {
    try {
      await unwrap(api.POST("/api/v1/system/database/cancel"));
      onDismiss();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const pct = move.total ? (100 * move.done) / move.total : 0;
  return (
    <Card title={move.target?.startsWith("backup ") ? tr("Restoring a backup") : tr("Moving the database")}>
      <div className="flex flex-col gap-3 text-sm">
        <div className="flex items-center gap-2">
          <Badge tone={move.stage === "failed" ? "err" : move.stage === "done" ? "ok" : "info"}>{stageLabel[move.stage ?? ""] ?? move.stage}</Badge>
          <span className="break-all font-mono text-xs text-muted">{move.target}</span>
        </div>
        {move.stage === "copying" && move.total > 0 && (
          <>
            <Progress value={pct} />
            <span className="text-muted">
              {move.table}: {move.done.toLocaleString()} / {move.total.toLocaleString()}{" " + t("rows")}</span>
          </>
        )}
        {move.result && <span className="text-muted">{t("Copied") + " "}{move.result.total.toLocaleString()}{" " + t("rows.")}</span>}
        {move.error && <ErrorBox error={move.error} />}
        {move.stage === "failed" && (
          <div>
            <Button onClick={onDismiss}>{t("Back")}</Button>
          </div>
        )}
        {move.restarting && <span>{back ? tr("mangarr is back. Reloading…") : move.target?.startsWith("backup ") ? tr("Restarting mangarr…") : tr("Restarting mangarr on the new database…")}</span>}
        {move.setEnv && (
          <div className="flex flex-col gap-3">
            <p>{t("The data is copied.") + " "}<code>MANGARR_DB</code>{" " + t("sets the database, so change it to the new address (with its password) and restart the container. Until then, changes are paused so nothing is lost.")}</p>
            <div className="flex gap-2">
              <Button icon={<RotateCcw className="size-4" />} onClick={resume}>{t("Stay on the current database")}</Button>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}
