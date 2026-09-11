'use client';

import { useCallback, useEffect, useState } from 'react';
import { useParams, useRouter } from 'next/navigation';
import Link from 'next/link';
import {
  AlertTriangle,
  ArrowLeft,
  Loader2,
  RotateCcw,
  Save,
} from 'lucide-react';

import { EmptyState, PageHeader, SectionHeading } from '@/components/ui/Workspace';
import { useProblem } from '@/hooks/useProblems';
import { updateProblem } from '@/lib/api';
import type { Problem, ProblemLevel, ProblemUpdateRequest } from '@/lib/types';
import {
  ALGORITHM_DIFFICULTY_MIN,
  DIFFICULTY_MAX,
  DIFFICULTY_MIN,
  DIFFICULTY_STEP,
  SYNTAX_DIFFICULTY_MAX,
  getDifficultyLabel,
} from '@/lib/constants';

type EditFormState = {
  title: string;
  statement: string;
  level: ProblemLevel;
  difficulty: number;
  tagsText: string;
  oneLineHint: string;
  detailedSolution: string;
  timeLimit: number;
  memoryLimit: number;
  metadataText: string;
};

function formFromProblem(problem: Problem): EditFormState {
  return {
    title: problem.title ?? '',
    statement: problem.statement ?? '',
    level: problem.level,
    difficulty: problem.difficulty,
    tagsText: (problem.tags ?? []).join(', '),
    oneLineHint: problem.one_line_hint ?? '',
    detailedSolution: problem.detailed_solution ?? '',
    timeLimit: problem.time_limit,
    memoryLimit: problem.memory_limit,
    metadataText: problem.metadata_json
      ? JSON.stringify(problem.metadata_json, null, 2)
      : '',
  };
}

function parseTags(value: string): string[] {
  return value
    .split(/[,\n]/)
    .map((tag) => tag.trim())
    .filter(Boolean);
}

export default function ProblemEditPage() {
  const params = useParams();
  const router = useRouter();
  const id = params.id as string;
  const { problem, loading, error, refresh } = useProblem(id);

  const [form, setForm] = useState<EditFormState | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (problem) {
      setForm(formFromProblem(problem));
    }
  }, [problem]);

  const updateField = useCallback(
    <K extends keyof EditFormState>(key: K, value: EditFormState[K]) => {
      setForm((prev) => (prev ? { ...prev, [key]: value } : prev));
    },
    [],
  );

  const handleReset = useCallback(() => {
    if (problem) {
      setSaveError(null);
      setForm(formFromProblem(problem));
    }
  }, [problem]);

  const handleSave = useCallback(async () => {
    if (!problem || !form) return;
    setSaveError(null);

    if (!form.title.trim()) {
      setSaveError('标题不能为空');
      return;
    }
    if (!form.statement.trim()) {
      setSaveError('题面不能为空');
      return;
    }
    if (form.timeLimit <= 0 || form.memoryLimit <= 0) {
      setSaveError('时间限制和内存限制必须为正数');
      return;
    }

    let metadata: Record<string, unknown> | undefined;
    if (form.metadataText.trim()) {
      try {
        const parsed = JSON.parse(form.metadataText) as unknown;
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
          setSaveError('元数据必须是 JSON object');
          return;
        }
        metadata = parsed as Record<string, unknown>;
      } catch (err) {
        setSaveError(err instanceof Error ? `元数据 JSON 无效：${err.message}` : '元数据 JSON 无效');
        return;
      }
    }

    const body: ProblemUpdateRequest = {
      expected_updated_at: problem.updated_at,
      title: form.title.trim(),
      statement: form.statement.trim(),
      level: form.level,
      difficulty: form.difficulty,
      tags: parseTags(form.tagsText),
      one_line_hint: form.oneLineHint.trim(),
      detailed_solution: form.detailedSolution.trim(),
      time_limit: form.timeLimit,
      memory_limit: form.memoryLimit,
      metadata_json: metadata,
    };

    setSaving(true);
    try {
      const res = await updateProblem(problem.id, body);
      if (res.data?.id) {
        router.push(`/problems/${res.data.id}`);
      } else {
        await refresh();
      }
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : '保存失败');
    } finally {
      setSaving(false);
    }
  }, [form, problem, refresh, router]);

  if (error || (!loading && !problem)) {
    return (
      <div className="af-page">
        <PageHeader eyebrow="编程题库" title="编辑题目" />
        <div className="af-panel"><EmptyState icon={AlertTriangle} title={error ?? '题目未找到'}
          description="题目加载成功后才能开始编辑。"
          action={<Link href="/problems" className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回列表</Link>} /></div>
      </div>
    );
  }

  if (loading || !form || !problem) {
    return <div className="af-page" aria-busy="true"><PageHeader eyebrow="编程题库" title="编辑题目" />
      <div className="af-panel space-y-5"><div className="forge-skeleton h-10 w-2/3" /><div className="forge-skeleton h-64 w-full" /></div>
    </div>;
  }

  const minDifficulty =
    form.level === 'syntax' ? DIFFICULTY_MIN : ALGORITHM_DIFFICULTY_MIN;
  const maxDifficulty =
    form.level === 'syntax' ? SYNTAX_DIFFICULTY_MAX : DIFFICULTY_MAX;

  return (
    <div className="af-page">
      <PageHeader eyebrow={`编程题库 · ${problem.serial_number}`} title="编辑题目" description="编辑题面、题解和题目属性，保存后更新当前题目。"
        actions={<>
          <Link href={`/problems/${problem.id}`} className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回题目详情</Link>
          <button type="button" className="forge-btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}{saving ? '保存中…' : '保存'}
          </button>
        </>}
      />

      {saveError && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{saveError}</div>}

      <div className="af-form-layout">
        <div className="af-form-main">
          <section className="af-panel space-y-5">
            <SectionHeading eyebrow="01" title="题面内容" description="面向做题者展示的标题与正文。" />
            <label className="block space-y-2">
              <span className="block text-sm font-medium">标题</span>
              <input className="forge-input" value={form.title} onChange={(event) => updateField('title', event.target.value)} />
            </label>
            <label className="block space-y-2">
              <span className="block text-sm font-medium">题面</span>
              <textarea className="forge-input min-h-[380px] resize-y font-mono text-sm leading-7" value={form.statement} onChange={(event) => updateField('statement', event.target.value)} />
            </label>
          </section>

          <section className="af-panel space-y-5">
            <SectionHeading eyebrow="02" title="提示与题解" description="补充解题思路，以及标准解法的说明。" />
            <label className="block space-y-2">
              <span className="block text-sm font-medium">一句话提示</span>
              <input className="forge-input" value={form.oneLineHint} onChange={(event) => updateField('oneLineHint', event.target.value)} />
            </label>
            <label className="block space-y-2">
              <span className="block text-sm font-medium">详细题解 / 标程说明</span>
              <textarea className="forge-input min-h-[280px] resize-y font-mono text-sm leading-7" value={form.detailedSolution} onChange={(event) => updateField('detailedSolution', event.target.value)} />
            </label>
          </section>

          <section className="af-panel space-y-5">
            <SectionHeading eyebrow="03" title="题目属性" description="设定分类、难度与运行限制。" />
            <div className="grid grid-cols-1 gap-5 sm:grid-cols-2">
              <label className="block space-y-2">
                <span className="block text-sm font-medium">类型</span>
                <select className="forge-input" value={form.level} onChange={(event) => updateField('level', event.target.value as ProblemLevel)}>
                  <option value="syntax">语法题</option><option value="algorithm">算法题</option>
                  <option value="gplt_l1">天梯 L1</option><option value="gplt_l2">天梯 L2</option><option value="gplt_l3">天梯 L3</option>
                </select>
              </label>
              <label className="block space-y-3">
                <span className="flex items-center justify-between gap-3 text-sm"><span className="font-medium">难度</span><span className="text-[var(--dm)]">{form.difficulty} ({getDifficultyLabel(form.difficulty)})</span></span>
                <input type="range" min={minDifficulty} max={maxDifficulty} step={DIFFICULTY_STEP} value={form.difficulty}
                  onChange={(event) => updateField('difficulty', Number(event.target.value))} className="w-full accent-[var(--da)]" />
              </label>
              <label className="block space-y-2">
                <span className="block text-sm font-medium">时间限制 (ms)</span>
                <input type="number" min={1} className="forge-input" value={form.timeLimit} onChange={(event) => updateField('timeLimit', Number(event.target.value) || 0)} />
              </label>
              <label className="block space-y-2">
                <span className="block text-sm font-medium">内存限制 (MB)</span>
                <input type="number" min={1} className="forge-input" value={form.memoryLimit} onChange={(event) => updateField('memoryLimit', Number(event.target.value) || 0)} />
              </label>
            </div>
            <label className="block space-y-2">
              <span className="block text-sm font-medium">标签（逗号或换行分隔）</span>
              <textarea className="forge-input min-h-[96px] resize-y" value={form.tagsText} onChange={(event) => updateField('tagsText', event.target.value)} />
            </label>
          </section>

          <details className="af-panel">
            <summary className="cursor-pointer text-sm font-semibold">元数据 JSON</summary>
            <label className="mt-4 block space-y-2">
              <span className="block text-xs text-[var(--dm)]">保留题目的附加属性，内容须为 JSON object。</span>
              <textarea aria-label="元数据 JSON" className="forge-input min-h-[240px] resize-y font-mono text-sm" value={form.metadataText}
                onChange={(event) => updateField('metadataText', event.target.value)} placeholder='{"source":"manual-edit"}' />
            </label>
          </details>

          <div className="af-sticky-actions">
            <span className="text-sm text-[var(--dm)]">确认内容后保存修改</span>
            <button type="button" className="forge-btn-primary" onClick={handleSave} disabled={saving}>
              {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}{saving ? '保存中…' : '保存'}
            </button>
          </div>
        </div>

        <aside className="af-form-rail">
          <section className="af-summary">
            <SectionHeading title="当前题目" />
            <p className="break-words text-sm font-medium">{form.title || '未填写标题'}</p>
            <p className="mt-2 break-all font-mono text-xs text-[var(--dm)]">{problem.serial_number}</p>
            <div className="mt-4">
              <div className="af-summary-row"><span>难度</span><strong>{form.difficulty}</strong></div>
              <div className="af-summary-row"><span>时间限制</span><strong>{form.timeLimit} ms</strong></div>
              <div className="af-summary-row"><span>内存限制</span><strong>{form.memoryLimit} MB</strong></div>
              <div className="af-summary-row"><span>标签</span><strong>{parseTags(form.tagsText).length} 个</strong></div>
            </div>
            <p className="mt-4 break-all text-xs leading-6 text-[var(--dm)]">最后更新：{problem.updated_at}</p>
            <button type="button" className="forge-btn-secondary" onClick={handleReset} disabled={saving}><RotateCcw className="h-4 w-4" />重置</button>
            <p className="mt-2 text-xs leading-6 text-[var(--dm)]">重置会恢复当前已保存的题目内容。</p>
          </section>
        </aside>
      </div>
    </div>
  );
}
