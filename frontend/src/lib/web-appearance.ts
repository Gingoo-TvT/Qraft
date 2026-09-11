import { useCallback, useEffect, useState } from 'react';
import { applyTheme, BUILTIN_THEMES } from '@/desktop/themes';
import { useAppStore } from '@/stores/appStore';

export type WebAppearance = { mode: 'system' | 'light' | 'dark'; colorTheme: string };
const KEY = 'algoforge_web_appearance';
const DEFAULT: WebAppearance = { mode: 'system', colorTheme: 'bay' };
function isMode(value: unknown): value is WebAppearance['mode'] { return value === 'system' || value === 'light' || value === 'dark'; }
export function readWebAppearance(): WebAppearance {
 try {
  const raw = localStorage.getItem(KEY);
  if (raw) {
   const saved = JSON.parse(raw);
   if (saved.version === 1 && isMode(saved.mode) && BUILTIN_THEMES.some(theme => theme.id === saved.colorTheme)) {
    return { mode: saved.mode, colorTheme: saved.colorTheme };
   }
  }
  const legacy = localStorage.getItem('algoforge_theme');
  if (isMode(legacy)) return { ...DEFAULT, mode: legacy };
  const legacyDark = localStorage.getItem('algoforge_dark_mode');
  if (legacyDark === 'true' || legacyDark === 'false') return { ...DEFAULT, mode: legacyDark === 'true' ? 'dark' : 'light' };
 } catch { /* A restricted profile still gets a usable theme for this session. */ }
 return { ...DEFAULT };
}
export function useWebAppearance() {
 const [appearance, setAppearance] = useState<WebAppearance>(DEFAULT);
 const [ready, setReady] = useState(false);
 const [storageError, setStorageError] = useState(false);
 useEffect(() => {
  setAppearance(readWebAppearance()); setReady(true);
  const sync = (event: StorageEvent) => {
   if (event.key === KEY || event.key === null) setAppearance(readWebAppearance());
  };
  window.addEventListener('storage', sync);
  return () => window.removeEventListener('storage', sync);
 }, []);
 useEffect(() => {
  if (!ready) return;
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  document.documentElement.dataset.webWorkspace = 'true';
  const apply = () => {
   const dark = applyTheme({ theme: appearance.mode, color_theme: appearance.colorTheme, density: 'comfortable', custom_themes: [] }, media.matches);
   useAppStore.setState({ darkMode: dark });
  };
  apply(); media.addEventListener('change', apply);
  return () => media.removeEventListener('change', apply);
 }, [appearance, ready]);
 const changeAppearance = useCallback((patch: Partial<WebAppearance>) => {
  const next = { ...appearance, ...patch };
  if (!isMode(next.mode) || !BUILTIN_THEMES.some(theme => theme.id === next.colorTheme)) return;
  try {
   localStorage.setItem(KEY, JSON.stringify({ version: 1, ...next }));
   localStorage.setItem('algoforge_theme', next.mode);
   setStorageError(false);
  } catch { setStorageError(true); }
  setAppearance(next);
 }, [appearance]);
 return { appearance, changeAppearance, storageError };
}
