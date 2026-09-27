'use client';
import { useState, type FormEvent } from 'react';
import Link from 'next/link';
import { ShieldCheck, LogIn } from 'lucide-react';
import { login, bootstrap } from '@/lib/auth-api';
import { desktopRuntime } from '@/lib/desktop-runtime';
import { useAuth } from './AuthProvider';
export default function SignIn() {
 const { session, accept, expired } = useAuth();
 const [email, setEmail] = useState('');
 const [name, setName] = useState('');
 const [password, setPassword] = useState('');
 const [token, setToken] = useState('');
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 const setup = session?.setup_required;
 const service = desktopRuntime()?.state.service_url || (typeof window !== 'undefined' ? window.location.origin : '');
 async function submit(e: FormEvent) {
  e.preventDefault(); setBusy(true); setError('');
  try { accept(setup ? await bootstrap(token, email, password, name) : await login(email, password)); setPassword(''); setToken(''); }
  catch (e) { setError(e instanceof Error ? e.message : '登录失败'); }
  finally { setBusy(false); }
 }
 return <section className="mx-auto my-10 w-full max-w-md rounded-2xl border border-[var(--dl)] bg-[var(--dp)] p-7 shadow-sm" aria-labelledby="account-signin-title">
  <ShieldCheck className="mb-4 text-[var(--da)]" size={30} /><p className="af-hint">Qraft · 题构</p><h1 id="account-signin-title" className="my-2 text-2xl font-semibold">{setup ? '创建首位管理员' : expired ? '登录已过期' : '登录团队工作区'}</h1>
  <p className="af-hint break-all">{service}</p>
  <p className="my-5 text-sm text-[var(--dm)]">{setup ? '使用部署时生成的一次性初始化令牌创建账号。完成后即可邀请团队成员。' : expired ? '重新登录后继续；当前页面尚未提交的内容会保留。' : '使用管理员邀请注册的邮箱和密码登录。没有账号时，请向管理员索取邀请链接。'}</p>
  <form className="space-y-4" onSubmit={submit}>
   {setup && <><label className="af-field"><span>初始化令牌</span><input className="forge-input" type="password" autoComplete="off" required value={token} onChange={e => setToken(e.target.value)} /></label><label className="af-field"><span>显示名称</span><input className="forge-input" maxLength={100} required value={name} onChange={e => setName(e.target.value)} /></label></>}
   <label className="af-field"><span>邮箱</span><input className="forge-input" type="email" autoComplete="username" required value={email} onChange={e => setEmail(e.target.value)} /></label>
   <label className="af-field"><span>密码{setup && '（至少 12 位）'}</span><input className="forge-input" type="password" autoComplete={setup ? 'new-password' : 'current-password'} minLength={setup ? 12 : undefined} maxLength={128} required value={password} onChange={e => setPassword(e.target.value)} /></label>
   {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
   <button className="forge-btn-primary w-full" disabled={busy}><LogIn size={16} />{busy ? '正在验证…' : setup ? '创建管理员并登录' : '登录'}</button>
  </form>
  {desktopRuntime() && <Link className="af-link mt-5 inline-block text-sm" href="/desktop/settings">服务连接与客户端设置</Link>}
 </section>;
}
