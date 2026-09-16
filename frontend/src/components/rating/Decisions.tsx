'use client';
import { useState } from 'react';
import { calibrateRating, decideRating, ratingError } from '@/lib/rating-api';
import type { Workspace } from '@/lib/rating-types';
import { BulletList, Field, Message, Panel, dateLabel } from './Primitives';
const ACTIONS: Record<string, string> = { accept: '接受建议', modify: '手动确认分数', reject: '拒绝', defer: '暂缓' };
export default function Decisions({ workspace, onRefresh }: { workspace: Workspace; onRefresh: () => Promise<void> }) {
 const [action, setAction] = useState('defer');
 const [score, setScore] = useState('');
 const [reason, setReason] = useState('');
 const [source, setSource] = useState('assessment');
 const [busy, setBusy] = useState(false);
 const [error, setError] = useState('');
 const assessment = workspace.assessments.find(item => item.status === 'completed' && item.report && !item.stale && item.subject.hash === workspace.subject.hash);
 const calibration = workspace.calibrations.find(item => item.subject_hash === workspace.subject.hash && item.feedback_hash === workspace.feedback_hash);
 const proposal = source === 'assessment' ? assessment?.report?.estimate.representative : calibration?.suggested_rating;
 const invalidProblem = source === 'assessment' && ['invalid', 'blocked'].includes(assessment?.report?.validity ?? '');
 const canAccept = proposal !== undefined && (source === 'calibration' || (assessment?.report?.validity === 'tested_candidates' && !assessment.report.disagreements?.length));
 const usable = source === 'assessment' ? Boolean(assessment) : Boolean(calibration);
 async function calibrate() {
  setBusy(true); setError('');
  try { await calibrateRating(workspace.subject.problem_id); await onRefresh(); setSource('calibration'); } catch (e) { setError(ratingError(e)); } finally { setBusy(false); }
 }
 async function submit(event: React.FormEvent) {
  event.preventDefault(); setBusy(true); setError('');
  try {
   await decideRating(workspace.subject.problem_id, {
    subject_hash: workspace.subject.hash, feedback_hash: workspace.feedback_hash,
    ...(workspace.official ? { expected_decision_id: workspace.official.decision_id } : {}),
    ...(source === 'assessment' ? { assessment_id: assessment?.id } : { calibration_id: calibration?.id }),
    action, ...(action === 'modify' ? { rating: Number(score) } : action === 'accept' && proposal !== undefined ? { rating: proposal } : {}), reason: reason.trim(),
   });
   setReason(''); await onRefresh();
  } catch (e) { setError(ratingError(e)); } finally { setBusy(false); }
 }
 return <div className="space-y-6">
  <Panel title="人类反馈复核" description="30 名去重有效评价者只是试点复核门槛。仍需检查人群覆盖、作答条件、独立性与不确定性，系统不会自动改分。">
   <p className="text-sm">当前 {workspace.human.effective_reviewers} / {workspace.human.review_threshold} 名有效评价者 · {workspace.human.review_triggered ? '已达到复核人数门槛' : '尚未达到人数门槛'}</p>
   <BulletList items={workspace.human.limitations} />
   <button className="forge-btn-secondary" disabled={busy} onClick={() => void calibrate()}>汇总当前反馈并生成复核建议</button>
   {calibration && <div className="rounded-lg bg-[var(--ds)] p-4"><p className="font-medium">{calibration.suggested_rating === undefined ? '尚不足以映射成分数' : '建议分数 ' + calibration.suggested_rating}</p><BulletList items={calibration.reasons} /></div>}
  </Panel>
  <Panel title="管理员确认" description="正式 rating 独立保存，不覆盖原出题目标。拒绝和暂缓也会留下理由与证据版本。">
   {error && <Message error>{error}</Message>}
   {!assessment && !calibration && <Message>当前版本尚无可确认的评估或反馈快照。旧版本报告可查阅，但不能用于当前正式评分。</Message>}
   {invalidProblem && <Message>已发现题目有效性问题，修题前不能确认正式分数；可以拒绝或暂缓。</Message>}
   {source === 'assessment' && assessment && !canAccept && !invalidProblem && <Message>仍有未解决分歧或验证限制，不能直接接受模型建议；请先审阅证据，再手动确认或暂缓。</Message>}
   <form className="space-y-4" onSubmit={submit}><div className="grid gap-4 sm:grid-cols-2">
    <Field label="本次依据"><select className="forge-input" value={source} onChange={e => { setSource(e.target.value); setAction('defer'); }}><option value="assessment">当前版本的模型与验证评估</option><option value="calibration">当前人类反馈快照</option></select></Field>
    <Field label="决定"><select className="forge-input" value={action} onChange={e => setAction(e.target.value)}>{Object.entries(ACTIONS).map(([key, label]) => <option key={key} value={key} disabled={(key === 'accept' && !canAccept) || (invalidProblem && (key === 'accept' || key === 'modify'))}>{label}</option>)}</select></Field>
   </div>
   <p className="text-sm text-[var(--dm)]">当前正式值：{workspace.official ? workspace.official.rating + (workspace.official.stale ? '（旧版本）' : '') : '暂未确认'} · 选定证据建议：{proposal ?? '无可靠数值，仅可人工判断或暂缓'}</p>
   {action === 'modify' && <Field label="管理员确认的 CF 风格参考 rating"><input className="forge-input max-w-xs" type="number" min={800} max={3500} step={100} value={score} onChange={e => setScore(e.target.value)} required /></Field>}
   <Field label="决定理由"><textarea className="forge-input min-h-24" required maxLength={8000} value={reason} onChange={e => setReason(e.target.value)} placeholder="说明采用的证据、人群与条件，以及仍存在的限制。" /></Field>
   <button className="forge-btn-primary" type="submit" disabled={busy || !usable || (action === 'accept' && !canAccept) || (invalidProblem && (action === 'accept' || action === 'modify'))}>{busy ? '正在保存…' : '记录管理员决定'}</button></form>
  </Panel>
  <Panel title="调整记录" description="每次决定保留题目版本、反馈快照与操作者。">
   {workspace.decisions.length ? <ul className="divide-y divide-[var(--dl)]">{workspace.decisions.map(item => <li key={item.id} className="space-y-2 py-4"><div className="flex flex-wrap justify-between gap-2"><strong className="text-sm">{ACTIONS[item.action] ?? item.action}{item.rating !== undefined ? ' · ' + (item.previous_rating ?? '暂定') + ' → ' + item.rating : ''}</strong><time className="text-xs text-[var(--dm)]">{dateLabel(item.created_at)}</time></div><p className="whitespace-pre-wrap text-sm">{item.reason}</p><p className="text-xs text-[var(--dm)]">{item.actor} · {item.subject_hash === workspace.subject.hash ? '当前题目版本' : '旧题目版本'} · 快照 {item.feedback_hash.slice(0, 12)}</p></li>)}</ul> : <p className="text-sm text-[var(--dm)]">还没有管理员决定。</p>}
  </Panel>
 </div>;
}
