import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, unwrap } from '../api/client';
import { useAccount } from './account';
import { getLocale, resolveLocale, setLocale, subscribeLocale, type LocalePreference } from './i18n/core';

export type UIOptions = {otherLanguageChapters?:boolean};
export type UIPreferences = {locale:LocalePreference; mode:'reading'|'editing'; options:UIOptions};
const defaults: UIPreferences = {locale:'auto',mode:'reading',options:{}};
const Ctx = createContext<{preferences:UIPreferences; save:(p:Partial<UIPreferences>)=>Promise<void>; canEdit:boolean; saving:boolean; error:unknown; ready:boolean}>({preferences:defaults,save:async()=>{},canEdit:false,saving:false,error:null,ready:false});
function readLocal(key:string): UIPreferences {
  try { const v=JSON.parse(localStorage.getItem(key) || '{}'); return {locale:['auto','en','ru','uk'].includes(v.locale)?v.locale:'auto',mode:v.mode==='editing'?'editing':'reading',options:typeof v.options==='object'&&v.options?v.options:{}}; } catch {return defaults;}
}
export function UIPreferencesProvider({children}:{children:ReactNode}) {
  const {account,can} = useAccount();
  const personal = account?.kind === 'user';
  const canEdit = can(['library.manage','requests.manage']);
  const key = `mangarr:ui:${account?.kind ?? 'guest'}:${account?.id ?? 0}`;
  // read in the same render as the key, so a new account never shows the previous one's mode
  const [version,setVersion] = useState(0);
  const local = useMemo(() => readLocal(key), [key, version]);
  const qc = useQueryClient();
  const query = useQuery({queryKey:['ui-preferences',account?.id],enabled:personal,queryFn:()=>unwrap(api.GET('/api/v1/me/ui-preferences'))});
  const preferences:UIPreferences = {...defaults,...(personal ? query.data ?? defaults : local),mode:canEdit ? (personal ? query.data?.mode ?? 'reading' : local.mode) : 'reading'};
  const requested = useRef<UIPreferences|null>(null);
  useEffect(()=>{requested.current=null;},[key]);
  useEffect(()=>{setLocale(resolveLocale(preferences.locale,navigator.languages));},[preferences.locale]);
  const mutation = useMutation({mutationFn:async(p:Partial<UIPreferences>)=>{
    // The API response also contains updatedAt. Build the strict request shape
    // explicitly so response-only fields are never sent back to the server.
    const current=requested.current ?? preferences;
    const next:UIPreferences={locale:p.locale ?? current.locale,mode:p.mode ?? current.mode,options:{...(current.options ?? {}),...(p.options ?? {})}};
    if(!canEdit)next.mode='reading';
    requested.current=next;
    if(personal){const result=await unwrap(api.PUT('/api/v1/me/ui-preferences',{body:next}));qc.setQueryData(['ui-preferences',account?.id],result);}
    else {localStorage.setItem(key,JSON.stringify(next));setVersion(v=>v+1);}
  }});
  // ready: the stored mode is known (the account is loaded, and a user's preferences fetched)
  const ready = !!account && (!personal || !query.isPending);
  return <Ctx.Provider value={{preferences,save:async(p)=>{await mutation.mutateAsync(p)},canEdit,saving:mutation.isPending,error:query.error ?? mutation.error,ready}}>{children}</Ctx.Provider>;
}
export const useUIPreferences = () => useContext(Ctx);
export function useUIMode(){const v=useUIPreferences();return {...v,editing:v.canEdit&&v.preferences.mode==='editing'};}
export const useLocale = () => useSyncExternalStore(subscribeLocale,getLocale,getLocale);
/** useUIOption reads one interface option with its default. */
export function useUIOption<K extends keyof UIOptions>(key:K, fallback:NonNullable<UIOptions[K]>):NonNullable<UIOptions[K]> {
  const v=useUIPreferences().preferences.options?.[key];
  return (v ?? fallback) as NonNullable<UIOptions[K]>;
}
