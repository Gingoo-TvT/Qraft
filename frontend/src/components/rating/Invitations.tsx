'use client';
import { useCallback, useEffect, useState } from 'react';
import { issueInvitation, listInvitations, ratingError, revokeInvitation } from '@/lib/rating-api';
import { desktopRuntime } from '@/lib/desktop-runtime';
import type { Invitation, IssuedInvitation } from '@/lib/rating-types';
import { Field, Message, Panel, dateLabel } from './Primitives';
export default function Invitations({ problemID, subjectHash }: { problemID: string; subjectHash: string }) {
 const [invitations, setInvitations] = useState<Invitation[]>([]);
 const [key, setKey] = useState('');
 const [minutes, setMinutes] = useState(60);
 const [context, setContext] = useState('practice');
 const [days, setDays] = useState(14);
 const [issued, setIssued] = useState<IssuedInvitation | null>(null);
 const [error, setError] = useState('');
 const [notice, setNotice] = useState('');
 const [loading, setLoading] = useState(false);
 const load = useCallback(async () => { try { setInvitations(await listInvitations(problemID) ?? []); } catch (e) { setError(ratingError(e)); } }, [problemID]);
 useEffect(() => { void load(); }, [load]);
 async function issue(event: React.FormEvent) {
  event.preventDefault(); setLoading(true); setError(''); setIssued(null);
  try { setIssued(await issueInvitation(problemID, { reviewer_key: key.trim(), window_minutes: minutes, context, expires_in_days: days })); await load(); } catch (e) { setError(ratingError(e)); } finally { setLoading(false); }
 }
 async function revoke(id: string) {
  setLoading(true); setError('');
  try { await revokeInvitation(problemID, id); if (issued?.invitation.id === id) setIssued(null); await load(); } catch (e) { setError(ratingError(e)); } finally { setLoading(false); }
 }
 function shareText() {
  if (!issued) return '';
  const desktop = desktopRuntime();
  if (desktop) return 'Qraft 受邀评价\n服务地址：' + (desktop.state.service_url || desktop.state.config.server_url || '请向邀请人索取可访问的服务地址') + '\n在服务网页 /rating/review 中粘贴评价令牌：\n' + issued.token;
  return window.location.origin + '/rating/review#token=' + encodeURIComponent(issued.token);
 }
 return <Panel title="邀请独立评价" description="管理员按真实评价人稳定编号发放，一人一号。令牌只授权本题此版本，重复评价会修订原记录。">
  {error && <Message error>{error}</Message>}
  <form className="space-y-4" onSubmit={issue}><div className="grid gap-4 sm:grid-cols-2">
   <Field label="评价人稳定编号" hint="同一个人跨题目和重复邀请须使用相同编号，勿使用公开页面自由填写的姓名计人数。"><input className="forge-input" required maxLength={200} value={key} onChange={e => setKey(e.target.value)} /></Field>
   <Field label="约定观察窗口 T（分钟）"><input className="forge-input" type="number" min={1} max={1440} required value={minutes} onChange={e => setMinutes(Number(e.target.value))} /></Field>
   <Field label="作答情境"><select className="forge-input" value={context} onChange={e => setContext(e.target.value)}><option value="practice">个人练习</option><option value="contest">个人比赛</option></select></Field>
   <Field label="邀请有效期（天）"><input className="forge-input" type="number" min={1} max={90} value={days} onChange={e => setDays(Number(e.target.value))} required /></Field>
  </div><button className="forge-btn-primary" disabled={loading} type="submit">创建专属评价邀请</button></form>
  {issued && <Message><p className="font-medium">邀请已生成，凭据仅本次显示。请单独交给对应评价人。</p><p className="mt-2">桌面端复制的信息包含服务地址和令牌；异地评价者需要可访问的服务地址。</p><textarea className="forge-input mt-3 min-h-24 text-xs" aria-label="专属评价邀请" readOnly value={shareText()} /><button className="forge-btn-secondary mt-3" onClick={() => { void navigator.clipboard.writeText(shareText()).then(() => setNotice('已复制邀请')).catch(() => setNotice('复制失败，请手动选中上方内容复制。')); }}>复制邀请</button></Message>}
  {notice && <p role="status" className="text-sm">{notice}</p>}
  <ul className="divide-y divide-[var(--dl)]">{invitations.map(invitation => {
   const inactive = Boolean(invitation.revoked_at) || Date.parse(invitation.expires_at) < Date.now();
   return <li key={invitation.id} className="flex flex-wrap items-center justify-between gap-3 py-3"><div><p className="text-sm font-medium">{invitation.reviewer_key}</p><p className="text-xs text-[var(--dm)]">{invitation.window_minutes} 分钟 · {invitation.context === 'contest' ? '比赛' : '练习'} · 截止 {dateLabel(invitation.expires_at)}{invitation.subject_hash !== subjectHash ? ' · 旧题目版本' : ''}{invitation.revoked_at ? ' · 已撤销' : inactive ? ' · 已过期' : ''}</p></div>{!inactive && <button className="forge-btn-secondary" disabled={loading} onClick={() => void revoke(invitation.id)}>撤销</button>}</li>;
  })}</ul>
 </Panel>;
}
