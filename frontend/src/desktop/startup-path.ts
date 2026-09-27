// Keep one-time creation text out of the saved startup location.
export function startupPath(pathname: string, search: string): string {
 if (pathname === '/account/invitation' || pathname === '/account/login') return '/';
 const query = new URLSearchParams(search);
 query.delete('brief');
 const suffix = query.toString();
 const target = pathname + (suffix ? '?' + suffix : '');
 return new TextEncoder().encode(target).length <= 500 ? target : pathname;
}
