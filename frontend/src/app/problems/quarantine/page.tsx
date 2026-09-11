'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { Eye, Loader2, ShieldAlert, ShieldCheck } from 'lucide-react';

import { EmptyState, PageHeader, SectionHeading } from '@/components/ui/Workspace';
import Pagination from '@/components/Pagination';
import RefreshButton from '@/components/RefreshButton';
import { useProblems } from '@/hooks/useProblems';
import { getReviewSettings, updateReviewSettings } from '@/lib/api';
import type { Problem, ReviewSettings } from '@/lib/types';
import {
  cn,
  difficultyColor,
  formatDate,
  levelBadgeColor,
  truncate,
} from '@/lib/utils';
import {
  DEFAULT_PAGE_SIZE,
  getDifficultyLabel,
  PROBLEM_LEVEL_LABELS,
} from '@/lib/constants';

function metadataText(problem: Problem, key: string): string {
  const value = problem.metadata_json?.[key];
  return typeof value === 'string' ? value.trim() : '';
}

const QUARANTINE_REASON_LABELS: Record<string, string> = {
  'automated LLM review rejected candidate': '自动验题未通过',
  'public_release denied by current provenance policy': '来源策略未允许公开发布',
  'missing detailed solution': '缺少完整题解',
  'missing successful main solution': '缺少已验证的标准程序',
  'missing successful brute solution': '缺少已验证的暴力校验程序',
  'missing runnable test artifacts': '缺少可运行测试数据',
  'missing current active statement vector': '缺少当前题面向量',
  'problem edit refresh is stale': '题目编辑后的验证已过期',
};

function localizeQuarantineReason(reason: string): string {
  return reason
    .split(';')
    .map((part) => {
      const normalized = part.trim();
      return QUARANTINE_REASON_LABELS[normalized] ?? normalized;
    })
    .filter(Boolean)
    .join('；');
}

function quarantineReason(problem: Problem): string {
  const reason =
    metadataText(problem, 'publication_quarantine_reason') ||
    metadataText(problem, 'review_quarantine_reason') ||
    metadataText(problem, 'quarantine_reason') ||
    '等待管理员审核';
  return localizeQuarantineReason(reason);
}

export default function QuarantinePage() {
  const { problems, total, page, loading, error, refresh, setFilter, filter } =
    useProblems({
      status: 'quarantined',
      page: 1,
      size: DEFAULT_PAGE_SIZE,
      sort_by: 'created_at',
      sort_order: 'desc',
    });

  const [reviewSettings, setReviewSettings] = useState<ReviewSettings | null>(null);
  const [settingsLoading, setSettingsLoading] = useState(true);
  const [settingsSaving, setSettingsSaving] = useState(false);
  const [settingsError, setSettingsError] = useState('');
  const [settingsMessage, setSettingsMessage] = useState('');

  const loadReviewSettings = useCallback(async () => {
    setSettingsLoading(true);
    setSettingsError('');
    try {
      const response = await getReviewSettings();
      if (!response.data) {
        throw new Error('自动审核设置响应为空');
      }
      setReviewSettings(response.data);
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : '读取自动审核设置失败');
    } finally {
      setSettingsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadReviewSettings();
  }, [loadReviewSettings]);

  const handleAutoApproveToggle = useCallback(async () => {
    if (!reviewSettings || settingsSaving) return;

    const nextValue = !reviewSettings.auto_approve_public_release;
    setSettingsSaving(true);
    setSettingsError('');
    setSettingsMessage('');
    try {
      const response = await updateReviewSettings({
        auto_approve_public_release: nextValue,
      });
      if (!response.data) {
        throw new Error('自动审核设置响应为空');
      }
      setReviewSettings(response.data);
      setSettingsMessage(
        nextValue
          ? '已开启：后续符合条件的新题将自动通过人工审核。'
          : '已关闭：后续题目仍需人工确认后发布。',
      );
    } catch (err) {
      setSettingsError(err instanceof Error ? err.message : '保存自动审核设置失败');
    } finally {
      setSettingsSaving(false);
    }
  }, [reviewSettings, settingsSaving]);

  const autoApproveEnabled = reviewSettings?.auto_approve_public_release ?? false;

  return (
    <div className="af-page">
      <PageHeader title="隔离区" description="查看暂缓发布的题目，按具体原因补齐材料、修复并审核。" actions={<RefreshButton onClick={refresh} loading={loading} />} />
      <section className="af-table-panel">
        <div className="af-toolbar"><SectionHeading title="待处理题目" description="隔离题目不会进入普通题库或 Hydro 导出；满足全部门禁后可在详情页审核发布。" /><span className="forge-badge bg-[var(--ds)] text-[var(--dm)]">{loading ? '读取中…' : error ? '暂时不可用' : total + ' 道题目'}</span></div>
        {error && <div role="alert" className="m-5 rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{error}<button type="button" className="ml-3 underline" onClick={refresh}>重试</button></div>}
        <div className="af-table-scroll">
        <table className="forge-table min-w-[1000px]">
          <thead>
            <tr>
              <th className="w-24">编号</th>
              <th>题目</th>
              <th className="w-24">类型</th>
              <th className="w-28">难度</th>
              <th>隔离原因</th>
              <th className="w-40">更新时间</th>
              <th className="w-24">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading && problems.length === 0 ? (
              Array.from({ length: 4 }).map((_, index) => (
                <tr key={`skeleton-${index}`}>
                  {Array.from({ length: 7 }).map((__, cell) => (
                    <td key={cell}>
                      <div className="forge-skeleton h-4 w-full" />
                    </td>
                  ))}
                </tr>
              ))
            ) : problems.length === 0 ? (
              <tr>
                <td colSpan={7}><EmptyState icon={ShieldAlert} title={error ? '暂时无法读取隔离题目' : '隔离区为空'} description={error ? '请刷新列表后重试。' : '有需要处理的题目时，会在这里显示具体原因。'} /></td>
              </tr>
            ) : (
              problems.map((problem) => (
                <tr key={problem.id}>
                  <td className="font-mono text-sm text-anvil-500">
                    {problem.serial_number}
                  </td>
                  <td>
                    <Link
                      href={`/problems/${problem.id}`}
                      className="af-link"
                    >
                      {truncate(problem.title, 45)}
                    </Link>
                  </td>
                  <td>
                    <span className={cn('forge-badge', levelBadgeColor(problem.level))}>
                      {PROBLEM_LEVEL_LABELS[problem.level] ?? problem.level}
                    </span>
                  </td>
                  <td>
                    <span className={cn('font-semibold', difficultyColor(problem.difficulty))}>
                      {problem.difficulty}
                    </span>
                    <span className="ml-1 text-xs text-anvil-400">
                      {getDifficultyLabel(problem.difficulty)}
                    </span>
                  </td>
                  <td>
                    <p className="max-w-xl break-words text-sm text-warning-700 dark:text-warning-300">
                      {quarantineReason(problem)}
                    </p>
                  </td>
                  <td className="text-sm text-anvil-500 dark:text-anvil-400">
                    {formatDate(problem.updated_at)}
                  </td>
                  <td>
                    <Link
                      href={`/problems/${problem.id}`}
                      className="af-link"
                    >
                      <Eye className="h-4 w-4" />
                      审核
                    </Link>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
        </div><div className="af-pagination">
      <Pagination
        page={page}
        total={total}
        size={filter.size ?? DEFAULT_PAGE_SIZE}
        onPageChange={(nextPage) => setFilter({ ...filter, page: nextPage })}
        onSizeChange={(size) => setFilter({ ...filter, size, page: 1 })}
      />
        </div>
      </section>
      <details className="af-form-details mt-6"><summary>测试时的自动审核设置<span className="ml-2 text-xs text-[var(--dm)]">{autoApproveEnabled ? '已开启' : '已关闭'}</span></summary><div>
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="max-w-3xl">
            <div className="flex flex-wrap items-center gap-2">
              <ShieldCheck className="h-5 w-5 text-forge-600 dark:text-forge-400" />
              <h2 className="font-semibold text-anvil-900 dark:text-white">
                测试时自动通过人工审核
              </h2>
              <span
                className={cn(
                  'forge-badge',
                  autoApproveEnabled
                    ? 'bg-success-100 text-success-700 dark:bg-success-500/20 dark:text-success-300'
                    : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-700 dark:text-anvil-300',
                )}
              >
                {autoApproveEnabled ? '已开启' : '已关闭'}
              </span>
            </div>
            <p className="mt-2 text-sm text-anvil-600 dark:text-anvil-300">
              开启后，后续生成且通过 GPT 验题、去重、编译、沙箱与数据完整性检查，仅等待来源人工确认的题目会自动发布。
            </p>
            <p className="mt-1 text-xs text-warning-700 dark:text-warning-300">
              GPT 验题未通过、重复题或其他门禁失败仍会进入隔离区；已有隔离题不会被批量放行。
            </p>
          </div>

          <div className="flex items-center gap-3">
            {settingsLoading ? (
              <div className="flex items-center gap-2 text-sm text-anvil-500">
                <Loader2 className="h-4 w-4 animate-spin" />
                读取设置
              </div>
            ) : (
              <>
                <span className="text-sm font-medium text-anvil-700 dark:text-anvil-200">
                  自动通过
                </span>
                <button
                  type="button"
                  role="switch"
                  aria-label="测试时自动通过人工审核"
                  aria-checked={autoApproveEnabled}
                  disabled={!reviewSettings || settingsSaving}
                  onClick={() => void handleAutoApproveToggle()}
                  className={cn(
                    'relative inline-flex h-7 w-12 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-forge-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-60',
                    autoApproveEnabled
                      ? 'bg-success-500'
                      : 'bg-anvil-300 dark:bg-anvil-600',
                  )}
                >
                  <span
                    className={cn(
                      'inline-flex h-5 w-5 items-center justify-center rounded-full bg-white shadow-sm transition-transform',
                      autoApproveEnabled ? 'translate-x-6' : 'translate-x-1',
                    )}
                  >
                    {settingsSaving && (
                      <Loader2 className="h-3 w-3 animate-spin text-anvil-500" />
                    )}
                  </span>
                </button>
              </>
            )}
          </div>
        </div>

        {settingsError && (
          <p className="mt-3 text-sm text-danger-600 dark:text-danger-400">
            {settingsError}
          </p>
        )}
        {settingsMessage && (
          <p className="mt-3 text-sm text-success-700 dark:text-success-300">
            {settingsMessage}
          </p>
        )}
      </div></details>

    </div>
  );
}
