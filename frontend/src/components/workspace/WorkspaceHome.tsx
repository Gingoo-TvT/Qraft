'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ArrowRight, ArrowUpRight, BookOpen, Check, Clock3, FileText, Layers3, RefreshCw, Settings2, Sparkles, Workflow } from 'lucide-react';
import { getStats } from '@/lib/api';
import type { DashboardStats } from '@/lib/types';
import { formatRelativeTime } from '@/lib/utils';
import { PageHeader, SectionHeading } from '@/components/ui/Workspace';

type CreationMode = 'problem' | 'mixed' | 'programming';
const MODES: { id: CreationMode; title: string; description: string }[] = [
 { id: 'problem', title: '一道编程题', description: '完整题面、解法与数据' },
 { id: 'mixed', title: '混合题型题集', description: '概念、代码理解与编程' },
 { id: 'programming', title: '纯编程比赛', description: '整场规划与难度梯度' },
];
const EXAMPLES: { label: string; mode: CreationMode; brief: string }[] = [
 { label: '数组与模拟入门', mode: 'problem', brief: '为刚学完数组与循环的学生设计一道模拟题。情境自然，关键在于正确处理边界，附有清晰的样例说明。' },
 { label: '数据结构综合练习', mode: 'mixed', brief: '一套数据结构综合练习，包含选择题、判断题和编程题，覆盖栈、队列、树和图。由基础概念过渡到综合应用，避免重复考查。' },
 { label: '五小时编程比赛', mode: 'programming', brief: '设计一场五小时、十道编程题的团队比赛。前段有入门得分题，中段区分算法应用能力，后段有综合思维题，算法主题尽量分散。' },
];

export default function WorkspaceHome({ serviceReady, settingsHref = '/settings', desktop = false }: { serviceReady?: boolean; settingsHref?: string; desktop?: boolean }) {
 const router = useRouter();
 const [mode, setMode] = useState<CreationMode>('problem');
 const [brief, setBrief] = useState('');
 const [stats, setStats] = useState<DashboardStats | null>(null);
 const [loading, setLoading] = useState(false);
 const [error, setError] = useState('');
 const active = useRef(false);
 const load = useCallback(async () => {
  if (active.current || serviceReady === false) return;
  active.current = true; setLoading(true); setError('');
  try { const result = await getStats(); if (!result.data) throw new Error('服务暂未返回工作区数据'); setStats(result.data); }
  catch (e) { setError(e instanceof Error ? e.message : '暂时无法读取工作区'); }
  finally { active.current = false; setLoading(false); }
 }, [serviceReady]);
 useEffect(() => { void load(); }, [load]);
 const unavailable = serviceReady === false || (!stats && Boolean(error));
 const activities = stats?.recent_activity ?? [];
 function start(event: React.FormEvent) {
  event.preventDefault();
  const query = new URLSearchParams();
  if (brief.trim()) query.set('brief', brief.trim());
  if (mode !== 'problem') query.set('format', mode);
  const path = mode === 'problem' ? '/problems/new' : '/problem-sets/new';
  router.push(path + (query.size ? '?' + query.toString() : ''));
 }
 return <div className="af-page af-home">
  <PageHeader eyebrow="创作空间" title="把下一道好题，写在这里。" description="从一个想法开始，让生成、验证与整理成为连贯的创作过程。"
   actions={<button type="button" className="forge-btn-ghost" onClick={() => void load()} disabled={loading || serviceReady === false} aria-label="刷新工作台"><RefreshCw size={16} className={loading ? 'animate-spin' : ''} />刷新</button>} />
  <div className="af-home-grid">
   <div className="af-home-main">
    <form className="af-composer" onSubmit={start}>
     <div className="af-composer-heading"><span className="af-composer-symbol"><Sparkles size={21} strokeWidth={1.6} /></span><div><h2>你想创作什么？</h2><p>先说想法，接下来再细化题型、难度与要求。</p></div></div>
     <fieldset className="af-creation-modes"><legend className="sr-only">创作类型</legend>{MODES.map(item => <label key={item.id} className={mode === item.id ? 'selected' : ''}><input type="radio" name="home-creation-mode" value={item.id} checked={mode === item.id} onChange={() => setMode(item.id)} /><span><strong>{item.title}</strong><small>{item.description}</small></span>{mode === item.id && <Check size={14} />}</label>)}</fieldset>
     <label className="af-brief-label" htmlFor="workspace-brief">描述你的出题想法</label>
     <textarea id="workspace-brief" value={brief} onChange={e => setBrief(e.target.value)} placeholder="例如：为学完图论的学生出一道需要转化建模的题，难度中等，避免直接套最短路模板……" maxLength={12000} />
     <div className="af-composer-examples"><span>试试这些想法</span>{EXAMPLES.map(item => <button type="button" key={item.label} onClick={() => { setBrief(item.brief); setMode(item.mode); }}>{item.label}<ArrowUpRight size={13} /></button>)}</div>
     <div className="af-composer-footer"><p>下一步确认需求后再启动生成</p><button type="submit" className="forge-btn-primary">继续创作<ArrowRight size={16} /></button></div>
    </form>
    <section className="af-home-activity">
     <SectionHeading title="最近的创作动态" description="继续处理已有成果，或查看生成与验证进度。" actions={<Link href="/workflows" className="af-link">全部任务<ArrowRight size={15} /></Link>} />
     {activities.length > 0 ? <div className="af-activity-list">{activities.slice(0, 5).map(item => {
      const href = item.reference_id ? (item.type.startsWith('workflow') ? '/workflows/' : '/problems/') + encodeURIComponent(item.reference_id) : null;
      return <div className="af-activity-row" key={item.id}><span className="af-activity-icon">{item.type.startsWith('workflow') ? <Workflow size={17} /> : <FileText size={17} />}</span><div><strong>{item.description}</strong><small>{formatRelativeTime(item.timestamp)}</small></div>{href && <Link href={href} aria-label={'查看：' + item.description}><ArrowUpRight size={17} /></Link>}</div>;
     })}</div> : <div className="af-home-empty"><Clock3 size={23} strokeWidth={1.5} /><div><strong>{loading ? '正在读取创作记录' : unavailable ? '连接工作区后，从这里继续' : '第一份作品，从一个想法开始'}</strong><p>{unavailable ? '已有题目与任务会在连接后显示。' : '创建题目或题集后，你可以在这里继续查看与整理。'}</p></div></div>}
    </section>
   </div>
   <aside className="af-home-rail">
    <section className="af-home-library">
     <span className="af-home-overline">已有题目，也能有新组合</span><Layers3 size={30} strokeWidth={1.3} /><h2>把好题组成一套</h2><p>从题库筛选，搭配知识点与难度。适合日常练习、课程测验和比赛。</p><Link href="/problem-sets/assemble" className="forge-btn-secondary">从题库组卷<ArrowRight size={15} /></Link>
    </section>
    <section className="af-home-overview">
     <SectionHeading title="我的工作区" />
     {stats ? <div className="af-overview-rows"><Link href="/problems"><span><BookOpen size={16} />编程题库</span><strong>{stats.total_problems.toLocaleString()}<ArrowUpRight size={14} /></strong></Link><Link href="/workflows"><span><Workflow size={16} />运行中任务</span><strong>{stats.active_workflows.toLocaleString()}<ArrowUpRight size={14} /></strong></Link><Link href="/problems"><span><Check size={16} />待审核题目</span><strong>{stats.review_problems.toLocaleString()}<ArrowUpRight size={14} /></strong></Link></div> :
      <div className="af-workspace-unavailable"><Settings2 size={19} /><strong>{loading ? '正在读取工作区' : '准备好你的创作环境'}</strong><p>{desktop ? '连接已有 Qraft 服务，继续生成与管理题目。' : '配置生成模型，开始创建题目与题集。'}</p><Link className="af-link" href={settingsHref}>{desktop ? '连接工作区' : '配置模型'}<ArrowRight size={14} /></Link></div>}
     {error && stats && <p className="af-small-error" role="alert">本次刷新失败，当前显示上次成功读取的数据。</p>}
     {error && !stats && <details className="af-home-error"><summary>连接信息</summary><p>{error}</p><button type="button" className="af-link" onClick={() => void load()}>重新连接</button></details>}
    </section>
    <div className="af-home-help"><span>更多创作方式</span><Link href="/quizzes/new">创建客观题<ArrowUpRight size={14} /></Link><Link href="/quizzes/import">导入已有题目<ArrowUpRight size={14} /></Link></div>
   </aside>
  </div>
 </div>;
}
