import { desktopRuntime } from './desktop-runtime';

export type AccountUser = { id: string; email: string; display_name: string; role: 'admin' | 'member'; disabled: boolean };
export type AuthSession = { mode: 'local' | 'shared'; authenticated: boolean; user?: AccountUser; csrf_token?: string; setup_required?: boolean; legacy?: boolean };
let session: AuthSession | null = null;
let availability: boolean | undefined;
export const SESSION_CHANGED = 'qraft:session-changed';
export const SESSION_EXPIRED = 'qraft:session-expired';
export function currentSession() { return session; }
export function setServiceAvailability(value: boolean) { availability = value; }
export function serviceIsSelected() {
 const desktop = desktopRuntime();
 return !desktop || (availability ?? (desktop.state.configured && Boolean(desktop.state.service_url)));
}
export function setSession(value: AuthSession | null) {
 session = value;
 if (typeof window !== 'undefined') window.dispatchEvent(new Event(SESSION_CHANGED));
}
export function sessionHeaders(method = 'GET'): Record<string, string> {
 const headers: Record<string, string> = { 'X-Qraft-Client': '1' };
 if (!['GET', 'HEAD', 'OPTIONS'].includes(method.toUpperCase()) && session?.csrf_token) headers['X-CSRF-Token'] = session.csrf_token;
 return headers;
}
export function expireSession() {
 if (session?.authenticated) {
  session = { mode: session.mode, authenticated: false };
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(SESSION_EXPIRED));
 }
}
export async function serviceFetch(url: string, init: RequestInit = {}, anonymous = false, authRequest = false) {
 if (!serviceIsSelected()) throw new Error('请先选择并保存服务连接，等待本地后端启动后重试。');
 const headers = new Headers(init.headers);
 headers.delete('Authorization');
 const desktop = desktopRuntime();
 if (desktop?.state.service_url) headers.set('X-Qraft-Service', desktop.state.service_url);
 if (anonymous) { headers.delete('X-CSRF-Token'); headers.delete('X-Actor'); }
 if (!anonymous) for (const [key, value] of Object.entries(sessionHeaders(init.method))) headers.set(key, value);
 const response = await fetch(url, { ...init, headers, credentials: anonymous ? 'omit' : 'include', cache: 'no-store', referrerPolicy: 'no-referrer' });
 if (!anonymous && !authRequest && response.status === 401) expireSession();
 return response;
}
export function isAdminRoute(path: string) {
 return ['/settings', '/embedding', '/integration', '/admin', '/rating', '/quizzes/import', '/problems/quarantine'].some(prefix => path === prefix || path.startsWith(prefix + '/')) ||
 /^\/problems\/[^/]+\/edit$/.test(path);
}
export function publicAccountRoute(path: string) { return path === '/account/invitation' || path === '/account/login'; }

export function managedLegacyServiceAllowed() {
 const desktop = desktopRuntime();
 if (!desktop?.state.configured || desktop.state.config.mode !== 'local') return false;
 try {
  const url = new URL(desktop.state.service_url);
  return ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname) && url.protocol === 'http:' && !url.username && !url.password && (url.pathname === '/' || url.pathname === '');
 } catch { return false; }
}
