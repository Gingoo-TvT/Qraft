'use client';

import { useMemo, useState } from 'react';

import OptionsEditor from '@/components/quiz/OptionsEditor';
import {
  QUIZ_DIFFICULTY_LABELS,
  QUIZ_TYPE_LABELS,
  QUIZ_VISIBILITY_LABELS,
} from '@/lib/constants';
import type {
  QuizDifficulty,
  QuizOption,
  QuizProblem,
  QuizVisibility,
} from '@/lib/types';

interface Props {
  quiz: QuizProblem;
  onSubmit: (body: Partial<QuizProblem>) => Promise<void> | void;
  saving?: boolean;
}

export default function QuizForm({ quiz, onSubmit, saving = false }: Props) {
  const [form, setForm] = useState({
    title: quiz.title,
    statement: quiz.statement,
    type: quiz.type,
    code_hint: quiz.code_hint ?? '',
    code_id: quiz.code_id?.toString() ?? '',
    options: quiz.options ?? [
      { label: 'A', content: '' },
      { label: 'B', content: '' },
    ],
    answersText: quiz.answers.join(', '),
    difficulty: quiz.difficulty,
    visibility: quiz.visibility,
    is_vip: quiz.is_vip,
    tagsText: quiz.tags.join(', '),
    explanation: quiz.explanation ?? '',
  });
  const [localError, setLocalError] = useState<string | null>(null);

  const answerList = useMemo(
    () =>
      form.answersText
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean),
    [form.answersText],
  );

  function setField<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((prev) => ({ ...prev, [key]: value }));
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLocalError(null);

    if (!form.title.trim()) {
      setLocalError('标题不能为空');
      return;
    }
    if (!form.statement.trim()) {
      setLocalError('题面不能为空');
      return;
    }
    if (answerList.length === 0) {
      setLocalError('答案不能为空');
      return;
    }
    if (
      form.type === 'choice' &&
      form.options.some((option) => !option.content.trim())
    ) {
      setLocalError('选择题选项不能为空');
      return;
    }

    const body: Partial<QuizProblem> = {
      title: form.title.trim(),
      statement: form.statement,
      type: quiz.type,
      code_hint: form.code_hint.trim() || undefined,
      code_id: form.code_id ? Number(form.code_id) : null,
      options:
        form.type === 'choice'
          ? form.options.map((option) => ({
              label: option.label,
              content: option.content.trim(),
            }))
          : undefined,
      answers: answerList,
      difficulty: form.difficulty,
      visibility: form.visibility,
      is_vip: form.is_vip,
      tags: form.tagsText
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      explanation: form.explanation.trim() || undefined,
      subject: quiz.subject,
    };

    await onSubmit(body);
  }

  return (
    <form className="space-y-6" onSubmit={handleSubmit}>
      {localError && (
        <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
          {localError}
        </div>
      )}

      <div className="forge-card space-y-4">
        <h2 className="forge-section-title">基础信息</h2>
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <label className="space-y-1">
            <span className="text-sm font-medium">标题</span>
            <input
              className="forge-input"
              value={form.title}
              onChange={(e) => setField('title', e.target.value)}
            />
          </label>
          <label className="space-y-1">
            <span className="text-sm font-medium">题型</span>
            <select className="forge-input" value={form.type} disabled>
              <option value={form.type}>
                {QUIZ_TYPE_LABELS[form.type] ?? form.type}
              </option>
            </select>
          </label>
          <label className="space-y-1">
            <span className="text-sm font-medium">难度</span>
            <select
              className="forge-input"
              value={form.difficulty}
              onChange={(e) =>
                setField('difficulty', e.target.value as QuizDifficulty)
              }
            >
              {Object.entries(QUIZ_DIFFICULTY_LABELS).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label className="space-y-1">
            <span className="text-sm font-medium">可见性</span>
            <select
              className="forge-input"
              value={form.visibility}
              onChange={(e) =>
                setField('visibility', e.target.value as QuizVisibility)
              }
            >
              {Object.entries(QUIZ_VISIBILITY_LABELS).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label className="space-y-1">
            <span className="text-sm font-medium">代码 ID</span>
            <input
              className="forge-input"
              type="number"
              value={form.code_id}
              onChange={(e) => setField('code_id', e.target.value)}
            />
          </label>
          <label className="space-y-1">
            <span className="text-sm font-medium">标签（逗号分隔）</span>
            <input
              className="forge-input"
              value={form.tagsText}
              onChange={(e) => setField('tagsText', e.target.value)}
            />
          </label>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            className="h-4 w-4 rounded border-anvil-300 text-forge-600 focus:ring-forge-500"
            checked={form.is_vip}
            onChange={(e) => setField('is_vip', e.target.checked)}
          />
          VIP 题目
        </label>
      </div>

      <div className="forge-card space-y-4">
        <h2 className="forge-section-title">题面</h2>
        <textarea
          className="forge-input min-h-[220px] resize-y"
          value={form.statement}
          onChange={(e) => setField('statement', e.target.value)}
        />
      </div>

      <div className="forge-card space-y-4">
        <h2 className="forge-section-title">答案与解析</h2>
        {form.type === 'choice' && (
          <OptionsEditor
            value={form.options as QuizOption[]}
            onChange={(options) => setField('options', options)}
          />
        )}
        <label className="space-y-1">
          <span className="text-sm font-medium">答案（多个用逗号分隔）</span>
          <input
            className="forge-input"
            value={form.answersText}
            onChange={(e) => setField('answersText', e.target.value)}
            placeholder="A 或 对 或 关键字"
          />
        </label>
        <label className="space-y-1">
          <span className="text-sm font-medium">代码提示</span>
          <textarea
            className="forge-input min-h-[80px] resize-y"
            value={form.code_hint}
            onChange={(e) => setField('code_hint', e.target.value)}
          />
        </label>
        <label className="space-y-1">
          <span className="text-sm font-medium">解析</span>
          <textarea
            className="forge-input min-h-[140px] resize-y"
            value={form.explanation}
            onChange={(e) => setField('explanation', e.target.value)}
          />
        </label>
      </div>

      <div className="flex justify-end">
        <button className="forge-btn-primary" type="submit" disabled={saving}>
          {saving ? '保存中...' : '保存'}
        </button>
      </div>
    </form>
  );
}
