import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import clsx from "clsx";
import { CheckCircle2, AlertTriangle, XCircle, Info, X } from "lucide-react";
import { Button } from "../components/ui";
import { t as tr } from "./i18n/core";

type Kind = "success" | "error" | "warning" | "info";
type Action = { label: string; icon?: ReactNode; onClick: () => void };
// a toast with an action stays until it's used or dismissed
type Toast = { id: number; kind: Kind; title: string; message?: string; action?: Action };

const Ctx = createContext<(kind: Kind, title: string, message?: string, action?: Action) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((kind: Kind, title: string, message?: string, action?: Action) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t.slice(-4), { id, kind, title, message, action }]);
    if (!action) setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === "error" ? 8000 : 4000);
  }, []);
  const icons = { success: CheckCircle2, error: XCircle, warning: AlertTriangle, info: Info };
  return (
    <Ctx.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-[100] flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2">
        {toasts.map((t) => {
          const Icon = icons[t.kind];
          return (
            <div
              key={t.id}
              className={clsx(
                "flex gap-3 rounded-lg border bg-panel-2 p-3 shadow-xl",
                t.kind === "success" && "border-ok/50",
                t.kind === "error" && "border-err/60",
                t.kind === "warning" && "border-warn/60",
                t.kind === "info" && "border-info/50",
              )}
            >
              <Icon
                className={clsx(
                  "mt-0.5 size-5 shrink-0",
                  t.kind === "success" && "text-ok",
                  t.kind === "error" && "text-err",
                  t.kind === "warning" && "text-warn",
                  t.kind === "info" && "text-info",
                )}
              />
              <div className="min-w-0 flex-1">
                <div className="text-sm font-medium">{t.title}</div>
                {t.message && <div className="mt-0.5 break-words text-sm text-muted">{t.message}</div>}
                {t.action && (
                  <Button size="sm" variant="primary" className="mt-2" icon={t.action.icon} onClick={t.action.onClick}>
                    {t.action.label}
                  </Button>
                )}
              </div>
              <button className="text-muted hover:text-fg" aria-label={tr("Dismiss")} onClick={() => setToasts((x) => x.filter((y) => y.id !== t.id))}>
                <X className="size-4" />
              </button>
            </div>
          );
        })}
      </div>
    </Ctx.Provider>
  );
}

export function useToast() {
  const push = useContext(Ctx);
  return {
    success: (title: string, message?: string) => push("success", title, message),
    error: (title: string, message?: string) => push("error", title, message),
    warning: (title: string, message?: string) => push("warning", title, message),
    info: (title: string, message?: string) => push("info", title, message),
    /** a toast that stays, with one action button */
    action: (kind: Kind, title: string, message: string | undefined, action: Action) => push(kind, title, message, action),
    fromError: (e: unknown, title = "Something went wrong") => push("error", title, e instanceof Error ? e.message : String(e)),
  };
}
