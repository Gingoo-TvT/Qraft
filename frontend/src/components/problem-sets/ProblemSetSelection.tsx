'use client';

import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { useEffect, useState } from 'react';
import { Layers, Loader2, X } from 'lucide-react';
import { useAuth } from '@/components/auth/AuthProvider';
import { addProblemSetItems, createProblemSet, getProblemSet, listProblemSets } from '@/lib/api';
import { isSetGenerationActive } from '@/lib/problem-set-generation';
import type { Problem, ProblemSet, ProblemSetKind, ProblemSetVisibility } from '@/lib/types';

type Props = {
  selected: Problem[];
  targetSetID?: string;
  onRemove: (id: string) => void;
  onClear: () => void;
  onBusyChange: (busy: boolean) => void;
};

export default function ProblemSetSelection({ selected, targetSetID, onRemove, onClear, onBusyChange }: Props) {
  const router = useRouter();
  const { isAdmin, session } = useAuth();
  const [expanded, setExpanded] = useState(Boolean(targetSetID));
  const [mode, setMode] = useState<'new' | 'existing'>(targetSetID ? 'existing' : 'new');
  const [title, setTitle] = useState('');
  const [kind, setKind] = useState<ProblemSetKind>('curriculum');
  const [visibility, setVisibility] = useState<ProblemSetVisibility>('private');
  const [score, setScore] = useState(100);
  const [section, setSection] = useState('');
  const [targetID, setTargetID] = useState(targetSetID ?? '');
  const [target, setTarget] = useState<ProblemSet | null>(null);
  const [sets, setSets] = useState<ProblemSet[]>([]);
  const [search, setSearch] = useState('');
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!expanded || mode !== 'existing') return;
    let cancelled = false;
    setLoading(true);
    const timer = window.setTimeout(() => {
      listProblemSets({ search, page, size: 20 }).then(response => {
        if (cancelled) return;
        setSets(response.data ?? []); setTotal(response.meta?.total ?? 0);
      }).catch(cause => { if (!cancelled) setError(cause instanceof Error ? cause.message : '题集加载失败'); })
        .finally(() => { if (!cancelled) setLoading(false); });
    }, 200);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [expanded, mode, search, page]);

  useEffect(() => {
    if (!targetID) { setTarget(null); return; }
    let cancelled = false;
    setTarget(null);
    getProblemSet(targetID).then(response => { if (!cancelled) setTarget(response.data ?? null); })
      .catch(cause => { if (!cancelled) setError(cause instanceof Error ? cause.message : '题集加载失败'); });
    return () => { cancelled = true; };
  }, [targetID]);

  function canEdit(set: ProblemSet) {
    return (isAdmin || (set.owner_user_id === session?.user?.id && set.visibility === 'private'))
      && !isSetGenerationActive(set.generation?.status);
  }
  const options = sets.filter(canEdit);
  if (target && canEdit(target) && !options.some(set => set.id === target.id)) options.unshift(target);
  const existingIDs = new Set(target?.items?.map(item => item.problem_id).filter(Boolean));
  const repeated = mode === 'existing' ? selected.filter(problem => existingIDs.has(problem.id)).length : 0;

  async function save() {
    if (!selected.length || saving) return;
    if (mode === 'new' && !title.trim()) { setError('请填写题集名称。'); return; }
    if (mode === 'existing' && (!target || !canEdit(target))) { setError('请选择可以编辑且未在生成中的题集。'); return; }
    if (!Number.isInteger(score) || score < 0) { setError('每题分值需要是非负整数。'); return; }
    setSaving(true); onBusyChange(true); setError('');
    const items = selected.map(problem => ({ problem_id: problem.id, score, section: section.trim() }));
    try {
      const response = mode === 'new'
        ? await createProblemSet({ title: title.trim(), kind, visibility, desired_item_count: items.length, cooldown_sets: 0, items })
        : await addProblemSetItems(targetID, items);
      if (!response.data) throw new Error('服务没有返回保存结果，请刷新题集确认。');
      onClear();
      router.push('/problem-sets/' + response.data.id);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '选题保存失败，请重试。'); }
    finally { setSaving(false); onBusyChange(false); }
  }

  return <section className="af-panel space-y-4 p-5" aria-label="选题组卷">
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div><strong>已选 {selected.length} 道题目</strong><p className="af-hint mt-1">支持跨页、跨筛选选择，按勾选顺序加入题集。</p></div>
      <div className="flex gap-2"><button type="button" className="forge-btn-secondary" disabled={saving || !selected.length} onClick={onClear}>清空选择</button><button type="button" className="forge-btn-primary" onClick={() => setExpanded(!expanded)} disabled={saving}><Layers className="h-4 w-4" />{expanded ? '收起组卷设置' : '加入题集'}</button></div>
    </div>
    {expanded && <form className="space-y-4 border-t border-[var(--dl)] pt-4" onSubmit={event => { event.preventDefault(); void save(); }}>
      <fieldset disabled={saving} className="space-y-4 disabled:opacity-60">
        <div className="flex gap-5 text-sm"><label className="flex items-center gap-2"><input type="radio" name="set-destination" checked={mode === 'new'} onChange={() => { setMode('new'); setError(''); }} />新建题集</label><label className="flex items-center gap-2"><input type="radio" name="set-destination" checked={mode === 'existing'} onChange={() => { setMode('existing'); setError(''); }} />加入已有题集</label></div>
        {mode === 'new' ? <div className="grid gap-4 sm:grid-cols-2">
          <label className="af-field"><span>题集名称 *</span><input className="forge-input" maxLength={255} value={title} onChange={event => setTitle(event.target.value)} placeholder="例如：C++ 数组入门练习" /></label>
          <label className="af-field"><span>用途</span><select className="forge-input" value={kind} onChange={event => setKind(event.target.value as ProblemSetKind)}><option value="curriculum">课程练习</option><option value="homework">作业</option><option value="contest">比赛</option><option value="mock_exam">模拟考试</option></select></label>
          {isAdmin && <label className="af-field"><span>可见范围</span><select className="forge-input" value={visibility} onChange={event => setVisibility(event.target.value as ProblemSetVisibility)}><option value="private">私人题集</option><option value="public">团队共享</option></select></label>}
        </div> : <div className="space-y-3">
          <label className="af-field"><span>搜索已有题集</span><input className="forge-input" value={search} onChange={event => { setSearch(event.target.value); setPage(1); }} placeholder="按名称搜索题集" /></label>
          <label className="af-field"><span>目标题集</span><select className="forge-input" value={targetID} onChange={event => { setTargetID(event.target.value); setError(''); }} disabled={loading}><option value="">{loading ? '正在读取题集…' : '选择题集'}</option>{options.map(set => <option key={set.id} value={set.id}>{set.title}</option>)}</select></label>
          <div className="flex items-center gap-3 text-sm"><button type="button" className="forge-btn-secondary" disabled={loading || page === 1} onClick={() => setPage(page - 1)}>上一页题集</button><span>第 {page} 页</span><button type="button" className="forge-btn-secondary" disabled={loading || page * 20 >= total} onClick={() => setPage(page + 1)}>下一页题集</button>{target && <Link href={'/problem-sets/' + target.id} className="af-link">查看目标题集</Link>}</div>
          <p className="af-hint">只列出你可以编辑且未在生成中的题集。{repeated > 0 && ` 已有 ${repeated} 道选中题目，保存时会跳过重复项。`}</p>
        </div>}
        <div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>每题分值</span><input className="forge-input" type="number" min={0} step={1} value={score} onChange={event => setScore(Number(event.target.value))} /></label><label className="af-field"><span>分区（可选）</span><input className="forge-input" value={section} onChange={event => setSection(event.target.value)} placeholder="例如：一维数组" /></label></div>
        <details><summary className="cursor-pointer text-sm">查看已选题目与顺序（{selected.length}）</summary><ol className="mt-3 max-h-64 space-y-2 overflow-auto">{selected.map((problem, index) => <li key={problem.id} className="flex items-center justify-between gap-3 text-sm"><span>{index + 1}. {problem.title}</span><button type="button" className="shrink-0 p-1 text-[var(--dm)]" aria-label={'取消选择：' + problem.title} onClick={() => onRemove(problem.id)}><X className="h-4 w-4" /></button></li>)}</ol></details>
        <p className="af-hint">草稿和待审题也可编排。导出 ZIP 时会检查题面与测试数据；加入题集不会自动发布题目。</p>
        {error && <p role="alert" className="text-sm text-danger-600">{error}</p>}
        <button type="submit" className="forge-btn-primary" disabled={saving || selected.length === 0 || (mode === 'existing' && (!target || !canEdit(target)))}>{saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Layers className="h-4 w-4" />}{saving ? '正在保存…' : mode === 'new' ? `创建题集（${selected.length} 题）` : `加入题集（${selected.length - repeated} 道新题）`}</button>
      </fieldset>
    </form>}
  </section>;
}
