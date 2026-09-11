'use client';

import { useState } from 'react';
import Link from 'next/link';
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  FileArchive,
  Loader2,
  Upload,
  XCircle,
} from 'lucide-react';

import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import { validateHydroPackage } from '@/lib/api';
import type {
  HydroValidatedProblem,
  HydroValidationIssue,
  HydroValidationReport,
} from '@/lib/types';
import { cn } from '@/lib/utils';

function IssueList({
  title,
  issues,
  tone,
}: {
  title: string;
  issues?: HydroValidationIssue[];
  tone: 'danger' | 'warning';
}) {
  if (!issues || issues.length === 0) return null;
  return (
    <div className="space-y-2">
      <h3
        className={cn(
          'text-sm font-semibold',
          tone === 'danger' ? 'text-danger-600' : 'text-ember-600',
        )}
      >
        {title}
      </h3>
      <ul className="space-y-1 text-sm text-anvil-600 dark:text-anvil-300">
        {issues.map((issue, idx) => (
          <li key={`${issue.path ?? issue.field ?? title}-${idx}`}>
            <span className="font-mono text-xs text-anvil-500">
              {[issue.path, issue.field].filter(Boolean).join(' / ') || '-'}
            </span>
            <span className="mx-2 text-anvil-300">·</span>
            <span>{issue.message}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function ProblemReportCard({ problem }: { problem: HydroValidatedProblem }) {
  return (
    <article className="af-collection-row">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-start gap-2">
            {problem.valid ? <CheckCircle2 className="mt-0.5 h-5 w-5 shrink-0 text-success-500" /> : <XCircle className="mt-0.5 h-5 w-5 shrink-0 text-danger-500" />}
            <h3 className="break-words text-base font-semibold">{problem.title || problem.path}</h3>
          </div>
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-[var(--dm)]">
            <span className="font-mono">{problem.pid || 'missing-pid'}</span>
            <span>{problem.config_mode || 'unknown'}</span>
            <span>{problem.case_count} cases</span>
            <span>题面文件 {problem.statement_files.length || 0}</span>
            <span>测试文件 {problem.testdata_files}</span>
          </div>
          <p className="mt-2 break-all font-mono text-xs text-[var(--dm)]">目录：{problem.path}</p>
        </div>
        <span className={cn('forge-badge', problem.valid
          ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300'
          : 'bg-danger-50 text-danger-700 dark:bg-danger-500/10 dark:text-danger-300')}>{problem.valid ? '可用' : '需修复'}</span>
      </div>
      <div className="mt-4 space-y-4">
        <IssueList title="错误" issues={problem.errors} tone="danger" />
        <IssueList title="阶段二保留项" issues={problem.unsupported} tone="danger" />
        <IssueList title="提示" issues={problem.warnings} tone="warning" />
      </div>
    </article>
  );
}

export default function HydroImportPage() {
  const [file, setFile] = useState<File | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<HydroValidationReport | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setReport(null);
    if (!file) {
      setError('请选择 Hydro ZIP 文件');
      return;
    }
    if (!file.name.toLowerCase().endsWith('.zip')) {
      setError('只支持 .zip 文件');
      return;
    }
    setLoading(true);
    try {
      const res = await validateHydroPackage(file);
      setReport(res.data ?? null);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Hydro 校验失败');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="af-page">
      <PageHeader eyebrow="编程题库" title="Hydro 包校验" description="阶段一兼容预检"
        actions={<Link href="/problems" className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回题目列表</Link>}
      />

      <form className="af-form-layout" onSubmit={handleSubmit}>
        <div className="af-form-main">
          {(error || report?.errors?.length) ? <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
            {error ?? report?.errors?.[0]?.message}
          </div> : null}

          <section className="af-panel space-y-5 p-6">
            <SectionHeading title="选择 Hydro 题目包" description="上传 ZIP，检查题面、配置与测试文件，并按题目查看校验结果。" />
            <div className="rounded-lg border border-dashed border-[var(--dl)] bg-[var(--ds)] p-5">
              <div className="mb-4 flex items-start gap-3">
                <FileArchive className="mt-0.5 h-6 w-6 shrink-0 text-[var(--da)]" />
                <div className="min-w-0">
                  <p className="break-all text-sm font-medium">{file?.name ?? '未选择文件'}</p>
                  <p className="mt-1 text-xs text-[var(--dm)]">{file ? `${(file.size / 1024).toFixed(1)} KB · 准备校验` : '支持 .zip 格式的 Hydro 题目包。'}</p>
                </div>
              </div>
              <label className="block space-y-2">
                <span className="sr-only">Hydro ZIP</span>
                <input className="forge-input" type="file" accept=".zip,application/zip" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
              </label>
            </div>
          </section>
        </div>

        <aside className="af-form-rail">
          <section className="af-summary">
            <SectionHeading title="校验任务" />
            <div className="af-summary-row"><span>文件格式</span><strong>Hydro ZIP</strong></div>
            <div className="af-summary-row"><span>文件</span><strong>{file ? '已选择' : '未选择'}</strong></div>
            <p className="my-4 text-xs leading-6 text-[var(--dm)]">校验结束后，逐题查看可用状态、错误路径和修复提示。</p>
            <button className="forge-btn-primary w-full" type="submit" disabled={loading || !file}>
              {loading ? <><Loader2 className="h-4 w-4 animate-spin" />校验中...</> : <><Upload className="h-4 w-4" />开始校验</>}
            </button>
          </section>
        </aside>
      </form>

      {report && <section className="af-table-panel" aria-label="Hydro 校验结果">
        <div className="flex flex-wrap items-center justify-between gap-4 border-b border-[var(--dl)] px-5 py-4">
          <div className="flex items-center gap-3">
            {report.valid ? <CheckCircle2 className="h-5 w-5 text-success-500" /> : <AlertTriangle className="h-5 w-5 text-danger-500" />}
            <SectionHeading title={report.valid ? '校验通过' : '校验未通过'} description="按题目核对结果与需要处理的问题。" />
          </div>
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-[var(--dm)]">
            <span className="font-medium text-[var(--dt)]">{report.success_count}/{report.problem_count} 可用</span>
            <span>{report.mode}</span>
            <span className="text-xs">{report.phase}</span>
          </div>
        </div>
        <div>{report.problems.map((problem) => <ProblemReportCard key={`${problem.path}-${problem.pid}`} problem={problem} />)}</div>
      </section>}
    </div>
  );
}
