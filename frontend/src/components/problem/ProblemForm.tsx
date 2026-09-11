'use client';

import { useState, useCallback } from 'react';
import { useRouter } from 'next/navigation';
import {
  BookOpen,
  Code2,
  Loader2,
  Sparkles,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { generateProblem } from '@/lib/api';
import {
  CONTEST_STYLES,
  LANGUAGES,
  ALGORITHM_DIFFICULTY_MIN,
  DIFFICULTY_MIN,
  DIFFICULTY_MAX,
  DIFFICULTY_STEP,
  SYNTAX_DIFFICULTY_MAX,
  getDifficultyLabel,
} from '@/lib/constants';
import { difficultyColor } from '@/lib/utils';
import type {
  ProblemLevel,
  ProblemGenParams,
  TestDataConfig as TDConfig,
} from '@/lib/types';
import TagSelector from './TagSelector';
import TestDataConfig from './TestDataConfig';

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

const DEFAULT_TEST_DATA_CONFIG: TDConfig = {
  groups: [],
  custom_cases: [],
  adaptive_count: true,
  min_count: 10,
  max_count: 20,
  total_count: 0,
  sample_count: 2,
  time_limit: 2000,
  memory_limit: 256,
  checker_type: 'exact',
};

// ---------------------------------------------------------------------------
// ProblemForm
// ---------------------------------------------------------------------------

export default function ProblemForm() {
  const router = useRouter();

  // ---- Form state ----
  const [level, setLevel] = useState<ProblemLevel>('algorithm');
  const [difficulty, setDifficulty] = useState(1600);
  const [tags, setTags] = useState<string[]>([]);
  const [contestStyle, setContestStyle] = useState('codeforces');
  const [language, setLanguage] = useState('cpp');
  const [timeLimit, setTimeLimit] = useState(2000);
  const [memoryLimit, setMemoryLimit] = useState(256);
  const [customRequirements, setCustomRequirements] = useState('');
  const [generateEditorial, setGenerateEditorial] = useState(true);
  const [locale, setLocale] = useState('zh');
  const [testDataConfig, setTestDataConfig] = useState<TDConfig>(
    DEFAULT_TEST_DATA_CONFIG,
  );

  // ---- Submission state ----
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // ---- Derived ----
  const effectiveMin =
    level === 'syntax' ? DIFFICULTY_MIN : ALGORITHM_DIFFICULTY_MIN;
  const effectiveMax =
    level === 'syntax' ? SYNTAX_DIFFICULTY_MAX : DIFFICULTY_MAX;
  const clampedDifficulty = Math.min(difficulty, effectiveMax);

  // ---- Level change handler ----
  function handleLevelChange(newLevel: ProblemLevel) {
    setLevel(newLevel);
    if (newLevel === 'syntax' && difficulty > SYNTAX_DIFFICULTY_MAX) {
      setDifficulty(SYNTAX_DIFFICULTY_MAX);
    }
    if (newLevel === 'algorithm' && difficulty < ALGORITHM_DIFFICULTY_MIN) {
      setDifficulty(ALGORITHM_DIFFICULTY_MIN);
    }
    // Clear tags when switching levels since tag pools are different
    setTags([]);
  }

  // ---- Validation ----
  function validate(): string | null {
    if (tags.length === 0) return '请至少选择一个标签';
    if (timeLimit < 100 || timeLimit > 30000)
      return '时间限制必须在 100 到 30000 ms 之间';
    if (memoryLimit < 16 || memoryLimit > 2048)
      return '内存限制必须在 16 到 2048 MB 之间';
    return null;
  }

  // ---- Submit ----
  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();

      const validationError = validate();
      if (validationError) {
        setError(validationError);
        return;
      }

      setSubmitting(true);
      setError(null);

      const params: ProblemGenParams = {
        level,
        difficulty: clampedDifficulty,
        tags,
        contest_style: contestStyle,
        time_limit: timeLimit,
        memory_limit: memoryLimit,
        test_data_config: {
          num_test_cases: 0,
          min_test_cases: 10,
          max_test_cases: 20,
          num_samples: testDataConfig.sample_count ?? 2,
          groups: testDataConfig.groups,
          custom_cases: testDataConfig.custom_cases,
        },
        require_review: false,
        generate_editorial: generateEditorial,
        custom_prompt: customRequirements || undefined,
        languages: [language],
        locale,
      };

      try {
        const res = await generateProblem(params);
        const data = res.data as unknown as { workflow_id?: string; run_id?: string };
        if (data?.workflow_id) {
          router.push(`/workflows/${data.workflow_id}`);
        }
      } catch (err) {
        setError(
          err instanceof Error ? err.message : 'Failed to generate problem.',
        );
      } finally {
        setSubmitting(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [
      level,
      clampedDifficulty,
      tags,
      contestStyle,
      language,
      timeLimit,
      memoryLimit,
      customRequirements,
      generateEditorial,
      testDataConfig,
      locale,
      router,
    ],
  );

  // ---- Input class helper ----
  const inputCls =
    'h-9 w-full rounded-lg border border-anvil-200 bg-white px-3 text-sm text-anvil-900 placeholder-anvil-400 transition-colors focus:border-forge-400 focus:outline-none focus:ring-2 focus:ring-forge-200 dark:border-anvil-700 dark:bg-anvil-900 dark:text-anvil-100 dark:placeholder-anvil-500 dark:focus:border-forge-600 dark:focus:ring-forge-900';

  return (
    <form onSubmit={handleSubmit} className="space-y-8">
      {/* ========== Level selector ========== */}
      <section>
        <h3 className="mb-3 text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          题目类型
        </h3>
        <div className="grid grid-cols-2 gap-4">
          {(
            [
              {
                value: 'syntax' as ProblemLevel,
                label: '语法题',
                desc: '基础语法和简单逻辑',
                Icon: BookOpen,
              },
              {
                value: 'algorithm' as ProblemLevel,
                label: '算法题',
                desc: '数据结构与算法思维',
                Icon: Code2,
              },
            ] as const
          ).map(({ value, label, desc, Icon }) => (
            <button
              key={value}
              type="button"
              onClick={() => handleLevelChange(value)}
              className={cn(
                'flex flex-col items-center gap-2 rounded-xl border-2 px-6 py-5 text-center transition-all',
                level === value
                  ? 'border-forge-500 bg-forge-50 shadow-sm dark:border-forge-400 dark:bg-forge-950/30'
                  : 'border-anvil-200 bg-white hover:border-anvil-300 dark:border-anvil-700 dark:bg-anvil-900 dark:hover:border-anvil-600',
              )}
            >
              <Icon
                className={cn(
                  'h-8 w-8',
                  level === value
                    ? 'text-forge-600 dark:text-forge-400'
                    : 'text-anvil-400 dark:text-anvil-500',
                )}
              />
              <span
                className={cn(
                  'text-sm font-semibold',
                  level === value
                    ? 'text-forge-700 dark:text-forge-300'
                    : 'text-anvil-700 dark:text-anvil-300',
                )}
              >
                {label}
              </span>
              <span className="text-xs text-anvil-500 dark:text-anvil-400">
                {desc}
              </span>
            </button>
          ))}
        </div>
      </section>

      {/* ========== Difficulty slider ========== */}
      <section>
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-semibold text-anvil-800 dark:text-anvil-200">
            难度
          </h3>
          <span
            className={cn(
              'text-sm font-bold',
              difficultyColor(clampedDifficulty),
            )}
          >
            {clampedDifficulty} &mdash; {getDifficultyLabel(clampedDifficulty)}
          </span>
        </div>
        <input
          type="range"
          min={effectiveMin}
          max={effectiveMax}
          step={DIFFICULTY_STEP}
          value={clampedDifficulty}
          onChange={(e) => setDifficulty(Number(e.target.value))}
          className="h-2 w-full cursor-pointer appearance-none rounded-full bg-anvil-200 accent-forge-600 dark:bg-anvil-700"
        />
        <div className="mt-1 flex justify-between text-xs text-anvil-400">
          <span>{effectiveMin}</span>
          <span>{effectiveMax}</span>
        </div>
      </section>

      {/* ========== Tags ========== */}
      <section>
        <h3 className="mb-2 text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          标签
        </h3>
        <TagSelector level={level} difficulty={clampedDifficulty} selectedTags={tags} onChange={setTags} />
      </section>

      {/* ========== Contest style & Language ========== */}
      <div className="grid grid-cols-2 gap-4">
        <section>
          <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
            比赛风格
          </label>
          <select
            value={contestStyle}
            onChange={(e) => setContestStyle(e.target.value)}
            className={inputCls}
          >
            {CONTEST_STYLES.map((cs) => (
              <option key={cs.value} value={cs.value}>
                {cs.label}
              </option>
            ))}
          </select>
        </section>

        <section>
          <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
            解法语言
          </label>
          <select
            value={language}
            onChange={(e) => setLanguage(e.target.value)}
            className={inputCls}
          >
            {LANGUAGES.map((lang) => (
              <option key={lang.value} value={lang.value}>
                {lang.label}
              </option>
            ))}
          </select>
        </section>
      </div>

      {/* ========== Limits ========== */}
      <div className="grid grid-cols-2 gap-4">
        <section>
          <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
            时间限制 (ms)
          </label>
          <input
            type="number"
            min={100}
            max={30000}
            step={100}
            value={timeLimit}
            onChange={(e) => setTimeLimit(Number(e.target.value))}
            className={inputCls}
          />
        </section>

        <section>
          <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
            内存限制 (MB)
          </label>
          <input
            type="number"
            min={16}
            max={2048}
            step={16}
            value={memoryLimit}
            onChange={(e) => setMemoryLimit(Number(e.target.value))}
            className={inputCls}
          />
        </section>
      </div>

      {/* ========== Custom requirements ========== */}
      <section>
        <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          自定义要求
        </label>
        <textarea
          rows={4}
          value={customRequirements}
          onChange={(e) => setCustomRequirements(e.target.value)}
          placeholder="描述对生成题目的特殊要求..."
          className="w-full rounded-lg border border-anvil-200 bg-white px-3 py-2 text-sm text-anvil-900 placeholder-anvil-400 transition-colors focus:border-forge-400 focus:outline-none focus:ring-2 focus:ring-forge-200 dark:border-anvil-700 dark:bg-anvil-900 dark:text-anvil-100 dark:placeholder-anvil-500 dark:focus:border-forge-600 dark:focus:ring-forge-900"
        />
      </section>

      {/* ========== Generate editorial ========== */}
      <label className="flex cursor-pointer items-center gap-3">
        <div
          className={cn(
            'relative h-5 w-9 rounded-full transition-colors',
            generateEditorial ? 'bg-forge-600' : 'bg-anvil-300 dark:bg-anvil-600',
          )}
        >
          <span
            className={cn(
              'absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white transition-transform shadow-sm',
              generateEditorial && 'translate-x-4',
            )}
          />
        </div>
        <input
          type="checkbox"
          checked={generateEditorial}
          onChange={(e) => setGenerateEditorial(e.target.checked)}
          className="sr-only"
        />
        <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
          生成题解
        </span>
      </label>

      {/* ========== Locale selector ========== */}
      <section>
        <label className="mb-1 block text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          题目语言
        </label>
        <select
          value={locale}
          onChange={(e) => setLocale(e.target.value)}
          className={inputCls}
        >
          <option value="zh">中文</option>
          <option value="en">English</option>
        </select>
      </section>

      {/* ========== Test data configuration ========== */}
      <section>
        <h3 className="mb-3 text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          测试数据配置
        </h3>
        <div className="rounded-xl border border-anvil-200 bg-anvil-50/30 p-4 dark:border-anvil-700 dark:bg-anvil-800/30">
          <TestDataConfig config={testDataConfig} onChange={setTestDataConfig} />
        </div>
      </section>

      {/* ========== Error ========== */}
      {error && (
        <div className="rounded-lg border border-danger-400 bg-danger-50 px-4 py-3 text-sm text-danger-600 dark:border-danger-500/30 dark:bg-danger-500/10 dark:text-danger-400">
          {error}
        </div>
      )}

      {/* ========== Submit ========== */}
      <button
        type="submit"
        disabled={submitting}
        className={cn(
          'inline-flex w-full items-center justify-center gap-2 rounded-xl px-6 py-3 text-sm font-semibold text-white shadow-sm transition-all',
          submitting
            ? 'cursor-not-allowed bg-forge-400 dark:bg-forge-700'
            : 'bg-forge-600 hover:bg-forge-700 active:bg-forge-800 dark:bg-forge-500 dark:hover:bg-forge-600',
        )}
      >
        {submitting ? (
          <>
            <Loader2 className="h-4 w-4 animate-spin" />
            正在生成...
          </>
        ) : (
          <>
            <Sparkles className="h-4 w-4" />
            开始生成
          </>
        )}
      </button>
    </form>
  );
}
