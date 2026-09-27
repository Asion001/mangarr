import { t } from "../lib/i18n/core";
import type { SelectHTMLAttributes } from "react";
import { useQueries } from "@tanstack/react-query";
import { api, unwrap } from "../api/client";
import { useCatalogs, useRootFolders } from "../api/queries";
import { languageName } from "../lib/format";
import { Select } from "./ui";

/** AUTO is the language of the automatic root folder (every other language). */
export const AUTO = "*";

/** editionLang: catalogs spanning several languages ("all", "multi") have no edition language. */
export function editionLang(code?: string): string {
  const v = (code ?? "").trim().toLowerCase();
  return v === "all" || v === "multi" ? "" : v;
}

/** useLanguages lists the languages you can pick: your folders' and your catalogs'. */
export function useLanguages(): string[] {
  const { data: catalogs } = useCatalogs();
  const { data: roots } = useRootFolders();
  const codes = new Set(["en", ...(roots ?? []).map((r) => editionLang(r.language)), ...(catalogs?.items ?? []).map((c) => editionLang(c.lang))]);
  codes.delete("");
  codes.delete(AUTO);
  return [...codes].sort((a, b) => languageName(a).localeCompare(languageName(b)));
}

/** LanguageSelect picks a language by its name; the value is the code. */
export function LanguageSelect({
  value,
  onChange,
  auto,
  placeholder,
  ...props
}: Omit<SelectHTMLAttributes<HTMLSelectElement>, "value" | "onChange"> & {
  value: string;
  onChange: (code: string) => void;
  /** auto offers the automatic folder ("Every language"). */
  auto?: boolean;
  placeholder?: string;
}) {
  const langs = useLanguages();
  const options = value && value !== AUTO && !langs.includes(value) ? [value, ...langs] : langs;
  return (
    <Select {...props} value={value} onChange={(e) => onChange(e.target.value)}>
      {placeholder !== undefined && <option value="">{placeholder}</option>}
      {auto && <option value={AUTO}>{t("Every language (automatic)")}</option>}
      {options.map((code) => (
        <option key={code} value={code}>
          {languageName(code)}
        </option>
      ))}
    </Select>
  );
}

/** useLanguageFolders says where each language's edition goes. */
export function useLanguageFolders(langs: string[]) {
  const qs = useQueries({
    queries: langs.map((lang) => ({
      queryKey: ["rootfolders", "for-language", lang],
      queryFn: () => unwrap(api.GET("/api/v1/rootfolders/for-language", { params: { query: { lang } } })),
      enabled: !!lang,
    })),
  });
  return new Map(langs.map((lang, i) => [lang, qs[i]?.data]));
}
