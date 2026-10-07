import { t } from "../../lib/i18n/core";
import { Plus, Trash2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap, type S } from "../../api/client";
import { Badge, Button, Card, ErrorBox, Field, IconButton, Input, Loading, PageHeader, SaveBar, SecretInput, Select, Switch, TagInput } from "../../components/ui";
import { useSettingsDoc } from "./useSettingsDoc";

type SSO = S["SSOSettings"];

/** SingleSignOnPage configures signing in with an OpenID Connect provider. */
export function SingleSignOnPage() {
  const { value: c, patch, save, saving, isLoading, error, dirty, reset } = useSettingsDoc<SSO>("sso");
  const { data: groups } = useQuery({ queryKey: ["groups"], queryFn: () => unwrap(api.GET("/api/v1/groups")) });
  const mappings = c?.groups ?? [];
  const setMapping = (i: number, p: Partial<{ claim: string; groupId: number }>) =>
    patch({ groups: mappings.map((m, k) => (k === i ? { ...m, ...p } : m)) });
  return (
    <>
      <PageHeader
        title={t("Single sign-on")}
        subtitle={t("Let people sign in with your identity provider (Authentik, Authelia, Keycloak, Pocket ID, Google, ...).")}
      />
      {isLoading && <Loading />}
      {error && <ErrorBox error={error} />}
      {c && (
        <div className="flex flex-col gap-6">
          <Card title={t("Provider")}>
            <div className="flex flex-col gap-4">
              <Switch checked={c.enabled} onChange={(enabled) => patch({ enabled })} label={t("Allow signing in with the provider")} />
              <div className="grid gap-4 md:grid-cols-2">
                <Field label={t("Issuer URL")} help={t("Where its /.well-known/openid-configuration lives, e.g. https://auth.example.com/application/o/mangarr/")}>
                  <Input value={c.issuer} onChange={(e) => patch({ issuer: e.target.value })} placeholder="https://auth.example.com" />
                </Field>
                <Field label={t("Client id")}>
                  <Input value={c.clientId} onChange={(e) => patch({ clientId: e.target.value })} />
                </Field>
                <Field label={t("Client secret")} help={t("Leave as it is to keep the stored one.")}>
                  <SecretInput value={c.clientSecret ?? ""} onChange={(clientSecret) => patch({ clientSecret })} />
                </Field>
                <Field label={t("Button label")}>
                  <Input value={c.buttonLabel} onChange={(e) => patch({ buttonLabel: e.target.value })} placeholder={t("Sign in with SSO")} />
                </Field>
                <Field label={t("Scopes")}>
                  <TagInput value={c.scopes ?? []} onChange={(scopes) => patch({ scopes })} placeholder="openid" />
                </Field>
                <Field label={t("Redirect URL")} help={t("Allow exactly this address at the provider.")}>
                  <Input readOnly value={c.redirectUrl} className="font-mono text-xs" />
                </Field>
              </div>
            </div>
          </Card>

          <Card title={t("Accounts")}>
            <div className="flex flex-col gap-4">
              <div className="grid gap-4 md:grid-cols-2">
                <Field label={t("Username claim")} help={t("Which claim names new accounts (an email keeps its first part).")}>
                  <Input value={c.usernameClaim} onChange={(e) => patch({ usernameClaim: e.target.value })} placeholder="preferred_username" />
                </Field>
                <Field label={t("Groups claim")} help={t("Which claim lists the provider's groups.")}>
                  <Input value={c.groupsClaim} onChange={(e) => patch({ groupsClaim: e.target.value })} placeholder={t("groups")} />
                </Field>
              </div>
              <Switch checked={c.createUsers} onChange={(createUsers) => patch({ createUsers })} label={t("Make an account on first sign-in (otherwise people need an invite)")} />
              <div>
                <Switch checked={c.linkByUsername} onChange={(linkByUsername) => patch({ linkByUsername })} label={t("Sign in to an account here with the same username")} />
                <p className="mt-1 text-xs text-muted">{t("Only with a provider where you control usernames: anyone who can pick one there could take over that account.")}</p>
              </div>
              <div>
                <Switch checked={c.passwordLogin} onChange={(passwordLogin) => patch({ passwordLogin })} label={t("Keep password sign-in for everyone")} />
                <p className="mt-1 text-xs text-muted">{t("Off: only administrators may still use a password, to get back in when the provider is down.")}</p>
              </div>
            </div>
          </Card>

          <Card
            title={t("Groups")}
            actions={
              <Button size="sm" icon={<Plus className="size-4" />} onClick={() => patch({ groups: [...mappings, { claim: "", groupId: groups?.[0]?.id ?? 0 }] })}>{t("Add")}</Button>
            }
          >
            <p className="mb-3 text-sm text-muted">{t("People land in the first group whose provider group they're in, at every sign-in. Without a match they keep the group they have (new accounts go to Users).")}</p>
            <div className="flex flex-col gap-2">
              {mappings.map((m, i) => (
                <div key={i} className="flex flex-wrap items-center gap-2">
                  <Input className="w-56" value={m.claim} onChange={(e) => setMapping(i, { claim: e.target.value })} placeholder={t("Group at the provider")} />
                  <span className="text-muted">→</span>
                  <Select className="w-48" value={m.groupId} onChange={(e) => setMapping(i, { groupId: Number(e.target.value) })}>
                    {(groups ?? []).map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))}
                  </Select>
                  <IconButton title={t("Remove")} onClick={() => patch({ groups: mappings.filter((_, k) => k !== i) })}>
                    <Trash2 className="size-4" />
                  </IconButton>
                </div>
              ))}
              {mappings.length === 0 && <p className="text-sm text-muted">{t("No mapping: everyone keeps the group they have here.")}</p>}
            </div>
            <div className="mt-4">
              <Switch checked={c.onlyMapped} onChange={(onlyMapped) => patch({ onlyMapped })} label={t("Turn away people in none of these groups")} />
            </div>
            {!c.enabled && (
              <p className="mt-4 text-xs text-muted">
                <Badge tone="warn">{t("off")}</Badge>{" " + t("Turn it on above once the provider is set up; mangarr checks the issuer when you save.")}</p>
            )}
          </Card>
        </div>
      )}
      <SaveBar dirty={dirty} saving={saving} onSave={() => void save()} onDiscard={reset} />
    </>
  );
}
