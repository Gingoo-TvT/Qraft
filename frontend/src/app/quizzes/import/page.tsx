'use client';

import { useState } from 'react';
import Link from 'next/link';
import { ArrowLeft, FileSpreadsheet, Loader2, Upload } from 'lucide-react';

import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import ImportReportPanel from '@/components/quiz/ImportReportPanel';
import { useImportQuizzes } from '@/hooks/useQuiz';
import { quizTemplateURL } from '@/lib/api';
import { QUIZ_ON_CONFLICT, QUIZ_SUBJECTS } from '@/lib/constants';
import type { OnConflict, QuizSubject } from '@/lib/types';
import { useAppStore } from '@/stores/appStore';

export default function ImportQuizzesPage() {
  const { selectedSubject, setSubject } = useAppStore();
  const { importQ, loading, error, report, reset } = useImportQuizzes();
  const [subject, setLocalSubject] = useState<QuizSubject>(selectedSubject);
  const [onConflict, setOnConflict] = useState<OnConflict>('error');
  const [file, setFile] = useState<File | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  function handleSubjectChange(next: QuizSubject) {
    setLocalSubject(next);
    setSubject(next);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    reset();
    setLocalError(null);

    if (!file) {
      setLocalError('请选择 .xlsx 文件');
      return;
    }
    if (!file.name.toLowerCase().endsWith('.xlsx')) {
      setLocalError('只支持 .xlsx 文件');
      return;
    }
    if (!subject) {
      setLocalError('请选择学科');
      return;
    }

    await importQ(file, subject, onConflict);
  }

  return (
    <div className="af-page">
      <PageHeader eyebrow="客观题库" title="Excel 导入" description="选择学科和导入策略，将已有题目加入客观题库。"
        actions={<Link href="/quizzes" className="forge-btn-secondary"><ArrowLeft className="h-4 w-4" />返回客观题题库</Link>}
      />

      <form className="af-form-layout" onSubmit={handleSubmit}>
        <div className="af-form-main">
          {(localError || error) && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{localError || error}</div>}

          <section className="af-panel space-y-5 p-6">
            <SectionHeading eyebrow="01" title="导入设置" description="指定题目所属学科，以及遇到重复题目时的处理方式。" />
            <div className="space-y-2">
              <span className="block text-sm font-medium">学科</span>
              <div className="af-segmented" role="group" aria-label="导入题目的学科">
                {QUIZ_SUBJECTS.map((item) => <button key={item.value} type="button"
                  aria-pressed={subject === item.value} className={subject === item.value ? 'is-active' : ''}
                  onClick={() => handleSubjectChange(item.value as QuizSubject)}>{item.label}</button>)}
              </div>
            </div>
            <label className="block max-w-lg space-y-2">
              <span className="block text-sm font-medium">冲突策略</span>
              <select className="forge-input" value={onConflict} onChange={(event) => setOnConflict(event.target.value as OnConflict)}>
                {QUIZ_ON_CONFLICT.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </label>
          </section>

          <section className="af-panel space-y-5 p-6">
            <SectionHeading eyebrow="02" title="选择 Excel 文件" description="仅支持 .xlsx 文件。上传后查看成功数量与失败明细。" actions={<a className="forge-btn-secondary" href={quizTemplateURL()}>下载空白模板</a>} />
            <div className="rounded-lg border border-dashed border-[var(--dl)] bg-[var(--ds)] p-5">
              <div className="mb-4 flex items-start gap-3">
                <FileSpreadsheet className="mt-0.5 h-6 w-6 shrink-0 text-[var(--da)]" />
                <div className="min-w-0">
                  <p className="break-all text-sm font-medium">{file?.name ?? '选择要导入的题库文件'}</p>
                  <p className="mt-1 text-xs text-[var(--dm)]">{file ? `${(file.size / 1024).toFixed(1)} KB · 准备上传` : '使用符合导入模板的 Excel 工作簿。'}</p>
                </div>
              </div>
              <label className="block space-y-2">
                <span className="sr-only">Excel 文件</span>
                <input className="forge-input" type="file" accept=".xlsx" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
              </label>
            </div>
          </section>
        </div>

        <aside className="af-form-rail">
          <section className="af-summary">
            <SectionHeading title="本次导入" />
            <div className="af-summary-row"><span>学科</span><strong>{QUIZ_SUBJECTS.find((item) => item.value === subject)?.label ?? subject}</strong></div>
            <div className="af-summary-row"><span>冲突策略</span><strong>{QUIZ_ON_CONFLICT.find((item) => item.value === onConflict)?.label ?? onConflict}</strong></div>
            <div className="af-summary-row"><span>文件</span><strong>{file ? '已选择' : '未选择'}</strong></div>
            <p className="my-4 text-xs leading-6 text-[var(--dm)]">上传后由后端解析 Excel，不在浏览器中处理模板内容。</p>
            <button className="forge-btn-primary w-full" type="submit" disabled={loading || !file}>
              {loading ? <><Loader2 className="h-4 w-4 animate-spin" />上传中...</> : <><Upload className="h-4 w-4" />上传导入</>}
            </button>
          </section>
        </aside>
      </form>

      {report && <ImportReportPanel report={report} subject={subject} />}
    </div>
  );
}
