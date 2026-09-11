'use client';

import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import Link from 'next/link';
import {
  BookOpen,
  Filter,
  Plus,
  Search,
  Upload,
  X,
} from 'lucide-react';

import Pagination from '@/components/Pagination';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import RefreshButton from '@/components/RefreshButton';
import { useProblems } from '@/hooks/useProblems';
import { useTags } from '@/hooks/useProblems';
import type { ProblemFilter, ProblemLevel, ProblemStatus } from '@/lib/types';
import {
  cn,
  formatDate,
  statusBadgeColor,
} from '@/lib/utils';
import {
  DEFAULT_PAGE_SIZE,
  DIFFICULTY_MAX,
  DIFFICULTY_MIN,
  DIFFICULTY_STEP,
  getDifficultyLabel,
  PROBLEM_LEVEL_LABELS,
  STATUS_LABELS,
} from '@/lib/constants';

// ---------------------------------------------------------------------------
// Filter Bar
// ---------------------------------------------------------------------------

interface FilterBarProps {
  filter: ProblemFilter;
  onFilterChange: (filter: ProblemFilter) => void;
}

function FilterBar({ filter, onFilterChange }: FilterBarProps) {
  const { tags: tagCategories } = useTags();
  const [searchInput, setSearchInput] = useState(filter.search ?? '');
  const [showAdvanced, setShowAdvanced] = useState(false);
  useEffect(() => { setSearchInput(filter.search ?? ''); }, [filter.search]);

  const handleSearch = useCallback(() => {
    onFilterChange({ ...filter, search: searchInput || undefined, page: 1 });
  }, [filter, onFilterChange, searchInput]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter') handleSearch();
    },
    [handleSearch],
  );

  const allTags = useMemo(() => {
    return tagCategories;
  }, [tagCategories]);

  return (
    <div className="border-b border-[var(--dl)]">
      {/* Primary filters */}
      <div className="af-toolbar">
        {/* Search */}
        <div className="relative flex-1 min-w-[200px]">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-anvil-400" />
          <input
            type="text"
            aria-label="搜索编程题标题"
            placeholder="搜索题目标题…"
            className="forge-input pl-10"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            onKeyDown={handleKeyDown}
          />
          {searchInput && (
            <button
              aria-label="清除标题搜索"
              className="absolute right-3 top-1/2 -translate-y-1/2 text-anvil-400 hover:text-anvil-600"
              onClick={() => {
                setSearchInput('');
                onFilterChange({ ...filter, search: undefined, page: 1 });
              }}
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        {/* Level filter */}
        <select
          aria-label="编程题类型"
          className="forge-input w-auto"
          value={filter.level ?? ''}
          onChange={(e) =>
            onFilterChange({
              ...filter,
              level: (e.target.value as ProblemLevel) || undefined,
              page: 1,
            })
          }
        >
          <option value="">全部类型</option>
          <option value="syntax">语法题</option>
          <option value="algorithm">算法题</option>
          <option value="gplt_l1">天梯 L1</option>
          <option value="gplt_l2">天梯 L2</option>
          <option value="gplt_l3">天梯 L3</option>
        </select>

        {/* Status filter */}
        <select
          aria-label="题目状态"
          className="forge-input w-auto"
          value={filter.status ?? ''}
          onChange={(e) =>
            onFilterChange({
              ...filter,
              status: (e.target.value as ProblemStatus) || undefined,
              page: 1,
            })
          }
        >
          <option value="">全部状态</option>
          <option value="draft">{STATUS_LABELS.draft}</option>
          <option value="generating">{STATUS_LABELS.generating}</option>
          <option value="review">{STATUS_LABELS.review}</option>
          <option value="published">{STATUS_LABELS.published}</option>
          <option value="rejected">{STATUS_LABELS.rejected}</option>
        </select>

        {/* Toggle advanced */}
        <button
          aria-expanded={showAdvanced}
          aria-controls="problem-advanced-filters"
          className="forge-btn-secondary"
          onClick={() => setShowAdvanced((prev) => !prev)}
        >
          <Filter className="h-4 w-4" />
          高级筛选
        </button>

        {/* Search button */}
        <button className="forge-btn-secondary" onClick={handleSearch}>
          <Search className="h-4 w-4" />
          搜索
        </button>
      </div>

      {/* Advanced filters */}
      {showAdvanced && (
        <div id="problem-advanced-filters" className="flex flex-wrap items-end gap-4 border-t border-[var(--dl)] bg-[var(--ds)] px-5 py-4">
          {/* Difficulty range */}
          <div className="space-y-1">
            <label className="text-xs font-medium text-anvil-500 dark:text-anvil-400">
              难度范围
            </label>
            <div className="flex items-center gap-2">
              <input
                type="number"
                aria-label="最低难度"
                min={DIFFICULTY_MIN}
                max={DIFFICULTY_MAX}
                step={DIFFICULTY_STEP}
                placeholder={String(DIFFICULTY_MIN)}
                className="forge-input w-24"
                value={filter.difficulty_min ?? ''}
                onChange={(e) =>
                  onFilterChange({
                    ...filter,
                    difficulty_min: e.target.value
                      ? Number(e.target.value)
                      : undefined,
                    page: 1,
                  })
                }
              />
              <span className="text-anvil-400">-</span>
              <input
                type="number"
                aria-label="最高难度"
                min={DIFFICULTY_MIN}
                max={DIFFICULTY_MAX}
                step={DIFFICULTY_STEP}
                placeholder={String(DIFFICULTY_MAX)}
                className="forge-input w-24"
                value={filter.difficulty_max ?? ''}
                onChange={(e) =>
                  onFilterChange({
                    ...filter,
                    difficulty_max: e.target.value
                      ? Number(e.target.value)
                      : undefined,
                    page: 1,
                  })
                }
              />
            </div>
          </div>

          {/* Tag filter */}
          <div className="space-y-1">
            <label className="text-xs font-medium text-anvil-500 dark:text-anvil-400">
              标签
            </label>
            <select
              aria-label="添加标签筛选"
              className="forge-input w-auto"
              value=""
              onChange={(e) => {
                if (!e.target.value) return;
                const current = filter.tags ?? [];
                if (!current.includes(e.target.value)) {
                  onFilterChange({
                    ...filter,
                    tags: [...current, e.target.value],
                    page: 1,
                  });
                }
              }}
            >
              <option value="">选择标签...</option>
              {allTags.map((tag) => (
                <option key={tag.id} value={tag.tag_name}>
                  {tag.display_name}
                </option>
              ))}
            </select>
          </div>

          {/* Sort */}
          <div className="space-y-1">
            <label className="text-xs font-medium text-anvil-500 dark:text-anvil-400">
              排序
            </label>
            <select
              aria-label="排序字段"
              className="forge-input w-auto"
              value={filter.sort_by ?? 'created_at'}
              onChange={(e) =>
                onFilterChange({
                  ...filter,
                  sort_by: e.target.value as ProblemFilter['sort_by'],
                })
              }
            >
              <option value="created_at">创建时间</option>
              <option value="difficulty">难度</option>
              <option value="title">标题</option>
              <option value="serial_number">编号</option>
            </select>
          </div>

          {/* Sort order */}
          <div className="space-y-1">
            <label className="text-xs font-medium text-anvil-500 dark:text-anvil-400">
              顺序
            </label>
            <select
              aria-label="排序顺序"
              className="forge-input w-auto"
              value={filter.sort_order ?? 'desc'}
              onChange={(e) =>
                onFilterChange({
                  ...filter,
                  sort_order: e.target.value as 'asc' | 'desc',
                })
              }
            >
              <option value="desc">降序</option>
              <option value="asc">升序</option>
            </select>
          </div>

          {/* Reset */}
          <button
            className="forge-btn-ghost"
            onClick={() => {
              setSearchInput('');
              onFilterChange({ page: 1, size: filter.size ?? DEFAULT_PAGE_SIZE });
            }}
          >
            <X className="h-4 w-4" />
            重置筛选
          </button>
        </div>
      )}

      {/* Active tag chips */}
      {filter.tags && filter.tags.length > 0 && (
        <div className="flex flex-wrap gap-2 border-t border-[var(--dl)] px-5 py-3">
          {filter.tags.map((tag) => (
            <span
              key={tag}
              className="forge-badge"
            >
              {tag}
              <button
                aria-label={`移除标签：${tag}`}
                className="ml-1 hover:text-forge-900 dark:hover:text-forge-200"
                onClick={() =>
                  onFilterChange({
                    ...filter,
                    tags: filter.tags!.filter((t) => t !== tag),
                    page: 1,
                  })
                }
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ProblemsList() {
  const querySearch = useSearchParams().get('search') ?? '';
  const previousSearch = useRef(querySearch);
  const { problems, total, page, loading, error, refresh, setFilter, filter } =
    useProblems({ page: 1, size: DEFAULT_PAGE_SIZE, sort_by: 'created_at', sort_order: 'desc', search: querySearch || undefined });
  useEffect(() => {
    if (previousSearch.current === querySearch) return;
    previousSearch.current = querySearch;
    setFilter({ ...filter, page: 1, search: querySearch || undefined });
  }, [querySearch, filter, setFilter]);
  const hasFilters = Boolean(filter.search || filter.level || filter.status || filter.tags?.length
    || filter.difficulty_min !== undefined || filter.difficulty_max !== undefined);

  return (
    <div className="af-page">
      <PageHeader eyebrow="内容管理" title="编程题库" description="从题面到测试数据，集中管理你的编程题与发布状态。"
        actions={<>
          <Link href="/problems/hydro/import" className="forge-btn-secondary"><Upload className="h-4 w-4" />校验 Hydro 包</Link>
          <Link href="/problems/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题目</Link>
        </>}
      />

      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
        {error}<button className="ml-3 underline" onClick={refresh}>重新加载</button>
      </div>}

      <section className="af-table-panel" aria-label="编程题列表" aria-busy={loading}>
        <FilterBar filter={filter} onFilterChange={setFilter} />
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm text-[var(--dm)]">
          <span>{loading && problems.length === 0 ? '正在读取题库…' : error ? '暂时无法读取题目数量' : <>共 <strong className="font-medium text-[var(--dt)]">{total}</strong> 道{hasFilters ? '符合条件的' : ''}题目</>}</span>
          <RefreshButton onClick={refresh} loading={loading} />
        </div>

        <div className="af-table-scroll">
          <table className="forge-table">
            <thead><tr><th>题目</th><th className="w-32">类型 / 难度</th><th className="w-28">状态</th><th className="w-40">创建时间</th><th className="w-24">操作</th></tr></thead>
            <tbody>
              {loading && problems.length === 0 ? Array.from({ length: 5 }).map((_, row) => <tr key={row}>{Array.from({ length: 5 }).map((_, column) => <td key={column}><div className="forge-skeleton h-5 w-full" /></td>)}</tr>)
                : problems.length === 0 ? <tr><td colSpan={5}><EmptyState icon={BookOpen}
                  title={error ? '题库暂时不可用' : hasFilters ? '没有符合条件的编程题' : '从第一道题开始积累'}
                  description={error ? '重新加载后查看题面、测试数据和发布状态。' : hasFilters ? '调整标题、类型或高级筛选，再试一次。' : '描述出题想法，生成题面、题解与测试数据，完成后会显示在题库中。'}
                  action={error ? <button className="forge-btn-secondary" onClick={refresh}>重新加载</button> : <Link href="/problems/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题目</Link>}
                /></td></tr>
                : problems.map((problem) => <tr key={problem.id}>
                  <td className="min-w-[280px]">
                    <Link href={`/problems/${problem.id}`} className="af-link font-medium">{problem.title}</Link>
                    <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-[var(--dm)]">
                      <span className="font-mono">{problem.serial_number}</span>
                      {problem.tags.slice(0, 3).map((tag) => <span key={tag}>{tag}</span>)}
                      {problem.tags.length > 3 && <span title={problem.tags.join('、')}>+{problem.tags.length - 3} 个标签</span>}
                    </div>
                  </td>
                  <td>
                    <div>{PROBLEM_LEVEL_LABELS[problem.level] ?? problem.level}</div>
                    <div className="mt-1 text-xs text-[var(--dm)]"><span className="font-medium">{problem.difficulty}</span> · {getDifficultyLabel(problem.difficulty)}</div>
                  </td>
                  <td><span className={cn('forge-badge', statusBadgeColor(problem.status))}>{STATUS_LABELS[problem.status] ?? problem.status}</span></td>
                  <td className="text-sm text-[var(--dm)]">{formatDate(problem.created_at)}</td>
                  <td><Link href={`/problems/${problem.id}`} className="af-link">查看</Link></td>
                </tr>)}
            </tbody>
          </table>
        </div>
        <div className="af-pagination"><Pagination page={page} total={total} size={filter.size ?? DEFAULT_PAGE_SIZE}
          onPageChange={(next) => setFilter({ ...filter, page: next })}
          onSizeChange={(next) => setFilter({ ...filter, size: next, page: 1 })} /></div>
      </section>
    </div>
  );
}

export default function ProblemsPage() {
  return <Suspense fallback={<div className="af-page"><div className="forge-skeleton h-32" /></div>}><ProblemsList /></Suspense>;
}
