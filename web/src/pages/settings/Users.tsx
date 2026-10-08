import { label, t as tr, t } from "../../lib/i18n/core";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Link2, LogOut, Pencil, Plus, Trash2, UserPlus } from "lucide-react";
import clsx from "clsx";
import { api, basePath, unwrap, type S } from "../../api/client";
import { useReaders, useRootFolders, useTags } from "../../api/queries";
import { Badge, Button, Card, Confirm, EmptyState, ErrorBox, Field, IconButton, Input, Loading, Modal, PageHeader, Select, Switch, Table, Tabs, Td, Th } from "../../components/ui";
import { useAccount } from "../../lib/account";
import { dateTime, relative } from "../../lib/format";
import { useToast } from "../../lib/toast";
import { useListParam } from "../../lib/urlState";

type User = S["UserView"];
type Group = S["GroupView"];
type Invite = S["InviteView"];
type Tab = "users" | "groups" | "invites";

const useUsers = () => useQuery({ queryKey: ["users"], queryFn: () => unwrap(api.GET("/api/v1/users")) });
const useGroups = () => useQuery({ queryKey: ["users", "groups"], queryFn: () => unwrap(api.GET("/api/v1/groups")) });
const usePermissions = () => useQuery({ queryKey: ["permissions"], queryFn: () => unwrap(api.GET("/api/v1/permissions")), staleTime: Infinity });

export function UsersPage() {
  const [tabParam, setTab] = useListParam("tab", "users");
  const tab = tabParam as Tab;
  return (
    <>
      <PageHeader title={t("Users & groups")} subtitle={t("Share the library: everyone reads the same series, with their own progress. Groups decide what members can do and see.")} />
      <Tabs
        tabs={[
          { value: "users", label: "Users" },
          { value: "groups", label: "Groups" },
          { value: "invites", label: "Invite links" },
        ]}
        value={tab}
        onChange={setTab}
      />
      <div className="mt-4">
        {tab === "users" && <UsersTab />}
        {tab === "groups" && <GroupsTab />}
        {tab === "invites" && <InvitesTab />}
      </div>
    </>
  );
}

function UsersTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const { account } = useAccount();
  const { data, isLoading, error } = useUsers();
  const [editing, setEditing] = useState<User | null>(null);
  const [creating, setCreating] = useState(false);
  const [password, setPassword] = useState<User | null>(null);
  const [deleting, setDeleting] = useState<User | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["users"] });
  const signout = async (u: User) => {
    try {
      await unwrap(api.POST("/api/v1/users/{id}/signout", { params: { path: { id: u.id } } }));
      toast.success(`${u.username} was signed out`);
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
  };
  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/users/{id}", { params: { path: { id: deleting.id } } }));
      refresh();
    } catch (e) {
      toast.fromError(e);
    }
    setDeleting(null);
  };
  return (
    <>
      <div className="mb-3 flex justify-end">
        <Button variant="primary" icon={<UserPlus className="size-4" />} onClick={() => setCreating(true)}>{t("Add user")}</Button>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data && (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <Th>{t("User")}</Th>
                <Th>{t("Group")}</Th>
                <Th>{t("Progress")}</Th>
                <Th>{t("Last sign-in")}</Th>
                <Th>{t("Signed in")}</Th>
                <Th />
              </tr>
            </thead>
            <tbody>
              {data.map((u) => (
                <tr key={u.id} className={clsx(u.disabled && "opacity-60")}>
                  <Td>
                    <div className="font-medium">{u.displayName || u.username}</div>
                    {u.displayName && <div className="text-xs text-muted">{u.username}</div>}
                    {u.disabled && <Badge tone="warn">{t("disabled")}</Badge>}
                  </Td>
                  <Td>
                    <Badge tone="info">{u.group}</Badge>
                  </Td>
                  <Td className="text-muted">{u.readerName || "—"}</Td>
                  <Td className="text-muted">{u.lastLoginAt ? relative(u.lastLoginAt) : tr("never")}</Td>
                  <Td className="text-muted">
                    {u.sessions}{" " + t("browser")}{u.sessions === 1 ? "" : tr("s")}, {u.devices}{" " + t("app")}{u.devices === 1 ? "" : tr("s")}
                  </Td>
                  <Td>
                    <div className="flex justify-end">
                      <IconButton title={t("Edit")} onClick={() => setEditing(u)}>
                        <Pencil className="size-4" />
                      </IconButton>
                      <IconButton title={t("Set password")} onClick={() => setPassword(u)}>
                        <KeyRound className="size-4" />
                      </IconButton>
                      <IconButton title={t("Sign out everywhere")} onClick={() => signout(u)}>
                        <LogOut className="size-4" />
                      </IconButton>
                      {u.id !== account?.id && (
                        <IconButton title={t("Delete")} onClick={() => setDeleting(u)}>
                          <Trash2 className="size-4" />
                        </IconButton>
                      )}
                    </div>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
      {creating && <UserModal onClose={() => setCreating(false)} />}
      {editing && <UserModal user={editing} onClose={() => setEditing(null)} />}
      {password && <PasswordModal user={password} onClose={() => setPassword(null)} />}
      <Confirm
        open={!!deleting}
        title={t("Delete user")}
        danger
        confirmLabel={t("Delete")}
        message={`Delete ${deleting?.username}? Their reader and its progress stay (Settings → Readers).`}
        onConfirm={remove}
        onClose={() => setDeleting(null)}
      />
    </>
  );
}

function UserModal({ user, onClose }: { user?: User; onClose: () => void }) {
  const qc = useQueryClient();
  const { data: groups } = useGroups();
  const { data: readers } = useReaders();
  const { account } = useAccount();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState(user?.displayName ?? "");
  const [groupId, setGroupId] = useState(user?.groupId ?? 0);
  const [readerId, setReaderId] = useState(user?.readerId ?? 0);
  const [disabled, setDisabled] = useState(user?.disabled ?? false);
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (user) {
        await unwrap(api.PUT("/api/v1/users/{id}", { params: { path: { id: user.id } }, body: { displayName, groupId, readerId, disabled } }));
      } else {
        await unwrap(api.POST("/api/v1/users", { body: { username, password, displayName: displayName || undefined, groupId: groupId || undefined } }));
      }
      qc.invalidateQueries({ queryKey: ["users"] });
      qc.invalidateQueries({ queryKey: ["readers"] });
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={user ? `Edit ${user.username}` : tr("Add a user")}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!user && (
          <>
            <Field label={t("Username")}>
              <Input autoFocus autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
            </Field>
            <Field label={t("Password")} help={t("At least 8 characters. They can change it under My account.")}>
              <Input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
          </>
        )}
        <Field label={t("Name to show")} help={t("Optional.")}>
          <Input value={displayName} onChange={(e) => setDisplayName(e.target.value)} />
        </Field>
        <Field label={t("Group")}>
          <Select value={groupId} onChange={(e) => setGroupId(Number(e.target.value))}>
            {!user && <option value={0}>{t("Users")}</option>}
            {groups
              ?.filter((g) => user || g.builtin !== "users")
              .map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
          </Select>
        </Field>
        {user && (
          <>
            <Field label={t("Progress")} help={t("The reader whose progress this user reads and writes (e.g. one you set up for their Komga account before).")}>
              <Select value={readerId} onChange={(e) => setReaderId(Number(e.target.value))}>
                {readers?.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </Field>
            {user.id !== account?.id && <Switch checked={disabled} onChange={setDisabled} label={t("Disabled (can't sign in)")} />}
          </>
        )}
        {error !== null && <ErrorBox error={error} />}
      </div>
    </Modal>
  );
}

function PasswordModal({ user, onClose }: { user: User; onClose: () => void }) {
  const toast = useToast();
  const [password, setPassword] = useState("");
  const [error, setError] = useState<unknown>(null);
  const save = async () => {
    try {
      await unwrap(api.POST("/api/v1/users/{id}/password", { params: { path: { id: user.id } }, body: { password } }));
      toast.success(`Password set for ${user.username}`, tr("They were signed out everywhere"));
      onClose();
    } catch (e) {
      setError(e);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={`Set ${user.username}'s password`}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" disabled={password.length < 8} onClick={save}>{t("Set password")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t("New password")} help={t("At least 8 characters.")}>
          <Input type="password" autoComplete="new-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {error !== null && <ErrorBox error={error} />}
      </div>
    </Modal>
  );
}

function GroupsTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data, isLoading, error } = useGroups();
  const { data: perms } = usePermissions();
  const { data: tags } = useTags();
  const { data: roots } = useRootFolders();
  const [editing, setEditing] = useState<Group | "new" | null>(null);
  const [deleting, setDeleting] = useState<Group | null>(null);
  const tagName = (id: number) => tags?.find((t) => t.id === id)?.label ?? `#${id}`;
  const rootName = (id: number) => roots?.find((r) => r.id === id)?.path ?? `#${id}`;
  const permName = (k: string) => perms?.find((p) => p.key === k)?.label ?? k;
  const remove = async () => {
    if (!deleting) return;
    try {
      await unwrap(api.DELETE("/api/v1/groups/{id}", { params: { path: { id: deleting.id } } }));
      qc.invalidateQueries({ queryKey: ["users"] });
    } catch (e) {
      toast.fromError(e);
    }
    setDeleting(null);
  };
  return (
    <>
      <div className="mb-3 flex justify-end">
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>{t("Add group")}</Button>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      <div className="grid gap-3 md:grid-cols-2">
        {data?.map((g) => {
          const limited = g.includeTags.length > 0 || g.excludeTags.length > 0 || g.rootFolders.length > 0;
          return (
            <Card
              key={g.id}
              title={
                <span className="flex items-center gap-2">
                  {g.name} {g.builtin && <Badge>{t("built-in")}</Badge>} <span className="text-xs font-normal text-muted">{g.members}{" " + t("member")}{g.members === 1 ? "" : tr("s")}</span>
                </span>
              }
              actions={
                <div className="flex">
                  <IconButton title={t("Edit")} onClick={() => setEditing(g)}>
                    <Pencil className="size-4" />
                  </IconButton>
                  {!g.builtin && (
                    <IconButton title={t("Delete")} onClick={() => setDeleting(g)}>
                      <Trash2 className="size-4" />
                    </IconButton>
                  )}
                </div>
              }
            >
              <div className="flex flex-col gap-2 text-sm">
                <div className="flex flex-wrap gap-1.5">
                  {g.permissions.length === 0 && <span className="text-muted">{t("Read only")}</span>}
                  {g.permissions.map((p) => (
                    <Badge key={p} tone={p === "admin" ? "accent" : "default"}>
                      {permName(p)}
                    </Badge>
                  ))}
                </div>
                <div className="text-xs text-muted">
                  {!limited
                    ? tr("Sees every series")
                    : [
                        g.includeTags.length > 0 && `only tagged ${g.includeTags.map(tagName).join(" or ")}`,
                        g.excludeTags.length > 0 && `never tagged ${g.excludeTags.map(tagName).join(", ")}`,
                        g.rootFolders.length > 0 && `only in ${g.rootFolders.map(rootName).join(", ")}`,
                      ]
                        .filter(Boolean)
                        .join("; ")}
                  {g.autoApproveRequests && tr(" · requests are added without approval")}
                </div>
              </div>
            </Card>
          );
        })}
      </div>
      {editing && <GroupModal group={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      <Confirm
        open={!!deleting}
        title={t("Delete group")}
        danger
        confirmLabel={t("Delete")}
        message={`Delete ${deleting?.name}? Its members move to Users.`}
        onConfirm={remove}
        onClose={() => setDeleting(null)}
      />
    </>
  );
}

function Chips<T extends number | string>({ options, value, onChange }: { options: { value: T; label: string }[]; value: T[]; onChange: (v: T[]) => void }) {
  if (options.length === 0) return <span className="text-sm text-muted">{t("None yet")}</span>;
  return (
    <div className="flex flex-wrap gap-1.5">
      {options.map((o) => {
        const on = value.includes(o.value);
        return (
          <button
            key={String(o.value)}
            type="button"
            onClick={() => onChange(on ? value.filter((v) => v !== o.value) : [...value, o.value])}
            className={clsx("rounded-full border px-2.5 py-0.5 text-xs", on ? "border-accent bg-accent/15 text-accent-2" : "border-border text-muted hover:text-fg")}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

/** The permission groups of the group editor, in order. */
const permissionSections: { title: string; keys: string[] }[] = [
  { title: "Requests", keys: ["requests.create", "requests.manage"] },
  { title: "Library", keys: ["library.add", "library.edit", "library.delete"] },
  { title: "Downloads", keys: ["activity.view", "queue.manage"] },
  { title: "Reading outside the browser", keys: ["apps", "download"] },
];

/** Presets fill the permissions in one go; the boxes fine-tune them. */
const presets: { name: string; permissions: string[] }[] = [
  { name: "Reader", permissions: ["apps", "download"] },
  { name: "Requester", permissions: ["requests.create", "apps", "download"] },
  { name: "Curator", permissions: ["requests.create", "library.add", "activity.view", "apps", "download"] },
  { name: "Manager", permissions: ["requests.create", "requests.manage", "library.add", "library.edit", "library.delete", "activity.view", "queue.manage", "apps", "download"] },
];

/** expandPermissions spells "Manage the library" out as its parts. */
function expandPermissions(perms: string[]): string[] {
  if (!perms.includes("library.manage")) return perms;
  const parts = ["library.add", "library.edit", "library.delete", "queue.manage", "activity.view", "requests.manage"];
  return [...new Set([...perms.filter((p) => p !== "library.manage"), ...parts])];
}

const sameSet = (a: string[], b: string[]) => a.length === b.length && a.every((x) => b.includes(x));

function GroupModal({ group, onClose }: { group?: Group; onClose: () => void }) {
  const qc = useQueryClient();
  const { data: perms } = usePermissions();
  const { data: tags } = useTags();
  const { data: roots } = useRootFolders();
  const [name, setName] = useState(group?.name ?? "");
  const [permissions, setPermissions] = useState<string[]>(expandPermissions(group?.permissions ?? ["requests.create", "apps", "download"]));
  const preset = presets.find((p) => sameSet(p.permissions, permissions))?.name ?? "";
  const toggle = (key: string, on: boolean) => {
    let next = on ? [...permissions, key] : permissions.filter((x) => x !== key);
    // managing the queue includes seeing it
    if (key === "queue.manage" && on) next = [...new Set([...next, "activity.view"])];
    if (key === "activity.view" && !on) next = next.filter((x) => x !== "queue.manage");
    setPermissions(next);
  };
  const [includeTags, setInclude] = useState<number[]>(group?.includeTags ?? []);
  const [excludeTags, setExclude] = useState<number[]>(group?.excludeTags ?? []);
  const [rootFolders, setRoots] = useState<number[]>(group?.rootFolders ?? []);
  const [autoApprove, setAutoApprove] = useState(group?.autoApproveRequests ?? false);
  const [maxRating, setMaxRating] = useState<string>(group?.maxRating ?? "");
  const [blocked, setBlocked] = useState<string[]>(group?.blockedGenres ?? []);
  const [genreDraft, setGenreDraft] = useState("");
  const addGenre = () => {
    const g = genreDraft.trim();
    if (g && !blocked.some((x) => x.toLowerCase() === g.toLowerCase())) setBlocked([...blocked, g]);
    setGenreDraft("");
  };
  const [error, setError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const admins = group?.builtin === "admins";
  const tagOptions = (tags ?? []).map((t) => ({ value: t.id, label: t.label }));
  const save = async () => {
    setSaving(true);
    setError(null);
    const body = { name, permissions, includeTags, excludeTags, rootFolders, autoApproveRequests: autoApprove, maxRating: maxRating as "" | "all" | "teen" | "mature", blockedGenres: blocked };
    try {
      if (group) await unwrap(api.PUT("/api/v1/groups/{id}", { params: { path: { id: group.id } }, body }));
      else await unwrap(api.POST("/api/v1/groups", { body }));
      qc.invalidateQueries({ queryKey: ["users"] });
      onClose();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={group ? `Edit ${group.name}` : tr("Add a group")}
      footer={
        <>
          <Button onClick={onClose}>{t("Cancel")}</Button>
          <Button variant="primary" loading={saving} disabled={!name.trim()} onClick={save}>{t("Save")}</Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Field label={t("Name")}>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        {admins ? (
          <p className="text-sm text-muted">{t("Admins can do everything and see every series.")}</p>
        ) : (
          <>
            <div className="flex flex-col gap-2">
              <span className="text-sm font-medium">{t("Start from a preset")}</span>
              <div role="radiogroup" aria-label={t("Preset")} className="flex flex-wrap items-center gap-2">
                {presets.map((p) => (
                  <button
                    key={p.name}
                    type="button"
                    role="radio"
                    aria-checked={preset === p.name}
                    onClick={() => setPermissions(p.permissions)}
                    className={clsx("min-h-10 rounded-full border px-4 text-sm", preset === p.name ? "border-accent bg-accent/15 font-semibold text-fg" : "border-border text-muted hover:text-fg")}
                  >
                    {label(p.name)}
                  </button>
                ))}
                {!preset && <Badge>{t("Custom")}</Badge>}
              </div>
            </div>
            <Field label={t("Members can")} help={t("Everyone can read the series their group sees and keep their own progress.")}>
              <div className="grid gap-3 sm:grid-cols-2">
                {permissionSections.map((section) => (
                  <fieldset key={section.title} className="flex flex-col gap-2 rounded-lg border border-border p-3">
                    <legend className="px-1 text-sm font-semibold">{label(section.title)}</legend>
                    {section.keys.map((key) => perms?.find((p) => p.key === key)).filter((p) => !!p).map((p) => (
                      <label key={p.key} className="flex items-start gap-2 text-sm">
                        <input type="checkbox" className="mt-1" checked={permissions.includes(p.key)} onChange={(e) => toggle(p.key, e.target.checked)} />
                        <span>
                          <span className="font-medium">{label(p.label)}</span>
                          <span className="block text-muted">{label(p.description)}</span>
                        </span>
                      </label>
                    ))}
                    {section.title === "Requests" && (
                      <Switch checked={autoApprove} onChange={setAutoApprove} label={t("Add members' requests without approval (when a source is found automatically)")} />
                    )}
                  </fieldset>
                ))}
              </div>
            </Field>
            <Field label={t("Highest rating members see")} help={t("Hidden titles don't show in the library, search, Discover, previews or requests. Titles without a rating count as All ages unless a genre says otherwise.")}>
              <div role="radiogroup" aria-label={t("Highest rating members see")} className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                {([["all", t("All ages")], ["teen", t("Teen 13+")], ["mature", t("Mature 16+")], ["", t("Adult 18+")]] as const).map(([value, text]) => (
                  <label key={value || "adult"} className={clsx("flex min-h-11 cursor-pointer items-center gap-2 rounded-lg border px-3 text-sm", maxRating === value ? "border-accent bg-accent/10" : "border-border")}>
                    <input type="radio" name="group-max-rating" className="accent-accent" checked={maxRating === value} onChange={() => setMaxRating(value)} />
                    {text}
                  </label>
                ))}
              </div>
            </Field>
            <Field label={t("Always hide these genres and tags")}>
              <div className="flex flex-col gap-2">
                {blocked.length > 0 && (
                  <div className="flex flex-wrap gap-2">
                    {blocked.map((g) => (
                      <span key={g} className="inline-flex items-center gap-1 rounded-full border border-err/40 bg-err/10 py-1 pl-3 pr-1 text-sm">
                        {g}
                        <IconButton title={t("Remove {name}", { name: g })} onClick={() => setBlocked(blocked.filter((x) => x !== g))}>×</IconButton>
                      </span>
                    ))}
                  </div>
                )}
                <form className="flex gap-2" onSubmit={(e) => (e.preventDefault(), addGenre())}>
                  <Input list="group-hidden-genres" value={genreDraft} onChange={(e) => setGenreDraft(e.target.value)} placeholder={t("e.g. Hentai")} aria-label={t("Genre or tag to hide")} />
                  <datalist id="group-hidden-genres">
                    {["Hentai", "Ecchi", "Smut", "Erotica", "Gore", "Yaoi", "Yuri"].map((g) => <option key={g} value={g} />)}
                  </datalist>
                  <Button type="submit" disabled={!genreDraft.trim()}>{t("Add")}</Button>
                </form>
              </div>
            </Field>
            <Field label={t("Only series tagged")} help={t("Members see series with any of these tags. None selected: every series.")}>
              <Chips options={tagOptions} value={includeTags} onChange={setInclude} />
            </Field>
            <Field label={t("Never series tagged")} help={t("Hide series with these tags, e.g. an nsfw tag.")}>
              <Chips options={tagOptions} value={excludeTags} onChange={setExclude} />
            </Field>
            <Field label={t("Only these root folders")} help={t("None selected: all root folders.")}>
              <Chips options={(roots ?? []).map((r) => ({ value: r.id, label: r.path }))} value={rootFolders} onChange={setRoots} />
            </Field>
          </>
        )}
        {error !== null && <ErrorBox error={error} />}
      </div>
    </Modal>
  );
}

function InvitesTab() {
  const qc = useQueryClient();
  const toast = useToast();
  const { data, isLoading, error } = useQuery({ queryKey: ["users", "invites"], queryFn: () => unwrap(api.GET("/api/v1/invites")) });
  const [creating, setCreating] = useState(false);
  const remove = async (inv: Invite) => {
    try {
      await unwrap(api.DELETE("/api/v1/invites/{id}", { params: { path: { id: inv.id } } }));
      qc.invalidateQueries({ queryKey: ["users", "invites"] });
    } catch (e) {
      toast.fromError(e);
    }
  };
  return (
    <>
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="text-sm text-muted">{t("Send a friend a link: they choose their own username and password.")}</p>
        <Button variant="primary" icon={<Link2 className="size-4" />} onClick={() => setCreating(true)}>{t("Create invite link")}</Button>
      </div>
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {data?.length === 0 && <EmptyState title={t("No invite links yet")} />}
      {!!data?.length && (
        <div className="overflow-x-auto">
          <Table>
            <thead>
              <tr>
                <Th>{t("For")}</Th>
                <Th>{t("Group")}</Th>
                <Th>{t("Used")}</Th>
                <Th>{t("Expires")}</Th>
                <Th>{t("Created")}</Th>
                <Th />
              </tr>
            </thead>
            <tbody>
              {data.map((inv) => (
                <tr key={inv.id} className={clsx(!inv.active && "opacity-60")}>
                  <Td>{inv.note || <span className="text-muted">—</span>}</Td>
                  <Td>
                    <Badge tone="info">{inv.group}</Badge>
                  </Td>
                  <Td>
                    {inv.uses} / {inv.maxUses} {!inv.active && <Badge>{t("done")}</Badge>}
                  </Td>
                  <Td className="text-muted">{inv.expiresAt ? dateTime(inv.expiresAt) : tr("never")}</Td>
                  <Td className="text-muted">{relative(inv.createdAt)}</Td>
                  <Td>
                    <IconButton title={t("Delete")} onClick={() => remove(inv)}>
                      <Trash2 className="size-4" />
                    </IconButton>
                  </Td>
                </tr>
              ))}
            </tbody>
          </Table>
        </div>
      )}
      {creating && <InviteModal onClose={() => setCreating(false)} />}
    </>
  );
}

function InviteModal({ onClose }: { onClose: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const { data: groups } = useGroups();
  const [groupId, setGroupId] = useState(0);
  const [note, setNote] = useState("");
  const [maxUses, setMaxUses] = useState(1);
  const [expireDays, setExpireDays] = useState(7);
  const [link, setLink] = useState("");
  const [error, setError] = useState<unknown>(null);
  const create = async () => {
    try {
      const inv = await unwrap(api.POST("/api/v1/invites", { body: { groupId, note: note || undefined, maxUses, expireDays } }));
      setLink(`${window.location.origin}${basePath}/invite/${inv.token}`);
      qc.invalidateQueries({ queryKey: ["users", "invites"] });
    } catch (e) {
      setError(e);
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link);
      toast.success(tr("Link copied"));
    } catch {
      toast.error(tr("Couldn't copy: select the link and copy it by hand"));
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={link ? tr("Invite link") : tr("Create an invite link")}
      footer={
        link ? (
          <Button variant="primary" onClick={onClose}>{t("Done")}</Button>
        ) : (
          <>
            <Button onClick={onClose}>{t("Cancel")}</Button>
            <Button variant="primary" onClick={create}>{t("Create link")}</Button>
          </>
        )
      }
    >
      {link ? (
        <div className="flex flex-col gap-3">
          <p className="text-sm">{t("Send this link to") + " "}{note || tr("your friend")}{t(". It's only shown now.")}</p>
          <div className="flex gap-2">
            <Input readOnly value={link} onFocus={(e) => e.currentTarget.select()} className="font-mono text-xs" />
            <Button icon={<Copy className="size-4" />} onClick={copy}>{t("Copy")}</Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <Field label={t("For")} help={t("A note for you, e.g. their name. They see it on the invite page.")}>
            <Input autoFocus value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <Field label={t("Group")}>
            <Select value={groupId} onChange={(e) => setGroupId(Number(e.target.value))}>
              <option value={0}>{t("Users")}</option>
              {groups
                ?.filter((g) => g.builtin !== "users")
                .map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
            </Select>
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label={t("Accounts it can create")}>
              <Input type="number" min={1} max={100} value={maxUses} onChange={(e) => setMaxUses(Number(e.target.value))} />
            </Field>
            <Field label={t("Expires after (days)")} help={t("0 = never")}>
              <Input type="number" min={0} max={365} value={expireDays} onChange={(e) => setExpireDays(Number(e.target.value))} />
            </Field>
          </div>
          {error !== null && <ErrorBox error={error} />}
        </div>
      )}
    </Modal>
  );
}
