'use client';

import { useEditorTheme } from '@/hooks/useEditorTheme';
import { ViewTabs, ViewPanel } from '@/components/ui/ViewTabs';

import { useCallback, useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import dynamic from 'next/dynamic';
import Link from 'next/link';
import {
  AlertTriangle,
  ArrowLeft,
  Check,
  ChevronDown,
  ChevronUp,
  Clock,
  Code2,
  Copy,
  Download,
  Edit,
  ExternalLink,
  FileJson,
  FileArchive,
  Loader2,
  MemoryStick,
  RefreshCw,
  ShieldCheck,
  Trash2,
} from 'lucide-react';

import { useProblem, useProblemSolutions, useTestcases } from '@/hooks/useProblems';
import {
  approveProblemPublicRelease,
  completeProblemEditRefresh,
  deleteProblem,
  downloadAllTestData,
  downloadHydroPackage,
  getEditorial,
  getMetadata,
  getWorkflow,
  validateProblem,
} from '@/lib/api';
import type {
  Problem,
  ProblemSolution,
  ProblemEditRefreshReport,
  PublicReleaseApprovalReport,
  TestCase,
} from '@/lib/types';
import {
  cn,
  difficultyColor,
  formatDate,
  levelBadgeColor,
  statusBadgeColor,
} from '@/lib/utils';
import { getDifficultyLabel, STATUS_LABELS } from '@/lib/constants';
import MarkdownRenderer from '@/components/MarkdownRenderer';

// ---------------------------------------------------------------------------
// Dynamic imports for heavy components
// ---------------------------------------------------------------------------

const MonacoEditor = dynamic(() => import('@monaco-editor/react'), {
  ssr: false,
  loading: () => (
    <div className="flex h-64 items-center justify-center rounded-lg border border-anvil-200 bg-anvil-50 dark:border-anvil-700 dark:bg-anvil-900">
      <Loader2 className="h-6 w-6 animate-spin text-anvil-400" />
    </div>
  ),
});

function monacoLanguageForSolution(solution: ProblemSolution): string {
  switch (solution.language.trim().toLowerCase()) {
    case 'cpp':
    case 'c++':
      return 'cpp';
    case 'c':
      return 'c';
    case 'python':
    case 'python3':
      return 'python';
    case 'java':
      return 'java';
    case 'javascript':
    case 'js':
      return 'javascript';
    case 'typescript':
    case 'ts':
      return 'typescript';
    case 'go':
      return 'go';
    case 'rust':
      return 'rust';
    default:
      return solution.language.trim() || 'plaintext';
  }
}

// ---------------------------------------------------------------------------
// Collapsible Section
// ---------------------------------------------------------------------------

function CollapsibleSection({
  title,
  icon: Icon,
  children,
  defaultOpen = false,
}: {
  title: string;
  icon: React.ElementType;
  children: React.ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);

  return (
    <div className="forge-card">
      <button
        className="flex w-full items-center justify-between"
        onClick={() => setOpen((prev) => !prev)}
      >
        <h2 className="forge-section-title flex items-center gap-2">
          <Icon className="h-5 w-5 text-anvil-400" />
          {title}
        </h2>
        {open ? (
          <ChevronUp className="h-5 w-5 text-anvil-400" />
        ) : (
          <ChevronDown className="h-5 w-5 text-anvil-400" />
        )}
      </button>
      {open && <div className="mt-4">{children}</div>}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Test Cases Table
// ---------------------------------------------------------------------------

function TestCasesTable({
  testcases,
  problemId,
  canExportHydro,
  exportBlockedReason,
}: {
  testcases: TestCase[];
  problemId: string;
  canExportHydro: boolean;
  exportBlockedReason: string;
}) {
  const sampleCases = testcases.filter((tc) => tc.is_sample);
  const hiddenCases = testcases.filter((tc) => !tc.is_sample);

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes} B`;
    return `${(bytes / 1024).toFixed(1)} KB`;
  };

  return (
    <div className="space-y-4">
      {/* Download buttons */}
      <div className="flex flex-wrap justify-end gap-2">
        {canExportHydro ? (
          <a
            href={downloadHydroPackage(problemId)}
            className="forge-btn-primary inline-flex items-center gap-2"
          >
            <Download className="h-4 w-4" />
            Hydro 上传包
          </a>
        ) : (
          <button
            type="button"
            className="forge-btn-secondary inline-flex cursor-not-allowed items-center gap-2 opacity-60"
            disabled
            title={exportBlockedReason}
          >
            <Download className="h-4 w-4" />
            Hydro 上传包
          </button>
        )}

        <a
          href={downloadAllTestData(problemId)}
          className="forge-btn-secondary inline-flex items-center gap-2"
        >
          <Download className="h-4 w-4" />
          裸测试数据
        </a>
      </div>

      {testcases.length === 0 && (
        <p className="py-4 text-center text-sm text-anvil-400">暂无测试数据</p>
      )}

      {sampleCases.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-semibold text-anvil-600 dark:text-anvil-300">
            样例测试 ({sampleCases.length})
          </h3>
          <div className="overflow-x-auto">
            <table className="forge-table">
              <thead>
                <tr>
                  <th>组号</th>
                  <th>序号</th>
                  <th>输入预览</th>
                  <th>输出预览</th>
                  <th>输入大小</th>
                  <th>输出大小</th>
                </tr>
              </thead>
              <tbody>
                {sampleCases.map((tc) => (
                  <tr key={tc.id}>
                    <td>{tc.group_index}</td>
                    <td>{tc.case_index}</td>
                    <td className="max-w-[200px] truncate font-mono text-xs">
                      {tc.input_preview ?? '-'}
                    </td>
                    <td className="max-w-[200px] truncate font-mono text-xs">
                      {tc.output_preview ?? '-'}
                    </td>
                    <td className="text-sm text-anvil-500">
                      {formatSize(tc.input_size_bytes ?? 0)}
                    </td>
                    <td className="text-sm text-anvil-500">
                      {formatSize(tc.output_size_bytes ?? 0)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {hiddenCases.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-semibold text-anvil-600 dark:text-anvil-300">
            隐藏测试 ({hiddenCases.length})
          </h3>
          <div className="overflow-x-auto">
            <table className="forge-table">
              <thead>
                <tr>
                  <th>组号</th>
                  <th>序号</th>
                  <th>输入大小</th>
                  <th>输出大小</th>
                  <th>描述</th>
                </tr>
              </thead>
              <tbody>
                {hiddenCases.map((tc) => (
                  <tr key={tc.id}>
                    <td>{tc.group_index}</td>
                    <td>{tc.case_index}</td>
                    <td className="text-sm text-anvil-500">
                      {formatSize(tc.input_size_bytes ?? 0)}
                    </td>
                    <td className="text-sm text-anvil-500">
                      {formatSize(tc.output_size_bytes ?? 0)}
                    </td>
                    <td className="text-sm text-anvil-400">
                      {tc.description ?? '-'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}

type GateTone = 'success' | 'warning' | 'danger';

interface PublicationGateView {
  status: string;
  label: string;
  tone: GateTone;
  canExportHydro: boolean;
  reason: string;
  stale: boolean;
  policyVersion: string;
  validationHash: string;
  refreshedAt: string;
}

const gateCardTone: Record<GateTone, string> = {
  success:
    'border-success-400/30 bg-success-50 dark:border-success-500/30 dark:bg-success-500/10',
  warning:
    'border-warning-400/30 bg-warning-50 dark:border-warning-500/30 dark:bg-warning-500/10',
  danger:
    'border-danger-400/30 bg-danger-50 dark:border-danger-500/30 dark:bg-danger-500/10',
};

const gateTextTone: Record<GateTone, string> = {
  success: 'text-success-700 dark:text-success-300',
  warning: 'text-warning-700 dark:text-warning-300',
  danger: 'text-danger-700 dark:text-danger-300',
};

function metadataString(metadata: Record<string, unknown> | undefined, key: string): string {
  const value = metadata?.[key];
  return typeof value === 'string' ? value.trim() : '';
}

function metadataBool(metadata: Record<string, unknown> | undefined, key: string): boolean {
  return metadata?.[key] === true;
}

function statusLabel(status: string): string {
  return STATUS_LABELS[status as keyof typeof STATUS_LABELS] ?? status;
}

function buildPublicationGateView(problem: Problem): PublicationGateView {
  const metadata = problem.metadata_json;
  const gateStatus = metadataString(metadata, 'publication_gate_status');
  const stale = metadataBool(metadata, 'stale');
  const deniedByGate = gateStatus !== '' && gateStatus !== 'published';
  const canExportHydro = problem.status === 'published' && !stale && !deniedByGate;
  const status = gateStatus || problem.status;
  const reason =
    metadataString(metadata, 'publication_quarantine_reason') ||
    metadataString(metadata, 'stale_reason') ||
    (problem.status !== 'published'
      ? `当前题目状态为 ${statusLabel(problem.status)}`
      : deniedByGate
        ? `发布 gate 状态为 ${status}`
        : '');

  let label = '可导出';
  let tone: GateTone = 'success';
  if (stale) {
    label = '编辑后待刷新';
    tone = 'warning';
  } else if (!canExportHydro) {
    label = deniedByGate || problem.status === 'quarantined' ? '发布 gate 阻断' : '未达到发布态';
    tone = deniedByGate || problem.status === 'quarantined' || problem.status === 'rejected'
      ? 'danger'
      : 'warning';
  }

  return {
    status,
    label,
    tone,
    canExportHydro,
    reason,
    stale,
    policyVersion:
      metadataString(metadata, 'publication_policy_version') ||
      metadataString(metadata, 'publication_gate_version'),
    validationHash: metadataString(metadata, 'last_edit_refresh_validation_sha256'),
    refreshedAt: metadataString(metadata, 'last_edit_refresh_at'),
  };
}

interface WorkflowReviewSummary {
  approved: boolean;
  confidence: number | null;
  estimatedDifficulty: number | null;
  isDuplicate: boolean;
  duplicateOf: string;
  duplicateReason: string;
  issues: string[];
  suggestions: string[];
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter(
    (item): item is string => typeof item === 'string' && item.trim() !== '',
  );
}

function extractWorkflowReview(value: unknown): WorkflowReviewSummary | null {
  const workflow = asRecord(value);
  const state = asRecord(workflow?.state);
  const steps = Array.isArray(state?.steps) ? state.steps : [];

  for (let index = steps.length - 1; index >= 0; index -= 1) {
    const step = asRecord(steps[index]);
    if (step?.step !== 'llm_review') continue;
    const output = asRecord(step.output);
    if (!output || typeof output.approved !== 'boolean') continue;

    return {
      approved: output.approved,
      confidence:
        typeof output.confidence === 'number' ? output.confidence : null,
      estimatedDifficulty:
        typeof output.estimated_difficulty === 'number'
          ? output.estimated_difficulty
          : null,
      isDuplicate: output.is_duplicate === true,
      duplicateOf:
        typeof output.duplicate_of === 'string' ? output.duplicate_of : '',
      duplicateReason:
        typeof output.duplicate_reason === 'string'
          ? output.duplicate_reason
          : '',
      issues: stringList(output.issues),
      suggestions: stringList(output.suggestions),
    };
  }
  return null;
}

// ---------------------------------------------------------------------------
// Problem Detail Page
// ---------------------------------------------------------------------------

export default function ProblemDetailPage() {
  const editorTheme = useEditorTheme();
  const [detailTab, setDetailTab] = useState('statement');
  const params = useParams();
  const router = useRouter();
  const id = params.id as string;

  const { problem, loading, error, refresh } = useProblem(id);
  const { testcases, loading: testcasesLoading } = useTestcases(id);
  const { solutions, loading: solutionsLoading, error: solutionsError } = useProblemSolutions(id);
  const sourceSolutions = ['main', 'brute'].flatMap((kind) => {
    const solution = solutions.find((item) => item.solution_type === kind);
    return solution ? [solution] : [];
  });

  const [editorial, setEditorial] = useState<string | null>(null);
  const [editorialLoading, setEditorialLoading] = useState(false);
  const [metadata, setMetadata] = useState<Record<string, unknown> | null>(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] = useState<{
    valid: boolean;
    issues: string[];
    workflow_id?: string;
  } | null>(null);
  const [refreshHash, setRefreshHash] = useState('');
  const [refreshingGate, setRefreshingGate] = useState(false);
  const [gateRefreshResult, setGateRefreshResult] =
    useState<ProblemEditRefreshReport | null>(null);
  const [gateRefreshError, setGateRefreshError] = useState<string | null>(null);
  const [approvingRelease, setApprovingRelease] = useState(false);
  const [releaseApprovalResult, setReleaseApprovalResult] =
    useState<PublicReleaseApprovalReport | null>(null);
  const [releaseApprovalError, setReleaseApprovalError] = useState<string | null>(null);
  const [workflowReview, setWorkflowReview] =
    useState<WorkflowReviewSummary | null>(null);
  const [workflowReviewLoading, setWorkflowReviewLoading] = useState(false);
  const [workflowReviewError, setWorkflowReviewError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Fetch editorial
  useEffect(() => {
    if (!id) return;
    setEditorialLoading(true);
    getEditorial(id)
      .then((res) => setEditorial(res.data?.editorial ?? null))
      .catch(() => setEditorial(null))
      .finally(() => setEditorialLoading(false));
  }, [id]);

  // Fetch metadata
  useEffect(() => {
    if (!id) return;
    setMetadataLoading(true);
    getMetadata(id)
      .then((res) => setMetadata(res.data ?? null))
      .catch(() => setMetadata(null))
      .finally(() => setMetadataLoading(false));
  }, [id]);

  // Fetch the GPT review recorded in the originating generation workflow.
  useEffect(() => {
    const workflowId = problem?.workflow_id;
    if (!workflowId) {
      setWorkflowReview(null);
      setWorkflowReviewError(null);
      return;
    }

    let cancelled = false;
    setWorkflowReviewLoading(true);
    setWorkflowReviewError(null);
    getWorkflow(workflowId)
      .then((res) => {
        if (cancelled) return;
        setWorkflowReview(extractWorkflowReview(res.data));
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setWorkflowReview(null);
        setWorkflowReviewError(
          err instanceof Error ? err.message : '无法读取验题结果',
        );
      })
      .finally(() => {
        if (!cancelled) setWorkflowReviewLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [problem?.workflow_id]);

  // Validate problem
  const handleValidate = useCallback(async () => {
    if (!id) return;
    setValidating(true);
    setValidationResult(null);
    try {
      const res = await validateProblem(id);
      const data = res.data;
      if (data?.workflow_id) {
        // Backend returned an async workflow response
        setValidationResult({
          valid: false,
          issues: [],
          workflow_id: data.workflow_id,
        });
      } else {
        setValidationResult({
          valid: data?.valid ?? false,
          issues: data?.issues ?? [],
        });
      }
    } catch (err) {
      setValidationResult({
        valid: false,
        issues: [err instanceof Error ? err.message : '验证失败'],
      });
    } finally {
      setValidating(false);
    }
  }, [id]);

  const handleRefreshGate = useCallback(async () => {
    const validationHash = refreshHash.trim();
    if (!id) return;
    if (!validationHash) {
      setGateRefreshError('请输入 validation_report_sha256');
      return;
    }

    setRefreshingGate(true);
    setGateRefreshError(null);
    setGateRefreshResult(null);
    try {
      const res = await completeProblemEditRefresh(id, {
        validation_report_sha256: validationHash,
      });
      setGateRefreshResult(res.data ?? null);
      await refresh();
    } catch (err) {
      setGateRefreshError(err instanceof Error ? err.message : '刷新 gate 失败');
    } finally {
      setRefreshingGate(false);
    }
  }, [id, refresh, refreshHash]);

  const handleApprovePublicRelease = useCallback(async () => {
    if (!id) return;
    if (!window.confirm(
      '确认你已核对题目来源与发布权利，并审核通过这道题吗？系统会记录本次管理员决定，再执行全部发布门禁。',
    )) return;

    setApprovingRelease(true);
    setReleaseApprovalError(null);
    setReleaseApprovalResult(null);
    try {
      const res = await approveProblemPublicRelease(id);
      setReleaseApprovalResult(res.data ?? null);
      await refresh();
    } catch (err) {
      setReleaseApprovalError(
        err instanceof Error ? err.message : '审核发布失败',
      );
    } finally {
      setApprovingRelease(false);
    }
  }, [id, refresh]);

  // Delete problem
  const handleDelete = useCallback(async () => {
    if (!id) return;
    if (!window.confirm('确定要删除此题目吗？此操作不可撤销。')) return;
    setDeleting(true);
    try {
      await deleteProblem(id);
      router.push('/problems');
    } catch {
      alert('删除失败');
    } finally {
      setDeleting(false);
    }
  }, [id, router]);

  // Loading state
  if (loading) {
    return (
      <div className="forge-page">
        <div className="space-y-4">
          <div className="forge-skeleton h-8 w-64" />
          <div className="forge-skeleton h-4 w-96" />
          <div className="forge-card">
            <div className="space-y-3">
              <div className="forge-skeleton h-4 w-full" />
              <div className="forge-skeleton h-4 w-3/4" />
              <div className="forge-skeleton h-4 w-5/6" />
            </div>
          </div>
        </div>
      </div>
    );
  }

  // Error state
  if (error || !problem) {
    return (
      <div className="forge-page">
        <div className="flex flex-col items-center justify-center gap-4 py-20">
          <AlertTriangle className="h-12 w-12 text-danger-400" />
          <p className="text-danger-500">{error ?? '题目未找到'}</p>
          <Link href="/problems" className="forge-btn-secondary">
            <ArrowLeft className="h-4 w-4" />
            返回列表
          </Link>
        </div>
      </div>
    );
  }

  const gate = buildPublicationGateView(problem);
  const rawMetadata = problem.metadata_json ?? metadata;
  const exportBlockedReason =
    gate.reason || 'Hydro 导出需要题目通过发布 gate 并处于已发布状态';

  return (
    <div className="af-page af-detail-page">
      {/* Back link */}
      <Link
        href="/problems"
        className="inline-flex items-center gap-1 text-sm text-anvil-500 hover:text-anvil-700 dark:text-anvil-400 dark:hover:text-anvil-200"
      >
        <ArrowLeft className="h-4 w-4" />
        返回题目列表
      </Link>

      {/* Header */}
      <div className="af-detail-header">
        <div className="min-w-0 space-y-3">
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <h1 className="min-w-0 break-words text-2xl font-bold">{problem.title}</h1>
            <span
              className={cn('forge-badge', statusBadgeColor(problem.status))}
            >
              {STATUS_LABELS[problem.status] ?? problem.status}
            </span>
          </div>
          <div className="flex flex-wrap items-center gap-3 text-sm text-anvil-500 dark:text-anvil-400">
            <span className="font-mono">{problem.serial_number}</span>
            <span className={cn('forge-badge', levelBadgeColor(problem.level))}>
              {problem.level === 'syntax' ? '语法题' : '算法题'}
            </span>
            <span className={cn('font-semibold', difficultyColor(problem.difficulty))}>
              {problem.difficulty} - {getDifficultyLabel(problem.difficulty)}
            </span>
            <span className="flex items-center gap-1">
              <Clock className="h-3.5 w-3.5" />
              {problem.time_limit}ms
            </span>
            <span className="flex items-center gap-1">
              <MemoryStick className="h-3.5 w-3.5" />
              {problem.memory_limit}MB
            </span>
          </div>
          {/* Tags */}
          <div className="flex flex-wrap gap-2">
            {problem.tags.map((tag) => (
              <span
                key={tag}
                className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300"
              >
                {tag}
              </span>
            ))}
          </div>
        </div>

        {/* Actions */}
        <div className="flex flex-wrap items-center justify-end gap-2">
          {gate.canExportHydro ? (
            <a
              href={downloadHydroPackage(problem.id)}
              className="forge-btn-primary"
            >
              <FileArchive className="h-4 w-4" />
              Hydro 包
            </a>
          ) : (
            <button
              type="button"
              className="forge-btn-secondary cursor-not-allowed opacity-60"
              disabled
              title={exportBlockedReason}
            >
              <FileArchive className="h-4 w-4" />
              Hydro 包
            </button>
          )}
          <a
            href={downloadAllTestData(problem.id)}
            className="forge-btn-secondary"
          >
            <Download className="h-4 w-4" />
            测试数据
          </a>
          <Link
            href={`/problems/${problem.id}/edit`}
            className="forge-btn-secondary"
          >
            <Edit className="h-4 w-4" />
            编辑
          </Link>
          <button
            className="forge-btn-secondary"
            onClick={handleValidate}
            disabled={validating}
          >
            {validating ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Check className="h-4 w-4" />
            )}
            验证
          </button>
          <button
            className="forge-btn-danger"
            onClick={handleDelete}
            disabled={deleting}
          >
            {deleting ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Trash2 className="h-4 w-4" />
            )}
            删除
          </button>
          {problem.workflow_id && (
            <Link
              href={`/workflows/${problem.workflow_id}`}
              className="forge-btn-ghost"
            >
              <ExternalLink className="h-4 w-4" />
              工作流
            </Link>
          )}
        </div>
      </div>


      <div className="af-quality-strip">
        <ShieldCheck size={18} /><span>{gate.label}</span>
        {gate.reason && <p>{gate.reason}</p>}
        <button type="button" className="af-link" onClick={() => setDetailTab('quality')}>查看质量与审核</button>
      </div>
      <ViewTabs id="problem-content" value={detailTab} onChange={setDetailTab} items={[
        { id: 'statement', label: '题目' }, { id: 'solutions', label: '解法与提示' },
        { id: 'data', label: '测试数据' }, { id: 'quality', label: '质量与审核' }, { id: 'metadata', label: '元信息' },
      ]} />
      {/* Validation result */}
      {validationResult && (
        validationResult.workflow_id ? (
          <div className="rounded-lg border border-forge-400/30 bg-forge-50 p-4 dark:bg-forge-500/10">
            <p className="text-sm font-medium text-forge-600">
              验证工作流已启动
            </p>
            <Link
              href={`/workflows/${validationResult.workflow_id}`}
              className="mt-1 inline-flex items-center gap-1 text-sm text-forge-600 hover:underline dark:text-forge-400"
            >
              <ExternalLink className="h-3.5 w-3.5" />
              查看工作流详情
            </Link>
          </div>
        ) : (
          <div
            className={cn(
              'rounded-lg border p-4',
              validationResult.valid
                ? 'border-success-400/30 bg-success-50 dark:bg-success-500/10'
                : 'border-danger-400/30 bg-danger-50 dark:bg-danger-500/10',
            )}
          >
            <p
              className={cn(
                'text-sm font-medium',
                validationResult.valid ? 'text-success-600' : 'text-danger-600',
              )}
            >
              {validationResult.valid ? '验证通过' : '验证未通过'}
            </p>
            {(validationResult.issues ?? []).length > 0 && (
              <ul className="mt-2 list-disc pl-5 text-sm text-danger-500">
                {(validationResult.issues ?? []).map((issue, i) => (
                  <li key={i}>{issue}</li>
                ))}
              </ul>
            )}
          </div>
        )
      )}

<ViewPanel id="problem-content" name="statement" active={detailTab}>      {/* Statement */}
      <div className="af-reading-surface">
        <MarkdownRenderer content={problem.statement} />
      </div>

</ViewPanel>
<ViewPanel id="problem-content" name="solutions" active={detailTab}>      {/* One-line hint */}
      {problem.one_line_hint && (
        <div className="forge-card">
          <h2 className="forge-section-title mb-2">提示</h2>
          <p className="text-sm text-anvil-600 dark:text-anvil-300">
            {problem.one_line_hint}
          </p>
        </div>
      )}

      {/* Executable sources are separate from detailed_solution editorial Markdown. */}
      {solutionsLoading ? (
        <div className="forge-card flex items-center justify-center gap-2 py-8 text-sm text-anvil-500">
          <Loader2 className="h-5 w-5 animate-spin" />
          正在读取解法代码…
        </div>
      ) : solutionsError ? (
        <p role="alert" className="forge-card text-sm text-danger-500">{solutionsError}</p>
      ) : sourceSolutions.length === 0 ? (
        <p className="forge-card text-sm text-anvil-500">暂无标准解或暴力解代码</p>
      ) : sourceSolutions.map((solution) => (
        <CollapsibleSection
          key={solution.id}
          title={solution.solution_type === 'main' ? '标准解法' : '暴力解法'}
          icon={Code2}
          defaultOpen={false}
        >
          <div className="monaco-container">
            <MonacoEditor
              height="400px"
              language={monacoLanguageForSolution(solution)}
              theme={editorTheme.theme}
              beforeMount={editorTheme.beforeMount}
              value={solution.source_code}
              options={{
                readOnly: true,
                minimap: { enabled: false },
                scrollBeyondLastLine: false,
                fontSize: 14,
                lineNumbers: 'on',
                wordWrap: 'on',
              }}
            />
          </div>
        </CollapsibleSection>
      ))}

      {/* Editorial */}
      <CollapsibleSection title="题解" icon={Code2} defaultOpen={false}>
        {editorialLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin text-anvil-400" />
          </div>
        ) : editorial ? (
          <MarkdownRenderer content={editorial} />
        ) : (
          <p className="py-4 text-center text-sm text-anvil-400">
            暂无题解
          </p>
        )}
      </CollapsibleSection>

</ViewPanel>
<ViewPanel id="problem-content" name="data" active={detailTab}>      {/* Test cases */}
      <CollapsibleSection title="测试数据" icon={Download} defaultOpen>
        {testcasesLoading ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin text-anvil-400" />
          </div>
        ) : (
          <TestCasesTable
            testcases={testcases}
            problemId={problem.id}
            canExportHydro={gate.canExportHydro}
            exportBlockedReason={exportBlockedReason}
          />
        )}
      </CollapsibleSection>

</ViewPanel>
<ViewPanel id="problem-content" name="quality" active={detailTab}>      {/* Publication gate */}
      <div className={cn('rounded-lg border p-4', gateCardTone[gate.tone])}>
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <h2 className={cn('flex items-center gap-2 text-base font-semibold', gateTextTone[gate.tone])}>
              <ShieldCheck className="h-5 w-5 shrink-0" />
              质量门禁
            </h2>
            <div className="mt-3 flex flex-wrap items-center gap-2 text-sm">
              <span className={cn('forge-badge', gateTextTone[gate.tone])}>
                {gate.label}
              </span>
              <span className="text-anvil-600 dark:text-anvil-300">
                gate: {statusLabel(gate.status)}
              </span>
              {gate.stale && (
                <span className="forge-badge bg-warning-100 text-warning-700 dark:bg-warning-500/10 dark:text-warning-300">
                  stale
                </span>
              )}
            </div>
            {gate.reason && (
              <p className="mt-2 break-words text-sm text-anvil-600 dark:text-anvil-300">
                {gate.reason}
              </p>
            )}
            <dl className="mt-3 grid gap-3 text-xs text-anvil-500 dark:text-anvil-400 sm:grid-cols-3">
              <div className="min-w-0">
                <dt className="font-medium text-anvil-600 dark:text-anvil-300">
                  策略版本
                </dt>
                <dd className="mt-1 break-all">{gate.policyVersion || '-'}</dd>
              </div>
              <div className="min-w-0">
                <dt className="font-medium text-anvil-600 dark:text-anvil-300">
                  刷新时间
                </dt>
                <dd className="mt-1 break-all">
                  {gate.refreshedAt ? formatDate(gate.refreshedAt) : '-'}
                </dd>
              </div>
              <div className="min-w-0">
                <dt className="font-medium text-anvil-600 dark:text-anvil-300">
                  验证证据
                </dt>
                <dd className="mt-1 break-all">{gate.validationHash || '-'}</dd>
              </div>
            </dl>
          </div>

          <div className="flex shrink-0 flex-wrap items-center gap-2">
            <button
              className="forge-btn-secondary"
              onClick={handleValidate}
              disabled={validating}
            >
              {validating ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <Check className="h-4 w-4" />
              )}
              验证
            </button>
            <Link
              href={`/problems/${problem.id}/edit`}
              className="forge-btn-secondary"
            >
              <Edit className="h-4 w-4" />
              编辑
            </Link>
          </div>
        </div>

        {!gate.canExportHydro && (
          <div className="mt-4 flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              className="forge-input min-w-0 flex-1 font-mono text-sm"
              value={refreshHash}
              onChange={(event) => setRefreshHash(event.target.value)}
              placeholder="validation_report_sha256"
              aria-label="validation_report_sha256"
            />
            <button
              className="forge-btn-primary shrink-0"
              onClick={handleRefreshGate}
              disabled={refreshingGate || refreshHash.trim() === ''}
            >
              {refreshingGate ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <RefreshCw className="h-4 w-4" />
              )}
              刷新 gate
            </button>
          </div>
        )}

        {problem.status === 'quarantined' && (
          <div className="mt-4 flex flex-col gap-3 rounded border border-forge-300/60 bg-white/70 p-3 dark:border-forge-700/60 dark:bg-anvil-900/40 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <p className="text-sm font-medium text-anvil-700 dark:text-anvil-200">
                管理员审核
              </p>
              <p className="mt-1 text-xs text-anvil-500 dark:text-anvil-400">
                点击后自动记录审核人和时间，并重新执行全部发布门禁。
              </p>
            </div>
            <button
              type="button"
              className="forge-btn-primary shrink-0"
              onClick={handleApprovePublicRelease}
              disabled={approvingRelease}
            >
              {approvingRelease ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : (
                <ShieldCheck className="h-4 w-4" />
              )}
              审核通过并发布
            </button>
          </div>
        )}

        {releaseApprovalError && (
          <p className="mt-3 break-words text-sm text-danger-600 dark:text-danger-300">
            {releaseApprovalError}
          </p>
        )}
        {releaseApprovalResult && (
          <p className="mt-3 rounded border border-success-300 bg-success-50 p-3 text-sm text-success-700 dark:border-success-700 dark:bg-success-500/10 dark:text-success-300">
            审核已记录：{releaseApprovalResult.approved_by}，结果为
            {' '}{statusLabel(releaseApprovalResult.release_status)}。
          </p>
        )}

        {gateRefreshError && (
          <p className="mt-3 break-words text-sm text-danger-600 dark:text-danger-300">
            {gateRefreshError}
          </p>
        )}
        {gateRefreshResult && (
          <div className="mt-3 rounded border border-anvil-200 bg-white/70 p-3 text-sm dark:border-anvil-700 dark:bg-anvil-900/40">
            <p className="font-medium text-anvil-700 dark:text-anvil-200">
              refresh: {gateRefreshResult.decision}
              {gateRefreshResult.release_status
                ? ` / ${statusLabel(gateRefreshResult.release_status)}`
                : ''}
            </p>
            {(gateRefreshResult.blocking_issues ?? []).length > 0 && (
              <ul className="mt-2 list-disc space-y-1 pl-5 text-anvil-600 dark:text-anvil-300">
                {(gateRefreshResult.blocking_issues ?? []).map((issue) => (
                  <li key={issue}>{issue}</li>
                ))}
              </ul>
            )}
            {gateRefreshResult.release_quarantine_reason && (
              <p className="mt-2 break-words text-danger-600 dark:text-danger-300">
                {gateRefreshResult.release_quarantine_reason}
              </p>
            )}
          </div>
        )}
      </div>

      {/* GPT review result */}
      {problem.workflow_id && (
        <div
          className={cn(
            'rounded-lg border p-4',
            workflowReview?.approved
              ? 'border-success-400/30 bg-success-50 dark:bg-success-500/10'
              : workflowReview
                ? 'border-danger-400/30 bg-danger-50 dark:bg-danger-500/10'
                : 'border-anvil-200 bg-white dark:border-anvil-700 dark:bg-anvil-900',
          )}
        >
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <h2 className="flex items-center gap-2 text-base font-semibold text-anvil-800 dark:text-anvil-100">
                {workflowReview?.approved ? (
                  <ShieldCheck className="h-5 w-5 text-success-500" />
                ) : (
                  <AlertTriangle className="h-5 w-5 text-danger-500" />
                )}
                GPT 验题结果
              </h2>
              <p className="mt-1 text-xs text-anvil-500 dark:text-anvil-400">
                来自生成工作流中的验题模型，供隔离区人工复核参考。
              </p>
            </div>
            <Link
              href={`/workflows/${problem.workflow_id}`}
              className="forge-btn-ghost shrink-0"
            >
              <ExternalLink className="h-4 w-4" />
              完整工作流
            </Link>
          </div>

          {workflowReviewLoading && (
            <div className="mt-4 flex items-center gap-2 text-sm text-anvil-500">
              <Loader2 className="h-4 w-4 animate-spin" />
              正在读取验题结果…
            </div>
          )}

          {!workflowReviewLoading && workflowReviewError && (
            <p className="mt-4 text-sm text-danger-600 dark:text-danger-300">
              {workflowReviewError}
            </p>
          )}

          {!workflowReviewLoading && !workflowReviewError && !workflowReview && (
            <p className="mt-4 text-sm text-anvil-500 dark:text-anvil-400">
              该工作流尚未产生 GPT 验题结果。
            </p>
          )}

          {workflowReview && (
            <div className="mt-4 space-y-4">
              <div className="flex flex-wrap gap-2 text-sm">
                <span
                  className={cn(
                    'forge-badge',
                    workflowReview.approved
                      ? 'bg-success-100 text-success-700 dark:bg-success-500/10 dark:text-success-300'
                      : 'bg-danger-100 text-danger-700 dark:bg-danger-500/10 dark:text-danger-300',
                  )}
                >
                  {workflowReview.approved ? '验题通过' : '验题未通过'}
                </span>
                {workflowReview.confidence !== null && (
                  <span className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300">
                    置信度 {Math.round(workflowReview.confidence * 100)}%
                  </span>
                )}
                {workflowReview.estimatedDifficulty !== null && (
                  <span className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300">
                    估计难度 {workflowReview.estimatedDifficulty}
                  </span>
                )}
                <span className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300">
                  {workflowReview.isDuplicate ? '疑似重复' : '未判定重复'}
                </span>
              </div>

              {workflowReview.issues.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold text-danger-700 dark:text-danger-300">
                    {workflowReview.approved ? '注意事项' : '未通过原因'}
                  </h3>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-anvil-700 dark:text-anvil-200">
                    {workflowReview.issues.map((issue, index) => (
                      <li key={`${index}-${issue}`}>{issue}</li>
                    ))}
                  </ul>
                </div>
              )}

              {workflowReview.isDuplicate && (
                <div className="rounded border border-warning-300 bg-warning-50 p-3 text-sm text-warning-800 dark:border-warning-700 dark:bg-warning-500/10 dark:text-warning-200">
                  <p className="font-medium">
                    重复对象：{workflowReview.duplicateOf || '未指定'}
                  </p>
                  {workflowReview.duplicateReason && (
                    <p className="mt-1">{workflowReview.duplicateReason}</p>
                  )}
                </div>
              )}

              {workflowReview.suggestions.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold text-anvil-700 dark:text-anvil-200">
                    修改建议
                  </h3>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-anvil-600 dark:text-anvil-300">
                    {workflowReview.suggestions.map((suggestion, index) => (
                      <li key={`${index}-${suggestion}`}>{suggestion}</li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          )}
        </div>
      )}

</ViewPanel>
<ViewPanel id="problem-content" name="metadata" active={detailTab}>      {/* JSON Metadata */}
      <CollapsibleSection title="元数据 (JSON)" icon={FileJson} defaultOpen={false}>
        {metadataLoading && !rawMetadata ? (
          <div className="flex items-center justify-center py-8">
            <Loader2 className="h-6 w-6 animate-spin text-anvil-400" />
          </div>
        ) : rawMetadata ? (
          <div className="relative">
            <button
              className="absolute right-2 top-2 rounded p-1 text-anvil-400 hover:bg-anvil-100 hover:text-anvil-600 dark:hover:bg-anvil-800"
              onClick={() => {
                navigator.clipboard.writeText(
                  JSON.stringify(rawMetadata, null, 2),
                );
              }}
              title="复制 JSON"
            >
              <Copy className="h-4 w-4" />
            </button>
            <pre className="code-block overflow-x-auto p-4 text-sm">
              <code>{JSON.stringify(rawMetadata, null, 2)}</code>
            </pre>
          </div>
        ) : (
          <p className="py-4 text-center text-sm text-anvil-400">
            暂无元数据
          </p>
        )}
      </CollapsibleSection>

</ViewPanel>
      {/* Created/Updated */}
      <div className="text-xs text-anvil-400 dark:text-anvil-500">
        创建于 {formatDate(problem.created_at)} | 更新于{' '}
        {formatDate(problem.updated_at)}
      </div>
    </div>
  );
}
