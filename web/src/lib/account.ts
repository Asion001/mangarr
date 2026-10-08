import { useAuthStatus } from "../api/queries";

/** Permission keys (internal/access). */
export type Perm = "admin" | "library.manage" | "library.add" | "library.edit" | "library.delete" | "queue.manage" | "activity.view" | "requests.manage" | "requests.create" | "apps" | "download";

/** useAccount is the signed-in account and what it may do. */
export function useAccount() {
  const { data } = useAuthStatus();
  const account = data?.account;
  const perms = new Set<string>(account?.permissions ?? []);
  // the same bundles as the server: "Manage the library" is its parts, and
  // managing the queue includes seeing it
  if (perms.has("library.manage")) for (const p of ["library.add", "library.edit", "library.delete", "queue.manage", "activity.view"]) perms.add(p);
  if (perms.has("queue.manage")) perms.add("activity.view");
  /** can: the account has the permission (any of them, for a list). */
  const can = (p: Perm | Perm[]) => perms.has("admin") || (Array.isArray(p) ? p : [p]).some((x) => perms.has(x));
  return { account, can, isAdmin: can("admin"), name: account?.displayName || account?.username || "" };
}
