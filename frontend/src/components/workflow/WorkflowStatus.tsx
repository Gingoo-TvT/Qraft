'use client';

import React, { useMemo } from 'react';
import {
  CheckCircle2,
  XCircle,
  Loader2,
  Clock,
  Pause,
  Ban,
  ThumbsUp,
  ThumbsDown,
  RotateCcw,
  StopCircle,
} from 'lucide-react';
import type { WorkflowState, WorkflowStatus as WFStatus } from '@/lib/types';
import { cn } from '@/lib/utils';
import { STATUS_LABELS } from '@/lib/constants';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface WorkflowStatusProps {
  workflowState: WorkflowState;
  onApprove?: () => void;
  onReject?: () => void;
  onRetry?: () => void;
  onCancel?: () => void;
}

// ---------------------------------------------------------------------------
// Status icon / color helpers
// ---------------------------------------------------------------------------

function statusIcon(status: WFStatus): React.ReactNode {
  switch (status) {
    case 'approved':
      return <CheckCircle2 className="h-5 w-5 text-success-500" />;
    case 'running':
      return <Loader2 className="h-5 w-5 text-forge-500 animate-spin" />;
    case 'waiting_review':
      return <Pause className="h-5 w-5 text-ember-500" />;
    case 'rejected_quarantined':
      return <Ban className="h-5 w-5 text-ember-500" />;
    case 'failed':
      return <XCircle className="h-5 w-5 text-danger-500" />;
    case 'rejected':
      return <Ban className="h-5 w-5 text-danger-500" />;
    case 'cancelled':
      return <Ban className="h-5 w-5 text-anvil-400" />;
    case 'pending':
    default:
      return <Clock className="h-5 w-5 text-anvil-400" />;
  }
}

function statusBgClass(status: WFStatus): string {
  switch (status) {
    case 'approved':
      return 'bg-success-50 dark:bg-success-500/10 border-success-400';
    case 'running':
      return 'bg-forge-50 dark:bg-forge-500/10 border-forge-400';
    case 'waiting_review':
      return 'bg-ember-50 dark:bg-ember-500/10 border-ember-400';
    case 'rejected_quarantined':
      return 'bg-ember-50 dark:bg-ember-500/10 border-ember-400';
    case 'failed':
    case 'rejected':
      return 'bg-danger-50 dark:bg-danger-500/10 border-danger-400';
    case 'cancelled':
      return 'bg-anvil-50 dark:bg-anvil-800 border-anvil-300';
    case 'pending':
    default:
      return 'bg-anvil-50 dark:bg-anvil-800 border-anvil-200';
  }
}

// ---------------------------------------------------------------------------
// Duration helper
// ---------------------------------------------------------------------------

function elapsedTime(createdAt: string, updatedAt: string, isActive: boolean): string {
  const start = new Date(createdAt).getTime();
  const end = isActive ? Date.now() : new Date(updatedAt).getTime();
  const diffMs = Math.max(0, end - start);

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSec = seconds % 60;
  if (minutes < 60) return `${minutes}m ${remainingSec}s`;
  const hours = Math.floor(minutes / 60);
  const remainingMin = minutes % 60;
  return `${hours}h ${remainingMin}m`;
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function WorkflowStatus({
  workflowState,
  onApprove,
  onReject,
  onRetry,
  onCancel,
}: WorkflowStatusProps) {
  const { status, steps: rawSteps, start_time, close_time } = workflowState;
  const steps = useMemo(
    () => rawSteps ?? [],
    [rawSteps],
  );

  const completedCount = useMemo(
    () => steps.filter((s) => s.status === 'completed').length,
    [steps],
  );

  const totalSteps = steps.length;
  const progressPercent = totalSteps > 0 ? Math.round((completedCount / totalSteps) * 100) : 0;

  const isActive = status === 'running' || status === 'pending';
  const elapsed = start_time ? elapsedTime(start_time, close_time ?? new Date().toISOString(), isActive) : '--';

  const runningStep = steps.find((s) => s.status === 'running');
  const currentStepName = runningStep ? (runningStep.description || runningStep.name) : '--';

  const statusLabel = STATUS_LABELS[status] ?? status;

  return (
    <div
      className={cn(
        'rounded-lg border-2 p-4 space-y-4 transition-colors duration-200',
        statusBgClass(status),
      )}
    >
      {/* Header: status icon + label */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {statusIcon(status)}
          <span className="text-sm font-semibold text-anvil-900 dark:text-anvil-100">
            {statusLabel}
          </span>
        </div>
        <span className="text-xs text-anvil-500 dark:text-anvil-400 flex items-center gap-1">
          <Clock className="h-3.5 w-3.5" />
          {elapsed}
        </span>
      </div>

      {/* Progress bar */}
      <div className="space-y-1">
        <div className="flex items-center justify-between text-xs text-anvil-600 dark:text-anvil-400">
          <span>
            Progress: {completedCount} / {totalSteps} steps
          </span>
          <span>{progressPercent}%</span>
        </div>
        <div className="h-2 w-full rounded-full bg-anvil-200 dark:bg-anvil-700 overflow-hidden">
          <div
            className={cn(
              'h-full rounded-full transition-all duration-500',
              status === 'failed' || status === 'rejected'
                ? 'bg-danger-500'
                : status === 'rejected_quarantined'
                  ? 'bg-ember-500'
                : status === 'approved'
                  ? 'bg-success-500'
                  : 'bg-forge-500',
            )}
            style={{ width: `${progressPercent}%` }}
          />
        </div>
      </div>

      {/* Current step */}
      <div className="text-xs text-anvil-600 dark:text-anvil-400">
        <span className="font-medium">Current step:</span>{' '}
        <span className="text-anvil-800 dark:text-anvil-200">{currentStepName}</span>
      </div>

      {/* Error summary placeholder */}

      {/* Action buttons */}
      <div className="flex items-center gap-2 pt-1">
        {status === 'waiting_review' && (
          <>
            {onApprove && (
              <button
                onClick={onApprove}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                  'bg-success-500 text-white hover:bg-success-600',
                )}
              >
                <ThumbsUp className="h-3.5 w-3.5" />
                Approve
              </button>
            )}
            {onReject && (
              <button
                onClick={onReject}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
                  'bg-danger-500 text-white hover:bg-danger-600',
                )}
              >
                <ThumbsDown className="h-3.5 w-3.5" />
                Reject
              </button>
            )}
          </>
        )}

        {status === 'failed' && onRetry && (
          <button
            onClick={onRetry}
            className={cn(
              'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
              'bg-ember-500 text-white hover:bg-ember-600',
            )}
          >
            <RotateCcw className="h-3.5 w-3.5" />
            Retry
          </button>
        )}

        {(status === 'running' || status === 'pending') && onCancel && (
          <button
            onClick={onCancel}
            className={cn(
              'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
              'bg-anvil-500 text-white hover:bg-anvil-600',
            )}
          >
            <StopCircle className="h-3.5 w-3.5" />
            Cancel
          </button>
        )}
      </div>
    </div>
  );
}
