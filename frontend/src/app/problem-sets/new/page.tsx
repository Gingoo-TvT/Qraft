'use client';

import Link from 'next/link';
import { FormEvent, useEffect, useState } from 'react';
import { ArrowLeft, Loader2, Sparkles } from 'lucide-react';
import { useRouter } from 'next/navigation';

import { FormSection, PageHeader } from '@/components/ui/Workspace';
import GenerationConfigFields from '@/components/problem-sets/GenerationConfigFields';
import { defaultSetGenerationConfig, SET_QUESTION_TYPES, setGenerationTotal, validateSetGenerationConfig } from '@/lib/problem-set-generation';
import { createProblemSet } from '@/lib/api';
import type { ProblemSetCreateRequest, ProblemSetKind } from '@/lib/types';

const DEFAULT_STYLE = '像天梯赛一样循序渐进，题面清晰、叙事克制，覆盖不同算法范式；最后保留 1-2 道真正需要综合思考的题。';
const DEFAULT_DIFFICULTY = '整体中等偏上，前段让基础选手有稳定得分点，后段达到区域赛银牌附近的思维强度，难度要有明显梯度。';

export default function NewProblemSetPage() {
  const router = useRouter();
  const [title, setTitle] = useState('');
  const [code, setCode] = useState('');
  const [kind, setKind] = useState<ProblemSetKind>('contest');
  const [subject, setSubject] = useState('数据结构与算法');
  const [style, setStyle] = useState(DEFAULT_STYLE);
  const [difficulty, setDifficulty] = useState(DEFAULT_DIFFICULTY);
  const [tags, setTags] = useState('');
  const [generationConfig, setGenerationConfig] = useState(defaultSetGenerationConfig);
  const [cooldown, setCooldown] = useState(2);
  const [autoGenerate, setAutoGenerate] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const query = new URLSearchParams(window.location.search);
    const brief = query.get('brief');
    const format = query.get('format');
    if (!brief && format !== 'mixed' && format !== 'programming') return;
    setGenerationConfig((current) => {
      const next = { ...current, requirements: brief ?? current.requirements };
      if (format === 'mixed') {
        const total = setGenerationTotal(current);
        next.mode = 'mixed';
        next.distribution = SET_QUESTION_TYPES.map(({ type, score }, index) => ({ type, score, count: Math.floor(total / 4) + (index < total % 4 ? 1 : 0) }));
      } else if (format === 'programming') {
        next.mode = 'programming';
        next.distribution = [{ type: 'programming', count: setGenerationTotal(current), score: current.distribution.find((item) => item.type === 'programming')?.score ?? 100 }];
      }
      return next;
    });
  }, []);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!title.trim()) {
      setError('请先填写题集名称。');
      return;
    }
    const configError = validateSetGenerationConfig(generationConfig);
    if (configError) {
      setError(configError);
      return;
    }
    setSaving(true);
    setError('');
    const payload: ProblemSetCreateRequest = {
      title: title.trim(),
      code: code.trim() || undefined,
      kind,
      subject: subject.trim(),
      style_prompt: style.trim(),
      difficulty_prompt: difficulty.trim(),
      tags: tags.split(/[,，\n]/).map((item) => item.trim()).filter(Boolean),
      desired_item_count: setGenerationTotal(generationConfig),
      generation_config: generationConfig,
      start_generation: autoGenerate,
      cooldown_sets: cooldown,
      generate_prompt: false,
    };
    try {
      const response = await createProblemSet(payload);
      if (!response.data) throw new Error('服务没有返回新题集。');
      router.push(`/problem-sets/${response.data.id}${response.data.generation_error ? '?generation_start_failed=1' : ''}`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '创建题集失败');
    } finally {
      setSaving(false);
    }
  }

  const totalScore = generationConfig.distribution.reduce((sum, quota) => sum + quota.count * quota.score, 0);
  return (
    <div className="af-page">
      <PageHeader eyebrow="创作 / 整套生成" title="创建题集" description="从一份需求开始，统一规划整套题目的知识覆盖、难度与题型。"
        actions={<Link href="/problem-sets/assemble" className="forge-btn-secondary">从已有题目组卷</Link>}>
        <Link href="/problem-sets" className="af-link inline-flex items-center gap-1"><ArrowLeft className="h-4 w-4" />返回题集</Link>
      </PageHeader>
      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{error}</div>}
      <form className="af-form-layout" onSubmit={(event) => void submit(event)}>
        <div className="af-form-main">
          <FormSection number="01" title="这套题要解决什么问题" description="先说明对象、目标与内容范围，再细化题目组成。">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>题集名称 *</span><input className="forge-input" value={title} onChange={(event) => setTitle(event.target.value)} placeholder="例如：数据结构期末综合练习" /></label>
              <label className="af-field"><span>题集类型</span><select className="forge-input" value={kind} onChange={(event) => setKind(event.target.value as ProblemSetKind)}><option value="contest">比赛</option><option value="mock_exam">模拟考试</option><option value="homework">作业</option><option value="curriculum">课程</option></select></label>
            </div>
            <label className="af-field"><span>整套需求</span><textarea className="forge-input min-h-40" maxLength={12000} value={generationConfig.requirements} onChange={(event) => setGenerationConfig((current) => ({ ...current, requirements: event.target.value }))} placeholder="例如：面向完成基础课程的学生，用两小时检验图论、动态规划和字符串知识；基础题保证稳定得分，综合题强调观察与建模。" /><span className="af-hint">可以写考试时长、适用水平、考查重点，以及题目之间需要满足的关系。</span></label>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>学科 / 方向</span><input className="forge-input" value={subject} onChange={(event) => setSubject(event.target.value)} /></label>
              <label className="af-field"><span>覆盖标签</span><input className="forge-input" value={tags} onChange={(event) => setTags(event.target.value)} placeholder="图论，动态规划，字符串" /><span className="af-hint">多个标签使用逗号分隔。</span></label>
            </div>
          </FormSection>
          <FormSection number="02" title="题型与配比" description="选择纯编程比赛，或为不同题型分别设置数量与分值。">
            <GenerationConfigFields value={generationConfig} onChange={setGenerationConfig} showRequirements={false} />
          </FormSection>
          <FormSection number="03" title="难度与表达" description="这些要求会用于整套规划，帮助题目形成一致的体验。">
            <label className="af-field"><span>难度描述</span><textarea className="forge-input min-h-28" value={difficulty} onChange={(event) => setDifficulty(event.target.value)} /></label>
            <label className="af-field"><span>风格描述</span><textarea className="forge-input min-h-28" value={style} onChange={(event) => setStyle(event.target.value)} /></label>
          </FormSection>
          <details className="af-form-details">
            <summary><strong>编号与去重设置</strong><span className="af-hint">自动编号、历史题集回看范围</span></summary>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>题集编号</span><input className="forge-input" value={code} onChange={(event) => setCode(event.target.value)} placeholder="留空自动生成" /></label>
              <label className="af-field"><span>去重台账回看场数</span><input className="forge-input" type="number" min={0} max={50} value={cooldown} onChange={(event) => setCooldown(Number(event.target.value))} /></label>
            </div>
          </details>
        </div>
        <aside className="af-form-rail">
          <div className="af-summary">
            <div><p className="af-hint">本次题集</p><h2 className="mt-2 break-words text-lg font-semibold">{title.trim() || '未命名题集'}</h2></div>
            <div className="grid grid-cols-2 gap-4 border-y border-[var(--dl)] py-5"><div><strong className="text-3xl font-semibold tabular-nums">{setGenerationTotal(generationConfig)}</strong><p className="af-hint mt-1">题目总数</p></div><div><strong className="text-3xl font-semibold tabular-nums">{totalScore}</strong><p className="af-hint mt-1">预期总分</p></div></div>
            <dl className="space-y-3 text-sm"><div className="af-summary-row"><dt>编排模式</dt><dd>{generationConfig.mode === 'mixed' ? '混合题型' : '纯编程比赛'}</dd></div>{generationConfig.distribution.filter((quota) => quota.count > 0).map((quota) => <div key={quota.type} className="af-summary-row"><dt>{SET_QUESTION_TYPES.find((type) => type.type === quota.type)?.label}</dt><dd>{quota.count} 题 × {quota.score} 分</dd></div>)}</dl>
            <label className="flex items-start gap-2 text-sm"><input className="mt-1" type="checkbox" checked={autoGenerate} onChange={(event) => setAutoGenerate(event.target.checked)} /><span>保存后自动生成整套题目<span className="af-hint mt-1 block">关闭后仅保存需求，可稍后开始。</span></span></label>
            <div className="af-sticky-actions"><button className="forge-btn-primary w-full" type="submit" disabled={saving}>{saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}{saving ? '保存中…' : autoGenerate ? '创建并开始生成' : '保存题集'}</button><Link href="/problem-sets" className="forge-btn-secondary w-full">取消</Link></div>
            <p className="af-hint">使用已保存的模型配置。开始后可离开页面，规划与生成进度会持续保存。</p>
            <Link href="/settings" className="af-link text-sm">查看模型配置</Link>
          </div>
        </aside>
      </form>
    </div>
  );
}
