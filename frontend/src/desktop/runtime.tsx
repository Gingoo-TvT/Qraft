import { applyTheme, normalizePreferences } from './themes';
import { useAppStore } from '@/stores/appStore';
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { desktopRuntime, type DesktopConfig, type DesktopPreferences, type DesktopState } from '@/lib/desktop-runtime';

export type Connection = { ready: boolean; url: string; release_version: string; problem_sets: boolean; error?: string };
export type ExportRecord = { id: string; name: string; path: string; status: string; bytes: number; total: number; error?: string; created: string };
export function boot() {
 const value = desktopRuntime();
 if (!value) throw new Error('请从 Qraft 桌面客户端打开工作台');
 return value;
}
export async function nativeRequest<T>(path: string, body?: unknown): Promise<T> {
 const headers: Record<string, string> = {};
 if (body !== undefined) headers['Content-Type'] = 'application/json';
 const token = localStorage.getItem('algoforge_token');
 if (path === 'download' && token) headers.Authorization = 'Bearer ' + token;
 const response = await fetch(boot().base + '/native/' + path, {
  method: body === undefined ? 'GET' : 'POST', headers,
  body: body === undefined ? undefined : JSON.stringify(body),
 });
 const result = await response.json();
 if (!response.ok || !result.success) throw new Error(result.error?.message ?? '桌面操作失败');
 return result.data as T;
}
export function serviceURL(state: DesktopState) {
 return state.service_url;
}
type DesktopContextValue = {
 state: DesktopState; preferences: DesktopPreferences; connection: Connection | null; probing: boolean;
 notice: string; setNotice: (message: string) => void;
 updatePreferences: (patch: Partial<DesktopPreferences>) => Promise<boolean>;
 saveConfig: (config: DesktopConfig) => Promise<void>;
 probe: (url?: string) => Promise<void>;
 operation: (action: string, arg?: string) => Promise<void>;
 refreshState: () => Promise<void>;
};
const DesktopContext = createContext<DesktopContextValue | null>(null);

export function DesktopProvider({ children }: { children: React.ReactNode }) {
 const [state, setState] = useState(boot().state);
 const [preferences, setPreferences] = useState(() => normalizePreferences(boot().preferences));
 const [connection, setConnection] = useState<Connection | null>(null);
 const [probing, setProbing] = useState(false);
 const [notice, setNotice] = useState('');
 const { theme: appearanceMode, density: appearanceDensity, color_theme: appearanceColor, custom_themes: customThemes } = preferences;
 const prefs = useRef(preferences);
 const savedPrefs = useRef(preferences);
 const writes = useRef<Promise<unknown>>(Promise.resolve());
 const probingRef = useRef('');
 const probeSequence = useRef(0);
 const endpointRef = useRef(serviceURL(state));
 endpointRef.current = serviceURL(state);
 const refreshState = useCallback(async () => {
  const result = await nativeRequest<{ state: DesktopState }>('state');
  setState(result.state);
 }, []);
 const probe = useCallback(async (url?: string) => {
  const target = url ?? endpointRef.current;
  if (!target) { setConnection(null); setProbing(false); return; }
  if (probingRef.current === target) return;
  const sequence = ++probeSequence.current;
  probingRef.current = target; setProbing(true);
  try {
   const result = await nativeRequest<Connection>('probe', { url: target });
   if (sequence === probeSequence.current && endpointRef.current === target) setConnection(result);
  } catch (error) {
   if (sequence === probeSequence.current && endpointRef.current === target) setConnection({ ready: false, url: target, release_version: '', problem_sets: false, error: String(error instanceof Error ? error.message : error) });
  } finally { if (sequence === probeSequence.current) { probingRef.current = ''; setProbing(false); } }
 }, []);
 useEffect(() => { setConnection(null); void probe(); }, [state.service_url, probe]);
 useEffect(() => {
  const id = window.setInterval(() => { if (!document.hidden && state.configured) void refreshState().then(() => probe()).catch(error => setNotice(error.message)); }, 30000);
  return () => window.clearInterval(id);
 }, [probe, refreshState, state.configured]);
 useEffect(() => {
  if (!state.operation.busy) return;
  const id = window.setInterval(() => { void refreshState().catch(error => setNotice(error.message)); }, 800);
  return () => window.clearInterval(id);
 }, [state.operation.busy, refreshState]);
 useEffect(() => {
  if (state.operation.updated && !state.operation.busy) void probe();
 }, [state.operation.updated, state.operation.busy, probe]);
 useEffect(() => {
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const apply = () => {
   const dark = applyTheme({ theme: appearanceMode, density: appearanceDensity, color_theme: appearanceColor, custom_themes: customThemes }, media.matches);
   useAppStore.setState({ darkMode: dark });
  };
  apply(); media.addEventListener('change', apply);
  return () => media.removeEventListener('change', apply);
 }, [appearanceMode, appearanceDensity, appearanceColor, customThemes]);
 const updatePreferences = useCallback((patch: Partial<DesktopPreferences>): Promise<boolean> => {
  const next = normalizePreferences({ ...prefs.current, ...patch }); prefs.current = next; setPreferences(next);
  const pending = writes.current.catch(() => {}).then(async () => {
   try {
    await nativeRequest('preferences', next);
    savedPrefs.current = next;
    return true;
   } catch (error) {
    if (prefs.current === next) { prefs.current = savedPrefs.current; setPreferences(savedPrefs.current); }
    setNotice(error instanceof Error ? error.message : String(error));
    return false;
   }
  });
  writes.current = pending;
  return pending;
 }, []);
 async function saveConfig(config: DesktopConfig) {
  await nativeRequest('config', config);
  // A token from one service must never be sent to a newly selected service.
  localStorage.removeItem('algoforge_token');
  // Start setup where the user needs to act; the reload also discards old service caches.
  if (!state.configured || config.mode === 'local') {
   window.history.replaceState({}, '', boot().ui_base + (config.mode === 'local' ? '/desktop/settings?tab=local' : '/'));
  }
  window.location.reload();
 }
 async function operation(action: string, arg = '') {
  await nativeRequest('operation', { action, arg });
  await refreshState();
 }
 return <DesktopContext.Provider value={{ state, preferences, connection, probing, notice, setNotice, updatePreferences, saveConfig, probe, operation, refreshState }}>{children}</DesktopContext.Provider>;
}
export function useDesktop() {
 const value = useContext(DesktopContext);
 if (!value) throw new Error('桌面工作台尚未初始化');
 return value;
}
