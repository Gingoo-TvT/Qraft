'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';
import { BookOpen, Plus, Search, X } from 'lucide-react';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';

import RefreshButton from '@/components/RefreshButton';
import { useKnowledgePoints } from '@/hooks/useKnowledgePoints';
import { QUIZ_SUBJECTS } from '@/lib/constants';
import type { KnowledgePoint, QuizSubject } from '@/lib/types';
import { useAppStore } from '@/stores/appStore';

function parentName(point: KnowledgePoint, byId: Map<string, KnowledgePoint>) {
  if (!point.parent_id) return '-';
  return byId.get(point.parent_id)?.name ?? point.parent_id;
}

export default function KnowledgePointsPage() {
  const { selectedSubject, setSubject } = useAppStore();
  const [keyword, setKeyword] = useState('');
  const { points, loading, error, refresh } = useKnowledgePoints(selectedSubject);

  const byId = useMemo(
    () => new Map(points.map((point) => [point.id, point])),
    [points],
  );

  const filteredPoints = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return [...points]
      .sort((a, b) => {
        if (!!a.parent_id !== !!b.parent_id) return a.parent_id ? 1 : -1;
        if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order;
        return a.code.localeCompare(b.code);
      })
      .filter((point) => {
        if (!q) return true;
        return (
          point.code.toLowerCase().includes(q) ||
          point.name.toLowerCase().includes(q)
        );
      });
  }, [keyword, points]);

  return (
    <div className="af-page">
      <PageHeader eyebrow="工具与配置" title="知识点目录" description="按学科查看当前可用于客观题生成的知识点。"
        actions={<Link href="/quizzes/new" className="forge-btn-primary"><Plus className="h-4 w-4" />按知识点出题</Link>}
      />

      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
        {error}<button className="ml-3 underline" onClick={refresh}>重新加载</button>
      </div>}

      <section className="af-table-panel" aria-label="知识点目录" aria-busy={loading}>
        <div className="af-toolbar">
          <div className="af-segmented" role="group" aria-label="知识点所属学科">
            {QUIZ_SUBJECTS.map((subject) => <button key={subject.value} type="button"
              aria-pressed={selectedSubject === subject.value} className={selectedSubject === subject.value ? 'is-active' : ''}
              onClick={() => setSubject(subject.value as QuizSubject)}>{subject.label}</button>)}
          </div>
          <div className="relative min-w-[220px] flex-1">
            <Search aria-hidden="true" className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--dm)]" />
            <input aria-label="按编号或名称搜索知识点" className="forge-input pl-10 pr-10" placeholder="搜索 code / name" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
            {keyword && <button aria-label="清除知识点搜索" className="absolute right-3 top-1/2 -translate-y-1/2 text-[var(--dm)]" onClick={() => setKeyword('')}><X className="h-4 w-4" /></button>}
          </div>
          <RefreshButton onClick={refresh} loading={loading} />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm text-[var(--dm)]">
          <span>{loading && points.length === 0 ? '正在读取知识点…' : error ? '暂时无法读取知识点数量' : <>显示 <strong className="font-medium text-[var(--dt)]">{filteredPoints.length}</strong> / {points.length} 个知识点</>}</span>
          <span className="text-xs">按层级、排序值与编号排列</span>
        </div>

        <div className="af-table-scroll">
          <table className="forge-table">
            <thead><tr><th>Name</th><th className="w-48">Code</th><th className="w-64">Parent</th><th className="w-24">Sort</th></tr></thead>
            <tbody>
              {loading && filteredPoints.length === 0 ? Array.from({ length: 8 }).map((_, row) => <tr key={row}>{Array.from({ length: 4 }).map((_, column) => <td key={column}><div className="forge-skeleton h-5 w-full" /></td>)}</tr>)
                : filteredPoints.length === 0 ? <tr><td colSpan={4}><EmptyState icon={BookOpen}
                  title={error ? '暂时无法查看知识点' : keyword.trim() ? '没有匹配的知识点' : '该学科暂未配置知识点'}
                  description={error ? '重新加载后查看当前学科的知识点。' : keyword.trim() ? '尝试其他编号或名称，或者切换学科。' : '该学科暂未配置知识点，请联系管理员通过种子脚本导入'}
                  action={error ? <button className="forge-btn-secondary" onClick={refresh}>重新加载</button> : keyword.trim() ? <button className="forge-btn-secondary" onClick={() => setKeyword('')}>清除搜索</button> : <button className="forge-btn-secondary" onClick={refresh}>刷新目录</button>}
                /></td></tr>
                : filteredPoints.map((point) => <tr key={point.id}>
                  <td className="min-w-[240px] font-medium">{point.name}</td>
                  <td className="font-mono text-sm text-[var(--dm)]">{point.code}</td>
                  <td className="text-sm text-[var(--dm)]">{parentName(point, byId)}</td>
                  <td className="tabular-nums text-[var(--dm)]">{point.sort_order}</td>
                </tr>)}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
