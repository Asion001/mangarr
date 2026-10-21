type ToastSink = {
  success: (title: string, message?: string) => void;
  error: (title: string, message?: string) => void;
  warning: (title: string, message?: string) => void;
  info: (title: string, message?: string) => void;
};

type Payload = {
  title?: string;
  message?: string;
};

type Options = {
  suppressed?: () => boolean;
};

/**
 * Turns server notices into toasts. Imported chapters get none: there are
 * too many of them, and the Updates inbox lists them anyway.
 */
export function createEventToastController(toast: ToastSink, options: Options = {}) {
  const suppressed = options.suppressed ?? (() => false);

  const handle = (type: string, value: unknown) => {
    if (suppressed()) return;
    const payload = (value ?? {}) as Payload;
    if (type === "series.added") toast.success(payload.title ?? "Series added", payload.message);
    if (type === "download.failed") toast.error(payload.title ?? "Download failed", payload.message);
    if (type === "health.issue") toast.warning(payload.title ?? "Health issue", payload.message);
    if (type === "cleanup.done") toast.info(payload.title ?? "Cleanup done", payload.message);
  };

  // nothing is held back any more; kept so callers can drop it on unmount
  const discard = () => {};

  return { handle, discard };
}
