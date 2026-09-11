'use client';

import { useCallback, useState } from 'react';
import Link from 'next/link';
import { useParams, useRouter } from 'next/navigation';
import { ArrowLeft, BookOpen, Check, ChevronDown, Edit3, Trash2, X } from 'lucide-react';

import RefreshButton from '@/components/RefreshButton';
import QuizForm from '@/components/quiz/QuizForm';
import MarkdownRenderer from '@/components/MarkdownRenderer';
import { EmptyState, PageHeader, SectionHeading } from '@/components/ui/Workspace';
import { QUIZ_DIFFICULTY_LABELS, QUIZ_SUBJECTS, QUIZ_TYPE_LABELS, QUIZ_VISIBILITY_LABELS } from '@/lib/constants';
import { cn, formatDate } from '@/lib/utils';
import { showToast } from '@/components/layout/NotificationToaster';
import { useDeleteQuiz, useQuiz, useUpdateQuiz } from '@/hooks/useQuiz';
import type { QuizProblem } from '@/lib/types';

function QuizReadingView({ quiz }: { quiz: QuizProblem }) {
  const [explanationOpen, setExplanationOpen] = useState(false);

  return (
    <div className="af-form-layout">
      <div className="af-form-main">
        <section className="af-panel space-y-6">
          <SectionHeading title="题面" />
          <div className="min-w-0"><MarkdownRenderer content={quiz.statement} /></div>

          {quiz.type === 'choice' && quiz.options && quiz.options.length > 0 && (
            <div className="space-y-4 border-t border-[var(--dl)] pt-6">
              <SectionHeading title="选项" />
              <div className="space-y-3">{quiz.options.map((option) => {
                const correct = quiz.answers.includes(option.label);
                return <div key={option.label} className={cn('flex items-start gap-3 rounded-lg border px-4 py-3',
                  correct ? 'border-success-400/50 bg-success-50 dark:bg-success-500/10' : 'border-[var(--dl)]')}>
                  <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded border border-[var(--dl)] bg-[var(--dp)] font-mono text-sm font-medium">{option.label}</span>
                  <div className="min-w-0 flex-1 [&>p:first-child]:mt-0 [&>p:last-child]:mb-0"><MarkdownRenderer content={option.content} /></div>
                  {correct && <span className="mt-0.5 inline-flex shrink-0 items-center gap-1 text-xs text-success-600 dark:text-success-300"><Check className="h-4 w-4" /><span className="sr-only sm:not-sr-only">正确答案</span></span>}
                </div>;
              })}</div>
            </div>
          )}
        </section>

        <section className="af-panel space-y-4 border-l-4 border-l-[var(--da)]">
          <SectionHeading title="答案" />
          <div className="flex flex-wrap gap-2">
            {quiz.answers.map((answer, index) => <span key={`${answer}-${index}`} className="max-w-full break-words rounded-md border border-[var(--dl)] bg-[var(--ds)] px-3 py-2 text-sm font-medium">{answer}</span>)}
          </div>
        </section>

        {quiz.explanation && <section className="af-panel">
          <SectionHeading title="解析" actions={<button type="button" className="af-link inline-flex items-center gap-1.5 text-sm"
            aria-expanded={explanationOpen} aria-controls="quiz-explanation" onClick={() => setExplanationOpen((prev) => !prev)}>
            {explanationOpen ? '收起解析' : '展开解析'}<ChevronDown className={cn('h-4 w-4 transition-transform', explanationOpen && 'rotate-180')} />
          </button>} />
          {explanationOpen && <div id="quiz-explanation" className="mt-5 border-t border-[var(--dl)] pt-5"><MarkdownRenderer content={quiz.explanation} /></div>}
        </section>}
      </div>

      <aside className="af-form-rail">
        <section className="af-summary">
          <SectionHeading title="题目属性" />
          <div className="af-summary-row"><span>题型</span><strong>{QUIZ_TYPE_LABELS[quiz.type] ?? quiz.type}</strong></div>
          <div className="af-summary-row"><span>难度</span><strong>{QUIZ_DIFFICULTY_LABELS[quiz.difficulty] ?? quiz.difficulty}</strong></div>
          <div className="af-summary-row"><span>可见范围</span><strong>{QUIZ_VISIBILITY_LABELS[quiz.visibility] ?? quiz.visibility}</strong></div>
          <div className="af-summary-row"><span>VIP</span><strong>{quiz.is_vip ? '是' : '否'}</strong></div>
          {quiz.tags.length > 0 && <div className="mt-5"><h3>标签</h3><div className="flex flex-wrap gap-2">{quiz.tags.map((tag) => <span key={tag} className="forge-badge">{tag}</span>)}</div></div>}
          <div className="mt-5 space-y-2 border-t border-[var(--dl)] pt-4 text-xs leading-6 text-[var(--dm)]">
            <p>创建于 {formatDate(quiz.created_at)}</p>
            <p>更新于 {formatDate(quiz.updated_at)}</p>
          </div>
        </section>
      </aside>
    </div>
  );
}

export default function QuizDetailPage() {
  const params = useParams();
  const router = useRouter();
  const id = params.id as string;
  const { quiz, loading, error, refresh } = useQuiz(id);
  const { update, loading: saving, error: saveError } = useUpdateQuiz();
  const { remove, loading: deleting, error: deleteError } = useDeleteQuiz();
  const [editing, setEditing] = useState(false);

  const handleSave = useCallback(
    async (body: Partial<QuizProblem>) => {
      const updated = await update(id, body);
      if (updated) {
        showToast('客观题已保存', 'success');
        setEditing(false);
        refresh();
      }
    },
    [id, refresh, update],
  );

  const handleDelete = useCallback(async () => {
    if (!window.confirm('确定删除这道客观题吗？')) return;
    const ok = await remove(id);
    if (ok) {
      showToast('客观题已删除', 'success');
      router.push('/quizzes');
    }
  }, [id, remove, router]);

  if (loading && !quiz) {
    return <div className="af-page" aria-busy="true"><PageHeader eyebrow="客观题库" title="客观题详情" />
      <div className="af-panel space-y-4"><div className="forge-skeleton h-8 w-2/3" /><div className="forge-skeleton h-64 w-full" /></div>
    </div>;
  }

  if (error || !quiz) {
    return <div className="af-page"><PageHeader eyebrow="客观题库" title="客观题详情" />
      <div className="af-panel"><EmptyState icon={BookOpen} title={error ?? '客观题未找到'}
        action={<Link href="/quizzes" className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回题库</Link>} /></div>
    </div>;
  }

  return (
    <div className="af-page">
      <PageHeader eyebrow={`客观题库 · ${quiz.code}`} title={editing ? '编辑客观题' : quiz.title}
        description={QUIZ_SUBJECTS.find((subject) => subject.value === quiz.subject)?.label ?? quiz.subject}
        actions={<>
          <Link href="/quizzes" className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回客观题题库</Link>
          <RefreshButton onClick={refresh} loading={loading} />
          <button className={editing ? 'forge-btn-secondary' : 'forge-btn-primary'} onClick={() => setEditing((prev) => !prev)}>
            {editing ? <X className="h-4 w-4" /> : <Edit3 className="h-4 w-4" />}{editing ? '取消编辑' : '编辑'}
          </button>
          <button className="forge-btn-ghost text-danger-500" onClick={() => void handleDelete()} disabled={deleting}>
            <Trash2 className="h-4 w-4" />删除
          </button>
        </>}
      />
      {(saveError || deleteError) && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{saveError || deleteError}</div>}
      {editing ? <QuizForm quiz={quiz} onSubmit={handleSave} saving={saving} /> : <QuizReadingView quiz={quiz} />}
    </div>
  );
}
