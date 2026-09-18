'use client';
import { Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { RefreshCw, Scale } from 'lucide-react';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { stateLabel } from '@/components/rating/labels';
import { Message, Panel } from '@/components/rating/Primitives';
import Anchors from '@/components/rating/Anchors';
import Invitations from '@/components/rating/Invitations';
import Decisions from '@/components/rating/Decisions';
import { HumanFeedback, Overview, Paths, Verification } from '@/components/rating/Report';
import { listProblems } from '@/lib/api';
import { cancelAssessment, getAssessment, getRatingWorkspace, ratingError, startAssessment } from '@/lib/rating-api';
import type { Workspace } from '@/lib/rating-types';
import type { Problem } from '@/lib/types';
const TABS = { overview: '概览', paths: '解法与 KC', verification: '验证与误区', human: '人类反馈', decisions: '调整记录' };
function RatingWorkspacePage() {
 const params = useSearchParams();
 const router = useRouter();
 const problemID = params.get('problem') ?? '';
 const assessmentID = params.get('assessment') ?? '';
 const [selectedReport, setSelectedReport] = useState('');
 const [workspace, setWorkspace] = useState<Workspace | null>(null);
 const [problems, setProblems] = useState<Problem[]>([]);
 const [search, setSearch] = useState('');
 const [tab, setTab] = useState<keyof typeof TABS>('overview');
 const [showAnchors, setShowAnchors] = useState(false);
 const [error, setError] = useState('');
 const [loading, setLoading] = useState(false);
 const [busy, setBusy] = useState(false);
 const currentProblem = useRef(problemID);
 currentProblem.current = problemID;
 const load = useCallback(async () => {
  if (!problemID) return;
  setLoading(true);
  try { const next = await getRatingWorkspace(problemID); if (currentProblem.current === problemID) { setWorkspace(next); setError(''); } }
  catch (e) { if (currentProblem.current === problemID) setError(ratingError(e)); }
  finally { if (currentProblem.current === problemID) setLoading(false); }
 }, [problemID]);
 useEffect(() => {
  setWorkspace(null); setError(''); setTab('overview'); setSelectedReport('');
  if (problemID) void load();
 }, [problemID, load]);
 useEffect(() => {
  if (!assessmentID || problemID) return;
  let active = true;
  void getAssessment(assessmentID).then(item => { if (active) router.replace('/rating?problem=' + encodeURIComponent(item.problem_id)); }).catch(e => { if (active) setError(ratingError(e)); });
  return () => { active = false; };
 }, [assessmentID, problemID, router]);
 useEffect(() => {
  if (problemID || showAnchors || assessmentID) return;
  let active = true; setLoading(true);
  const timer = window.setTimeout(() => {
   void listProblems({ page: 1, size: 50, search: search.trim() || undefined }).then(response => { if (active) { setProblems(response.data ?? []); setError(''); } }).catch(e => { if (active) setError(ratingError(e)); }).finally(() => { if (active) setLoading(false); });
  }, 250);
  return () => { active = false; window.clearTimeout(timer); };
 }, [problemID, search, showAnchors, assessmentID]);
 const running = workspace?.assessments.find(item => ['queued', 'pending', 'running', 'cancelling', 'cancel_requested'].includes(item.status));
 useEffect(() => {
  if (!running) return;
  const timer = window.setInterval(() => { void load(); }, 4000);
  return () => window.clearInterval(timer);
 }, [running, load]);
 async function start() {
  setBusy(true); setError('');
  try { await startAssessment(problemID); await load(); } catch (e) { setError(ratingError(e)); } finally { setBusy(false); }
 }
 async function cancel() {
  if (!running) return; setBusy(true); setError('');
  try { await cancelAssessment(running.id); await load(); } catch (e) { setError(ratingError(e)); } finally { setBusy(false); }
 }
 const assessment = workspace?.assessments.find(item => item.id === selectedReport && item.report) ?? workspace?.assessments.find(item => item.report && !item.stale) ?? workspace?.assessments.find(item => item.report);
 return <div className="af-page space-y-6">
  <PageHeader eyebrow="证据与校准" title="题目评估" description="从多条解法和知识组件出发，以可检验的证据与人类作答逐步校准难度。"
   actions={<>{problemID && <Link className="forge-btn-secondary" href="/rating">选择其他题目</Link>}<button className="forge-btn-secondary" onClick={() => setShowAnchors(!showAnchors)}>{showAnchors ? '返回题目评估' : '管理参照题'}</button></>} />
  {error && <Message error>{error}{problemID && <button className="ml-3 underline" onClick={() => void load()}>重新加载</button>}</Message>}
  {showAnchors ? <Anchors /> : !problemID ? <Panel title="选择一道编程题" description="首期评估单道编程题。客观题、整场比赛的时间分配和难度校准暂不混入。">
   <input className="forge-input" aria-label="搜索待评估题目" placeholder="按标题搜索…" value={search} onChange={e => setSearch(e.target.value)} />
   {loading ? <p role="status" className="text-sm text-[var(--dm)]">正在读取题库…</p> : !problems.length ? <EmptyState icon={Scale} title={error ? '暂时无法读取题库' : '没有匹配的编程题'} description="可先创建题目，或从编程题详情进入评估。" action={<Link className="forge-btn-secondary" href="/problems">打开编程题库</Link>} /> : <ul className="divide-y divide-[var(--dl)]">{problems.map(problem => <li key={problem.id} className="flex items-center justify-between gap-4 py-4"><div><Link className="af-link font-medium" href={'/rating?problem=' + encodeURIComponent(problem.id)}>{problem.title}</Link><p className="mt-1 text-xs text-[var(--dm)]">{problem.serial_number} · 出题目标 {problem.difficulty}</p></div><Link className="forge-btn-secondary" href={'/rating?problem=' + encodeURIComponent(problem.id)}>进入评估</Link></li>)}</ul>}
   <p className="text-xs text-[var(--dm)]">最多展示 50 条搜索结果。目标难度不会直接视为正式 rating。</p>
  </Panel> : workspace ? <>
   <section className="flex flex-wrap items-start justify-between gap-4"><div><Link className="af-link text-lg font-semibold" href={'/problems/' + encodeURIComponent(problemID)}>{workspace.subject.title}</Link><p className="mt-1 text-xs text-[var(--dm)]">题目版本 {workspace.subject.hash.slice(0, 12)} · 当前反馈快照 {workspace.feedback_hash.slice(0, 12)}</p></div><div className="flex flex-wrap gap-2"><button className="forge-btn-secondary" aria-label="刷新评估" disabled={loading} onClick={() => void load()}><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新</button>{running ? <button className="forge-btn-secondary" disabled={busy || running.status === 'cancel_requested'} onClick={() => void cancel()}>取消本次评估</button> : <button className="forge-btn-primary" disabled={busy} onClick={() => void start()}>{busy ? '正在提交…' : '启动独立评估'}</button>}</div></section>
   {running && <Message>评估进行中：{stateLabel(running.phase) || '等待执行'}。可以离开此页面，服务端会继续运行；此页每 4 秒刷新状态。</Message>}
   {workspace.assessments.filter(item => item.report).length > 1 && <label className="flex flex-wrap items-center gap-3 text-sm">查看报告<select className="forge-input w-auto" value={assessment?.id ?? ''} onChange={event => setSelectedReport(event.target.value)}>{workspace.assessments.filter(item => item.report).map(item => <option key={item.id} value={item.id}>{new Date(item.created_at).toLocaleString('zh-CN')} · {item.stale ? '旧版本' : '当前版本'}</option>)}</select></label>}
   <nav aria-label="题目评估内容" className="flex flex-wrap gap-2 border-b border-[var(--dl)] pb-3">{Object.entries(TABS).map(([key, label]) => <button key={key} className={tab === key ? 'forge-btn-primary' : 'forge-btn-secondary'} aria-current={tab === key ? 'page' : undefined} onClick={() => setTab(key as keyof typeof TABS)}>{label}</button>)}</nav>
   {tab === 'overview' && <Overview workspace={workspace} assessment={assessment} />}
   {tab === 'paths' && <Paths assessment={assessment} />}
   {tab === 'verification' && <Verification assessment={assessment} />}
   {tab === 'human' && <><HumanFeedback workspace={workspace} /><Invitations key={workspace.subject.hash} problemID={problemID} subjectHash={workspace.subject.hash} /></>}
   {tab === 'decisions' && <Decisions key={problemID} workspace={workspace} onRefresh={load} />}
  </> : !error ? <p role="status">正在读取评估工作区…</p> : null}
 </div>;
}
export default function RatingPage() { return <Suspense fallback={<p role="status">正在打开题目评估…</p>}><RatingWorkspacePage /></Suspense>; }
