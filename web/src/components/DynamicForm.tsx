import { t as tr, t, label as translateLabel } from "../lib/i18n/core";
import { useState } from "react";
import type { ModuleField } from "../api/client";
import { EnvLock, Field, Input, KeyValueEditor, Locked, SecretInput, Select, Switch, TagInput, Textarea } from "./ui";

type Values = Record<string, unknown>;

/** DynamicForm renders module settings from the server's field schema. */
export function DynamicForm({
  fields,
  values,
  onChange,
  locks,
}: {
  fields: ModuleField[];
  values: Values;
  onChange: (v: Values) => void;
  /** field name -> environment variable pinning it */
  locks?: Record<string, string>;
}) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const sorted = [...fields].sort((a, b) => a.order - b.order);
  const basic = sorted.filter((f) => !f.advanced);
  const advanced = sorted.filter((f) => f.advanced);
  const set = (name: string, v: unknown) => onChange({ ...values, [name]: v });

  const render = (f: ModuleField) => {
    const v = values[f.name] ?? f.default;
    const env = locks?.[f.name];
    const label = (
      <>
        {translateLabel(f.label)}
        {f.required && <span className="text-accent"> *</span>}
      </>
    );
    switch (f.type) {
      case "bool":
        return (
          <div key={f.name} className="flex flex-col gap-1">
            <Locked env={env}>
              <Switch
                checked={Boolean(v)}
                onChange={(x) => set(f.name, x)}
                label={
                  <span className="inline-flex items-center gap-2 font-medium">
                    {translateLabel(f.label)}
                    <EnvLock env={env} />
                  </span>
                }
              />
            </Locked>
            {f.help && <p className="text-xs text-muted">{f.help ? translateLabel(f.help) : undefined}</p>}
          </div>
        );
      case "number":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <Input type="number" value={v === undefined || v === null ? "" : String(v)} onChange={(e) => set(f.name, e.target.value === "" ? 0 : Number(e.target.value))} />
          </Field>
        );
      case "select":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <Select value={String(v ?? "")} onChange={(e) => set(f.name, isNaN(Number(e.target.value)) || typeof f.default !== "number" ? e.target.value : Number(e.target.value))}>
              {(f.options ?? []).map((o) => (
                <option key={o.value} value={o.value}>
                  {translateLabel(o.label)}
                </option>
              ))}
            </Select>
          </Field>
        );
      case "tags":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <TagInput value={Array.isArray(v) ? (v as string[]) : []} onChange={(x) => set(f.name, x)} placeholder={f.placeholder} />
          </Field>
        );
      case "keyvalue":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <KeyValueEditor value={(v as Record<string, string>) ?? {}} onChange={(x) => set(f.name, x)} />
          </Field>
        );
      case "textarea":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <Textarea value={String(v ?? "")} placeholder={f.placeholder} onChange={(e) => set(f.name, e.target.value)} />
          </Field>
        );
      case "password":
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <SecretInput value={String(v ?? "")} placeholder={f.placeholder} onChange={(x) => set(f.name, x)} />
          </Field>
        );
      default:
        return (
          <Field key={f.name} label={label} help={f.help ? translateLabel(f.help) : undefined} env={env}>
            <Input
              type={f.type === "url" ? "url" : "text"}
              value={String(v ?? "")}
              placeholder={f.placeholder}
              autoComplete="off"
              onChange={(e) => set(f.name, e.target.value)}
            />
          </Field>
        );
    }
  };

  return (
    <div className="flex flex-col gap-4">
      {basic.map(render)}
      {advanced.length > 0 && (
        <>
          <button type="button" className="self-start text-sm text-accent-2 hover:underline" onClick={() => setShowAdvanced(!showAdvanced)}>
            {showAdvanced ? tr("Hide") : tr("Show")}{" " + t("advanced settings (")}{advanced.length})
          </button>
          {showAdvanced && advanced.map(render)}
        </>
      )}
    </div>
  );
}

/** defaultsOf builds initial settings from field defaults. */
export function defaultsOf(fields: ModuleField[]): Values {
  const out: Values = {};
  for (const f of fields) if (f.default !== undefined && f.default !== null) out[f.name] = f.default;
  return out;
}
