'use client';
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { usePathname } from 'next/navigation';
import { getIntegrationCapabilities } from '@/lib/api';
import { getSession, logout } from '@/lib/auth-api';
import { currentSession, managedLegacyServiceAllowed, SESSION_EXPIRED, setSession, setServiceAvailability, type AuthSession } from '@/lib/auth-session';
type AuthContextValue = { session: AuthSession | null; checking: boolean; error: string; expired: boolean; enabled: boolean; isAdmin: boolean; accept: (session: AuthSession) => void; refresh: () => Promise<void>; signOut: () => Promise<void> };
const Context = createContext<AuthContextValue | null>(null);
export function AuthProvider({ children, enabled = true }: { children: ReactNode; enabled?: boolean }) {
 const path = usePathname();
 const anonymousPage = path === '/rating/review' || path.startsWith('/annotate') || path === '/account/invitation';
 const [session, update] = useState<AuthSession | null>(null);
 const [checking, setChecking] = useState(enabled);
 const [error, setError] = useState('');
 const [expired, setExpired] = useState(false);
 const identity = useRef<string | undefined>();
 const sequence = useRef(0);
 const accept = useCallback((next: AuthSession) => {
  // Changing accounts must not preserve another person's task state or cache.
  if (identity.current && next.user && identity.current !== next.user.id) { setSession(next); window.location.reload(); return; }
  if (next.user) identity.current = next.user.id;
  setSession(next); update(next); setExpired(false); setError(''); setChecking(false);
 }, []);
 const refresh = useCallback(async () => {
  if (!enabled || anonymousPage) { setChecking(false); return; }
  const request = ++sequence.current; setChecking(true); setError('');
  try { const result = await getSession(); if (request === sequence.current) accept(result); }
  catch (e) {
   if (request !== sequence.current) return;
   if ((e as { status?: number }).status === 404) {
    if (managedLegacyServiceAllowed()) {
     try {
      const capability = await getIntegrationCapabilities();
      if (request === sequence.current) {
       if (capability.release_version) accept({ mode: 'local', authenticated: false, legacy: true });
       else setError('本地服务版本信息缺失，请升级匹配后端。');
      }
     } catch { if (request === sequence.current) setError('本地服务能力校验失败，请检查或升级匹配后端。'); }
    } else setError('该服务版本不支持账号登录，请升级匹配后端。客户端设置仍可使用。');
   } else setError(e instanceof Error ? e.message : '无法检查登录状态');
  } finally { if (request === sequence.current) setChecking(false); }
 }, [enabled, anonymousPage, accept]);
 const invalidate = useCallback(() => { ++sequence.current; }, []);
 useEffect(() => {
  setServiceAvailability(enabled);
  if (enabled) void refresh();
  else { ++sequence.current; setSession(null); update(null); setChecking(false); }
  return invalidate;
 }, [enabled, refresh, invalidate]);
 useEffect(() => {
  const expire = () => { update(currentSession()); setExpired(true); setChecking(false); };
  window.addEventListener(SESSION_EXPIRED, expire);
  return () => window.removeEventListener(SESSION_EXPIRED, expire);
 }, []);
 const signOut = useCallback(async () => {
  await logout();
  setSession(null);
  // Reload only after server-side revocation, discarding mounted pages and caches.
  window.location.reload();
 }, []);
 return <Context.Provider value={{ session, checking, error, expired, enabled, isAdmin: !enabled || session?.mode === 'local' || session?.user?.role === 'admin', accept, refresh, signOut }}>{children}</Context.Provider>;
}
export function useAuth() {
 const value = useContext(Context);
 if (!value) throw new Error('账号状态尚未初始化');
 return value;
}
