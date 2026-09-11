'use client';

import { useCallback, useState } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';
import { ArrowLeft, Loader2, Sparkles } from 'lucide-react';

import { FormSection, PageHeader } from '@/components/ui/Workspace';
import KnowledgePointPicker from '@/components/quiz/KnowledgePointPicker';
import { useGenerateQuiz } from '@/hooks/useQuiz';
import {
  QUIZ_DIFFICULTY_LABELS,
  QUIZ_GENERATE_COUNT_MAX,
  QUIZ_SUBJECTS,
  QUIZ_TYPE_LABELS,
  QUIZ_VISIBILITY_LABELS,
} from '@/lib/constants';
import type {
  QuizDifficulty,
  QuizSubject,
  QuizType,
  QuizVisibility,
} from '@/lib/types';
import { cn } from '@/lib/utils';
import { useAppStore } from '@/stores/appStore';

type GeneratableQuizType = Exclude<QuizType, 'programming'>;

export default function NewQuizPage() {
  const router = useRouter();
  const { selectedSubject, setSubject } = useAppStore();
  const { generate, loading, error, reset } = useGenerateQuiz();
  const [localError, setLocalError] = useState<string | null>(null);
  const [form, setForm] = useState({
    subject: selectedSubject,
    type: 'choice' as GeneratableQuizType,
    difficulty: 'easy' as QuizDifficulty,
    count: 5,
    knowledge_point_codes: [] as string[],
    tagsText: '',
    visibility: 'public' as QuizVisibility,
    is_vip: false,
    custom_prompt: '',
  });

  const setFormSubject = useCallback(
    (subject: QuizSubject) => {
      setSubject(subject);
      setForm((prev) => ({
        ...prev,
        subject,
        knowledge_point_codes: [],
      }));
    },
    [setSubject],
  );

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    reset();
    setLocalError(null);

    if (form.knowledge_point_codes.length === 0) {
      setLocalError('请至少选 1 个知识点');
      return;
    }
    if (form.count < 1 || form.count > QUIZ_GENERATE_COUNT_MAX) {
      setLocalError(`生成数量必须在 1-${QUIZ_GENERATE_COUNT_MAX} 之间`);
      return;
    }

    const data = await generate({
      subject: form.subject,
      type: form.type,
      difficulty: form.difficulty,
      count: form.count,
      knowledge_point_codes: form.knowledge_point_codes,
      tags: form.tagsText
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      visibility: form.visibility,
      is_vip: form.is_vip,
      custom_prompt: form.custom_prompt.trim() || undefined,
    });

    if (data?.workflow_id) {
      router.push(`/workflows/${data.workflow_id}`);
    }
  }

  return (
    <div className="af-page">
      <PageHeader eyebrow="创作 / 客观题" title="按知识点出题" description="将知识范围、考查方式与难度组合成清楚的出题需求。"
        actions={<Link className="forge-btn-secondary" href="/problem-sets/new?format=mixed">需要多种题型？创建混合题集</Link>}>
        <Link href="/quizzes" className="af-link inline-flex items-center gap-1"><ArrowLeft className="h-4 w-4" />返回客观题题库</Link>
      </PageHeader>
      {(localError || error) && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{localError || error}</div>}
      <form className="af-form-layout" onSubmit={handleSubmit}>
        <div className="af-form-main">
          <FormSection number="01" title="学习目标与知识范围" description="知识点是本次出题的基础，补充要求用来表达具体的教学意图。">
            <label className="af-field"><span>补充出题要求（可选）</span><textarea className="forge-input min-h-32 resize-y" value={form.custom_prompt} onChange={(event) => setForm((prev) => ({ ...prev, custom_prompt: event.target.value }))} placeholder="例如：考查学生对循环边界的理解，选项覆盖常见误解，解析要说明每种错误的原因。" /></label>
            <div className="af-choice-grid" role="group" aria-label="学科">{QUIZ_SUBJECTS.map((subject) => <button key={subject.value} type="button" className="af-choice-card" aria-pressed={form.subject === subject.value} onClick={() => setFormSubject(subject.value as QuizSubject)}><span><strong className="block text-sm font-semibold">{subject.label}</strong><span className="af-hint mt-1 block">使用该学科的知识点池</span></span></button>)}</div>
            <div className="border-t border-[var(--dl)] pt-5"><div className="mb-3 flex items-center justify-between gap-3"><h3 className="text-sm font-semibold">选择知识点 *</h3><span className="af-hint">已选 {form.knowledge_point_codes.length} 个</span></div><KnowledgePointPicker subject={form.subject} value={form.knowledge_point_codes} onChange={(codes) => setForm((prev) => ({ ...prev, knowledge_point_codes: codes }))} /></div>
          </FormSection>
          <FormSection number="02" title="考查方式" description="一次生成一种题型。需要混合题型时，使用创建题集统一编排。">
            <div className="grid gap-3 sm:grid-cols-3" role="group" aria-label="题型">{(['choice', 'judge', 'fill_blank'] as GeneratableQuizType[]).map((type) => <button key={type} type="button" className={cn('af-choice-card', 'justify-center')} aria-pressed={form.type === type} onClick={() => setForm((prev) => ({ ...prev, type }))}><strong className="text-sm font-semibold">{QUIZ_TYPE_LABELS[type]}</strong></button>)}</div>
            <div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>生成数量</span><input className="forge-input" type="number" min={1} max={QUIZ_GENERATE_COUNT_MAX} value={form.count} onChange={(event) => setForm((prev) => ({ ...prev, count: Number(event.target.value) }))} /><span className="af-hint">每次支持 1–{QUIZ_GENERATE_COUNT_MAX} 题。</span></label><label className="af-field"><span>难度</span><select className="forge-input" value={form.difficulty} onChange={(event) => setForm((prev) => ({ ...prev, difficulty: event.target.value as QuizDifficulty }))}>{Object.entries(QUIZ_DIFFICULTY_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label></div>
          </FormSection>
          <details className="af-form-details">
            <summary><strong>题库标签与访问设置</strong><span className="af-hint">标签、可见性与 VIP 标记</span></summary>
            <div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>标签（逗号分隔）</span><input className="forge-input" value={form.tagsText} onChange={(event) => setForm((prev) => ({ ...prev, tagsText: event.target.value }))} /></label><label className="af-field"><span>可见性</span><select className="forge-input" value={form.visibility} onChange={(event) => setForm((prev) => ({ ...prev, visibility: event.target.value as QuizVisibility }))}>{Object.entries(QUIZ_VISIBILITY_LABELS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label></div>
            <label className="mt-4 flex items-center gap-2 text-sm"><input type="checkbox" checked={form.is_vip} onChange={(event) => setForm((prev) => ({ ...prev, is_vip: event.target.checked }))} />VIP 题目</label>
          </details>
        </div>
        <aside className="af-form-rail"><div className="af-summary">
          <div><p className="af-hint">本次生成</p><h2 className="mt-2 text-lg font-semibold">{QUIZ_TYPE_LABELS[form.type]}</h2></div>
          <div className="border-y border-[var(--dl)] py-5"><strong className="text-4xl font-semibold tabular-nums">{form.count}</strong><span className="ml-2 text-sm text-[var(--dm)]">道题目</span></div>
          <dl className="space-y-3 text-sm"><div className="af-summary-row"><dt>学科</dt><dd>{QUIZ_SUBJECTS.find((subject) => subject.value === form.subject)?.label}</dd></div><div className="af-summary-row"><dt>知识点</dt><dd>{form.knowledge_point_codes.length} 个</dd></div><div className="af-summary-row"><dt>难度</dt><dd>{QUIZ_DIFFICULTY_LABELS[form.difficulty]}</dd></div><div className="af-summary-row"><dt>可见性</dt><dd>{QUIZ_VISIBILITY_LABELS[form.visibility]}{form.is_vip ? ' · VIP' : ''}</dd></div></dl>
          {form.knowledge_point_codes.length === 0 && <p className="af-hint">开始前请至少选择一个知识点，让题目有明确的考查范围。</p>}
          <div className="af-sticky-actions"><button type="submit" className="forge-btn-primary w-full" disabled={loading}>{loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}{loading ? '提交中…' : '开始生成'}</button><Link href="/quizzes" className="forge-btn-secondary w-full">返回题库</Link></div>
          <p className="af-hint">提交后转到任务详情，生成在后台继续执行。模型使用当前已保存的配置。</p>
          <Link className="af-link text-sm" href="/settings">查看模型配置</Link>
        </div></aside>
      </form>
    </div>
  );
}
