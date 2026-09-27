'use client';
import { useState, type FormEvent } from 'react';
import { changePassword } from '@/lib/auth-api';
import { useAuth } from '@/components/auth/AuthProvider';
import { PageHeader } from '@/components/ui/Workspace';
export default function AccountPage() {
 const { session, signOut, refresh } = useAuth();
 const [current, setCurrent] = useState('');
 const [password, setPassword] = useState('');
 const [repeat, setRepeat] = useState('');
 const [busy, setBusy] = useState(false);
 const [message, setMessage] = useState('');
 const [error, setError] = useState('');
 async function submit(e: FormEvent) {
  e.preventDefault(); setError(''); setMessage('');
  if (password !== repeat) { setError('两次输入的新密码不一致'); return; }
  setBusy(true);
  try { await changePassword(current, password); setCurrent(''); setPassword(''); setRepeat(''); setMessage('密码已更新。请使用新密码重新登录。'); await refresh(); }
  catch (e) { setError(e instanceof Error ? e.message : '修改失败'); }
  finally { setBusy(false); }
 }
 async function exit() {
  setBusy(true); setError('');
  try { await signOut(); } catch (e) { setError(e instanceof Error ? e.message : '退出失败，请重试'); setBusy(false); }
 }
 return <div className="af-page max-w-3xl"><PageHeader eyebrow="工作区 / 账号" title="我的账号" description="管理当前服务的登录身份与密码。" />
  {session?.mode === 'local' ? <p className="af-hint">当前为本机可信模式，无需账号登录。向团队提供服务前，请由部署管理员启用共享模式并创建首位管理员。</p> : <><section className="af-panel space-y-4 p-6"><h2 className="text-lg font-semibold">{session?.user?.display_name}</h2><p>{session?.user?.email}</p><p className="af-hint">{session?.user?.role === 'admin' ? '工作区管理员' : '团队成员'} · 团队共享题库，个人任务单独管理</p><button className="forge-btn-secondary" disabled={busy} onClick={() => void exit()}>退出当前账号</button></section>
  <form className="mt-6 space-y-4 rounded-xl border border-[var(--dl)] p-6" onSubmit={submit}><h2 className="text-lg font-semibold">修改密码</h2><label className="af-field"><span>当前密码</span><input className="forge-input" type="password" required autoComplete="current-password" value={current} onChange={e => setCurrent(e.target.value)} /></label><label className="af-field"><span>新密码（至少 12 位）</span><input className="forge-input" type="password" required autoComplete="new-password" minLength={12} maxLength={128} value={password} onChange={e => setPassword(e.target.value)} /></label><label className="af-field"><span>再次输入新密码</span><input className="forge-input" type="password" required autoComplete="new-password" value={repeat} onChange={e => setRepeat(e.target.value)} /></label><button className="forge-btn-primary" disabled={busy}>{busy ? '正在保存…' : '更新密码'}</button></form></>}
  {message && <p role="status" className="mt-4 text-sm">{message}</p>}{error && <p role="alert" className="mt-4 text-sm text-red-600">{error}</p>}
 </div>;
}
