import { useAuthStatus } from "../api/queries";

/** Permission keys (internal/access). */
export type Perm = "admin" | "library.manage" | "requests.manage" | "requests.create" | "activity.view" | "apps" | "download";

/** useAccount is the signed-in account and what it may do. */
export function useAccount() {
  const { data } = useAuthStatus();
  const account = data?.account;
  const perms = new Set(account?.permissions ?? []);
  /** can: the account has the permission (any of them, for a list). */
  const can = (p: Perm | Perm[]) => perms.has("admin") || (Array.isArray(p) ? p : [p]).some((x) => perms.has(x));
  return { account, can, isAdmin: can("admin"), name: account?.displayName || account?.username || "" };
}
