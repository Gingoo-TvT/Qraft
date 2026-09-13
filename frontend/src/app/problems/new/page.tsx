'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  ArrowLeft,
  Loader2,
  RotateCcw,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';
import Link from 'next/link';

import { FormSection, PageHeader } from '@/components/ui/Workspace';
import ApiHealthBanner from '@/components/ApiHealthBanner';
import { useGenerateProblem } from '@/hooks/useProblems';
import { useTags } from '@/hooks/useProblems';
import { useAppStore } from '@/stores/appStore';
import type {
  LLMRuntimeConfig,
  ProblemGenParams,
  ProblemLevel,
  ProblemProviderConfig,
} from '@/lib/types';
import { cn } from '@/lib/utils';
import {
  CONTEST_STYLES,
  ALGORITHM_DIFFICULTY_MIN,
  DIFFICULTY_MAX,
  DIFFICULTY_MIN,
  DIFFICULTY_STEP,
  getDifficultyLabel,
  LANGUAGES,
  SYNTAX_DIFFICULTY_MAX,
} from '@/lib/constants';

// ---------------------------------------------------------------------------
// New Problem Page
// ---------------------------------------------------------------------------

function runtimeConfig(
  model: string,
  apiKeyInput: string,
  baseUrl: string,
  provider: string,
): LLMRuntimeConfig | undefined {
  const trimmedModel = model.trim();
  const trimmedKeyInput = apiKeyInput.trim();
  const trimmedBaseUrl = baseUrl.trim();
  const trimmedProvider = provider.trim();
  if (!trimmedModel && !trimmedKeyInput && !trimmedBaseUrl && !trimmedProvider) {
    return undefined;
  }
  const config: LLMRuntimeConfig = {
    model: trimmedModel || undefined,
    base_url: trimmedBaseUrl || undefined,
    provider: trimmedProvider || (trimmedBaseUrl ? 'anthropic-compatible' : undefined),
  };
  if (trimmedKeyInput.startsWith('env:') || trimmedKeyInput.startsWith('runtime:')) {
    config.api_key_ref = trimmedKeyInput;
  } else if (trimmedKeyInput) {
    config.api_key = trimmedKeyInput;
  }
  return config;
}

export default function NewProblemPage() {
  const router = useRouter();
  const { generate, loading, error, reset } = useGenerateProblem();
  const {
    tags: tagCategories,
    loading: tagsLoading,
    error: tagsError,
    refetch: refetchTags,
  } = useTags();
  const { selectedLevel, setLevel, testDataConfig } = useAppStore();

  const initialFormState = useMemo(
    () => ({
      level: selectedLevel as ProblemLevel,
      difficulty: 1200,
      tags: [] as string[],
      contest_style: 'codeforces',
      language: 'cpp',
      custom_prompt: '',
      similar_limit: 3,
      locale: 'zh',
      statement_model: '',
      statement_api_key: '',
      statement_base_url: '',
      statement_provider: '',
      verification_model: '',
      verification_api_key: '',
      verification_base_url: '',
      verification_provider: '',
    }),
    [selectedLevel],
  );
  const [formState, setFormState] = useState(initialFormState);

  useEffect(() => {
    const brief = new URLSearchParams(window.location.search).get('brief');
    if (brief) setFormState((current) => ({ ...current, custom_prompt: brief }));
  }, []);

  const handleLevelChange = useCallback(
    (level: ProblemLevel) => {
      setLevel(level);
      setFormState((prev) => ({
        ...prev,
        level,
        // Clamp difficulty for syntax level
        difficulty:
          level === 'syntax'
            ? Math.min(prev.difficulty, SYNTAX_DIFFICULTY_MAX)
            : Math.max(prev.difficulty, ALGORITHM_DIFFICULTY_MIN),
        // Clear tags when switching levels
        tags: [],
      }));
    },
    [setLevel],
  );

  const handleFieldChange = useCallback(
    (key: string, value: unknown) => {
      setFormState((prev) => ({ ...prev, [key]: value }));
    },
    [],
  );

  const handleTagToggle = useCallback((tagName: string) => {
    setFormState((prev) => ({
      ...prev,
      tags: prev.tags.includes(tagName)
        ? prev.tags.filter((t) => t !== tagName)
        : [...prev.tags, tagName],
    }));
  }, []);

  const handleResetForm = useCallback(() => {
    reset();
    setFormState({
      ...initialFormState,
      level: selectedLevel as ProblemLevel,
    });
  }, [initialFormState, reset, selectedLevel]);

  const handleSubmit = useCallback(async () => {
    reset();
    const statementConfig = runtimeConfig(
      formState.statement_model,
      formState.statement_api_key,
      formState.statement_base_url,
      formState.statement_provider,
    );
    const verificationConfig = runtimeConfig(
      formState.verification_model,
      formState.verification_api_key,
      formState.verification_base_url,
      formState.verification_provider,
    );
    const providerConfig: ProblemProviderConfig | undefined =
      statementConfig || verificationConfig
        ? {
            statement: statementConfig,
            verification: verificationConfig,
          }
        : undefined;
    const params: ProblemGenParams = {
      level: formState.level,
      difficulty: formState.difficulty,
      tags: formState.tags,
      contest_style: formState.contest_style,
      time_limit: testDataConfig.time_limit || 2000,
      memory_limit: testDataConfig.memory_limit || 256,
      test_data_config: {
        num_test_cases: 0,
        min_test_cases: 10,
        max_test_cases: 20,
        num_samples: testDataConfig.sample_count ?? 2,
        groups: testDataConfig.groups,
        custom_cases: testDataConfig.custom_cases,
      },
      require_review: false,
      generate_editorial: true,
      custom_prompt: formState.custom_prompt || undefined,
      languages: [formState.language],
      similar_limit: formState.similar_limit,
      locale: formState.locale,
      provider_config: providerConfig,
    };
    const result = await generate(params);
    if (result?.workflow_id) {
      router.push(`/workflows/${result.workflow_id}`);
    }
  }, [formState, testDataConfig, generate, reset, router]);

  const minDifficulty =
    formState.level === 'syntax' ? DIFFICULTY_MIN : ALGORITHM_DIFFICULTY_MIN;
  const maxDifficulty =
    formState.level === 'syntax' ? SYNTAX_DIFFICULTY_MAX : DIFFICULTY_MAX;

  const levelTags = tagCategories.filter(
    (cat) => cat.level === formState.level,
  );
  const submitDisabled = loading || tagsLoading || Boolean(tagsError);

  return (
    <div className="af-page">
      <PageHeader eyebrow="创作 / 编程题" title="创作一道好题" description="先表达你希望考查的思考，再确定知识范围与生成约束。"
        actions={<Link className="forge-btn-secondary" href="/problem-sets/new?format=programming">创建整场比赛</Link>}>
        <Link href="/problems" className="af-link inline-flex items-center gap-1"><ArrowLeft className="h-4 w-4" />返回编程题题库</Link>
      </PageHeader>
      <ApiHealthBanner />
      {(error || tagsError) && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{error ?? `标签加载失败：${tagsError}`}</div>}
      <form className="af-form-layout" onSubmit={(event) => { event.preventDefault(); if (!submitDisabled) void handleSubmit(); }}>
        <div className="af-form-main">
          <FormSection number="01" title="出题意图" description="描述目标选手、希望出现的观察或建模过程，以及需要避免的套路。">
            <label className="af-field"><span>具体要求</span><textarea className="forge-input min-h-40 resize-y" placeholder="例如：面向掌握最短路的学生，设计一道需要先发现状态关系再建图的题目。题面保持简洁，避免直接暴露算法名称。" value={formState.custom_prompt ?? ''} onChange={(event) => handleFieldChange('custom_prompt', event.target.value)} /></label>
            <div className="af-choice-grid" role="group" aria-label="题目类型">
              <button type="button" className="af-choice-card" aria-pressed={formState.level === 'syntax'} onClick={() => handleLevelChange('syntax')}><span><strong className="block text-sm font-semibold">语法与基础逻辑</strong><span className="af-hint mt-1 block">基本语法、实现与简单观察 · {DIFFICULTY_MIN}–{SYNTAX_DIFFICULTY_MAX}</span></span></button>
              <button type="button" className="af-choice-card" aria-pressed={formState.level === 'algorithm'} onClick={() => handleLevelChange('algorithm')}><span><strong className="block text-sm font-semibold">算法与建模</strong><span className="af-hint mt-1 block">数据结构、算法思想与综合推理 · {ALGORITHM_DIFFICULTY_MIN}–{DIFFICULTY_MAX}</span></span></button>
            </div>
          </FormSection>
          <FormSection number="02" title="知识范围与难度" description="标签限定知识范围，难度表达预期强度；两者一起参与题目生成。">
            <div className="space-y-3"><div className="flex items-center justify-between gap-4"><label htmlFor="problem-difficulty" className="text-sm font-medium">目标难度</label><span className="text-lg font-semibold tabular-nums">{formState.difficulty}<span className="ml-2 text-xs font-normal text-[var(--dm)]">{getDifficultyLabel(formState.difficulty)}</span></span></div><input id="problem-difficulty" aria-label="目标难度" type="range" min={minDifficulty} max={maxDifficulty} step={DIFFICULTY_STEP} value={formState.difficulty} onChange={(event) => handleFieldChange('difficulty', Number(event.target.value))} className="w-full accent-[var(--da)]" /><div className="flex justify-between text-xs text-[var(--dm)]"><span>{minDifficulty}</span><span>{maxDifficulty}</span></div></div>
            <div className="border-t border-[var(--dl)] pt-5"><div className="mb-3 flex items-center justify-between gap-3"><h3 className="text-sm font-semibold">覆盖标签</h3><span className="af-hint">已选 {formState.tags.length} 项</span></div>
              {tagsLoading ? <div className="flex items-center gap-2 text-sm text-[var(--dm)]"><Loader2 className="h-4 w-4 animate-spin" />正在加载标签</div> : levelTags.length === 0 ? <div className="af-hint"><p>{tagsError ? '标签尚未加载，连接服务后可重试；已填写的需求会保留。' : '暂无可用标签。'}</p>{tagsError && <button type="button" className="forge-btn-secondary mt-3" onClick={refetchTags}>重新加载标签</button>}</div> : <div className="flex flex-wrap gap-2">{levelTags.map((tag) => <button key={tag.id} type="button" aria-pressed={formState.tags.includes(tag.tag_name)} className={cn('rounded-md border px-3 py-2 text-left text-sm transition-colors', formState.tags.includes(tag.tag_name) ? 'border-[var(--da)] bg-[var(--dg)] text-[var(--da)]' : 'border-[var(--dl)] bg-[var(--dp)] text-[var(--dt)] hover:bg-[var(--dh)]')} onClick={() => handleTagToggle(tag.tag_name)}>{tag.display_name}</button>)}</div>}
            </div>
          </FormSection>
          <FormSection number="03" title="表达与解法" description="保持题面风格、题目语言与参考解法的一致性。">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>比赛风格</span><select className="forge-input" value={formState.contest_style ?? ''} onChange={(event) => handleFieldChange('contest_style', event.target.value || undefined)}>{CONTEST_STYLES.map((style) => <option key={style.value} value={style.value}>{style.label}</option>)}</select></label>
              <label className="af-field"><span>解法语言</span><select className="forge-input" value={formState.language} onChange={(event) => handleFieldChange('language', event.target.value)}>{LANGUAGES.map((language) => <option key={language.value} value={language.value}>{language.label}</option>)}</select></label>
              <label className="af-field"><span>题目语言</span><select className="forge-input" value={formState.locale} onChange={(event) => handleFieldChange('locale', event.target.value)}><option value="zh">中文</option><option value="en">English</option></select></label>
            </div>
            <div className="flex flex-wrap items-start justify-between gap-3 rounded-lg border border-[var(--dl)] bg-[var(--ds)] p-4"><div><h3 className="text-sm font-medium">测试数据配置</h3><p className="af-hint mt-1">{testDataConfig.time_limit || 2000} ms · {testDataConfig.memory_limit || 256} MB · {testDataConfig.sample_count ?? 2} 个样例<br />生成 10–20 个测试点，保留已配置的测试组与自定义样例。</p></div><Link className="af-link text-sm" href="/testdata-config">编辑测试配置</Link></div>
          </FormSection>
          <details className="af-form-details">
            <summary><strong>模型覆盖与去重设置</strong><span className="af-hint">使用已保存配置；需要时仅覆盖本次调用</span></summary>
            <div className="mb-5 flex flex-wrap items-center justify-between gap-3"><p className="af-hint">全部留空时使用默认模型。下面的值不会改写永久配置。</p><Link className="af-link text-sm" href="/settings">管理模型配置</Link></div>
            <div className="grid gap-6 sm:grid-cols-2">{(['statement', 'verification'] as const).map((role) => <div key={role} className="space-y-3"><h3 className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="h-4 w-4 text-[var(--da)]" />{role === 'statement' ? '题面生成' : '独立验算'}</h3>{(['model', 'base_url', 'provider', 'api_key'] as const).map((field) => {
              const name = `${role}_${field}` as const;
              const label = { model: '模型', base_url: 'Base URL', provider: 'Provider', api_key: 'API Key' }[field];
              return <label className="af-field" key={field}><span>{label}</span><input className="forge-input" type={field === 'api_key' ? 'password' : 'text'} inputMode={field === 'base_url' ? 'url' : undefined} autoComplete={field === 'api_key' ? 'off' : undefined} value={formState[name]} placeholder={field === 'model' ? '默认模型' : field === 'base_url' ? 'https://api.example.com' : field === 'provider' ? 'anthropic-compatible' : role === 'statement' ? 'sk-... 或 env:ALGOFORGE_STATEMENT_LLM_KEY' : 'sk-... 或 env:ALGOFORGE_REVIEW_LLM_KEY'} onChange={(event) => handleFieldChange(name, event.target.value)} /></label>;
            })}</div>)}</div>
            <div className="mt-5 border-t border-[var(--dl)] pt-5"><label className="af-field"><span>向量去重：近邻数量上限</span><input className="forge-input max-w-48" type="number" min={0} max={20} value={formState.similar_limit} onChange={(event) => handleFieldChange('similar_limit', Math.max(0, Math.min(20, Number(event.target.value) || 0)))} /><span className="af-hint">填 0 关闭本次相似检测；范围 0–20。</span></label></div>
          </details>
        </div>
        <aside className="af-form-rail"><div className="af-summary">
          <div><p className="af-hint">本次创作</p><h2 className="mt-2 text-lg font-semibold">{formState.level === 'syntax' ? '语法题' : '算法题'}</h2></div>
          <div className="border-y border-[var(--dl)] py-5"><strong className="text-4xl font-semibold tabular-nums">{formState.difficulty}</strong><p className="af-hint mt-1">{getDifficultyLabel(formState.difficulty)} · 1 道编程题</p></div>
          <dl className="space-y-3 text-sm"><div className="af-summary-row"><dt>风格</dt><dd>{CONTEST_STYLES.find((style) => style.value === formState.contest_style)?.label ?? '未设置'}</dd></div><div className="af-summary-row"><dt>解法语言</dt><dd>{LANGUAGES.find((language) => language.value === formState.language)?.label ?? formState.language}</dd></div><div className="af-summary-row"><dt>标签</dt><dd className="break-words">{formState.tags.length > 0 ? formState.tags.join('、') : '未选择'}</dd></div><div className="af-summary-row"><dt>题面模型</dt><dd className="break-words">{formState.statement_model || '默认模型'}</dd></div><div className="af-summary-row"><dt>验算模型</dt><dd className="break-words">{formState.verification_model || '默认模型'}</dd></div></dl>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={formState.similar_limit > 0} onChange={(event) => handleFieldChange('similar_limit', event.target.checked ? 3 : 0)} />开启相似检测<span className="ml-auto text-xs text-[var(--dm)]">{formState.similar_limit > 0 ? `最多 ${formState.similar_limit} 个近邻` : '已关闭'}</span></label>
          <div className="af-sticky-actions"><button type="submit" className="forge-btn-primary w-full" disabled={submitDisabled}>{loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}{loading ? '正在提交…' : formState.similar_limit > 0 ? '生成并去重' : '开始生成'}</button><button type="button" className="forge-btn-secondary w-full" onClick={handleResetForm} disabled={loading}><RotateCcw className="h-4 w-4" />重置表单</button></div>
          <p className="af-hint">生成包含题面、解法、题解与测试数据。提交后可在任务详情查看实际执行结果。</p>
        </div></aside>
      </form>
    </div>
  );
}
