export const OUTCOME_LABELS: Record<string, string> = { solved: '已完成', unsolved: '观察结束仍未完成', in_progress: '仍在尝试', stopped: '提前停止', not_attempted: '未尝试' };
export const STATE_LABELS: Record<string, string> = {
 queued: '等待执行', pending: '等待执行', running: '执行中', completed: '已完成', failed: '执行失败', cancelled: '已取消', cancelling: '正在取消', cancel_requested: '已请求取消',
 tested_candidates: '候选解通过当前测试', blocked: '存在阻断问题',
 unverified: '未核验', pending_review: '待复核', uncertain: '待确认', invalid: '发现有效性问题', valid: '当前证据未发现无效',
 candidate: '候选', verified: '已核验', passed: '通过当前测试', passed_current_tests: '通过当前测试', failed_tests: '测试失败', not_run: '未执行', needs_review: '需要复核', equivalent_dependency: '实质依赖等价知识', insufficient_evidence: '证据不足',
 blind_solve: '独立盲解', blind_solve_a: '独立盲解 A', blind_solve_b: '独立盲解 B', analysis: '解法与 KC 分析', challenge: '定向挑战', validation: '执行验证', synthesis: '证据汇总', configuration: '检查配置',
};
export const stateLabel = (state: string) => STATE_LABELS[state] ?? state;
export const helpLabel = (value: string) => ({ hint: '提示', editorial: '题解', tags: '标签', ai: 'AI', discussion: '讨论' } as Record<string, string>)[value] ?? value;
export function groupLabel(key: string): string {
 return key.split('|').map(part => {
  if (part === 'practice') return '练习';
  if (part === 'contest') return '比赛';
  if (part === 'first') return '首次接触';
  if (part === 'repeat') return '复习';
  if (part === 'independent') return '无帮助';
  if (part === 'assisted') return '受助';
  if (part === 'unknown') return '能力未知';
  if (part.startsWith('T=')) return '窗口 ' + part.slice(2) + ' 分钟';
  if (part.startsWith('self_report_')) return '自报 CF ' + part.slice(12);
  return part;
 }).join(' · ');
}
