'use client';
import { useRef, type ReactNode } from 'react';
import { usePathname } from 'next/navigation';
import Link from 'next/link';
import { desktopRuntime } from '@/lib/desktop-runtime';
import { isAdminRoute, publicAccountRoute } from '@/lib/auth-session';
import { useAuth } from './AuthProvider';
import SignIn from './SignIn';
export default function AuthBoundary({ children }: { children: ReactNode }) {
 const path = usePathname();
 const auth = useAuth();
 const rendered = useRef(false);
 const publicPage = path === '/rating/review' || path === '/annotate' || path.startsWith('/annotate/') || publicAccountRoute(path) || path.startsWith('/desktop/');
 if (publicPage || !auth.enabled) return children;
 const allowed = auth.session?.mode === 'local' || auth.session?.authenticated;
 if (allowed) rendered.current = true;
 if (auth.error && !rendered.current) return <section className="mx-auto my-10 max-w-lg space-y-4 rounded-xl border border-[var(--dl)] p-6"><h1 className="text-xl font-semibold">暂时无法连接工作区</h1><p role="alert">{auth.error}</p><button className="forge-btn-secondary" onClick={() => void auth.refresh()}>重试</button>{desktopRuntime() && <Link className="af-link block" href="/desktop/settings">检查服务连接</Link>}</section>;
 if (auth.checking && !rendered.current) return <p role="status" className="p-8 text-[var(--dm)]">正在检查工作区登录状态…</p>;
 if (allowed && !auth.isAdmin && isAdminRoute(path)) return <section className="mx-auto my-10 max-w-lg space-y-4 rounded-xl border border-[var(--dl)] p-6"><h1 className="text-xl font-semibold">此功能由管理员管理</h1><p className="text-sm text-[var(--dm)]">你可以使用共享题库并管理自己的创作任务。工作区配置、共享内容修改和用户管理请联系管理员。</p><Link className="forge-btn-secondary" href="/">返回工作台</Link></section>;
 return <><div style={{ display: allowed ? undefined : 'none' }} aria-hidden={!allowed || undefined}>{rendered.current ? children : null}</div>{!allowed && <div role={auth.expired ? 'dialog' : undefined} aria-modal={auth.expired ? true : undefined} aria-label={auth.expired ? '重新登录' : undefined}><SignIn /></div>}{auth.session?.legacy && <p role="status" className="border-b border-[var(--dl)] px-5 py-2 text-xs text-[var(--dm)]">当前服务尚未提供账号认证，仅适用于可信使用者。团队服务请先升级后端。</p>}</>;
}
