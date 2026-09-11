import type { ProblemSet, ProblemSetGenerationConfig, QuizType } from './types';

export const SET_QUESTION_TYPES: { type: QuizType; label: string; score: number }[] = [
  { type: 'programming', label: '编程题', score: 100 },
  { type: 'choice', label: '选择题', score: 2 },
  { type: 'fill_blank', label: '填空题', score: 5 },
  { type: 'judge', label: '判断题', score: 2 },
];

export function defaultSetGenerationConfig(total = 10): ProblemSetGenerationConfig {
  return { mode: 'programming', requirements: '', distribution: [{ type: 'programming', count: total, score: 100 }] };
}

export function setGenerationTotal(config: ProblemSetGenerationConfig): number {
  return config.distribution.reduce((sum, quota) => sum + quota.count, 0);
}

export function validateSetGenerationConfig(config: ProblemSetGenerationConfig): string {
  const total = setGenerationTotal(config);
  if (!Number.isInteger(total) || total < 1 || total > 1000) return '总题数必须在 1-1000 之间。';
  if (config.distribution.some((q) => !Number.isInteger(q.count) || q.count < 0 || q.count > 1000 || !Number.isInteger(q.score) || q.score < 0 || q.score > 10000)) {
    return '题数和分值请填写非负整数；每题分值最多 10000。';
  }
  if (config.requirements.length > 12000) return '整体需求请控制在 12000 字以内。';
  return '';
}

export function generationConfigForSet(set: ProblemSet): ProblemSetGenerationConfig {
  if (set.generation_config) return set.generation_config;
  const total = Math.max(set.desired_item_count || set.min_item_count || 10, set.items?.length || 0);
  const distribution = SET_QUESTION_TYPES.map(({ type, score }) => ({
    type, score, count: (set.items || []).filter((item) => (item.problem_id ? 'programming' : item.quiz?.type) === type).length,
  }));
  distribution[0].count += Math.max(0, total - distribution.reduce((sum, q) => sum + q.count, 0));
  const mixed = distribution.some((q) => q.type !== 'programming' && q.count > 0);
  return { mode: mixed ? 'mixed' : 'programming', requirements: '', distribution: mixed ? distribution : distribution.slice(0, 1) };
}

export function isSetGenerationActive(status?: string): boolean {
  return status === 'queued' || status === 'planning' || status === 'generating';
}

export function setGenerationStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    queued: '等待启动', planning: '规划整套题目', generating: '生成与校验中', completed: '全部完成',
    partial: '部分完成', cancelled: '已停止', failed: '生成失败',
    pending: '待生成', ready: '已规划', running: '生成与校验中', generated: '正在入集', succeeded: '已完成',
  };
  return labels[status] || status;
}
