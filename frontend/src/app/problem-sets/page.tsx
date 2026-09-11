'use client';

import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { ArrowRight, Download, Layers3, Plus, Search, X } from 'lucide-react';

import RefreshButton from '@/components/RefreshButton';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { exportProblemSetURL, listProblemSets } from '@/lib/api';
import type { ProblemSet } from '@/lib/types';
import { formatDate } from '@/lib/utils';

const KIND_LABELS: Record<string, string> = {
  contest: '比赛', homework: '作业', curriculum: '课程', mock_exam: '模拟考试',
};
const STATUS_LABELS: Record<string, string> = { draft: '草稿', ready: '待导出', exported: '已导出' };

export default function ProblemSetsPage() {
  const [sets, setSets] = useState<ProblemSet[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [kind, setKind] = useState('');
  const [status, setStatus] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const response = await listProblemSets({ page: 1, size: 100 });
      setSets(response.data ?? []);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '题集加载失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const visibleSets = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return sets.filter((set) => (!kind || set.kind === kind) && (!status || set.status === status)
      && (!query || `${set.title} ${set.code}`.toLocaleLowerCase().includes(query)));
  }, [sets, search, kind, status]);
  const hasFilters = Boolean(search || kind || status);

  function clearFilters() { setSearch(''); setKind(''); setStatus(''); }

  return (
    <div className="af-page">
      <PageHeader eyebrow="内容管理" title="题集与组卷" description="将题目编排成比赛、课程练习和作业，在这里继续编辑与导出。"
        actions={<>
          <Link href="/problem-sets/assemble" className="forge-btn-secondary"><Layers3 className="h-4 w-4" />从题库组卷</Link>
          <Link href="/problem-sets/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题集</Link>
        </>}
      />

      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
        {error}<button className="ml-3 underline" onClick={() => void load()}>重新加载</button>
      </div>}

      <section className="af-table-panel" aria-label="题集列表" aria-busy={loading}>
        <div className="af-toolbar">
          <div className="relative min-w-[220px] flex-1">
            <Search aria-hidden="true" className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--dm)]" />
            <input aria-label="在已载入题集中搜索" className="forge-input pl-10" placeholder="在已载入题集中查找标题或编号…" value={search} onChange={(event) => setSearch(event.target.value)} />
          </div>
          <select aria-label="题集类型" className="forge-input w-auto" value={kind} onChange={(event) => setKind(event.target.value)}>
            <option value="">全部类型</option>{Object.entries(KIND_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <select aria-label="题集状态" className="forge-input w-auto" value={status} onChange={(event) => setStatus(event.target.value)}>
            <option value="">全部状态</option>{Object.entries(STATUS_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <RefreshButton onClick={() => void load()} loading={loading} />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm text-[var(--dm)]">
          <div className="flex flex-wrap items-center gap-3">
            <span>{loading && sets.length === 0 ? '正在读取题集…' : error && sets.length === 0 ? '暂时无法读取题集数量' : <>显示 <strong className="font-medium text-[var(--dt)]">{visibleSets.length}</strong> / {sets.length} 个已载入题集</>}</span>
            {hasFilters && <button className="af-link inline-flex items-center gap-1" onClick={clearFilters}><X className="h-3.5 w-3.5" />清除筛选</button>}
          </div>
          <span className="text-xs">本列表最多载入 100 个题集</span>
        </div>

        {loading && sets.length === 0 ? <div className="space-y-5 p-5">{Array.from({ length: 4 }).map((_, index) => <div key={index} className="flex items-center gap-4"><div className="forge-skeleton h-10 w-10" /><div className="flex-1 space-y-2"><div className="forge-skeleton h-5 w-2/5" /><div className="forge-skeleton h-4 w-3/5" /></div></div>)}</div>
          : visibleSets.length === 0 ? <EmptyState icon={Layers3}
            title={error ? '暂时无法查看题集' : hasFilters ? '没有找到匹配的题集' : '把题目组织成一套练习'}
            description={error ? '重新加载后继续管理已有题集。' : hasFilters ? '换一个标题、编号或筛选条件。搜索范围为当前已载入的题集。' : '从已有题库选题组卷，或描述需求，让系统生成一整套新题。'}
            action={error ? <button className="forge-btn-secondary" onClick={() => void load()}>重新加载</button> : hasFilters ? <button className="forge-btn-secondary" onClick={clearFilters}>清除筛选</button> : <div className="flex flex-wrap items-center justify-center gap-2"><Link href="/problem-sets/new" className="forge-btn-primary">创建题集</Link><Link href="/problem-sets/assemble" className="forge-btn-secondary">从题库组卷</Link></div>}
          />
            : <ul aria-label="已载入题集">
              {visibleSets.map((set) => <li key={set.id} className="af-collection-row">
                <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-4">
                  <div className="flex min-w-0 flex-1 items-start gap-4">
                    <div aria-hidden="true" className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-lg border border-[var(--dl)] bg-[var(--ds)] text-[var(--dm)]"><Layers3 className="h-5 w-5" /></div>
                    <div className="min-w-0">
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                        <Link href={`/problem-sets/${set.id}`} className="af-link break-words text-base font-medium">{set.title}</Link>
                        <span className="forge-badge">{STATUS_LABELS[set.status] ?? set.status}</span>
                      </div>
                      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--dm)]">
                        <span className="font-mono">{set.code}</span>
                        <span>{KIND_LABELS[set.kind] ?? set.kind}</span>
                        <span>{set.generation_config?.assembly ? '题库组卷' : set.generation_config?.mode === 'mixed' ? '混合题型' : set.generation_config?.mode === 'programming' ? '编程题集' : '自定义题集'}</span>
                        <span>目标 {set.desired_item_count || set.min_item_count || '—'} 题</span>
                        <span>{set.total_score} 分</span>
                      </div>
                      {set.description && <p className="mt-2 line-clamp-2 max-w-[720px] text-sm text-[var(--dm)]">{set.description}</p>}
                    </div>
                  </div>
                  <div className="flex w-full flex-wrap items-center justify-between gap-3 pl-14 sm:w-auto sm:flex-col sm:items-end sm:pl-0">
                    <time className="text-xs text-[var(--dm)]" dateTime={set.updated_at}>更新于 {formatDate(set.updated_at)}</time>
                    <div className="flex items-center gap-5 text-sm">
                      <a href={exportProblemSetURL(set.id)} className="af-link inline-flex items-center gap-1.5" title="质量检查通过后下载题集包"><Download className="h-4 w-4" />导出</a>
                      <Link href={`/problem-sets/${set.id}`} className="af-link inline-flex items-center gap-1">管理题集<ArrowRight className="h-3.5 w-3.5" /></Link>
                    </div>
                  </div>
                </div>
              </li>)}
            </ul>}
      </section>
    </div>
  );
}
