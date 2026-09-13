'use client';

import { Suspense, useEffect, useMemo, useState, type FormEvent } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { ArrowUpRight, Search, SlidersHorizontal } from 'lucide-react';
import Pagination from '@/components/Pagination';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { APIError, searchQuestions } from '@/lib/api';
import { QUIZ_DIFFICULTY_LABELS, QUIZ_TYPE_LABELS, getDifficultyLabel } from '@/lib/constants';
import { parseQuestionSearch, questionDetailHref, questionResultsHref } from '@/lib/question-search-state';
import type { QuestionSearchFilter, QuestionSearchItem } from '@/lib/types';

function SearchFilters({ filter, onApply }: {
  filter: QuestionSearchFilter; onApply: (filter: QuestionSearchFilter) => void;
}) {
  const [difficultyMode, setDifficultyMode] = useState<string>(filter.quiz_difficulty ??
    (filter.min_difficulty !== undefined || filter.max_difficulty !== undefined ? 'numeric' : ''));
  const [formError, setFormError] = useState('');
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const params = new URLSearchParams();
    for (const [key, value] of data.entries()) if (typeof value === 'string') params.set(key, value);
    if (difficultyMode && difficultyMode !== 'numeric') params.set('quiz_difficulty', difficultyMode);
    const next = parseQuestionSearch(params);
    if (next.min_difficulty !== undefined && next.max_difficulty !== undefined &&
      next.min_difficulty > next.max_difficulty) {
      setFormError('最低难度不能高于最高难度');
      return;
    }
    setFormError('');
    onApply({ ...next, q: filter.q, page: 1, size: filter.size });
  }
  return <form onSubmit={submit} className="space-y-4 border-b border-anvil-200 p-5 dark:border-anvil-700" aria-label="筛选题目">
    <div className="flex items-center gap-2 text-sm font-medium"><SlidersHorizontal size={16} />筛选范围</div>
    <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <label className="space-y-2 text-sm"><span>题型</span>
        <select name="type" className="forge-input" defaultValue={filter.type ?? ''}>
          <option value="">全部题型</option>
          {Object.entries(QUIZ_TYPE_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
        </select>
      </label>
      <label className="space-y-2 text-sm"><span>标签</span>
        <input name="tag" className="forge-input" maxLength={200} defaultValue={filter.tag ?? ''} placeholder="标签名称或部分文字" />
      </label>
      <label className="space-y-2 text-sm"><span>知识点</span>
        <input name="knowledge_point" className="forge-input" maxLength={200} defaultValue={filter.knowledge_point ?? ''} placeholder="知识点名称或编码" />
      </label>
      <label className="space-y-2 text-sm"><span>难度</span>
        <select className="forge-input" value={difficultyMode} onChange={e => setDifficultyMode(e.target.value)}>
          <option value="">不限难度</option><option value="numeric">编程题评分范围</option>
          {Object.entries(QUIZ_DIFFICULTY_LABELS).map(([value, label]) => <option key={value} value={value}>客观题 · {label}</option>)}
        </select>
      </label>
    </div>
    {difficultyMode === 'numeric' && <div className="flex flex-wrap items-end gap-3">
      <label className="space-y-2 text-sm"><span className="block">最低难度</span>
        <input name="min_difficulty" type="number" min={800} max={3500} required className="forge-input w-36"
          defaultValue={filter.min_difficulty ?? 800} />
      </label>
      <label className="space-y-2 text-sm"><span className="block">最高难度</span>
        <input name="max_difficulty" type="number" min={800} max={3500} required className="forge-input w-36"
          defaultValue={filter.max_difficulty ?? 3500} />
      </label>
      <p className="pb-2 text-xs text-anvil-500">按编程题库原有评分筛选（800–3500）。</p>
    </div>}
    {difficultyMode && difficultyMode !== 'numeric' &&
      <p className="text-xs text-anvil-500">按客观题库原有难度筛选；编程题评分与客观题难度分别保留。</p>}
    {formError && <p role="alert" className="text-sm text-red-600">{formError}</p>}
    <div className="flex gap-2">
      <button className="forge-btn-primary" type="submit">应用筛选</button>
      <button className="forge-btn-ghost" type="button" onClick={event => {
        event.currentTarget.form?.reset();
        setDifficultyMode('');
        setFormError('');
        onApply({ q: filter.q, page: 1, size: filter.size });
      }}>清除筛选</button>
    </div>
  </form>;
}

type ResultState = { query: string; items: QuestionSearchItem[]; total: number; error?: string; unsupported?: boolean };

function SearchContent() {
  const router = useRouter();
  const params = useSearchParams();
  const query = params.toString();
  const filter = useMemo(() => parseQuestionSearch(new URLSearchParams(query)), [query]);
  const [result, setResult] = useState<ResultState>();
  const [attempt, setAttempt] = useState(0);
  const [pending, setPending] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    setPending(true);
    searchQuestions(filter, controller.signal).then(response => {
      if (!controller.signal.aborted) setResult({ query, items: response.data ?? [], total: response.meta?.total ?? 0 });
    }).catch(error => {
      if (controller.signal.aborted) return;
      const unsupported = error instanceof APIError && error.status === 404;
      setResult({ query, items: [], total: 0, unsupported, error: unsupported
        ? '当前服务尚不支持统一搜索，请先升级服务端。'
        : error instanceof Error ? error.message : '搜索失败，请稍后重试。' });
    }).finally(() => { if (!controller.signal.aborted) setPending(false); });
    return () => controller.abort();
  }, [filter, query, attempt]);
  const current = result?.query === query ? result : undefined;
  const loading = pending || !current;
  const go = (next: QuestionSearchFilter) => router.push(questionResultsHref(next));

  return <div className="af-page">
    <PageHeader eyebrow="内容库" title="搜索题目"
      description="按题号、标题、标签或知识点查找，支持部分文字匹配，覆盖编程题与客观题。" />
    <section className="af-table-panel">
      <SearchFilters key={query} filter={filter} onApply={go} />
      <div aria-live="polite" aria-busy={loading}>
        {loading ? <div className="p-8 text-sm text-anvil-500">正在搜索题目…</div> : current?.error ?
          <EmptyState icon={Search} title="暂时无法搜索" description={current.error} action={current.unsupported ?
            <div className="flex flex-wrap gap-3"><Link className="forge-btn-secondary" href="/problems">打开编程题库</Link>
              <Link className="forge-btn-secondary" href="/quizzes">打开客观题库</Link></div> :
            <button className="forge-btn-secondary" onClick={() => setAttempt(value => value + 1)}>重试</button>} /> :
          <>
            <div className="border-b border-anvil-200 px-5 py-4 text-sm text-anvil-500 dark:border-anvil-700">
              {filter.q ? '“' + filter.q + '” · ' : ''}找到 {current?.total ?? 0} 道题目
            </div>
            {current?.items.length ? <ul className="divide-y divide-anvil-200 dark:divide-anvil-700">
              {current.items.map(item => <li key={item.source + ':' + item.id} className="p-5">
                <div className="flex flex-wrap items-center gap-2 text-xs text-anvil-500">
                  <span className="forge-badge">{QUIZ_TYPE_LABELS[item.type]}</span><span>题号 {item.code || item.id}</span>
                  <span>{item.difficulty !== undefined ? item.difficulty + ' · ' + getDifficultyLabel(item.difficulty) :
                    item.quiz_difficulty ? QUIZ_DIFFICULTY_LABELS[item.quiz_difficulty] : '未设置难度'}</span>
                </div>
                <Link className="my-2 flex w-fit max-w-full items-start gap-2 text-base font-semibold text-anvil-900 hover:text-forge-600 dark:text-anvil-100"
                  href={questionDetailHref(item)}><span className="break-words">{item.title || '未命名题目'}</span>
                  <ArrowUpRight size={17} className="mt-1 shrink-0" /></Link>
                <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-anvil-500">
                  {item.tags.length > 0 && <span className="break-words">标签：{item.tags.join('、')}</span>}
                  {item.knowledge_points.length > 0 && <span className="break-words">知识点：{item.knowledge_points.join('、')}</span>}
                </div>
              </li>)}
            </ul> : <EmptyState icon={Search} title="没有找到匹配题目" description="试试更短的关键词，或减少标签、知识点和难度条件。" />}
            {(current?.total ?? 0) > 0 && <div className="px-5 pb-5">
              <Pagination page={filter.page ?? 1} size={filter.size ?? 20} total={current?.total ?? 0}
                onPageChange={page => go({ ...filter, page })}
                onSizeChange={size => go({ ...filter, size, page: 1 })} />
            </div>}
          </>}
      </div>
    </section>
  </div>;
}

export default function SearchPage() {
  return <Suspense fallback={<div className="p-8 text-sm text-anvil-500">正在加载搜索…</div>}><SearchContent /></Suspense>;
}
