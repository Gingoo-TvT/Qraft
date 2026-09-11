import React, { useMemo, useSyncExternalStore } from 'react';
import { desktopRuntime } from '@/lib/desktop-runtime';

function base() { return desktopRuntime()?.ui_base ?? ''; }
export function localHref(path: string) {
 const root = desktopRuntime()?.base;
 if (root && path.startsWith(root + '/')) return path;
 return path.startsWith('/') && !path.startsWith('//') ? base() + path : path;
}
function snapshot() { return window.location.pathname + window.location.search; }
function subscribe(fn: () => void) {
 window.addEventListener('popstate', fn);
 return () => window.removeEventListener('popstate', fn);
}
export function usePathname() {
 const location = useSyncExternalStore(subscribe, snapshot, () => '/');
 const pathname = location.split('?')[0];
 return pathname.startsWith(base() + '/') ? pathname.slice(base().length) : '/';
}
export function navigate(path: string, replace = false) {
 if (!path.startsWith('/') || path.startsWith('//')) throw new Error('无效的工作台页面');
 window.history[replace ? 'replaceState' : 'pushState']({}, '', localHref(path));
 window.dispatchEvent(new PopStateEvent('popstate'));
 document.querySelector('[data-workbench-content]')?.scrollTo(0, 0);
}
export function useRouter() {
 return useMemo(() => ({
  push: (path: string) => navigate(path),
  replace: (path: string) => navigate(path, true),
  back: () => window.history.back(),
  forward: () => window.history.forward(),
  refresh: () => window.location.reload(),
  prefetch: () => {},
 }), []);
}
export function useParams<T extends Record<string, string | string[]> = Record<string, string>>() {
 const pathname = usePathname();
 const segments = pathname.split('/').filter(Boolean);
 return (segments.length > 1 ? { id: decodeURIComponent(segments[1]), token: decodeURIComponent(segments[1]) } : {}) as T;
}
export function useSearchParams() {
 const location = useSyncExternalStore(subscribe, snapshot, () => '/');
 return useMemo(() => new URLSearchParams(location.includes('?') ? location.slice(location.indexOf('?')) : ''), [location]);
}
type URLValue = string | { pathname?: string; query?: Record<string, unknown>; hash?: string };
type LinkProps = Omit<React.AnchorHTMLAttributes<HTMLAnchorElement>, 'href'> & {
 href: URLValue; prefetch?: boolean; replace?: boolean; scroll?: boolean;
};
export default function Link({ href, prefetch: _prefetch, replace, scroll: _scroll, onClick, ...props }: LinkProps) {
 let path = typeof href === 'string' ? href : href.pathname ?? '/';
 if (typeof href !== 'string') {
  const query = new URLSearchParams();
  Object.entries(href.query ?? {}).forEach(([key, value]) => { if (value != null) query.set(key, String(value)); });
  if (query.size) path += '?' + query.toString();
  if (href.hash) path += '#' + href.hash;
 }
 return <a {...props} target={path.startsWith("/") ? undefined : props.target} href={localHref(path)} onClick={event => {
  onClick?.(event);
  if (event.defaultPrevented || event.button !== 0) return;
  if (path.startsWith('/') && !path.startsWith('//')) { event.preventDefault(); navigate(path, replace); }
 }} />;
}
