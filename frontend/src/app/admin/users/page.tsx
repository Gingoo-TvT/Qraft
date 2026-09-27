'use client';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { issueAccountInvitation, listInvitations, listUsers, revokeAccountInvitation, updateUser, type AccountInvitation } from '@/lib/auth-api';
import { type AccountUser } from '@/lib/auth-session';
import { desktopRuntime } from '@/lib/desktop-runtime';
import { useAuth } from '@/components/auth/AuthProvider';
import { PageHeader } from '@/components/ui/Workspace';
export default function UsersPage() {
 const { session } = useAuth();
 const [users, setUsers] = useState<AccountUser[]>([]);
 const [invitations, setInvitations] = useState<AccountInvitation[]>([]);
 const [email, setEmail] = useState('');
 const [kind, setKind] = useState<'register' | 'reset'>('register');
 const [link, setLink] = useState('');
 const [error, setError] = useState('');
 const [busy, setBusy] = useState(false);
 const [copied, setCopied] = useState(false);
 const load = useCallback(async () => {
  const [a, b] = await Promise.all([listUsers(), listInvitations()]);
  setUsers(a.items ?? []); setInvitations(b.items ?? []);
 }, []);
 useEffect(() => { if (session?.mode !== 'local') void load().catch(e => setError(e.message)); }, [load, session?.mode]);
 async function action(fn: () => Promise<unknown>) { setBusy(true); setError(''); try { await fn(); await load(); } catch (e) { setError(e instanceof Error ? e.message : '操作失败'); } finally { setBusy(false); } }
 async function invite(e: FormEvent) {
  e.preventDefault(); setLink(''); setCopied(false);
  await action(async () => {
   const value = await issueAccountInvitation(email, kind);
   const base = desktopRuntime()?.state.service_url || window.location.origin;
   setLink(base.replace(/\/$/, '') + '/account/invitation#token=' + encodeURIComponent(value.token)); setEmail('');
  });
 }
 if (session?.mode === 'local') return <div className="af-page"><PageHeader title="用户与邀请" description="团队账号仅在共享服务模式启用。" /><p>请在部署配置中启用共享模式，再使用一次性初始化令牌创建管理员。</p></div>;
 return <div className="af-page space-y-6"><PageHeader eyebrow="管理 / 团队" title="用户与邀请" description="邀请团队成员加入共享题库；每位成员仅管理自己的创作任务。" />
  {error && <p role="alert" className="rounded-lg border border-red-300 p-4 text-sm text-red-600">{error}</p>}
  <form className="space-y-4 rounded-xl border border-[var(--dl)] p-6" onSubmit={invite}><h2 className="text-lg font-semibold">创建邀请</h2><div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>邮箱</span><input type="email" className="forge-input" required value={email} onChange={e => setEmail(e.target.value)} /></label><label className="af-field"><span>用途</span><select className="forge-input" value={kind} onChange={e => setKind(e.target.value as 'register' | 'reset')}><option value="register">邀请新成员注册</option><option value="reset">帮助现有账号重置密码</option></select></label></div><p className="af-hint">注册账号默认为成员。邀请链接只显示一次，请私下发送给该邮箱的使用者。</p><button className="forge-btn-primary" disabled={busy}>生成邀请链接</button>
   {link && <div className="space-y-3 rounded-lg bg-[var(--dh)] p-4"><label className="af-field"><span>一次性邀请链接</span><textarea className="forge-input min-h-20" readOnly value={link} onFocus={e => e.target.select()} /></label><button type="button" className="forge-btn-secondary" onClick={() => void navigator.clipboard.writeText(link).then(() => setCopied(true)).catch(() => setError('无法读取剪贴板权限，请选中链接手动复制。'))}>{copied ? '已复制' : '复制链接'}</button></div>}
  </form>
  <section className="overflow-x-auto rounded-xl border border-[var(--dl)]"><h2 className="p-5 text-lg font-semibold">团队用户</h2><table className="forge-table"><thead><tr><th>用户</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>{users.map(user => <tr key={user.id}><td><strong>{user.display_name}</strong><p className="text-xs text-[var(--dm)]">{user.email}</p></td><td>{user.role === 'admin' ? '管理员' : '成员'}</td><td>{user.disabled ? '已禁用' : '正常'}</td><td><div className="flex flex-wrap gap-2"><button className="forge-btn-secondary" disabled={busy || user.id === session?.user?.id} onClick={() => { if (window.confirm((user.disabled ? '恢复' : '禁用') + '账号 ' + user.email + '？禁用会使其会话失效。')) void action(() => updateUser(user.id, { disabled: !user.disabled })); }}>{user.disabled ? '恢复账号' : '禁用账号'}</button><button className="forge-btn-secondary" disabled={busy || user.id === session?.user?.id} onClick={() => { if (window.confirm('将 ' + user.email + '设为' + (user.role === 'admin' ? '成员' : '管理员') + '？')) void action(() => updateUser(user.id, { role: user.role === 'admin' ? 'member' : 'admin' })); }}>{user.role === 'admin' ? '设为成员' : '设为管理员'}</button></div></td></tr>)}</tbody></table></section>
  <section className="overflow-x-auto rounded-xl border border-[var(--dl)]"><h2 className="p-5 text-lg font-semibold">邀请记录</h2><table className="forge-table"><thead><tr><th>邮箱 / 用途</th><th>有效期</th><th>状态</th><th>操作</th></tr></thead><tbody>{invitations.map(item => <tr key={item.id}><td>{item.email}<p className="af-hint">{item.kind === 'register' ? '注册' : '重置密码'}</p></td><td>{new Date(item.expires_at).toLocaleString('zh-CN')}</td><td>{item.revoked_at ? '已撤销' : item.used_at ? '已使用' : new Date(item.expires_at).getTime() < Date.now() ? '已过期' : '待使用'}</td><td>{!item.used_at && !item.revoked_at && <button className="forge-btn-secondary" disabled={busy} onClick={() => void action(() => revokeAccountInvitation(item.id))}>撤销</button>}</td></tr>)}</tbody></table>{!invitations.length && <p className="px-5 pb-5 text-sm text-[var(--dm)]">尚未创建邀请。</p>}</section>
 </div>;
}
