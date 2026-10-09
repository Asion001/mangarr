import { useCallback } from "react";
import { useSearchParams, type SetURLSearchParams } from "react-router";

// Updates made in the same tick (e.g. a filter plus "back to page 1") are
// applied together, on top of the URL as it is now: setSearchParams's
// "prev" is the URL of the setter's last render, so a change made before
// the page re-rendered (two quick picks) would be undone by the next one.
let pending: { updates: Map<string, string | null>; replace: boolean; set: SetURLSearchParams } | null = null;

function queueParam(set: SetURLSearchParams, name: string, value: string | null, replace: boolean) {
  if (!pending) {
    pending = { updates: new Map(), replace: true, set };
    queueMicrotask(() => {
      const p = pending!;
      pending = null;
      p.set(
        (prev) => {
          const n = new URLSearchParams(typeof window === "undefined" ? prev : window.location.search);
          for (const [k, v] of p.updates) {
            if (v === null) n.delete(k);
            else n.set(k, v);
          }
          return n;
        },
        { replace: p.replace },
      );
    });
  }
  pending.updates.set(name, value);
  pending.replace = pending.replace && replace; // any history entry wins
}

/**
 * useQueryParam keeps a value in the URL query string (so it survives reloads
 * and can be shared). Setting the default value removes the parameter.
 */
export function useQueryParam(name: string, def = ""): [string, (v: string, opts?: { replace?: boolean }) => void] {
  const [params, setParams] = useSearchParams();
  const value = params.get(name) ?? def;
  const set = useCallback(
    (v: string, opts?: { replace?: boolean }) => queueParam(setParams, name, v === def || v === "" ? null : v, opts?.replace ?? true),
    [name, def, setParams],
  );
  return [value, set];
}

/**
 * useListParam keeps a list's sorting, filtering or page in the URL and adds a
 * history entry, so going back restores the list the way it was. Use it for
 * what someone picks (a filter, a sort, a page); useQueryParam suits values
 * that change as they type.
 */
export function useListParam(name: string, def = ""): [string, (v: string) => void] {
  const [value, set] = useQueryParam(name, def);
  const push = useCallback((v: string) => set(v, { replace: false }), [set]);
  return [value, push];
}

function readLocalValue(key: string, def: string, allowed?: readonly string[]) {
  try {
    const value = localStorage.getItem(key);
    return value !== null && (!allowed || allowed.includes(value)) ? value : def;
  } catch {
    return def;
  }
}

/**
 * useStoredListParam behaves like useListParam, but remembers the last choice
 * in this browser. An explicit URL parameter still takes precedence so shared
 * links open with the state chosen by the sender.
 */
export function useStoredListParam(name: string, def: string, storageKey: string, allowed?: readonly string[]): [string, (v: string) => void] {
  const [params, setParams] = useSearchParams();
  const fromURL = params.get(name);
  const value = fromURL === null
    ? readLocalValue(storageKey, def, allowed)
    : (!allowed || allowed.includes(fromURL)) ? fromURL : def;
  const push = useCallback(
    (v: string) => {
      const next = !allowed || allowed.includes(v) ? v : def;
      try {
        if (next === def) localStorage.removeItem(storageKey);
        else localStorage.setItem(storageKey, next);
      } catch {
        /* storage full or disabled; URL state still works */
      }
      queueParam(setParams, name, next === def || next === "" ? null : next, false);
    },
    [allowed, def, name, setParams, storageKey],
  );
  return [value, push];
}

/** sessionState stores small JSON values per browser tab. */
export const sessionState = {
  get<T>(key: string, fallback: T): T {
    try {
      const v = sessionStorage.getItem(key);
      return v ? (JSON.parse(v) as T) : fallback;
    } catch {
      return fallback;
    }
  },
  set(key: string, v: unknown) {
    try {
      sessionStorage.setItem(key, JSON.stringify(v));
    } catch {
      /* storage full or disabled */
    }
  },
  remove(key: string) {
    try {
      sessionStorage.removeItem(key);
    } catch {
      /* ignore */
    }
  },
};
