// ============================================================================
// Qraft - Shared Constants
// ============================================================================

// ---------------------------------------------------------------------------
// Difficulty
// ---------------------------------------------------------------------------

export const DIFFICULTY_MIN = 800;
export const ALGORITHM_DIFFICULTY_MIN = 1300;
export const DIFFICULTY_MAX = 3500;
export const DIFFICULTY_STEP = 100;
export const SYNTAX_DIFFICULTY_MAX = 1200;

export const DIFFICULTY_LABELS: Record<number, string> = {
  800: '入门',
  1200: '基础',
  1600: '中等',
  2100: '提高',
  2600: '省选',
  3500: '专家',
};

/**
 * Returns the difficulty label for a given rating by finding the closest
 * threshold that does not exceed the rating.
 */
export function getDifficultyLabel(difficulty: number): string {
  const thresholds = Object.keys(DIFFICULTY_LABELS)
    .map(Number)
    .sort((a, b) => a - b);

  let label = DIFFICULTY_LABELS[thresholds[0]];
  for (const t of thresholds) {
    if (difficulty >= t) {
      label = DIFFICULTY_LABELS[t];
    } else {
      break;
    }
  }
  return label;
}

// ---------------------------------------------------------------------------
// Workflow Steps
// ---------------------------------------------------------------------------

export const WORKFLOW_STEPS = [
  {
    name: 'similarity_check',
    description: '相似题目检测',
  },
  {
    name: 'generate_statement',
    description: '生成题目描述',
  },
  {
    name: 'post_statement_similarity',
    description: '题面相似度精筛',
  },
  {
    name: 'generate_solution',
    description: '生成标准解法',
  },
  {
    name: 'compile_check',
    description: '编译检查',
  },
  {
    name: 'generate_testdata',
    description: '生成测试数据',
  },
  {
    name: 'run_sandbox',
    description: '沙箱执行',
  },
  {
    name: 'validate',
    description: '验证输出',
  },
  {
    name: 'assess_feasibility',
    description: '可行性评估',
  },
  {
    name: 'llm_review',
    description: 'LLM 审核',
  },
  {
    name: 'human_review',
    description: '人工审核',
  },
  {
    name: 'store',
    description: '存储题目',
  },
] as const;

export type WorkflowStepName = (typeof WORKFLOW_STEPS)[number]['name'];

// ---------------------------------------------------------------------------
// Contest Styles
// ---------------------------------------------------------------------------

export const CONTEST_STYLES = [
  { value: 'icpc', label: 'ICPC' },
  { value: 'ioi', label: 'IOI' },
  { value: 'codeforces', label: 'Codeforces' },
  { value: 'leetcode', label: 'LeetCode' },
  { value: 'noip', label: 'NOIP / CSP' },
  { value: 'custom', label: '自定义' },
] as const;

export type ContestStyle = (typeof CONTEST_STYLES)[number]['value'];

// ---------------------------------------------------------------------------
// Languages
// ---------------------------------------------------------------------------

export const LANGUAGES = [
  { value: 'cpp', label: 'C++', extension: '.cpp' },
  { value: 'c', label: 'C', extension: '.c' },
  { value: 'java', label: 'Java', extension: '.java' },
  { value: 'python', label: 'Python', extension: '.py' },
  { value: 'rust', label: 'Rust', extension: '.rs' },
  { value: 'go', label: 'Go', extension: '.go' },
] as const;

export type Language = (typeof LANGUAGES)[number]['value'];

// ---------------------------------------------------------------------------
// Pagination Defaults
// ---------------------------------------------------------------------------

export const DEFAULT_PAGE_SIZE = 20;
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100] as const;

// ---------------------------------------------------------------------------
// Status Labels (Chinese)
// ---------------------------------------------------------------------------

export const STATUS_LABELS: Record<string, string> = {
  draft: '草稿',
  generating: '生成中',
  review: '待审核',
  published: '已发布',
  rejected: '已拒绝',
  quarantined: '已隔离',
  pending: '等待中',
  running: '运行中',
  waiting_review: '待审核',
  rejected_quarantined: '已拒绝并隔离',
  approved: '已通过',
  failed: '失败',
  cancelled: '已取消',
};

export const PROBLEM_LEVEL_LABELS: Record<string, string> = {
  syntax: '语法',
  algorithm: '算法',
  gplt_l1: '天梯 L1',
  gplt_l2: '天梯 L2',
  gplt_l3: '天梯 L3',
};

// ---------------------------------------------------------------------------
// Quiz Labels
// ---------------------------------------------------------------------------

export const QUIZ_TYPE_LABELS: Record<string, string> = {
  programming: '编程题',
  choice: '选择题',
  fill_blank: '填空题',
  judge: '判断题',
};

export const QUIZ_DIFFICULTY_LABELS: Record<string, string> = {
  easy: '简单',
  medium: '中等',
  hard: '困难',
};

export const QUIZ_VISIBILITY_LABELS: Record<string, string> = {
  public: '公开',
  private: '私有',
};

export const QUIZ_SUBJECTS = [
  { value: 'c_language', label: 'C 语言程序设计' },
  { value: 'data_structure_algorithm', label: '数据结构与算法' },
] as const;

export const QUIZ_ON_CONFLICT = [
  { value: 'error', label: '冲突时报错' },
  { value: 'skip', label: '冲突时跳过' },
  { value: 'update', label: '冲突时更新' },
] as const;

export const QUIZ_GENERATE_COUNT_MAX = 20;
