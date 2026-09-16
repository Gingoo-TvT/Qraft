// Review credentials belong to the current tab only and never enter API URLs.
const SESSION_KEY = 'qraft.rating.review.token';
export function consumeReviewToken(location: Pick<Location, 'hash' | 'pathname' | 'search'>, history: Pick<History, 'replaceState'>, storage: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>): string {
 const fragment = new URLSearchParams(location.hash.replace(/^#/, ''));
 const supplied = fragment.get('token')?.trim() ?? '';
 if (fragment.has('token')) {
  // Remove credentials before any request, including when tab storage is disabled.
  history.replaceState(null, '', location.pathname + location.search);
  if (supplied) { try { storage.setItem(SESSION_KEY, supplied); } catch { /* Keep only in memory. */ } }
  else { try { storage.removeItem(SESSION_KEY); } catch { /* No persisted token. */ } }
  return supplied;
 }
 try { return storage.getItem(SESSION_KEY) ?? ''; } catch { return ''; }
}
export function saveReviewToken(token: string, storage: Pick<Storage, 'setItem'>) {
 try { storage.setItem(SESSION_KEY, token.trim()); } catch { /* Keep only in memory. */ }
}
export function clearReviewToken(storage: Pick<Storage, 'removeItem'>) {
 try { storage.removeItem(SESSION_KEY); } catch { /* No persisted token. */ }
}
export function reviewRequestInit(token: string, payload?: unknown): RequestInit {
 return {
  method: payload === undefined ? 'GET' : 'POST',
  credentials: 'omit',
  cache: 'no-store',
  referrerPolicy: 'no-referrer',
  headers: { 'Content-Type': 'application/json', 'X-Qraft-Review-Token': token },
  ...(payload === undefined ? {} : { body: JSON.stringify(payload) }),
 };
}

const fallback = new Map<string, string>();
export function reviewSessionStorage(): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> {
 try { return window.sessionStorage; } catch {
  return { getItem: key => fallback.get(key) ?? null, setItem: (key, value) => { fallback.set(key, value); }, removeItem: key => { fallback.delete(key); } };
 }
}
