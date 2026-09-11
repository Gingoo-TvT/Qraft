'use client';

import Link from 'next/link';
import { FormEvent, useState } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Loader2, Shuffle, Save } from 'lucide-react';
import { EmptyState, FormSection, PageHeader } from '@/components/ui/Workspace';
import GenerationConfigFields from '@/components/problem-sets/GenerationConfigFields';
import { assembleProblemSet, previewProblemSetAssembly } from '@/lib/api';
import { defaultSetGenerationConfig, SET_QUESTION_TYPES, setGenerationTotal, validateSetGenerationConfig } from '@/lib/problem-set-generation';
import type { ProblemSetAssemblyFilter, ProblemSetAssemblyPreview, ProblemSetKind, QuizDifficulty } from '@/lib/types';

const difficultyNames: Record<string, string> = { easy: '简单', medium: '中等', hard: '困难' };
const typeName = (type: string) => SET_QUESTION_TYPES.find((item) => item.type === type)?.label ?? type;

export default function AssembleProblemSetPage() {
  const router = useRouter();
  const [title, setTitle] = useState('');
  const [kind, setKind] = useState<ProblemSetKind>('contest');
  const [description, setDescription] = useState('');
  const [config, setConfig] = useState(defaultSetGenerationConfig);
  const [tags, setTags] = useState('');
  const [keyword, setKeyword] = useState('');
  const [minDifficulty, setMinDifficulty] = useState(800);
  const [maxDifficulty, setMaxDifficulty] = useState(3500);
  const [quizDifficulty, setQuizDifficulty] = useState<'' | QuizDifficulty>('');
  const [recent, setRecent] = useState(2);
  const [preview, setPreview] = useState<ProblemSetAssemblyPreview | null>(null);
  const [busy, setBusy] = useState<'preview' | 'save' | null>(null);
  const [error, setError] = useState('');

  async function previewAssembly(event?: FormEvent) {
    event?.preventDefault();
    const configError = validateSetGenerationConfig(config);
    if (configError) { setError(configError); return; }
    const filter: ProblemSetAssemblyFilter = {
      tags: tags.split(/[,，\n]/).map((tag) => tag.trim()).filter(Boolean), keyword,
      min_difficulty: minDifficulty, max_difficulty: maxDifficulty, quiz_difficulty: quizDifficulty,
      exclude_recent_sets: recent, seed: crypto.randomUUID(),
    };
    setBusy('preview'); setError(''); setPreview(null);
    try {
      const response = await previewProblemSetAssembly({ config, filter });
      if (!response.data) throw new Error('服务没有返回选题结果。');
      setPreview(response.data);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '选题失败，请重试。'); }
    finally { setBusy(null); }
  }

  async function save() {
    if (!preview?.items.length) return;
    if (!title.trim()) { setError('请填写试卷或题集名称。'); return; }
    setBusy('save'); setError('');
    try {
      const response = await assembleProblemSet({
        title: title.trim(), kind, description: description.trim(), config, filter: preview.filter,
        items: preview.items.map(({ id, type, updated_at }) => ({ id, type, updated_at })),
      });
      if (!response.data) throw new Error('服务没有返回保存结果。');
      router.push('/problem-sets/' + response.data.id);
    } catch (cause) { setError(cause instanceof Error ? cause.message : '保存失败，请重试。'); }
    finally { setBusy(null); }
  }

  const hasProgramming = config.distribution.some((q) => q.type === 'programming' && q.count > 0);
  const hasQuiz = config.distribution.some((q) => q.type !== 'programming' && q.count > 0);

  return (
    <div className="af-page">
      <PageHeader eyebrow="创作 / 题库组卷" title="从题库组成一套题" description="左侧设置题型与选题条件，右侧预览实际结果；确认后保存为试卷、作业或比赛。"
        actions={<Link href="/problem-sets/new" className="forge-btn-secondary">需要新题？自动生成题集</Link>}>
        <Link href="/problem-sets" className="af-link inline-flex items-center gap-1"><ArrowLeft className="h-4 w-4" />返回题集</Link>
      </PageHeader>
      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{error}</div>}
      <div className="af-form-layout af-assembly-layout">
        <form className="af-form-main" onSubmit={(event) => void previewAssembly(event)}>
          <fieldset disabled={busy !== null} className="min-w-0 space-y-6 disabled:opacity-60">
            <FormSection number="01" title="试卷信息" description="先确定用途，名称也可以在预览选题后补充。">
              <label className="af-field"><span>试卷或题集名称 *</span><input className="forge-input" maxLength={255} value={title} onChange={(event) => setTitle(event.target.value)} placeholder="例如：数据结构期末模拟卷" /></label>
              <div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>用途</span><select className="forge-input" value={kind} onChange={(event) => setKind(event.target.value as ProblemSetKind)}><option value="contest">比赛</option><option value="mock_exam">模拟考试</option><option value="homework">作业</option><option value="curriculum">课程练习</option></select></label><label className="af-field"><span>说明（可选）</span><input className="forge-input" value={description} onChange={(event) => setDescription(event.target.value)} placeholder="考试时长、适用对象等" /></label></div>
            </FormSection>
            <div className="space-y-6" onChange={() => setPreview(null)}>
              <FormSection number="02" title="题型与配比" description="按配额选题，系统优先覆盖不同知识点，并在每种题型内按难度递增排列。">
                <GenerationConfigFields assembly value={config} onChange={(value) => { setConfig(value); setPreview(null); }} />
              </FormSection>
              <FormSection number="03" title="选题范围" description="留空表示不限；条件变化后需要重新预览。">
                <label className="af-field"><span>标签 / 知识点</span><input className="forge-input" value={tags} onChange={(event) => setTags(event.target.value)} placeholder="图论，动态规划；留空不限" /><span className="af-hint">多个标签用逗号分隔，命中任意一个即可；客观题也匹配知识点名称。</span></label>
                <label className="af-field"><span>题目关键词</span><input className="forge-input" maxLength={200} value={keyword} onChange={(event) => setKeyword(event.target.value)} placeholder="匹配标题或题面；留空不限" /></label>
                {hasProgramming && <div className="grid gap-4 sm:grid-cols-2"><label className="af-field"><span>编程题难度下限</span><input className="forge-input" type="number" min={800} max={3500} step={100} value={minDifficulty} onChange={(event) => setMinDifficulty(Number(event.target.value))} /></label><label className="af-field"><span>编程题难度上限</span><input className="forge-input" type="number" min={800} max={3500} step={100} value={maxDifficulty} onChange={(event) => setMaxDifficulty(Number(event.target.value))} /></label></div>}
                {hasQuiz && <label className="af-field"><span>客观题难度</span><select className="forge-input" value={quizDifficulty} onChange={(event) => setQuizDifficulty(event.target.value as '' | QuizDifficulty)}><option value="">不限</option><option value="easy">简单</option><option value="medium">中等</option><option value="hard">困难</option></select></label>}
                <details className="af-form-details"><summary><strong>历史复用设置</strong><span className="af-hint">当前避开最近 {recent} 套题集</span></summary><label className="af-field"><span>避开最近几套题集的题</span><input className="forge-input" type="number" min={0} max={50} step={1} value={recent} onChange={(event) => setRecent(Number(event.target.value))} /><span className="af-hint">填 0 允许复用历史题目。同一套内去除重复题目和相同题面；编程题仅从已发布且未隔离的题目中选择。</span></label></details>
              </FormSection>
            </div>
          </fieldset>
          <div className="af-panel flex flex-wrap items-center justify-between gap-4 p-5"><div><p className="text-sm font-medium">目标 {setGenerationTotal(config)} 题 · {config.distribution.reduce((sum, item) => sum + item.count * item.score, 0)} 分</p><p className="af-hint mt-1">预览不会创建题集，也不会调用模型生成新题。</p></div><button type="submit" className="forge-btn-primary" disabled={busy !== null}>{busy === 'preview' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Shuffle className="h-4 w-4" />}{preview ? '重新预览' : '预览组卷'}</button></div>
        </form>
        <aside className="af-form-rail">
          <section className="af-summary" aria-label="组卷预览">
            <div className="flex items-center justify-between gap-3"><div><p className="af-hint">选题结果</p><h2 className="mt-1 text-lg font-semibold">{preview ? `已选 ${preview.items.length} 题 · ${preview.total_score} 分` : '等待选题预览'}</h2></div>{preview && <button type="button" className="forge-btn-secondary" onClick={() => void previewAssembly()} disabled={busy !== null}><Shuffle className="h-4 w-4" />换一批</button>}</div>
            {busy === 'preview' ? <div className="flex min-h-64 flex-col items-center justify-center gap-3 text-sm text-[var(--dm)]"><Loader2 className="h-6 w-6 animate-spin" />正在从题库匹配题目…</div> : !preview ? <EmptyState icon={Shuffle} title="配置好后，先看实际选题" description="这里只展示按条件匹配到的真实题目。预览不会保存，确认结果后再创建题集。" action={<button type="button" className="forge-btn-secondary" onClick={() => void previewAssembly()} disabled={busy !== null}>预览组卷</button>} /> : <>
              <div className="grid grid-cols-2 gap-3">{preview.distribution.map((quota) => <div key={quota.type} className="rounded-lg border border-[var(--dl)] bg-[var(--ds)] p-3"><p className="text-sm font-medium">{typeName(quota.type)}</p><p className="af-hint mt-1">目标 {quota.requested} · 可选 {quota.available} · 已选 {quota.selected}</p>{quota.missing > 0 && <p className="mt-1 text-xs text-warning-600 dark:text-warning-400">还缺 {quota.missing} 道</p>}</div>)}</div>
              {preview.missing_count > 0 && <div role="status" className="rounded-lg bg-warning-50 p-4 text-sm text-warning-700 dark:bg-warning-500/10 dark:text-warning-300">符合条件的题目还缺 {preview.missing_count} 道。可以调整筛选，或先保存已有结果再补题。</div>}
              {preview.items.length === 0 ? <EmptyState title="当前条件没有匹配题目" description="可以放宽筛选条件、减少回看题集数，或先向题库添加题目。" /> : <div className="af-table-panel max-h-[32rem] overflow-auto"><table className="forge-table"><thead><tr><th>题目</th><th>题型 / 难度</th><th>分值</th></tr></thead><tbody>{preview.items.map((item, index) => <tr key={item.type + item.id}><td><Link className="af-link font-medium" href={(item.type === 'programming' ? '/problems/' : '/quizzes/') + item.id} target="_blank" rel="noreferrer">{index + 1}. {item.title}</Link><p className="af-hint mt-1">{item.code}</p><p className="af-hint">{(item.tags ?? []).slice(0, 6).join(' / ')}</p></td><td className="whitespace-nowrap">{typeName(item.type)}<p className="af-hint">{item.type === 'programming' ? item.difficulty : difficultyNames[item.quiz_difficulty ?? '']}</p></td><td>{item.score}</td></tr>)}</tbody></table></div>}
            </>}
            <div className="af-sticky-actions"><button type="button" className="forge-btn-primary w-full" onClick={() => void save()} disabled={busy !== null || !preview?.items.length}>{busy === 'save' ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}{preview && preview.missing_count > 0 ? '保存已有结果' : '保存组卷'}</button><p className="af-hint">保存本次预览的题目。之后仍可增删、调整顺序并导出；换一批可能包含相同题目。</p></div>
          </section>
        </aside>
      </div>
    </div>
  );
}
