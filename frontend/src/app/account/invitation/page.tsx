'use client';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import Link from 'next/link';
import { getInvitation, redeemInvitation, type AccountInvitation } from '@/lib/auth-api';
import { desktopRuntime } from '@/lib/desktop-runtime';
import { useAuth } from '@/components/auth/AuthProvider';
export default function InvitationPage() {
 const { enabled, accept } = useAuth();
 const token = useRef('');
 const [inputToken, setInputToken] = useState('');
 const [invitation, setInvitation] = useState<AccountInvitation | null>(null);
 const [password, setPassword] = useState('');
 const [repeat, setRepeat] = useState('');
 const [name, setName] = useState('');
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 const [done, setDone] = useState(false);
 async function load(value: string) {
  if (!value) { setError('请粘贴管理员提供的完整邀请链接或邀请码。'); return; }
  let selected = value.trim();
  try { if (selected.includes('#')) selected = new URL(selected).hash.slice(1); } catch { /* A manually entered token is supported. */ }
  if (selected.startsWith('token=')) selected = new URLSearchParams(selected).get('token') || '';
  token.current = selected; setInputToken(''); setError(''); setBusy(true);
  try { setInvitation(await getInvitation(selected)); }
  catch (e) { setError(e instanceof Error ? e.message : '邀请无效或已过期'); }
  finally { setBusy(false); }
 }
 useEffect(() => {
  const value = new URLSearchParams(window.location.hash.slice(1)).get('token') || '';
  window.history.replaceState(null, '', window.location.pathname + window.location.search);
  if (value) token.current = value;
  if (enabled && token.current) void load(token.current);
  return () => { /* Token remains only in this mounted component, never browser storage. */ };
 }, [enabled]);
 async function submit(event: FormEvent) {
  event.preventDefault(); if (password !== repeat) { setError('两次输入的密码不一致'); return; }
  if (!invitation) return;
  setBusy(true); setError('');
  try {
   const session = await redeemInvitation(invitation.kind, token.current, password, name);
   token.current = ''; setPassword(''); setRepeat(''); setDone(true);
   if (session?.mode && session.authenticated) accept(session);
  } catch (e) { setError(e instanceof Error ? e.message : '无法完成邀请'); }
  finally { setBusy(false); }
 }
 return <section className="mx-auto my-10 w-full max-w-md space-y-5 rounded-2xl border border-[var(--dl)] bg-[var(--dp)] p-7">
  <p className="af-hint">Qraft · 团队邀请</p><h1 className="text-2xl font-semibold">{done ? '账号已准备好' : invitation?.kind === 'reset' ? '重置账号密码' : '加入团队工作区'}</h1>
  {!enabled ? <><p>先在客户端选择并保存邀请所属的服务，再回到此页输入邀请链接。</p><Link className="forge-btn-secondary" href="/desktop/settings">选择服务</Link></> : done ? <><p>邀请已使用。请进入工作台；如需登录，使用刚设置的密码。</p><Link className="forge-btn-primary" href="/">进入工作台</Link></> : invitation ? <form onSubmit={submit} className="space-y-4">
   <p className="break-all text-sm">{invitation.email}</p><p className="af-hint">邀请有效期至 {new Date(invitation.expires_at).toLocaleString('zh-CN')}</p>
   {invitation.kind === 'register' && <label className="af-field"><span>显示名称</span><input className="forge-input" required maxLength={100} value={name} onChange={e => setName(e.target.value)} /></label>}
   <label className="af-field"><span>新密码（至少 12 位）</span><input className="forge-input" required type="password" autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} /></label>
   <label className="af-field"><span>再次输入密码</span><input className="forge-input" required type="password" autoComplete="new-password" value={repeat} onChange={e => setRepeat(e.target.value)} /></label>
   <button className="forge-btn-primary w-full" disabled={busy}>{busy ? '正在保存…' : invitation.kind === 'reset' ? '保存新密码' : '注册并加入'}</button>
  </form> : <form className="space-y-4" onSubmit={event => { event.preventDefault(); void load(inputToken); }}><p className="text-sm text-[var(--dm)]">邀请仅可使用一次。链接无效、过期或已使用时，请联系管理员重新邀请。</p><label className="af-field"><span>邀请链接或邀请码</span><input className="forge-input" type="password" autoComplete="off" required value={inputToken} onChange={e => setInputToken(e.target.value)} /></label><button className="forge-btn-primary" disabled={busy}>{busy ? '正在检查…' : '打开邀请'}</button></form>}
  {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
  <Link className="af-link inline-block text-sm" href="/account/login">已有账号，前往登录</Link>
  {desktopRuntime() && enabled && <Link className="af-link block text-sm" href="/desktop/settings">核对服务连接</Link>}
 </section>;
}
