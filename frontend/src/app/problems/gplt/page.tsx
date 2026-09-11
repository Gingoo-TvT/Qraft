'use client';

import { useCallback, useState } from 'react';
import { useRouter } from 'next/navigation';
import {
  ArrowLeft,
  Loader2,
  ShieldCheck,
  Sparkles,
} from 'lucide-react';
import Link from 'next/link';

import { FormSection, PageHeader } from '@/components/ui/Workspace';
import ApiHealthBanner from '@/components/ApiHealthBanner';
import { useGenerateGPLTBatch } from '@/hooks/useProblems';
import type {
  GPLTBatchParams,
  LLMRuntimeConfig,
  ProblemProviderConfig,
} from '@/lib/types';

// ---------------------------------------------------------------------------
// GPLT One-Click Page — 团体程序设计天梯赛 一键出题
// ---------------------------------------------------------------------------

const TIER_INFO = [
  {
    tier: 'L1',
    count: 8,
    scoreBreakdown: '5/5/10/10/15/15/20/20',
    total: 100,
    desc: '基础编程（循环/数组/字符串/模拟）',
  },
  {
    tier: 'L2',
    count: 4,
    scoreBreakdown: '25×4',
    total: 100,
    desc: '特定数据结构或算法的应用（并非代码量更大，而是考点更明确）',
  },
  {
    tier: 'L3',
    count: 3,
    scoreBreakdown: '30×3',
    total: 90,
    desc: '综合难题（复杂 DP/图论进阶/数据结构综合）',
  },
] as const;

function runtimeConfig(
  model: string,
  apiKeyInput: string,
): LLMRuntimeConfig | undefined {
  const trimmedModel = model.trim();
  const trimmedKeyInput = apiKeyInput.trim();
  if (!trimmedModel && !trimmedKeyInput) {
    return undefined;
  }
  const config: LLMRuntimeConfig = {
    model: trimmedModel || undefined,
  };
  if (trimmedKeyInput.startsWith('env:') || trimmedKeyInput.startsWith('runtime:')) {
    config.api_key_ref = trimmedKeyInput;
  } else if (trimmedKeyInput) {
    config.api_key = trimmedKeyInput;
  }
  return config;
}

export default function GPLTBatchPage() {
  const router = useRouter();
  const { generate, loading, error } = useGenerateGPLTBatch();

  const [formState, setFormState] = useState<GPLTBatchParams>({
    time_limit: 1000,
    memory_limit: 256,
    require_review: false,
    generate_editorial: true,
    languages: ['cpp'],
    locale: 'zh',
    similar_limit: 3,
    custom_prompt: '',
  });
  const [runtimeState, setRuntimeState] = useState({
    statement_model: '',
    statement_api_key: '',
    verification_model: '',
    verification_api_key: '',
  });

  const handleSubmit = useCallback(async () => {
    const statementConfig = runtimeConfig(
      runtimeState.statement_model,
      runtimeState.statement_api_key,
    );
    const verificationConfig = runtimeConfig(
      runtimeState.verification_model,
      runtimeState.verification_api_key,
    );
    const providerConfig: ProblemProviderConfig | undefined =
      statementConfig || verificationConfig
        ? {
            statement: statementConfig,
            verification: verificationConfig,
          }
        : undefined;

    const result = await generate({
      ...formState,
      require_review: false,
      provider_config: providerConfig,
    });
    if (result) {
      router.push(`/workflows/${result.workflow_id}`);
    }
  }, [formState, runtimeState, generate, router]);

  const totalProblems = TIER_INFO.reduce((sum, tier) => sum + tier.count, 0);
  const totalScore = TIER_INFO.reduce((sum, tier) => sum + tier.total, 0);
  return (
    <div className="af-page">
      <PageHeader eyebrow="创作 / 天梯赛" title="创建天梯赛套题" description="按内置 L1、L2、L3 编排生成整套题目，统一设置题目背景、运行限制与解法。"
        actions={<Link href="/problem-sets/new?format=programming" className="forge-btn-secondary">自定义比赛编排</Link>}>
        <Link href="/problems" className="af-link inline-flex items-center gap-1"><ArrowLeft className="h-4 w-4" />返回编程题题库</Link>
      </PageHeader>
      <ApiHealthBanner />
      {error && <div role="alert" className="rounded-lg border border-danger-400/30 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">{error}</div>}
      <form className="af-form-layout" onSubmit={(event) => { event.preventDefault(); if (!loading) void handleSubmit(); }}>
        <div className="af-form-main">
          <FormSection number="01" title="整套出题意图" description="附加要求会应用到每道题，建议描述适用选手、背景风格与需要避免的内容。">
            <label className="af-field"><span>附加说明（可选）</span><textarea className="forge-input min-h-36 resize-y" value={formState.custom_prompt ?? ''} onChange={(event) => setFormState((current) => ({ ...current, custom_prompt: event.target.value }))} placeholder="例如：面向第一次参加团体赛的校队，背景贴近校园生活，L1 强调细致实现，后续题目逐步增加思考深度。" /></label>
            <div className="af-table-panel overflow-x-auto"><table className="forge-table"><thead><tr><th>阶段</th><th>题数 / 分值</th><th>考查方向</th></tr></thead><tbody>{TIER_INFO.map((tier) => <tr key={tier.tier}><th scope="row" className="px-4 py-4 text-left text-sm font-semibold">{tier.tier}</th><td className="whitespace-nowrap">{tier.count} 道 · {tier.total} 分<p className="af-hint mt-1">{tier.scoreBreakdown}</p></td><td className="text-sm">{tier.desc}</td></tr>)}</tbody></table></div>
          </FormSection>
          <FormSection number="02" title="运行限制与解法" description="为整套题目设置默认值，保留题解生成与每题相似检测。">
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>默认时间限制（ms）</span><input className="forge-input" type="number" min={100} step={100} value={formState.time_limit} onChange={(event) => setFormState((current) => ({ ...current, time_limit: Number(event.target.value) }))} /></label>
              <label className="af-field"><span>默认内存限制（MB）</span><input className="forge-input" type="number" min={32} step={32} value={formState.memory_limit} onChange={(event) => setFormState((current) => ({ ...current, memory_limit: Number(event.target.value) }))} /></label>
              <label className="af-field"><span>解题代码语言</span><select className="forge-input" value={formState.languages[0] ?? 'cpp'} onChange={(event) => setFormState((current) => ({ ...current, languages: [event.target.value] }))}><option value="cpp">C++</option><option value="python3">Python 3</option><option value="java">Java</option></select></label>
              <label className="af-field"><span>每题近邻数量上限</span><input className="forge-input" type="number" min={0} max={20} value={formState.similar_limit ?? 0} onChange={(event) => setFormState((current) => ({ ...current, similar_limit: Math.max(0, Math.min(20, Number(event.target.value) || 0)) }))} /><span className="af-hint">范围 0–20，填 0 关闭相似检测。</span></label>
            </div>
            <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={formState.generate_editorial} onChange={(event) => setFormState((current) => ({ ...current, generate_editorial: event.target.checked }))} />为每道题生成题解</label>
          </FormSection>
          <details className="af-form-details">
            <summary><strong>本次模型覆盖</strong><span className="af-hint">题面生成与验算模型、API Key</span></summary>
            <div className="mb-5 flex flex-wrap items-center justify-between gap-3"><p className="af-hint">全部留空时使用已保存的默认模型；这里只影响本次生成。</p><Link className="af-link text-sm" href="/settings">管理模型配置</Link></div>
            <div className="grid gap-6 sm:grid-cols-2">{(['statement', 'verification'] as const).map((role) => <div key={role} className="space-y-3"><h3 className="flex items-center gap-2 text-sm font-semibold"><ShieldCheck className="h-4 w-4 text-[var(--da)]" />{role === 'statement' ? '题面生成' : '验算'}</h3>{(['model', 'api_key'] as const).map((field) => { const name = `${role}_${field}` as const; return <label key={field} className="af-field"><span>{field === 'model' ? '模型' : 'API Key'}</span><input className="forge-input" type={field === 'api_key' ? 'password' : 'text'} autoComplete={field === 'api_key' ? 'off' : undefined} value={runtimeState[name]} onChange={(event) => setRuntimeState((current) => ({ ...current, [name]: event.target.value }))} placeholder={field === 'model' ? '默认模型' : role === 'statement' ? 'sk-... 或 env:ALGOFORGE_STATEMENT_LLM_KEY' : 'sk-... 或 env:ALGOFORGE_REVIEW_LLM_KEY'} /></label>; })}</div>)}</div>
          </details>
        </div>
        <aside className="af-form-rail"><div className="af-summary">
          <div><p className="af-hint">本次套题</p><h2 className="mt-2 text-lg font-semibold">天梯赛 · L1 / L2 / L3</h2></div>
          <div className="grid grid-cols-2 gap-4 border-y border-[var(--dl)] py-5"><div><strong className="text-3xl font-semibold tabular-nums">{totalProblems}</strong><p className="af-hint mt-1">道题目</p></div><div><strong className="text-3xl font-semibold tabular-nums">{totalScore}</strong><p className="af-hint mt-1">总分</p></div></div>
          <dl className="space-y-3 text-sm"><div className="af-summary-row"><dt>默认限制</dt><dd>{formState.time_limit} ms / {formState.memory_limit} MB</dd></div><div className="af-summary-row"><dt>解法语言</dt><dd>{formState.languages[0] === 'cpp' ? 'C++' : formState.languages[0] === 'python3' ? 'Python 3' : formState.languages[0] === 'java' ? 'Java' : formState.languages[0]}</dd></div><div className="af-summary-row"><dt>题解</dt><dd>{formState.generate_editorial ? '生成' : '不生成'}</dd></div><div className="af-summary-row"><dt>题面模型</dt><dd>{runtimeState.statement_model || '默认模型'}</dd></div><div className="af-summary-row"><dt>验算模型</dt><dd>{runtimeState.verification_model || '默认模型'}</dd></div></dl>
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={(formState.similar_limit ?? 0) > 0} onChange={() => setFormState((current) => ({ ...current, similar_limit: (current.similar_limit ?? 0) > 0 ? 0 : 3 }))} />开启每题相似检测</label>
          <div className="af-sticky-actions"><button type="submit" className="forge-btn-primary w-full" disabled={loading}>{loading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Sparkles className="h-4 w-4" />}{loading ? '正在启动工作流…' : `生成 ${totalProblems} 道天梯赛题目`}</button><Link className="forge-btn-secondary w-full" href="/problems">返回题库</Link></div>
          <p className="af-hint">启动后进入任务详情。生成在后台继续，耗时取决于模型响应与实际校验结果。</p>
        </div></aside>
      </form>
    </div>
  );
}
