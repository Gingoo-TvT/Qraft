import type { FeedbackInput } from '@/lib/rating-types';
export const emptyFeedback = (): FeedbackInput => ({
 outcome: 'in_progress', seen_before: false, assistance: [], independent_minutes: 0, elapsed_minutes: 0,
 observed_full_window: false, first_route: '', final_route: '', blockers: '', code: '', result_source: 'self_report', result_url: '', notes: '',
});
export function validateFeedback(value: FeedbackInput, windowMinutes: number): string {
 if (!['solved', 'unsolved', 'in_progress', 'not_attempted', 'stopped'].includes(value.outcome)) return '请选择作答结果。';
 if (!Number.isInteger(value.elapsed_minutes) || value.elapsed_minutes < 0 || value.elapsed_minutes > 10080 || !Number.isInteger(value.independent_minutes) || value.independent_minutes < 0) return '有效耗时与独立尝试时间须为非负整数分钟。';
 if (value.independent_minutes > value.elapsed_minutes) return '独立尝试时间不能超过总有效耗时。';
 if (value.outcome === 'not_attempted' && (value.elapsed_minutes !== 0 || value.independent_minutes !== 0)) return '未尝试时请将耗时填写为 0；提前停止请选择“停止尝试”。';
 if (value.outcome === 'solved' && value.elapsed_minutes <= 0) return '完成后请填写有效耗时。';
 if (value.observed_full_window && value.elapsed_minutes < windowMinutes) return '观察不足约定窗口，不能标记已完整观察。';
 if (value.assistance.length && (value.assistance_after_minutes === undefined || !Number.isInteger(value.assistance_after_minutes) || value.assistance_after_minutes < 0 || value.assistance_after_minutes > value.elapsed_minutes)) return '请填写首次获得提示或帮助的时间，不能超过总有效耗时。';
 if (value.assistance.length && value.independent_minutes > (value.assistance_after_minutes ?? 0)) return '独立尝试时间不能超过首次获得帮助的时间。';
 if (value.result_source === 'external_link' && !/^https?:\/\//.test(value.result_url ?? '')) return '请填写完整的结果链接（https:// 或 http://）。';
 if (value.subjective_rating !== undefined && (!Number.isInteger(value.subjective_rating) || value.subjective_rating < 800 || value.subjective_rating > 3500 || value.subjective_rating % 100 !== 0)) return '主观 CF 参考分数须在 800–3500 之间，以 100 分为一档。';
 if (value.cf_rating !== undefined && (!Number.isInteger(value.cf_rating) || value.cf_rating < 0 || value.cf_rating > 5000)) return '自报 CF 水平须在 0–5000 之间。';
 if (value.cf_rating !== undefined && !value.cf_rating_at) return '填写自报 CF 水平时，请一并填写记录日期。';
 if (value.cf_rating_at && (!Number.isFinite(Date.parse(value.cf_rating_at)) || Date.parse(value.cf_rating_at) > Date.now() + 24 * 60 * 60 * 1000)) return '请核对 CF 水平的记录日期。';
 const bytes = (text: string) => new TextEncoder().encode(text).length;
 if ([value.first_route, value.final_route, value.blockers, value.notes].some(text => bytes(text) > 8000)) return '某项说明过长，请精简后重试。';
 if (bytes(value.code ?? '') > 128 * 1024) return '代码过长，请限制在 128 KiB 以内。';
 return '';
}
export function serializeFeedback(value: FeedbackInput): FeedbackInput {
 // Whitelist fields; a review task must never echo hidden ratings or admin IDs.
 return {
  outcome: value.outcome, seen_before: value.seen_before, assistance: [...value.assistance],
  ...(value.assistance.length ? { assistance_after_minutes: value.assistance_after_minutes } : {}),
  independent_minutes: value.independent_minutes, elapsed_minutes: value.elapsed_minutes, observed_full_window: value.observed_full_window,
  first_route: value.first_route.trim(), final_route: value.final_route.trim(), blockers: value.blockers.trim(),
  ...(value.subjective_rating === undefined ? {} : { subjective_rating: value.subjective_rating }),
  ...(value.cf_rating === undefined ? {} : { cf_rating: value.cf_rating, ...(value.cf_rating_at ? { cf_rating_at: value.cf_rating_at } : {}) }),
  code: value.code?.trim(), result_source: value.result_source, ...(value.result_source === 'external_link' ? { result_url: value.result_url?.trim() } : {}),
  notes: value.notes.trim(),
 };
}
