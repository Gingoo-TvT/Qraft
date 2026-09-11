'use client';

import React, { useState } from 'react';
import { Handle, Position, type NodeProps } from 'reactflow';
import {
  Search,
  FileText,
  Code,
  Cpu,
  Database,
  Play,
  CheckCircle2,
  Brain,
  User,
  Upload,
  Loader2,
  AlertCircle,
  MinusCircle,
  Clock,
  ShieldCheck,
} from 'lucide-react';
import type { WorkflowStep } from '@/lib/types';
import { cn, truncate } from '@/lib/utils';
import { STATUS_LABELS } from '@/lib/constants';

// ---------------------------------------------------------------------------
// Step icon mapping
// ---------------------------------------------------------------------------

const STEP_ICONS: Record<string, React.ElementType> = {
  similarity_check: Search,
  generate_statement: FileText,
  post_statement_similarity: ShieldCheck,
  generate_solution: Code,
  compile_check: Cpu,
  generate_testdata: Database,
  run_sandbox: Play,
  sandbox_run: Play,
  validate: CheckCircle2,
  assess_feasibility: Brain,
  validate_solution: CheckCircle2,
  validate_testdata: CheckCircle2,
  llm_review: Brain,
  generate_editorial: Brain,
  human_review: User,
  final_review: User,
  store: Upload,
  store_publish: Upload,
};

function getStepIcon(stepName: string): React.ElementType {
  return STEP_ICONS[stepName] ?? FileText;
}

// ---------------------------------------------------------------------------
// Status colors and badges
// ---------------------------------------------------------------------------

function statusNodeClasses(status: WorkflowStep['status']): string {
  switch (status) {
    case 'completed':
      return 'border-success-500 bg-success-50 dark:bg-success-500/10';
    case 'running':
      return 'border-forge-500 bg-forge-50 dark:bg-forge-500/10';
    case 'failed':
      return 'border-danger-500 bg-danger-50 dark:bg-danger-500/10';
    case 'skipped':
      return 'border-anvil-300 bg-anvil-50 dark:bg-anvil-800 border-dashed';
    case 'pending':
    default:
      return 'border-anvil-200 bg-white dark:bg-anvil-900 dark:border-anvil-700';
  }
}

function statusBadgeClasses(status: WorkflowStep['status']): string {
  switch (status) {
    case 'completed':
      return 'bg-success-500 text-white';
    case 'running':
      return 'bg-forge-500 text-white';
    case 'failed':
      return 'bg-danger-500 text-white';
    case 'skipped':
      return 'bg-anvil-300 text-anvil-700 dark:bg-anvil-600 dark:text-anvil-200';
    case 'pending':
    default:
      return 'bg-anvil-200 text-anvil-600 dark:bg-anvil-700 dark:text-anvil-300';
  }
}

function statusIcon(status: WorkflowStep['status']): React.ReactNode {
  switch (status) {
    case 'completed':
      return <CheckCircle2 className="h-3 w-3" />;
    case 'running':
      return <Loader2 className="h-3 w-3 animate-spin" />;
    case 'failed':
      return <AlertCircle className="h-3 w-3" />;
    case 'skipped':
      return <MinusCircle className="h-3 w-3" />;
    case 'pending':
    default:
      return <Clock className="h-3 w-3" />;
  }
}

// ---------------------------------------------------------------------------
// Duration helper
// ---------------------------------------------------------------------------

function computeDuration(
  startedAt?: string,
  completedAt?: string,
): string | null {
  if (!startedAt) return null;
  const start = new Date(startedAt).getTime();
  const end = completedAt ? new Date(completedAt).getTime() : Date.now();
  const diffMs = end - start;

  if (diffMs < 1000) return `${diffMs}ms`;
  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainingSec = seconds % 60;
  return `${minutes}m ${remainingSec}s`;
}

// ---------------------------------------------------------------------------
// StepNode data interface
// ---------------------------------------------------------------------------

export interface StepNodeData {
  step: WorkflowStep;
  stepIndex: number;
}

// ---------------------------------------------------------------------------
// StepNode Component
// ---------------------------------------------------------------------------

export function StepNode({ data }: NodeProps<StepNodeData>) {
  const { step, stepIndex } = data;
  const [expanded, setExpanded] = useState(false);

  const Icon = getStepIcon(step.name);
  const duration = computeDuration(step.started_at, step.completed_at);
  const errorMessage = step.result && !step.result.success ? step.result.message : null;
  const statusLabel = STATUS_LABELS[step.status] ?? step.status;

  return (
    <>
      <Handle type="target" position={Position.Top} className="!bg-anvil-400" />

      <div
        className={cn(
          'relative rounded-lg border-2 px-4 py-3 shadow-sm transition-all duration-200 cursor-pointer',
          'min-w-[220px] max-w-[280px]',
          statusNodeClasses(step.status),
          step.status === 'running' && 'animate-pulse-slow shadow-forge-200 dark:shadow-forge-900',
        )}
        onClick={() => setExpanded(!expanded)}
        title={errorMessage ? `Error: ${errorMessage}` : undefined}
      >
        {/* Running animated ring */}
        {step.status === 'running' && (
          <div className="absolute -inset-[2px] rounded-lg border-2 border-forge-400 animate-pulse-slow pointer-events-none" />
        )}

        {/* Header */}
        <div className="flex items-center gap-2">
          <div
            className={cn(
              'flex h-8 w-8 shrink-0 items-center justify-center rounded-md',
              step.status === 'completed'
                ? 'bg-success-500/20 text-success-600'
                : step.status === 'running'
                  ? 'bg-forge-500/20 text-forge-600'
                  : step.status === 'failed'
                    ? 'bg-danger-500/20 text-danger-600'
                    : 'bg-anvil-200/60 text-anvil-500 dark:bg-anvil-700/60 dark:text-anvil-300',
            )}
          >
            <Icon className="h-4 w-4" />
          </div>

          <div className="flex-1 min-w-0">
            <p className="text-xs font-medium text-anvil-500 dark:text-anvil-400">
              Step {stepIndex + 1}
            </p>
            <p className="text-sm font-semibold text-anvil-900 dark:text-anvil-100 truncate">
              {step.description || step.name}
            </p>
          </div>
        </div>

        {/* Status badge + duration */}
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <span
            className={cn(
              'inline-flex items-center gap-1 whitespace-nowrap rounded-full px-2 py-0.5 text-xs font-semibold',
              statusBadgeClasses(step.status),
            )}
          >
            {statusIcon(step.status)}
            {statusLabel}
          </span>

          {duration && (
            <span className="text-xs text-anvil-400 dark:text-anvil-500 flex shrink-0 items-center gap-1 whitespace-nowrap">
              <Clock className="h-3 w-3" />
              {duration}
            </span>
          )}
        </div>

        {/* Error message (truncated) */}
        {errorMessage && (
          <div className="mt-2 rounded bg-danger-50 dark:bg-danger-500/10 p-1.5 text-xs break-words text-danger-600 dark:text-danger-400">
            {truncate(errorMessage, 80)}
          </div>
        )}

        {/* Expanded detail panel */}
        {expanded && (
          <div className="mt-3 border-t border-anvil-200 dark:border-anvil-700 pt-2 text-xs text-anvil-600 dark:text-anvil-400 space-y-1">
            <p>
              <span className="font-medium">Name:</span> {step.name}
            </p>
            {step.started_at && (
              <p>
                <span className="font-medium">Started:</span>{' '}
                {new Date(step.started_at).toLocaleTimeString()}
              </p>
            )}
            {step.completed_at && (
              <p>
                <span className="font-medium">Completed:</span>{' '}
                {new Date(step.completed_at).toLocaleTimeString()}
              </p>
            )}
            {step.result?.message && (
              <p>
                <span className="font-medium">Message:</span>{' '}
                {step.result.message}
              </p>
            )}
            {step.result?.artifacts && step.result.artifacts.length > 0 && (
              <p>
                <span className="font-medium">Artifacts:</span>{' '}
                {step.result.artifacts.join(', ')}
              </p>
            )}
          </div>
        )}
      </div>

      <Handle type="source" position={Position.Bottom} className="!bg-anvil-400" />
    </>
  );
}

// ---------------------------------------------------------------------------
// Node types export for React Flow
// ---------------------------------------------------------------------------

export const workflowNodeTypes = {
  stepNode: StepNode,
};
