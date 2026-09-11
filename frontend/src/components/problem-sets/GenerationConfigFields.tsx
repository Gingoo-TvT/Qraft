'use client';

import type { ProblemSetGenerationConfig, QuizType } from '@/lib/types';
import { SET_QUESTION_TYPES, setGenerationTotal } from '@/lib/problem-set-generation';

export default function GenerationConfigFields({ value, onChange, assembly = false, showRequirements = true }: {
  assembly?: boolean;
  showRequirements?: boolean;
  value: ProblemSetGenerationConfig;
  onChange: (value: ProblemSetGenerationConfig) => void;
}) {
  const total = setGenerationTotal(value);
  function changeMode(mode: ProblemSetGenerationConfig['mode']) {
    if (mode === value.mode) return;
    if (mode === 'programming') {
      onChange({ ...value, mode, distribution: [{ type: 'programming', count: total || 10, score: value.distribution.find((q) => q.type === 'programming')?.score ?? 100 }] });
    } else {
      const count = total || 10;
      const each = Math.floor(count / 4);
      onChange({ ...value, mode, distribution: SET_QUESTION_TYPES.map(({ type, score }, i) => ({ type, score, count: each + (i < count % 4 ? 1 : 0) })) });
    }
  }
  function changeQuota(type: QuizType, field: 'count' | 'score', number: number) {
    const distribution = [...value.distribution];
    const index = distribution.findIndex((q) => q.type === type);
    if (index >= 0) distribution[index] = { ...distribution[index], [field]: number };
    else distribution.push({ type, count: 0, score: SET_QUESTION_TYPES.find((t) => t.type === type)?.score ?? 0, [field]: number });
    onChange({ ...value, distribution });
  }
  const types = value.mode === 'programming' ? SET_QUESTION_TYPES.slice(0, 1) : SET_QUESTION_TYPES;
  return (
    <div className="space-y-5">
      <div className="af-choice-grid" role="group" aria-label="题集编排模式">
        <label className="af-choice-card" data-selected={value.mode === 'programming'}>
          <input type="radio" name="generation-mode" checked={value.mode === 'programming'} onChange={() => changeMode('programming')} />
          <span><strong className="block text-sm font-semibold">纯编程比赛</strong><span className="af-hint mt-1 block">所有题目使用编程题工作流，适合比赛与算法训练。</span></span>
        </label>
        <label className="af-choice-card" data-selected={value.mode === 'mixed'}>
          <input type="radio" name="generation-mode" checked={value.mode === 'mixed'} onChange={() => changeMode('mixed')} />
          <span><strong className="block text-sm font-semibold">混合题型题集</strong><span className="af-hint mt-1 block">自由搭配编程、选择、填空与判断题，适合考试与课程。</span></span>
        </label>
      </div>
      <div className="overflow-x-auto">
        <table className="af-quota-table w-full">
          <thead><tr><th className="text-left">题型</th><th className="text-left">题数</th><th className="text-left">每题分值</th><th className="text-right">小计</th></tr></thead>
          <tbody>{types.map(({ type, label, score }) => {
            const quota = value.distribution.find((q) => q.type === type);
            return <tr key={type}>
              <th scope="row" className="text-left text-sm font-medium">{label}</th>
              <td><input aria-label={label + '题数'} className="forge-input min-w-20" type="number" min={0} max={1000} step={1} value={quota?.count ?? 0} onChange={(event) => changeQuota(type, 'count', Number(event.target.value))} /></td>
              <td><input aria-label={label + '分值'} className="forge-input min-w-20" type="number" min={0} max={10000} step={1} value={quota?.score ?? score} onChange={(event) => changeQuota(type, 'score', Number(event.target.value))} /></td>
              <td className="whitespace-nowrap text-right text-sm tabular-nums">{(quota?.count ?? 0) * (quota?.score ?? score)} 分</td>
            </tr>;
          })}</tbody>
        </table>
      </div>
      <p className="af-hint">共 {total} 题 · 预期总分 {value.distribution.reduce((sum, q) => sum + q.count * q.score, 0)} 分。{assembly ? '按配额从题库选题，填 0 的题型不选。' : '按上述数量精确生成，填 0 的题型不生成。'}</p>
      {!assembly && showRequirements && <label className="af-field">
        <span>整套需求</span>
        <textarea className="forge-input min-h-32" maxLength={12000} value={value.requirements} onChange={(event) => onChange({ ...value, requirements: event.target.value })} placeholder={value.mode === 'mixed' ? '说明考试目标、适用学生、知识范围，以及每类题型应考查的能力。' : '说明比赛时长、目标选手、难度梯度与希望考查的算法思维。'} />
        <span className="af-hint">先统筹覆盖与梯度，再分题生成、独立校验；编程题保留完整解法和测试数据校验。</span>
      </label>}
    </div>
  );
}
