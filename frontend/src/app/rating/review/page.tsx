'use client';
import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import ReviewProblem from '@/components/rating/ReviewProblem';
import { PageHeader } from '@/components/ui/Workspace';
import { Field, Message, Panel } from '@/components/rating/Primitives';
import { clearReviewToken, consumeReviewToken, reviewSessionStorage, saveReviewToken } from '@/components/rating/review-session';
import { emptyFeedback, serializeFeedback, validateFeedback } from '@/components/rating/review-form';
import { getReviewTask, reviewError, submitRatingFeedback } from '@/lib/rating-api';
import { desktopRuntime } from '@/lib/desktop-runtime';
import type { FeedbackInput, ReviewTask } from '@/lib/rating-types';

const RESULTS = { solved: '已完成', unsolved: '尚未完成（本次观察已结束）', in_progress: '仍在尝试', stopped: '提前停止尝试', not_attempted: '未尝试' };
const HELP = { hint: '提示', editorial: '题解', tags: '标签', ai: 'AI', discussion: '讨论或他人帮助' };
export default function RatingReviewPage() {
 const [token, setToken] = useState('');
 const [entry, setEntry] = useState('');
 const [task, setTask] = useState<ReviewTask | null>(null);
 const [value, setValue] = useState<FeedbackInput>(emptyFeedback);
 const [error, setError] = useState('');
 const [loading, setLoading] = useState(false);
 const [saved, setSaved] = useState(false);
 const [ready, setReady] = useState(true);
 const load = useCallback(async (credential: string) => {
  if (!credential) return;
  setLoading(true); setError(''); setTask(null);
  try { const result = await getReviewTask(credential); setTask(result); setValue(result.feedback ?? emptyFeedback()); setSaved(Boolean(result.feedback)); }
  catch (cause) { setError(reviewError(cause)); } finally { setLoading(false); }
 }, []);
 useEffect(() => {
  const credential = consumeReviewToken(window.location, window.history, reviewSessionStorage());
  setToken(credential);
  const desktop = desktopRuntime();
  if (desktop && !desktop.state.configured) { setReady(false); return; }
  void load(credential);
 }, [load]);
 function patch<K extends keyof FeedbackInput>(key: K, next: FeedbackInput[K]) { setValue(current => ({ ...current, [key]: next })); setSaved(false); }
 async function submit(event: React.FormEvent) {
  event.preventDefault(); if (!task) return;
  const invalid = validateFeedback(value, task.window_minutes);
  if (invalid) { setError(invalid); return; }
  setLoading(true); setError('');
  try { await submitRatingFeedback(token, serializeFeedback(value)); setSaved(true); }
  catch (cause) { setError(reviewError(cause)); } finally { setLoading(false); }
 }
 return <main className="af-page mx-auto max-w-4xl px-5 py-10">
  <PageHeader eyebrow="Qraft · 独立评价" title="记录你的真实作答过程" description="这里隐藏题目分数、标准解、知识组件分析与他人反馈。完成与未完成都能提供有价值的观察。" />
  {!ready ? <Message>请先选择并保存服务连接，再返回此评价入口。<Link className="af-link ml-2" href="/desktop/settings">配置服务</Link></Message> : !token ? <Panel title="打开邀请" description="粘贴邀请人提供的评价令牌；令牌仅在当前浏览器标签页中保留。">
   <form className="space-y-3" onSubmit={event => { event.preventDefault(); const next = entry.trim(); if (!next) return; saveReviewToken(next, reviewSessionStorage()); setToken(next); setEntry(''); void load(next); }}>
    <Field label="评价令牌"><input className="forge-input" type="password" autoComplete="off" value={entry} onChange={event => setEntry(event.target.value)} required /></Field>
    <button className="forge-btn-primary" type="submit">打开评价</button>
   </form>
  </Panel> : null}
  {error && <div className="my-4"><Message error>{error}{token && ready && <button className="ml-3 underline" onClick={() => void load(token)}>重新加载邀请</button>}</Message></div>}
  {loading && !task && <p role="status" className="py-8">正在读取邀请…</p>}
  {task && <div className="space-y-6">
   <ReviewProblem task={task} />
   <form onSubmit={submit} className="space-y-6">
    <Panel title="本次作答" description="如果先独立尝试、后获得帮助，请分别填写时间。提前停止和仍在尝试不会直接计为窗口内失败。">
     <div className="grid gap-4 sm:grid-cols-2">
      <Field label="作答结果"><select className="forge-input" value={value.outcome} onChange={e => patch('outcome', e.target.value)}>{Object.entries(RESULTS).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></Field>
      <Field label="总有效耗时（分钟）"><input className="forge-input" type="number" min={0} value={value.elapsed_minutes} onChange={e => patch('elapsed_minutes', Number(e.target.value))} required /></Field>
      <Field label="首次独立尝试时间（分钟）"><input className="forge-input" type="number" min={0} value={value.independent_minutes} onChange={e => patch('independent_minutes', Number(e.target.value))} required /></Field>
      <div className="space-y-3 self-end pb-2"><label className="flex gap-2 text-sm"><input type="checkbox" checked={value.seen_before} onChange={e => patch('seen_before', e.target.checked)} />以前见过本题或其解法</label><label className="flex gap-2 text-sm"><input type="checkbox" checked={value.observed_full_window} onChange={e => patch('observed_full_window', e.target.checked)} />已完整观察约定的 {task.window_minutes} 分钟</label></div>
     </div>
     <fieldset><legend className="mb-2 text-sm font-medium">作答过程中获得的帮助（可多选）</legend><div className="flex flex-wrap gap-4">{Object.entries(HELP).map(([key, label]) => <label className="flex gap-2 text-sm" key={key}><input type="checkbox" checked={value.assistance.includes(key)} onChange={e => patch('assistance', e.target.checked ? [...value.assistance, key] : value.assistance.filter(item => item !== key))} />{label}</label>)}</div></fieldset>
     {value.assistance.length > 0 && <Field label="首次获得帮助时已用时（分钟）"><input className="forge-input max-w-xs" type="number" min={0} value={value.assistance_after_minutes ?? ''} onChange={e => patch('assistance_after_minutes', e.target.value === '' ? undefined : Number(e.target.value))} required /></Field>}
    </Panel>
    <Panel title="思路与卡点" description="可简短填写，不要求完整题解。路线与卡点会用于后续 AI 分析；留空内容不会被补成推断结果。">
     <Field label="最初尝试的路线"><textarea className="forge-input min-h-20" maxLength={8000} value={value.first_route} onChange={e => patch('first_route', e.target.value)} /></Field>
     <Field label="主要卡点或走过的弯路"><textarea className="forge-input min-h-20" maxLength={8000} value={value.blockers} onChange={e => patch('blockers', e.target.value)} /></Field>
     <Field label="最终使用的路线"><textarea className="forge-input min-h-20" maxLength={8000} value={value.final_route} onChange={e => patch('final_route', e.target.value)} /></Field>
    </Panel>
    <details className="forge-card space-y-4"><summary className="cursor-pointer font-medium">补充信息（选填）</summary><div className="grid gap-4 pt-3 sm:grid-cols-2">
     <Field label="你认为的 CF 风格参考分数" hint="这是主观评价，不会直接改分。"><input className="forge-input" type="number" min={800} max={3500} step={100} value={value.subjective_rating ?? ''} onChange={e => patch('subjective_rating', e.target.value === '' ? undefined : Number(e.target.value))} /></Field>
     <Field label="你的 CF rating（自报）"><input className="forge-input" type="number" min={0} max={5000} value={value.cf_rating ?? ''} onChange={e => patch('cf_rating', e.target.value === '' ? undefined : Number(e.target.value))} /></Field>
     <Field label="CF rating 记录日期" hint="填写自报能力时需一并注明日期。"><input className="forge-input" type="date" value={value.cf_rating_at?.slice(0, 10) ?? ''} onChange={e => patch('cf_rating_at', e.target.value ? e.target.value + 'T00:00:00Z' : undefined)} /></Field>
     <Field label="完成结果来源"><select className="forge-input" value={value.result_source} onChange={e => patch('result_source', e.target.value)}><option value="self_report">本人自报</option><option value="external_link">外部结果链接（未经系统核验）</option></select></Field>
    </div>{value.result_source === 'external_link' && <Field label="结果链接"><input type="url" className="forge-input" value={value.result_url ?? ''} onChange={e => patch('result_url', e.target.value)} /></Field>}
     <Field label="代码"><textarea className="forge-input min-h-40 font-mono" maxLength={131072} value={value.code ?? ''} onChange={e => patch('code', e.target.value)} /></Field>
     <Field label="其他说明"><textarea className="forge-input" maxLength={8000} value={value.notes} onChange={e => patch('notes', e.target.value)} /></Field>
    </details>
    {saved && <Message>评价已保存。再次提交会修订你对本题版本的同一份评价，不会增加评价人数。</Message>}
    <div className="flex flex-wrap gap-3"><button type="submit" className="forge-btn-primary" disabled={loading}>{loading ? '正在保存…' : saved ? '保存修改' : '提交评价'}</button><button type="button" className="forge-btn-secondary" onClick={() => { clearReviewToken(reviewSessionStorage()); setToken(''); setTask(null); setSaved(false); setError(''); }}>结束本次评价</button></div>
   </form>
  </div>}
  {!task && token && <button className="forge-btn-secondary mt-4" onClick={() => { clearReviewToken(reviewSessionStorage()); setToken(''); setError(''); }}>使用其他邀请</button>}
 </main>;
}
