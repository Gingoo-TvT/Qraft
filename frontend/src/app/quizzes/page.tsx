'use client';

import { useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import { BookOpen, Download, Plus, Search, Trash2, Upload, X } from 'lucide-react';

import Pagination from '@/components/Pagination';
import RefreshButton from '@/components/RefreshButton';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { useKnowledgePoints } from '@/hooks/useKnowledgePoints';
import { useQuizzes } from '@/hooks/useQuiz';
import { deleteQuiz, exportQuizzesURL } from '@/lib/api';
import { DEFAULT_PAGE_SIZE, QUIZ_DIFFICULTY_LABELS, QUIZ_SUBJECTS, QUIZ_TYPE_LABELS } from '@/lib/constants';
import type { QuizDifficulty, QuizFilter, QuizType } from '@/lib/types';
import { formatDate } from '@/lib/utils';

export default function QuizzesPage() {
  const [tagInput, setTagInput] = useState('');
  const [deleteError, setDeleteError] = useState('');
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const { quizzes, total, page, loading, error, filter, setFilter, refresh } =
    useQuizzes({ page: 1, size: DEFAULT_PAGE_SIZE });
  const { points } = useKnowledgePoints(filter.subject);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const subject = params.get('subject');
    if (subject && subject !== filter.subject) setFilter({ ...filter, subject, page: 1 });
    // Run once on mount; filters are controlled by local state afterwards.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const exportURL = useMemo(() => exportQuizzesURL(filter), [filter]);
  const hasFilters = Boolean(filter.subject || filter.type || filter.difficulty || filter.knowledge_point_id || filter.tag);

  async function handleDelete(id: string) {
    if (!window.confirm('确定删除这道客观题吗？')) return;
    setDeleteError('');
    setDeletingId(id);
    try {
      await deleteQuiz(id);
      refresh();
    } catch (cause) {
      setDeleteError(cause instanceof Error ? cause.message : '删除失败，请重试。');
    } finally {
      setDeletingId(null);
    }
  }

  function patchFilter(next: QuizFilter) {
    setFilter({ ...filter, ...next, page: next.page ?? 1 });
  }

  function clearFilters() {
    setTagInput('');
    setFilter({ page: 1, size: filter.size ?? DEFAULT_PAGE_SIZE });
  }

  return (
    <div className="af-page">
      <PageHeader eyebrow="内容管理" title="客观题库" description="按学科和知识点整理选择题、判断题与填空题。"
        actions={<>
          <Link href="/quizzes/import" className="forge-btn-secondary"><Upload className="h-4 w-4" />导入 Excel</Link>
          <Link href="/quizzes/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建客观题</Link>
        </>}
      />

      {(error || deleteError) && (
        <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
          {deleteError || error}
          {error && <button className="ml-3 underline" onClick={refresh}>重新加载</button>}
        </div>
      )}

      <section className="af-table-panel" aria-label="客观题列表" aria-busy={loading}>
        <div className="af-toolbar">
          <form className="flex min-w-[220px] flex-1 items-center gap-2" onSubmit={(event) => {
            event.preventDefault();
            patchFilter({ tag: tagInput || undefined });
          }}>
            <div className="relative min-w-0 flex-1">
              <Search aria-hidden="true" className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--dm)]" />
              <input aria-label="按标签查找客观题" className="forge-input pl-10" placeholder="按标签查找…" value={tagInput} onChange={(event) => setTagInput(event.target.value)} />
            </div>
            <button type="submit" className="forge-btn-secondary">查找</button>
          </form>
          <select aria-label="学科" className="forge-input w-auto" value={filter.subject ?? ''} onChange={(event) => patchFilter({ subject: event.target.value || undefined })}>
            <option value="">全部学科</option>
            {QUIZ_SUBJECTS.map((subject) => <option key={subject.value} value={subject.value}>{subject.label}</option>)}
          </select>
          <select aria-label="题型" className="forge-input w-auto" value={filter.type ?? ''} onChange={(event) => patchFilter({ type: (event.target.value as QuizType) || undefined })}>
            <option value="">全部题型</option>
            {Object.entries(QUIZ_TYPE_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <select aria-label="难度" className="forge-input w-auto" value={filter.difficulty ?? ''} onChange={(event) => patchFilter({ difficulty: (event.target.value as QuizDifficulty) || undefined })}>
            <option value="">全部难度</option>
            {Object.entries(QUIZ_DIFFICULTY_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
          <select aria-label="知识点" title={filter.subject ? '按知识点筛选' : '先选择学科，再选择知识点'} className="forge-input w-auto max-w-[240px]" value={filter.knowledge_point_id ?? ''} disabled={!filter.subject} onChange={(event) => patchFilter({ knowledge_point_id: event.target.value || undefined })}>
            <option value="">全部知识点</option>
            {points.map((point) => <option key={point.id} value={point.id}>{point.code} {point.name}</option>)}
          </select>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm">
          <div className="flex flex-wrap items-center gap-3 text-[var(--dm)]">
            <span>{loading && quizzes.length === 0 ? '正在读取题库…' : error ? '暂时无法读取题目数量' : <>共 <strong className="font-medium text-[var(--dt)]">{total}</strong> 道题目</>}</span>
            {filter.tag && <span className="forge-badge">标签：{filter.tag}</span>}
            {hasFilters && <button className="af-link inline-flex items-center gap-1" onClick={clearFilters}><X className="h-3.5 w-3.5" />清除筛选</button>}
          </div>
          <div className="flex items-center gap-3">
            <a href={exportURL} download className="af-link inline-flex items-center gap-1.5"><Download className="h-4 w-4" />导出当前筛选</a>
            <RefreshButton onClick={refresh} loading={loading} />
          </div>
        </div>

        <div className="af-table-scroll">
          <table className="forge-table">
            <thead><tr>
              <th>题目</th><th className="w-28">题型 / 难度</th><th className="w-44">学科 / 权限</th><th className="w-40">创建时间</th><th className="w-32">操作</th>
            </tr></thead>
            <tbody>
              {loading && quizzes.length === 0 ? (
                Array.from({ length: 5 }).map((_, row) => <tr key={row}>{Array.from({ length: 5 }).map((_, column) => <td key={column}><div className="forge-skeleton h-5 w-full" /></td>)}</tr>)
              ) : quizzes.length === 0 ? (
                <tr><td colSpan={5}>
                  <EmptyState icon={BookOpen} title={error ? '题库暂时不可用' : hasFilters ? '没有符合筛选条件的客观题' : '开始整理你的客观题库'}
                    description={error ? '题目未能加载，重新连接后再试。' : hasFilters ? '调整学科、题型或标签，查看其他题目。' : '按知识点生成新题，或导入已有的 Excel 题库。'}
                    action={error ? <button className="forge-btn-secondary" onClick={refresh}>重新加载</button> : hasFilters ? <button className="forge-btn-secondary" onClick={clearFilters}>清除筛选</button> : <Link href="/quizzes/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建客观题</Link>}
                  />
                </td></tr>
              ) : quizzes.map((quiz) => (
                <tr key={quiz.id}>
                  <td className="min-w-[280px]">
                    <Link href={`/quizzes/${quiz.id}`} className="af-link font-medium">{quiz.title}</Link>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--dm)]">
                      <span className="font-mono">{quiz.code}</span>
                      {quiz.tags.slice(0, 3).map((tag) => <span key={tag}>{tag}</span>)}
                      {quiz.tags.length > 3 && <span title={quiz.tags.join('、')}>+{quiz.tags.length - 3} 个标签</span>}
                    </div>
                  </td>
                  <td><div>{QUIZ_TYPE_LABELS[quiz.type] ?? quiz.type}</div><div className="mt-1 text-xs text-[var(--dm)]">{QUIZ_DIFFICULTY_LABELS[quiz.difficulty] ?? quiz.difficulty}</div></td>
                  <td><div>{QUIZ_SUBJECTS.find((subject) => subject.value === quiz.subject)?.label ?? quiz.subject}</div><div className="mt-1 text-xs text-[var(--dm)]">{quiz.is_vip ? 'VIP 题目' : '非 VIP 题目'}</div></td>
                  <td className="text-sm text-[var(--dm)]">{formatDate(quiz.created_at)}</td>
                  <td><div className="flex items-center gap-4">
                    <Link href={`/quizzes/${quiz.id}`} className="af-link">查看</Link>
                    <button className="text-danger-500 hover:text-danger-600 disabled:opacity-50" disabled={deletingId !== null} onClick={() => void handleDelete(quiz.id)} aria-label={`删除题目：${quiz.title}`} title="删除题目"><Trash2 className="h-4 w-4" /></button>
                  </div></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="af-pagination">
          <Pagination page={page} total={total} size={filter.size ?? DEFAULT_PAGE_SIZE}
            onPageChange={(next) => setFilter({ ...filter, page: next })}
            onSizeChange={(next) => setFilter({ ...filter, size: next, page: 1 })} />
        </div>
      </section>
    </div>
  );
}
