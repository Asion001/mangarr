/**
 * A browser-side copy of an answer, shown at once on the next visit while the
 * current one loads ("cache first, then refresh"). One copy per name and
 * account; it is only used for the same request (match) it was saved for.
 * Storage can be full, blocked or wiped: then the page just waits as before.
 */
const prefix = "mangarr:cache:";

export function readCopy<T>(name: string, match: string): T | undefined {
  try {
    const raw = localStorage.getItem(prefix + name);
    if (!raw) return undefined;
    const saved = JSON.parse(raw) as { match: string; data: T };
    return saved.match === match ? saved.data : undefined;
  } catch {
    return undefined;
  }
}

export function saveCopy(name: string, match: string, data: unknown) {
  try {
    localStorage.setItem(prefix + name, JSON.stringify({ match, data }));
  } catch {
    /* full or blocked */
  }
}

/** accountKey names the signed-in account, so a shared browser never shows one person's copy to another. */
export const accountKey = (account?: { kind?: string; id?: number } | null) => `${account?.kind ?? "guest"}:${account?.id ?? 0}`;
