'use client';

import { useState } from 'react';
import { ChevronDown } from 'lucide-react';

import MarkdownRenderer from '@/components/MarkdownRenderer';
import {
  QUIZ_DIFFICULTY_LABELS,
  QUIZ_TYPE_LABELS,
  QUIZ_VISIBILITY_LABELS,
} from '@/lib/constants';
import type { QuizProblem } from '@/lib/types';
import { cn, formatDate } from '@/lib/utils';

interface Props {
  quiz: QuizProblem;
}

export default function QuizPreview({ quiz }: Props) {
  const [explanationOpen, setExplanationOpen] = useState(false);

  return (
    <div className="space-y-6">
      <div className="forge-card">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p className="font-mono text-sm text-anvil-400">{quiz.code}</p>
            <h1 className="mt-1 text-2xl font-bold">{quiz.title}</h1>
          </div>
          <div className="flex flex-wrap gap-2">
            <span className="forge-badge bg-forge-100 text-forge-700 dark:bg-forge-900/30 dark:text-forge-300">
              {QUIZ_TYPE_LABELS[quiz.type] ?? quiz.type}
            </span>
            <span className="forge-badge bg-anvil-100 text-anvil-700 dark:bg-anvil-800 dark:text-anvil-300">
              {QUIZ_DIFFICULTY_LABELS[quiz.difficulty] ?? quiz.difficulty}
            </span>
            <span className="forge-badge bg-anvil-100 text-anvil-700 dark:bg-anvil-800 dark:text-anvil-300">
              {QUIZ_VISIBILITY_LABELS[quiz.visibility] ?? quiz.visibility}
            </span>
            {quiz.is_vip && (
              <span className="forge-badge bg-ember-100 text-ember-700 dark:bg-ember-900/30 dark:text-ember-300">
                VIP
              </span>
            )}
          </div>
        </div>

        {quiz.tags.length > 0 && (
          <div className="mt-4 flex flex-wrap gap-2">
            {quiz.tags.map((tag) => (
              <span
                key={tag}
                className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300"
              >
                {tag}
              </span>
            ))}
          </div>
        )}

        <div className="mt-6 rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
          <MarkdownRenderer content={quiz.statement} />
        </div>
      </div>

      {quiz.type === 'choice' && quiz.options && quiz.options.length > 0 && (
        <div className="forge-card">
          <h2 className="forge-section-title mb-4">选项</h2>
          <div className="space-y-3">
            {quiz.options.map((option) => (
              <div
                key={option.label}
                className={cn(
                  'rounded-lg border p-4',
                  quiz.answers.includes(option.label)
                    ? 'border-success-400 bg-success-50 dark:bg-success-500/10'
                    : 'border-anvil-200 dark:border-anvil-700',
                )}
              >
                <div className="flex items-start gap-3">
                  <span className="shrink-0 font-mono font-semibold">
                    {option.label}.
                  </span>
                  <div className="min-w-0 flex-1 [&>p:first-child]:mt-0 [&>p:last-child]:mb-0">
                    <MarkdownRenderer content={option.content} />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="forge-card">
        <h2 className="forge-section-title mb-4">答案</h2>
        <div className="flex flex-wrap gap-2">
          {quiz.answers.map((answer) => (
            <span
              key={answer}
              className="forge-badge bg-danger-100 text-danger-700 dark:bg-danger-900/30 dark:text-danger-300"
            >
              {answer}
            </span>
          ))}
        </div>
      </div>

      {quiz.explanation && (
        <div className="forge-card">
          <button
            type="button"
            className="flex w-full items-center justify-between text-left"
            onClick={() => setExplanationOpen((prev) => !prev)}
          >
            <h2 className="forge-section-title">解析</h2>
            <ChevronDown
              className={cn(
                'h-4 w-4 transition-transform',
                explanationOpen && 'rotate-180',
              )}
            />
          </button>
          {explanationOpen && (
            <div className="mt-4 rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
              <MarkdownRenderer content={quiz.explanation} />
            </div>
          )}
        </div>
      )}

      <p className="text-xs text-anvil-400">
        创建于 {formatDate(quiz.created_at)}，更新于 {formatDate(quiz.updated_at)}
      </p>
    </div>
  );
}
