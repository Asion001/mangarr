type Options = {
  /** show the notice for a new build */
  show: (build: string) => void;
  /** true while the notice must wait (the reader is open) */
  suppressed?: () => boolean;
};

/**
 * Spots a server update from the build named in each SSE hello. The first
 * build seen is the one this tab's bundle came with; a different one later
 * means the server restarted on a new build, and the notice is shown once
 * for it. While suppressed, the notice waits until flush.
 */
export function createUpdateNotice({ show, suppressed = () => false }: Options) {
  let baseline: string | undefined;
  let notified: string | undefined;
  let pending: string | undefined;

  const flush = () => {
    if (!pending || suppressed()) return;
    notified = pending;
    pending = undefined;
    show(notified);
  };

  const seen = (build: string | undefined) => {
    if (!build) return;
    if (baseline === undefined) {
      baseline = build;
      return;
    }
    if (build === baseline || build === notified) {
      pending = undefined;
      return;
    }
    pending = build;
    flush();
  };

  return { seen, flush };
}
