'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import {
  AlertCircle,
  ArrowLeft,
  Check,
  ExternalLink,
  Loader2,
  RotateCcw,
  Wifi,
  WifiOff,
  X,
  XCircle,
} from 'lucide-react';

import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import RefreshButton from '@/components/RefreshButton';
import SimilarityPanel from '@/components/workflow/SimilarityPanel';
import {
  useWorkflow,
  useWorkflowActions,
  useWorkflowEvents,
} from '@/hooks/useWorkflow';
import type { SimilarityResult, WorkflowEvent } from '@/lib/types';
import type { WorkflowReviewReference } from '@/lib/types';
import { cn, formatDate, formatRelativeTime, statusBadgeColor } from '@/lib/utils';
import { STATUS_LABELS } from '@/lib/constants';
import { isTerminal as statusIsTerminal, normalizeStatus } from '@/lib/status';

// ---------------------------------------------------------------------------
// Event Log
// ---------------------------------------------------------------------------

const EVENT_TYPE_LABELS: Record<string, string> = {
  step_started: '开始',
  step_completed: '完成',
  step_failed: '失败',
  workflow_completed: '完成',
  workflow_failed: '失败',
  log: '日志',
};

function EventLog({ events }: { events: WorkflowEvent[] }) {
  if (events.length === 0) {
    return (
      <p className="py-4 text-center text-sm text-anvil-400">
        暂无可显示的事件记录
      </p>
    );
  }

  return (
    <div className="af-event-log scrollbar-thin">
      {events.map((event, i) => (
        <div
          key={i}
          className="flex items-start gap-2 rounded p-2 text-sm hover:bg-anvil-50 dark:hover:bg-anvil-800/50"
        >
          <span className="mt-0.5 shrink-0 text-xs text-anvil-400">
            {new Date(event.timestamp).toLocaleTimeString('zh-CN')}
          </span>
          <span
            className={cn(
              'forge-badge shrink-0 text-xs',
              event.type === 'step_completed' || event.type === 'workflow_completed'
                ? 'bg-success-100 text-success-700 dark:bg-success-900/30 dark:text-success-400'
                : event.type === 'step_failed' || event.type === 'workflow_failed'
                  ? 'bg-danger-100 text-danger-700 dark:bg-danger-900/30 dark:text-danger-400'
                  : event.type === 'step_started'
                    ? 'bg-forge-100 text-forge-700 dark:bg-forge-900/30 dark:text-forge-400'
                    : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300',
            )}
          >
            {EVENT_TYPE_LABELS[event.type] ?? event.type}
          </span>
          <span className="flex-1 text-anvil-600 dark:text-anvil-300">
            {event.message ?? event.step_name ?? '-'}
          </span>
        </div>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Reject Dialog
// ---------------------------------------------------------------------------

function RejectDialog({
  open,
  onClose,
  onConfirm,
  loading,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: (reason: string) => void;
  loading: boolean;
}) {
  const [reason, setReason] = useState('');

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="mx-4 w-full max-w-md rounded-xl bg-white p-6 shadow-xl dark:bg-anvil-900">
        <h3 className="text-lg font-semibold">拒绝工作流</h3>
        <p className="mt-2 text-sm text-anvil-500 dark:text-anvil-400">
          请输入拒绝原因（必填）：
        </p>
        <textarea
          className="forge-input mt-3 min-h-[100px] resize-y"
          placeholder="输入拒绝原因..."
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          required
        />
        <div className="mt-4 flex justify-end gap-3">
          <button
            className="forge-btn-secondary"
            onClick={onClose}
            disabled={loading}
          >
            取消
          </button>
          <button
            className="forge-btn-danger"
            onClick={() => onConfirm(reason.trim())}
            disabled={loading || reason.trim() === ''}
          >
            {loading ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <XCircle className="h-4 w-4" />
            )}
            确认拒绝
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Workflow Detail Page
// ---------------------------------------------------------------------------

export default function WorkflowDetailPage() {
  const params = useParams();
  const id = params.id as string;

  const { workflow, loading, error, refresh } = useWorkflow(id);
  const { events, latestEvent, connected, error: sseError } =
    useWorkflowEvents(id);
  const {
    approve,
    reject,
    retry,
    cancel,
    loading: actionLoading,
    error: actionError,
  } = useWorkflowActions(id);

  const [rejectDialogOpen, setRejectDialogOpen] = useState(false);
  const [submittedReview, setSubmittedReview] =
    useState<WorkflowReviewReference | null>(null);

  // Refresh workflow data when we receive new events
  useEffect(() => {
    if (latestEvent) {
      refresh();
    }
  }, [latestEvent, refresh]);

  // Handle approve
  const handleApprove = useCallback(async () => {
    if (!workflow?.review_request) return;
    setSubmittedReview(workflow.review_request);
    const result = await approve(workflow.review_request);
    await refresh();
    if (!result) setSubmittedReview(null);
  }, [approve, refresh, workflow?.review_request]);

  // Handle reject
  const handleReject = useCallback(
    async (reason: string) => {
      if (!workflow?.review_request) return;
      setSubmittedReview(workflow.review_request);
      const result = await reject(workflow.review_request, reason);
      await refresh();
      if (result) {
        setRejectDialogOpen(false);
      } else {
        setSubmittedReview(null);
      }
    },
    [reject, refresh, workflow?.review_request],
  );

  // Handle retry
  const handleRetry = useCallback(async () => {
    const result = await retry();
    if (result) refresh();
  }, [retry, refresh]);

  // Handle cancel
  const handleCancel = useCallback(async () => {
    if (!window.confirm('确定要取消此工作流吗？')) return;
    const result = await cancel();
    if (result) refresh();
  }, [cancel, refresh]);

  // Loading state
  if (loading && !workflow) {
    return (
      <div className="forge-page">
        <div className="space-y-4">
          <div className="forge-skeleton h-8 w-64" />
          <div className="forge-skeleton h-4 w-96" />
          <div className="forge-card">
            <div className="space-y-6">
              {Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="flex gap-4">
                  <div className="forge-skeleton h-5 w-5 rounded-full" />
                  <div className="flex-1 space-y-2">
                    <div className="forge-skeleton h-4 w-48" />
                    <div className="forge-skeleton h-3 w-32" />
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    );
  }

  // Error state
  if (error || !workflow) {
    return (
      <div className="forge-page">
        <div className="flex flex-col items-center justify-center gap-4 py-20">
          <AlertCircle className="h-12 w-12 text-danger-400" />
          <p className="text-danger-500">{error ?? '工作流未找到'}</p>
          <Link href="/workflows" className="forge-btn-secondary">
            <ArrowLeft className="h-4 w-4" />
            返回列表
          </Link>
        </div>
      </div>
    );
  }

  const friendly = normalizeStatus(workflow.status);
  const isTerminal = statusIsTerminal(friendly);
  const problemId = workflow.state?.problem_id;
  const currentReview = workflow.review_request;
  const submittedCurrentReview = Boolean(
    submittedReview &&
      currentReview &&
      submittedReview.token === currentReview.token &&
      submittedReview.run_id === currentReview.run_id &&
      submittedReview.review_attempt === currentReview.review_attempt,
  );
  const reviewRequest = currentReview?.decision || submittedCurrentReview
    ? undefined
    : currentReview;

  const stepLabels: Record<string, string> = { pending: '等待中', running: '进行中', completed: '已完成', failed: '失败', skipped: '已跳过' };
  return <div className="af-page af-task-detail">
    <Link href="/workflows" className="af-link"><ArrowLeft size={15} />返回任务中心</Link>
    <PageHeader eyebrow="生成与验证" title="生成任务" description={workflow.start_time ? '开始于 ' + formatDate(workflow.start_time) : '查看当前进度与生成结果'}
      actions={<>
        <div className="flex items-center gap-2">
          {friendly === 'waiting_review' && reviewRequest && (
            <>
              <button
                className="forge-btn-primary"
                onClick={handleApprove}
                disabled={actionLoading}
              >
                {actionLoading ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Check className="h-4 w-4" />
                )}
                通过审核
              </button>
              <button
                className="forge-btn-danger"
                onClick={() => setRejectDialogOpen(true)}
                disabled={actionLoading}
              >
                <XCircle className="h-4 w-4" />
                拒绝
              </button>
            </>
          )}
          {(friendly === 'failed') && (
            <button
              className="forge-btn-secondary"
              onClick={handleRetry}
              disabled={actionLoading}
            >
              {actionLoading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <RotateCcw className="h-4 w-4" />
              )}
              重试
            </button>
          )}
          {friendly === 'running' && (
            <button
              className="forge-btn-ghost text-danger-500 hover:text-danger-600"
              onClick={handleCancel}
              disabled={actionLoading}
            >
              <X className="h-4 w-4" />
              取消
            </button>
          )}
          <RefreshButton
            onClick={refresh}
            loading={loading}
            label="刷新"
            variant="ghost"
          />
        </div>
</>} />
    <div className="af-task-state">
      <span className={cn('forge-badge', statusBadgeColor(friendly))}>{STATUS_LABELS[friendly] ?? friendly}</span>
      <div><strong>{friendly === 'completed' ? '这次创作已完成' : friendly === 'failed' ? '任务需要处理' : friendly === 'waiting_review' ? '等待你的审核' : friendly === 'cancelled' ? '任务已停止' : '正在推进创作'}</strong>
        <p>{problemId ? '生成结果已保存，可以打开题目继续查看与编辑。' : workflow.close_time ? '结束于 ' + formatRelativeTime(workflow.close_time) : '进度保存在服务端，可以稍后返回查看。'}</p></div>
      {problemId && <Link href={'/problems/' + problemId} className="forge-btn-primary"><ExternalLink size={16} />打开题目</Link>}
      {!isTerminal && <span className="af-task-connection">{connected ? <Wifi size={15} /> : <WifiOff size={15} />}{connected ? '实时更新' : '正在重连'}</span>}
    </div>
      {/* Action error */}
      {actionError && (
        <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
          {actionError}
        </div>
      )}

      {/* Failure reason */}
      {workflow.failure_reason && (
        <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 dark:bg-danger-500/10">
          <div className="flex items-start gap-2">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0 text-danger-500" />
            <div>
              <p className="text-sm font-medium text-danger-600 dark:text-danger-400">
                工作流失败原因
              </p>
              <p className="mt-1 whitespace-pre-wrap break-words font-mono text-xs text-danger-600 dark:text-danger-400">
                {workflow.failure_reason}
              </p>
            </div>
          </div>
        </div>
      )}


    <div className="af-task-layout">
      <div className="af-task-main">
        {workflow.steps && workflow.steps.length > 0 && <section className="af-panel">
          <SectionHeading title="执行步骤" description={workflow.steps.filter(step => step.status === 'completed').length + ' / ' + workflow.steps.length + ' 步已完成'} />
          <ol className="af-step-timeline">{workflow.steps.map((step, index) => <li key={index} className={'is-' + step.status}>
            <span className="af-step-dot">{step.status === 'completed' ? <Check size={15} /> : index + 1}</span>
            <div><strong>{step.description || step.name}</strong>{step.result?.message && <p>{step.result.message}</p>}</div><small>{stepLabels[step.status] ?? step.status}</small>
          </li>)}</ol>
        </section>}
        <section className="af-panel">
          <SectionHeading title="任务动态" description="按发生顺序查看生成、验证和处理信息。" actions={<span className="af-muted">{events.length} 条事件</span>} />
          <EventLog events={events} />
          {sseError && <p role="status" className="af-small-error">{sseError}</p>}
        </section>
      </div>
      <aside className="af-task-rail">
        <SimilarityPanel result={workflow.state?.similarity as SimilarityResult | undefined} />
        <details className="af-panel af-runtime-details"><summary>运行信息</summary>
          <dl className="space-y-4 text-sm">
            <div className="flex justify-between">
              <dt className="text-anvil-500 dark:text-anvil-400">Workflow ID</dt>
              <dd className="font-mono text-xs break-all max-w-[70%] text-right">{workflow.workflow_id}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-anvil-500 dark:text-anvil-400">Run ID</dt>
              <dd className="font-mono text-xs break-all max-w-[70%] text-right">{workflow.run_id}</dd>
            </div>
            <div className="flex justify-between">
              <dt className="text-anvil-500 dark:text-anvil-400">状态</dt>
              <dd>
                <span className={cn('forge-badge', statusBadgeColor(friendly))}>
                  {STATUS_LABELS[friendly] ?? friendly}
                </span>
              </dd>
            </div>
            {workflow.start_time && (
              <div className="flex justify-between">
                <dt className="text-anvil-500 dark:text-anvil-400">开始时间</dt>
                <dd>{formatDate(workflow.start_time)}</dd>
              </div>
            )}
            {workflow.close_time && (
              <div className="flex justify-between">
                <dt className="text-anvil-500 dark:text-anvil-400">结束时间</dt>
                <dd>{formatDate(workflow.close_time)}</dd>
              </div>
            )}
            {problemId && (
              <div className="flex justify-between">
                <dt className="text-anvil-500 dark:text-anvil-400">关联题目</dt>
                <dd>
                  <Link
                    href={`/problems/${problemId}`}
                    className="text-forge-600 hover:underline dark:text-forge-400"
                  >
                    查看
                  </Link>
                </dd>
              </div>
            )}
          </dl>
        </details>
      </aside>
    </div>
    <RejectDialog open={rejectDialogOpen} onClose={() => setRejectDialogOpen(false)} onConfirm={handleReject} loading={actionLoading} />
  </div>;
}
