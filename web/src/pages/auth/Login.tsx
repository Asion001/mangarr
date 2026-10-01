import { t as tr, t } from "../../lib/i18n/core";
import { useDocumentTitle } from "../../lib/documentTitle";
import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogIn } from "lucide-react";
import { api, basePath, unwrap } from "../../api/client";
import { useAuthStatus } from "../../api/queries";
import { Button, ErrorBox, Field, Input } from "../../components/ui";

export function LoginPage({ setup }: { setup: boolean }) {
  const qc = useQueryClient();
  const { data: status } = useAuthStatus();
  const sso = !setup ? status?.sso : undefined;
  // with passwords off, administrators can still use theirs to get back in
  const [showPassword, setShowPassword] = useState(false);
  const passwords = setup || (status?.passwordLogin ?? true) || showPassword;
  const failed = new URLSearchParams(window.location.search).get("error");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (setup && password !== confirm) {
      setError(new Error("Passwords do not match"));
      return;
    }
    setLoading(true);
    try {
      const body = { username, password };
      await unwrap(setup ? api.POST("/api/v1/auth/setup", { body }) : api.POST("/api/v1/auth/login", { body }));
      await qc.invalidateQueries();
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  };

  useDocumentTitle(setup ? tr("Create the administrator account") : tr("Sign in"));
  return (
    <div className="flex min-h-full items-center justify-center p-4">
      <form onSubmit={submit} className="w-full max-w-sm rounded-xl border border-border bg-panel p-6 shadow-xl">
        <div className="mb-6 flex items-center gap-3">
          <img src="./favicon.svg" className="size-10" alt="" />
          <div>
            <h1 className="text-lg font-semibold">{status?.appearance?.instanceName || "mangarr"}</h1>
            <p className="text-sm text-muted">{setup ? tr("Create the administrator account") : tr("Sign in to continue")}</p>
          </div>
        </div>
        <div className="flex flex-col gap-4">
          {failed && <ErrorBox error={failed} />}
          {sso && (
            <>
              <a href={`${basePath}/api/v1/auth/oidc/login`} className="w-full">
                <Button variant="primary" type="button" className="w-full" icon={<LogIn className="size-4" />}>
                  {sso.label}
                </Button>
              </a>
              {passwords && <div className="flex items-center gap-3 text-xs text-muted">
                <span className="h-px flex-1 bg-border" />{" " + t("or") + " "}<span className="h-px flex-1 bg-border" />
              </div>}
              {!passwords && (
                <button type="button" className="text-xs text-muted hover:text-fg" onClick={() => setShowPassword(true)}>
                  <KeyRound className="mr-1 inline size-3" />{" " + t("Administrator? Sign in with a password")}</button>
              )}
            </>
          )}
          {passwords && <>
          <Field label={t("Username")}>
            <Input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
          </Field>
          <Field label={t("Password")}>
            <Input type="password" autoComplete={setup ? "new-password" : "current-password"} value={password} onChange={(e) => setPassword(e.target.value)} required />
          </Field>
          {setup && (
            <Field label={t("Confirm password")} help={t("At least 8 characters.")}>
              <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
            </Field>
          )}
          {error !== null && <ErrorBox error={error} />}
          <Button variant={sso ? "secondary" : "primary"} type="submit" loading={loading}>
            {setup ? tr("Create account") : tr("Sign in")}
          </Button>
          </>}
        </div>
        {status?.appearance?.loginMessage && <p className="mt-5 whitespace-pre-line border-t border-border pt-4 text-sm text-muted">{status.appearance.loginMessage}</p>}
      </form>
    </div>
  );
}
