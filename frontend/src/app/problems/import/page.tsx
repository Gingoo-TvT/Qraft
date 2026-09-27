'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ArrowRight, CheckCircle2, ExternalLink, FileText, Link2, Loader2, Upload, Pencil, Trash2 } from 'lucide-react';
import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import { useAuth } from '@/components/auth/AuthProvider';
import { APIError } from '@/lib/api';
import { getProblemImport, importActive, previewProblemSource, startProblemImport, resumeProblemImport, type ImportMode, type ImportReport } from '@/lib/problem-import-api';
import { importFailureSummary } from '@/lib/problem-import-errors';
import { parseSourceURLs, previewSources, resolveSelectedSources, type SourceCandidate } from '@/lib/problem-import-flow';

const labels: Record<string, string> = {
 pending: '等待处理', queued: '等待处理', running: '处理中', imported: '已导入',
 skipped_duplicate: '重复，已跳过', failed: '失败', assessment_failed: '已入库，评估待重试',
 completed: '处理完成', completed_with_errors: '处理完成，部分需处理', cancelled: '已停止',
};

export default function ProblemImportPage() {
 const { isAdmin } = useAuth();
 const [mode, setMode] = useState<ImportMode>('preserve_statement');
 const [sourceTab, setSourceTab] = useState<'links' | 'text'>('links');
 const [urls, setURLs] = useState('');
 const [editingID, setEditingID] = useState('');
 const [manualTitle, setManualTitle] = useState('');
 const [manualStatement, setManualStatement] = useState('');
 const [candidates, setCandidates] = useState<SourceCandidate[]>([]);
 const [warnings, setWarnings] = useState<string[]>([]);
 const [setTitle, setSetTitle] = useState('');
 const [createSet, setCreateSet] = useState(true);
 const [difficulty, setDifficulty] = useState(1200);
 const [busy, setBusy] = useState(false);
 const [busyMessage, setBusyMessage] = useState('');
 const [error, setError] = useState('');
 const [jobID, setJobID] = useState('');
 const [report, setReport] = useState<ImportReport | null>(null);
 const [progressError, setProgressError] = useState('');
 const selectedCount = candidates.filter(item => item.selected).length;
 const running = Boolean(jobID && (!report || importActive(report.status)));

 useEffect(() => {
  const id = new URLSearchParams(window.location.search).get('workflow');
  if (id) setJobID(id);
 }, []);

 useEffect(() => {
  if (!jobID) return;
  let cancelled = false, inFlight = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  async function refresh() {
   if (inFlight) return;
   inFlight = true;
   let again = true;
   try {
    const value = await getProblemImport(jobID);
    if (!cancelled) { setReport(value); setProgressError(''); }
    again = importActive(value.status);
   } catch (cause) {
    if (!cancelled) setProgressError(cause instanceof Error ? cause.message : '进度同步失败，正在重连。');
    if (cause instanceof APIError && [400, 403, 404].includes(cause.status)) again = false;
   } finally {
    inFlight = false;
    if (!cancelled && again) timer = setTimeout(() => { void refresh(); }, 4000);
   }
  }
  void refresh();
  return () => { cancelled = true; if (timer) clearTimeout(timer); };
 }, [jobID]);

 async function preview() {
  setError(''); setWarnings([]);
  let links: string[];
  try { links = parseSourceURLs(urls); }
  catch (cause) { setError(cause instanceof Error ? cause.message : '请检查链接。'); return; }
  setBusy(true); setBusyMessage('正在读取来源…');
  try {
   const result = await previewSources(links, previewProblemSource);
   const room = Math.max(0, 50 - candidates.length);
   const accepted = result.items.slice(0, room).map(item => ({ ...item, item_id: 'batch-' + Date.now() + '-' + item.item_id }));
   setCandidates(current => [...current, ...accepted]);
   setWarnings([...result.warnings, ...(result.items.length > room ? ['清单已达到 50 项，新增的其余条目未加入，请分批处理。'] : [])]);
   if (result.items.length && !setTitle.trim()) setSetTitle('链接导入题集');
  } catch (cause) { setError(cause instanceof Error ? cause.message : '读取失败'); }
  finally { setBusy(false); setBusyMessage(''); }
 }

 function addText() {
  setError('');
  if (!manualTitle.trim() || !manualStatement.trim()) { setError('请填写标题和完整题面。'); return; }
  if (!editingID && candidates.length >= 50) { setError('每次最多 50 道题，请分批处理。'); return; }
  setCandidates(current => editingID
   ? current.map(item => item.item_id === editingID ? { ...item, title: manualTitle.trim(), statement: manualStatement, selected: true, error: undefined } : item)
   : [...current, { item_id: 'text-' + Date.now(), title: manualTitle.trim(), statement: manualStatement, selected: true }]);
  setEditingID('');
  setManualTitle(''); setManualStatement('');
 }

 function editCandidate(item: SourceCandidate) {
  setEditingID(item.item_id); setManualTitle(item.title); setManualStatement(item.statement ?? '');
  setSourceTab('text'); setError('');
  if (typeof document !== 'undefined') document.getElementById('source-editor')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
 }
 function removeCandidate(id: string) {
  setCandidates(current => current.filter(item => item.item_id !== id));
  if (editingID === id) { setEditingID(''); setManualTitle(''); setManualStatement(''); }
 }
 function cancelEdit() { setEditingID(''); setManualTitle(''); setManualStatement(''); }
 async function submit() {
  setError(''); setBusy(true); setBusyMessage('正在读取所选题面…');
  try {
   const resolved = await resolveSelectedSources(candidates, previewProblemSource);
   setCandidates(current => current.map(item => {
    const failure = resolved.failures.find(value => value.item_id === item.item_id);
    const accepted = resolved.items.find(value => value.item_id === item.item_id);
    if (failure) return { ...item, error: failure.error, selected: false };
    return accepted ? { ...item, ...accepted, error: undefined } : item;
   }));
   if (resolved.failures.length) {
    setWarnings(current => [...current, resolved.failures.length + ' 项来源读取失败，已单独标记；其余题目继续提交。']);
   }
   if (!resolved.items.length) throw new Error('没有可提交的题面，请修正失败项或直接粘贴题面。');
   setBusyMessage('正在创建后台任务…');
   const started = await startProblemImport({
    mode, items: resolved.items, create_set: createSet,
    ...(createSet && setTitle.trim() ? { title: setTitle.trim() } : {}),
    ...(mode === 'inspiration' ? { difficulty } : {}),
    language: 'cpp', locale: 'zh',
   });
   setReport(null); setJobID(started.workflow_id);
   const url = new URL(window.location.href);
   url.searchParams.set('workflow', started.workflow_id);
   window.history.replaceState(null, '', url.pathname + url.search);
  } catch (cause) { setError(cause instanceof Error ? cause.message : '任务提交失败'); }
  finally { setBusy(false); setBusyMessage(''); }
 }

 function retryUnread() {
  setCandidates(current => current.filter(item => item.error).map(item => ({ ...item, selected: true })));
  setJobID(''); setReport(null); setProgressError(''); setError('');
  setWarnings(['仅重试未读取的来源；已经提交的后台任务会继续执行。']);
  const url = new URL(window.location.href); url.searchParams.delete('workflow');
  window.history.replaceState(null, '', url.pathname + url.search);
 }

 async function resumeBatch() {
  setError(''); setBusy(true);
  try {
   const result = await resumeProblemImport(jobID);
   setReport(null); setJobID(result.workflow_id);
   const url = new URL(window.location.href); url.searchParams.set('workflow', result.workflow_id);
   window.history.replaceState(null, '', url.pathname + url.search);
  } catch (cause) { setError(cause instanceof Error ? cause.message : '恢复失败，请稍后重试。'); }
  finally { setBusy(false); }
 }

 function newBatch() {
  cancelEdit(); setJobID(''); setReport(null); setProgressError(''); setCandidates([]); setWarnings([]); setError('');
  const url = new URL(window.location.href); url.searchParams.delete('workflow');
  window.history.replaceState(null, '', url.pathname + url.search);
 }

 return <div className="af-page">
  <PageHeader eyebrow="创作 / 外部来源" title="从链接与已有题目开始" description="导入单题或整套题目，也可以把外部内容作为新题的创意。测试数据全部重新生成。"
   actions={<Link className="forge-btn-secondary" href="/problems">返回题库<ArrowRight size={16} /></Link>} />

  {report && <section className="af-panel space-y-4 p-6" aria-label="导入结果">
   <div className="flex flex-wrap items-center justify-between gap-3">
    <div><h2 className="text-lg font-semibold">{labels[report.status] ?? report.status}</h2>
     <p className="af-hint mt-1">{report.items?.filter(item => item.status === 'imported').length ?? 0} 项已导入 · {report.items?.filter(item => item.status === 'skipped_duplicate').length ?? 0} 项重复跳过 · {report.items?.filter(item => ['failed', 'assessment_failed'].includes(item.status)).length ?? 0} 项需处理</p>
    </div>
    <div className="flex flex-wrap gap-2">
     {report.problem_set_id && <Link href={'/problem-sets/' + report.problem_set_id} className="forge-btn-primary">打开题集与下载 ZIP<ArrowRight size={16} /></Link>}
     {!running && report.items.some(item => ['failed', 'assessment_failed', 'pending', 'running'].includes(item.status)) && <button type="button" className="forge-btn-primary" disabled={busy} onClick={() => { void resumeBatch(); }}>继续未完成项目</button>}
     {!running && <button type="button" className="forge-btn-secondary" onClick={newBatch}>新建导入</button>}
    </div>
   </div>
   <p className="af-hint">重复项自动跳过。新导入只由模型给出参考难度，不做 KC 校准或外部平台对齐；难度估计失败不会阻止题目和数据保存。恢复时保留已入库题目。</p>
   {report.error && <p role="alert" className="text-sm text-danger-600">{report.error}</p>}
   <ol className="divide-y divide-[var(--dl)]">
    {(report.items ?? []).map((item, index) => <li key={item.item_id || index} className="py-4">
     <div className="flex flex-wrap justify-between gap-2"><strong className="text-sm">{index + 1}. {item.title}</strong><span className={'text-sm ' + (item.status === 'failed' ? 'text-danger-600' : item.status === 'imported' ? 'text-success-700' : 'text-[var(--dm)]')}>{labels[item.status] ?? item.status}</span></div>
     {item.error && <div className="mt-2 text-sm text-danger-600"><p>{importFailureSummary(item.error)}</p><details className="mt-1 text-xs"><summary className="cursor-pointer">查看错误详情</summary><p className="mt-2 break-words">{item.error}</p></details></div>}
     {item.estimated_difficulty && <p className="af-hint mt-2">参考难度 {item.estimated_difficulty} · 模型粗估{item.difficulty_reason ? '：' + item.difficulty_reason : ''}</p>}
     {item.warning && <p className="mt-2 text-sm text-warning-700">{item.warning}</p>}
     {item.statement_changed && <p className="mt-2 text-sm text-warning-700">题面已整理为 OJ 格式：{item.clarification_reason || '请查看题目来源记录。'}</p>}
     <div className="mt-2 flex flex-wrap gap-4 text-sm">
      {item.problem_id && <Link className="af-link" href={'/problems/' + item.problem_id}>查看题目</Link>}
      {isAdmin && item.rating_assessment_id && item.problem_id && <Link className="af-link" href={'/rating?problem=' + encodeURIComponent(item.problem_id)}>查看难度评估</Link>}
      {item.workflow_id && <Link className="af-link" href={'/workflows/' + item.workflow_id}>生成详情</Link>}
      {item.duplicate_of && <span className="text-[var(--dm)]">已存在相同题目</span>}
     </div>
    </li>)}
   </ol>
  </section>}

  {jobID && !report && <div role="status" className="af-panel flex items-center gap-3 p-6"><Loader2 className="animate-spin" size={18} />正在读取导入进度…</div>}
  {jobID && <p className="af-hint">进度已保存在服务端。可以关闭页面，再从当前链接或任务中心返回。<Link className="af-link ml-2" href={'/workflows/' + jobID}>任务详情</Link></p>}
  {progressError && <div role="alert" className="space-y-2 rounded-lg border border-[var(--dl)] p-4"><p className="text-sm text-warning-700">{progressError}</p><button type="button" className="forge-btn-secondary" onClick={newBatch}>返回新建导入</button><p className="af-hint">返回不会取消已提交的任务。</p></div>}
  {jobID && candidates.some(item => item.error) && <section className="af-panel space-y-3 p-6" aria-label="未提交的来源"><SectionHeading title="这些来源尚未提交" description="读取失败的条目没有进入后台任务，可单独重试。其他条目已继续处理。" /><ul className="space-y-3">{candidates.filter(item => item.error).map(item => <li key={item.item_id} className="text-sm"><strong>{item.title}</strong>{item.source_url && <p className="break-all text-xs text-[var(--dm)]">{item.source_url}</p>}<p className="text-danger-600">{item.error}</p></li>)}</ul><button type="button" className="forge-btn-secondary" onClick={retryUnread}>单独重试未读取的来源</button></section>}

  {!jobID && <div className="af-form-layout">
   <div className="af-form-main">
    <section className="af-panel space-y-5 p-6">
     <SectionHeading title="如何使用来源" />
     <div className="grid gap-3 sm:grid-cols-2">
      <label className={'cursor-pointer rounded-lg border p-4 ' + (mode === 'preserve_statement' ? 'border-[var(--da)] bg-[var(--dg)]' : 'border-[var(--dl)]')}>
       <span className="flex items-center gap-2 font-medium"><input type="radio" name="import-mode" checked={mode === 'preserve_statement'} onChange={() => setMode('preserve_statement')} disabled={busy} />原题导入</span>
       <span className="af-hint mt-2 block">保留题意和来源，由模型整理为标准 OJ 题面并校验样例；必要修订保留说明。去重、重新生成并验证数据；模型直接估计参考难度，不进行校准。</span>
      </label>
      <label className={'cursor-pointer rounded-lg border p-4 ' + (mode === 'inspiration' ? 'border-[var(--da)] bg-[var(--dg)]' : 'border-[var(--dl)]')}>
       <span className="flex items-center gap-2 font-medium"><input type="radio" name="import-mode" checked={mode === 'inspiration'} onChange={() => setMode('inspiration')} disabled={busy} />作为创意生成</span>
       <span className="af-hint mt-2 block">参考内容中的想法创作新题，走完整生成与验证流程，保留来源。</span>
      </label>
     </div>
    </section>

    <section id="source-editor" className="af-panel space-y-5 p-6">
     <div className="flex flex-wrap items-center justify-between gap-3"><SectionHeading title={editingID ? "粘贴替换这道题" : "添加题目来源"} /><div className="flex gap-2">
      <button type="button" className="forge-btn-secondary" aria-pressed={sourceTab === 'links'} onClick={() => setSourceTab('links')}><Link2 size={16} />外部链接</button>
      <button type="button" className="forge-btn-secondary" aria-pressed={sourceTab === 'text'} onClick={() => setSourceTab('text')}><FileText size={16} />粘贴题面</button>
     </div></div>
     {sourceTab === 'links' ? <>
      <label className="af-field"><span>公开题目、题集或创意文章链接</span><textarea className="forge-input min-h-28 resize-y" value={urls} onChange={event => setURLs(event.target.value)} disabled={busy} placeholder="每行一个链接。支持公开网页与 Markdown，题集页面可展开选题。" /></label>
      <p className="af-hint">解析后核对题面和题目清单。需登录、验证码或无法完整读取的网页，可改用粘贴题面。</p>
      <button type="button" className="forge-btn-secondary" onClick={() => { void preview(); }} disabled={busy || !urls.trim()}>{busy ? <Loader2 size={16} className="animate-spin" /> : <Link2 size={16} />}解析链接</button>
     </> : <>
      <label className="af-field"><span>题目标题</span><input className="forge-input" value={manualTitle} onChange={event => setManualTitle(event.target.value)} disabled={busy} /></label>
      <label className="af-field"><span>完整题面（Markdown）</span><textarea className="forge-input min-h-56 resize-y font-mono text-sm" value={manualStatement} onChange={event => setManualStatement(event.target.value)} disabled={busy} placeholder="包括题意、输入输出、约束与样例。导入时统一整理为 OJ 格式。" /></label>
      <div className="flex flex-wrap gap-2"><button type="button" className="forge-btn-secondary" onClick={addText} disabled={busy}><Upload size={16} />{editingID ? '保存替换' : '加入清单'}</button>{editingID && <button type="button" className="forge-btn-secondary" disabled={busy} onClick={cancelEdit}>取消替换</button>}</div>
      {editingID && <p className="af-hint">保存后替换清单中的这一项，保留来源链接；其余题目不变。</p>}
     </>}
    </section>

    {candidates.length > 0 && <section className="af-panel p-6" aria-label="待导入清单">
     <div className="flex flex-wrap items-start justify-between gap-2"><SectionHeading title={'题目清单 · 已选 ' + selectedCount + ' / ' + candidates.length} description="最多 50 道。可删除或粘贴替换任意条目。题集成员的正文会在提交前读取，失败不影响其他题目。" /><button type="button" className="forge-btn-secondary" disabled={busy} onClick={() => { setCandidates([]); cancelEdit(); }}>清空清单</button></div>
     <ol className="mt-4 divide-y divide-[var(--dl)]">{candidates.map(item => <li key={item.item_id} className="py-4">
      <label className="flex items-start gap-3"><input className="mt-1" type="checkbox" checked={item.selected} disabled={busy} onChange={event => setCandidates(current => current.map(value => value.item_id === item.item_id ? { ...value, selected: event.target.checked } : value))} /><span className="min-w-0 break-words text-sm font-medium">{item.title}</span></label>
      <div className="ml-6 mt-2 flex gap-3"><button type="button" className="af-link inline-flex items-center gap-1 text-xs" disabled={busy} aria-label={'粘贴替换 ' + item.title} onClick={() => editCandidate(item)}><Pencil size={13} />粘贴替换</button><button type="button" className="inline-flex items-center gap-1 text-xs text-danger-600" disabled={busy} aria-label={'删除 ' + item.title} onClick={() => removeCandidate(item.item_id)}><Trash2 size={13} />删除</button></div>
      {item.source_url && <a className="af-link ml-6 mt-1 inline-flex max-w-full items-center gap-1 break-all text-xs" href={item.source_url} target="_blank" rel="noreferrer">查看来源<ExternalLink size={12} /></a>}
      {item.error && <p role="alert" className="ml-6 mt-2 break-words text-sm text-danger-600">{item.error}</p>}
      {item.statement ? <details className="ml-6 mt-2"><summary className="cursor-pointer text-xs text-[var(--dm)]">预览题面</summary><pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded bg-[var(--ds)] p-3 text-xs leading-6">{item.statement}</pre></details> : !item.error && <p className="af-hint ml-6 mt-1">提交前读取完整题面</p>}
     </li>)}</ol>
    </section>}
   </div>
   <aside className="af-form-rail"><section className="af-summary space-y-4">
    <SectionHeading title="本次处理" />
    <div className="af-summary-row"><span>方式</span><strong>{mode === 'preserve_statement' ? '原题导入' : '创意生成'}</strong></div>
    <div className="af-summary-row"><span>已选择</span><strong>{selectedCount} 道</strong></div>
    <p className="af-hint">使用管理员已保存的模型。数据全部新生成，经参考解法与沙箱验证后保存。</p>
    {mode === 'inspiration' && <label className="af-field"><span>目标难度</span><input className="forge-input" type="number" min={800} max={3500} step={100} value={difficulty} onChange={event => setDifficulty(Number(event.target.value))} disabled={busy} /></label>}
    <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={createSet} onChange={event => setCreateSet(event.target.checked)} disabled={busy} />将成功题目汇成题集</label>
    {createSet && <label className="af-field"><span>题集名称</span><input className="forge-input" value={setTitle} onChange={event => setSetTitle(event.target.value)} placeholder="例如：前缀和练习" disabled={busy} /></label>}
    <button type="button" className="forge-btn-primary w-full" disabled={busy || !selectedCount || !!editingID} onClick={() => { void submit(); }}>{busy ? <Loader2 size={16} className="animate-spin" /> : <CheckCircle2 size={16} />}{busyMessage || '开始处理'}</button>
   </section></aside>
  </div>}
  {busy && <p role="status" className="af-hint">{busyMessage}</p>}
  {error && <p role="alert" className="rounded-lg border border-danger-400/30 p-4 text-sm text-danger-600">{error}</p>}
  {warnings.length > 0 && <ul className="space-y-2 rounded-lg border border-[var(--dl)] p-4 text-sm text-[var(--dm)]">{warnings.map((warning, index) => <li key={index}>{warning}</li>)}</ul>}
 </div>;
}
