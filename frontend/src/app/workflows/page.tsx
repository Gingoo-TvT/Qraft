'use client';

import { useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Activity, AlertCircle, ArrowUpRight, CheckCircle2, Clock, Loader2, Pause, Plus, XCircle } from 'lucide-react';

import RefreshButton from '@/components/RefreshButton';
import Pagination from '@/components/Pagination';
import { EmptyState, PageHeader } from '@/components/ui/Workspace';
import { useWorkflows } from '@/hooks/useWorkflow';
import { cn, formatDate, formatRelativeTime, statusBadgeColor } from '@/lib/utils';
import { DEFAULT_PAGE_SIZE, STATUS_LABELS } from '@/lib/constants';
import { normalizeStatus } from '@/lib/status';

const STATUS_TABS = [
  { value: '', label: '全部任务' },
  { value: 'running', label: '运行中' },
  { value: 'completed', label: '已完成' },
  { value: 'failed', label: '失败' },
  { value: 'cancelled', label: '已取消' },
];

function StatusIcon({ status }: { status: string }) {
  switch (normalizeStatus(status)) {
    case 'running': return <Loader2 className="h-4 w-4 animate-spin text-cyan-500" />;
    case 'pending': return <Clock className="h-4 w-4 text-[var(--dm)]" />;
    case 'waiting_review': return <Pause className="h-4 w-4 text-blue-500" />;
    case 'rejected_quarantined': return <Pause className="h-4 w-4 text-amber-500" />;
    case 'approved': return <CheckCircle2 className="h-4 w-4 text-success-500" />;
    case 'failed': return <AlertCircle className="h-4 w-4 text-danger-500" />;
    case 'cancelled': return <XCircle className="h-4 w-4 text-[var(--dm)]" />;
    default: return <Activity className="h-4 w-4 text-[var(--dm)]" />;
  }
}

export default function WorkflowsPage() {
  const [statusFilter, setStatusFilter] = useState('');
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(DEFAULT_PAGE_SIZE);
  const { workflows, total, loading, error, refresh, setParams } = useWorkflows({
    page, size, status: statusFilter || undefined,
  });

  useEffect(() => {
    setParams({ page, size, status: statusFilter || undefined });
  }, [page, size, statusFilter, setParams]);

  const hasRunning = workflows.some((workflow) => normalizeStatus(workflow.status) === 'running');
  const refreshIntervalRef = useRef<ReturnType<typeof setInterval>>();

  useEffect(() => {
    if (hasRunning) refreshIntervalRef.current = setInterval(() => refresh(), 5000);
    return () => {
      if (refreshIntervalRef.current) clearInterval(refreshIntervalRef.current);
    };
  }, [hasRunning, refresh]);

  return (
    <div className="af-page">
      <PageHeader eyebrow="工作空间" title="任务中心" description="查看生成进度、执行结果和审核记录。任务在后台持续运行。"
        actions={<Link href="/problems/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题目</Link>}
      />

      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
        {error}<button className="ml-3 underline" onClick={refresh}>重新加载</button>
      </div>}

      <section className="af-table-panel" aria-label="生成任务列表" aria-busy={loading}>
        <div className="af-toolbar justify-between">
          <div className="af-segmented" role="group" aria-label="按任务状态筛选">
            {STATUS_TABS.map((tab) => <button key={tab.value} aria-pressed={statusFilter === tab.value}
              className={statusFilter === tab.value ? 'is-active' : ''}
              onClick={() => { setStatusFilter(tab.value); setPage(1); }}>{tab.label}</button>)}
          </div>
          <RefreshButton onClick={refresh} loading={loading} />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--dl)] px-5 py-3 text-sm text-[var(--dm)]">
          <span>{loading && workflows.length === 0 ? '正在读取任务…' : error ? '暂时无法读取任务数量' : <>共 <strong className="font-medium text-[var(--dt)]">{total}</strong> 个{statusFilter ? STATUS_TABS.find((tab) => tab.value === statusFilter)?.label : ''}任务</>}</span>
          {hasRunning ? <span className="inline-flex items-center gap-2"><span className="h-1.5 w-1.5 animate-pulse rounded-full bg-[var(--da)]" />本页任务每 5 秒刷新</span> : <span>选择任务，查看执行与审核详情</span>}
        </div>

        <div className="af-table-scroll">
          <table className="forge-table">
            <thead><tr><th>任务 / 状态</th><th className="w-40">开始时间</th><th className="w-40">结束时间</th><th className="w-56">执行结果</th><th className="w-28">操作</th></tr></thead>
            <tbody>
              {loading && workflows.length === 0 ? Array.from({ length: 5 }).map((_, row) => <tr key={row}>{Array.from({ length: 5 }).map((_, column) => <td key={column}><div className="forge-skeleton h-5 w-full" /></td>)}</tr>)
                : workflows.length === 0 ? <tr><td colSpan={5}><EmptyState icon={Activity}
                  title={error ? '暂时无法查看任务' : statusFilter ? '没有这个状态的任务' : '你的创作任务会显示在这里'}
                  description={error ? '恢复服务连接后，可以继续查看执行记录。' : statusFilter ? '切换状态查看其他任务，或创建一道新题。' : '创建题目后，生成、验算和审核进度都会保存在任务记录里。'}
                  action={error ? <button className="forge-btn-secondary" onClick={refresh}>重新加载</button> : statusFilter ? <button className="forge-btn-secondary" onClick={() => { setStatusFilter(''); setPage(1); }}>查看全部任务</button> : <Link href="/problems/new" className="forge-btn-primary"><Plus className="h-4 w-4" />创建题目</Link>}
                /></td></tr>
                : workflows.map((workflow) => {
                  const friendly = normalizeStatus(workflow.status);
                  return <tr key={workflow.workflow_id}>
                    <td className="min-w-[240px]">
                      <div className="flex items-center gap-2">
                        <StatusIcon status={workflow.status} />
                        <span className={cn('forge-badge', statusBadgeColor(friendly))}>{STATUS_LABELS[friendly] ?? friendly}</span>
                      </div>
                      <Link href={`/workflows/${workflow.workflow_id}`} className="af-link mt-2 block max-w-[360px] break-all font-mono text-xs" title={workflow.workflow_id}>{workflow.workflow_id}</Link>
                    </td>
                    <td>{workflow.start_time ? <time dateTime={workflow.start_time} title={formatDate(workflow.start_time)}>
                      <span className="block">{formatRelativeTime(workflow.start_time)}</span>
                      <span className="mt-1 block text-xs text-[var(--dm)]">{formatDate(workflow.start_time)}</span>
                    </time> : <span className="text-[var(--dm)]">未提供</span>}</td>
                    <td>{workflow.close_time ? <time dateTime={workflow.close_time} title={formatDate(workflow.close_time)}>
                      <span className="block">{formatRelativeTime(workflow.close_time)}</span>
                      <span className="mt-1 block text-xs text-[var(--dm)]">{formatDate(workflow.close_time)}</span>
                    </time> : <span className="text-[var(--dm)]">{friendly === 'running' ? '运行中' : '—'}</span>}</td>
                    <td>{workflow.failure_reason ? <p className="line-clamp-2 max-w-[240px] text-sm text-danger-500" title={workflow.failure_reason}>{workflow.failure_reason}</p>
                      : workflow.state?.problem_id ? <Link className="af-link inline-flex items-center gap-1" href={`/problems/${workflow.state.problem_id}`}>打开生成题目<ArrowUpRight className="h-3.5 w-3.5" /></Link>
                        : <span className="text-sm text-[var(--dm)]">在详情中查看执行记录</span>}</td>
                    <td><Link href={`/workflows/${workflow.workflow_id}`} className="af-link whitespace-nowrap">查看详情</Link></td>
                  </tr>;
                })}
            </tbody>
          </table>
        </div>
        <div className="af-pagination"><Pagination page={page} total={total} size={size} onPageChange={setPage} onSizeChange={(nextSize) => { setSize(nextSize); setPage(1); }} /></div>
      </section>
    </div>
  );
}
