import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { basePath } from "../../api/client";
import { useToast } from "../../lib/toast";

type Doc = "media" | "downloads" | "cleanup" | "readsync" | "general" | "sources" | "schedule" | "reading" | "sso" | "appearance" | "messenger";

/** useSettingsDoc loads a settings document into editable local state. */
export function useSettingsDoc<T extends object>(name: Doc) {
  const qc = useQueryClient();
  const toast = useToast();
  const url = `${basePath}/api/v1/settings/${name}`;
  const { data, isLoading, error } = useQuery({
    queryKey: ["settings", name],
    queryFn: async () => {
      const r = await fetch(url);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return (await r.json()) as T;
    },
  });
  const [value, setValue] = useState<T | null>(null);
  const [saving, setSaving] = useState(false);
  useEffect(() => {
    if (data) setValue(data);
  }, [data]);
  const save = async (v: T = value as T) => {
    setSaving(true);
    try {
      const r = await fetch(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(v) });
      if (!r.ok) {
        const body = await r.json().catch(() => ({}));
        throw new Error(body.detail || body.title || `HTTP ${r.status}`);
      }
      qc.invalidateQueries({ queryKey: ["settings", name] });
      toast.success("Settings saved");
    } catch (e) {
      toast.fromError(e, "Could not save settings");
    } finally {
      setSaving(false);
    }
  };
  const patch = (p: Partial<T>) => setValue((v) => (v ? { ...v, ...p } : v));
  /** dirty: the form differs from what the server has. */
  const dirty = !!data && !!value && JSON.stringify(data) !== JSON.stringify(value);
  /** reset discards unsaved edits. */
  const reset = () => data && setValue(data);
  const locks = useSettingsLocks();
  /** lock returns the environment variable pinning a field (JSON path), if any. */
  const lock = (path: string) => locks.data?.[name]?.find((l) => l.path === path)?.env;
  return { value, setValue, patch, save, saving, isLoading, error, lock, dirty, reset };
}

type Lock = { path: string; env: string };

/** useSettingsLocks lists settings fields pinned by environment variables. */
export function useSettingsLocks() {
  return useQuery({
    queryKey: ["settings", "locks"],
    queryFn: async () => {
      const r = await fetch(`${basePath}/api/v1/settings/locks`);
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return (await r.json()) as Record<string, Lock[]>;
    },
    staleTime: 5 * 60_000,
  });
}
