'use client';

import { saveTextFile } from '@/lib/export-file';
import { useCallback, useState } from 'react';
import {
  Download,
  Loader2,
  RefreshCw,
  Save,
  Trash2,
  Upload,
} from 'lucide-react';

import Link from 'next/link';
import { FormSection, PageHeader } from '@/components/ui/Workspace';
import { useAppStore } from '@/stores/appStore';
import type {
  BoundaryConfig,
  ConstraintRange,
  CustomTestCase,
  TestDataConfig,
  TestGroup,
} from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Component imports (will be created separately)
// ---------------------------------------------------------------------------

import GroupEditor from '@/components/testdata/GroupEditor';
import ConstraintPanel from '@/components/testdata/ConstraintPanel';
import BoundaryToggle from '@/components/testdata/BoundaryToggle';
import CustomCaseEditor from '@/components/testdata/CustomCaseEditor';
import DataPreview from '@/components/testdata/DataPreview';
import ScoreDistribution from '@/components/testdata/ScoreDistribution';

// ---------------------------------------------------------------------------
// Preset Configurations
// ---------------------------------------------------------------------------

const TEST_DATA_CONFIG_STORAGE_KEY = 'algoforge_testdata_config';
const TEST_DATA_CONFIG_STORAGE_SCHEMA = 'algoforge.testdata-config.v2';
const MEMORY_LIMIT_MIN_MB = 16;
const MEMORY_LIMIT_MAX_MB = 1024;
const KILOBYTES_PER_MEGABYTE = 1024;
const ADAPTIVE_TEST_CASE_MIN = 10;
const ADAPTIVE_TEST_CASE_MAX = 20;

interface NormalizedTestDataConfig {
  config: TestDataConfig;
  migratedLegacyMemoryLimit: boolean;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value);
}

function isInteger(value: unknown): value is number {
  return isFiniteNumber(value) && Number.isInteger(value);
}

function isConstraintRange(value: unknown): value is ConstraintRange {
  return (
    isRecord(value) &&
    isFiniteNumber(value.min) &&
    isFiniteNumber(value.max) &&
    typeof value.variable === 'string' &&
    (value.description === undefined || typeof value.description === 'string')
  );
}

function isBoundaryConfig(value: unknown): value is BoundaryConfig {
  return (
    isRecord(value) &&
    typeof value.include_min === 'boolean' &&
    typeof value.include_max === 'boolean' &&
    typeof value.include_zero === 'boolean' &&
    typeof value.include_negative === 'boolean' &&
    Array.isArray(value.custom_boundaries) &&
    value.custom_boundaries.every(isFiniteNumber)
  );
}

function isTestGroup(value: unknown): value is TestGroup {
  return (
    isRecord(value) &&
    typeof value.name === 'string' &&
    isInteger(value.points) &&
    isInteger(value.count) &&
    Array.isArray(value.constraints) &&
    value.constraints.every(isConstraintRange) &&
    (value.boundary_config === undefined ||
      isBoundaryConfig(value.boundary_config))
  );
}

function isCustomTestCase(value: unknown): value is CustomTestCase {
  return (
    isRecord(value) &&
    typeof value.input === 'string' &&
    (value.expected_output === undefined ||
      typeof value.expected_output === 'string') &&
    (value.description === undefined || typeof value.description === 'string')
  );
}

function normalizeTestDataConfig(value: unknown): NormalizedTestDataConfig {
  let candidate = value;
  let allowLegacyKB = true;
  if (isRecord(value) && value.schema_version !== undefined) {
    if (
      value.schema_version !== TEST_DATA_CONFIG_STORAGE_SCHEMA ||
      value.memory_unit !== 'MB'
    ) {
      throw new Error('不支持的配置版本或内存单位');
    }
    candidate = value.config;
    allowLegacyKB = false;
  }
  if (!isRecord(candidate)) {
    throw new Error('配置必须是 JSON 对象');
  }

  const totalCount = candidate.total_count;
  const adaptiveCount = candidate.adaptive_count === true || totalCount === 0;
  const minCount = candidate.min_count ?? ADAPTIVE_TEST_CASE_MIN;
  const maxCount = candidate.max_count ?? ADAPTIVE_TEST_CASE_MAX;
  const sampleCount = candidate.sample_count;
  const timeLimit = candidate.time_limit;
  const checkerType = candidate.checker_type;
  const groups = candidate.groups;
  const customCases = candidate.custom_cases;
  let memoryLimit = candidate.memory_limit;
  let migratedLegacyMemoryLimit = false;

  if (
    !isInteger(totalCount) ||
    (adaptiveCount
      ? (totalCount as number) !== 0 ||
        !isInteger(minCount) ||
        !isInteger(maxCount) ||
        (minCount as number) < ADAPTIVE_TEST_CASE_MIN ||
        (maxCount as number) > ADAPTIVE_TEST_CASE_MAX ||
        (minCount as number) > (maxCount as number)
      : (totalCount as number) < 1 || (totalCount as number) > 32) ||
    !isInteger(sampleCount) ||
    (sampleCount as number) < 0 ||
    (sampleCount as number) >
      (adaptiveCount ? (maxCount as number) : (totalCount as number)) ||
    !isInteger(timeLimit) ||
    (timeLimit as number) < 100 ||
    (timeLimit as number) > 10000 ||
    !Array.isArray(groups) ||
    !groups.every(isTestGroup) ||
    !Array.isArray(customCases) ||
    !customCases.every(isCustomTestCase) ||
    !['exact', 'float_tolerance', 'special_judge'].includes(
      checkerType as string,
    ) ||
    (candidate.float_tolerance !== undefined &&
      (!isFiniteNumber(candidate.float_tolerance) ||
        candidate.float_tolerance <= 0))
  ) {
    throw new Error('配置字段无效或超出页面允许范围');
  }

  if (!isInteger(memoryLimit)) {
    throw new Error('内存限制必须是整数 MB');
  }
  if (
    allowLegacyKB &&
    (memoryLimit as number) >=
      MEMORY_LIMIT_MIN_MB * KILOBYTES_PER_MEGABYTE &&
    (memoryLimit as number) <=
      MEMORY_LIMIT_MAX_MB * KILOBYTES_PER_MEGABYTE &&
    (memoryLimit as number) % KILOBYTES_PER_MEGABYTE === 0
  ) {
    memoryLimit = (memoryLimit as number) / KILOBYTES_PER_MEGABYTE;
    migratedLegacyMemoryLimit = true;
  }
  if (
    (memoryLimit as number) < MEMORY_LIMIT_MIN_MB ||
    (memoryLimit as number) > MEMORY_LIMIT_MAX_MB
  ) {
    throw new Error(
      `内存限制必须为 ${MEMORY_LIMIT_MIN_MB}-${MEMORY_LIMIT_MAX_MB} MB`,
    );
  }

  return {
    config: {
      groups,
      custom_cases: customCases,
      adaptive_count: adaptiveCount,
      min_count: adaptiveCount ? (minCount as number) : undefined,
      max_count: adaptiveCount ? (maxCount as number) : undefined,
      total_count: totalCount as number,
      sample_count: sampleCount as number,
      time_limit: timeLimit as number,
      memory_limit: memoryLimit as number,
      checker_type: checkerType as TestDataConfig['checker_type'],
      ...(candidate.float_tolerance === undefined
        ? {}
        : { float_tolerance: candidate.float_tolerance as number }),
    },
    migratedLegacyMemoryLimit,
  };
}

function serializeStoredTestDataConfig(config: TestDataConfig): string {
  return JSON.stringify({
    schema_version: TEST_DATA_CONFIG_STORAGE_SCHEMA,
    memory_unit: 'MB',
    config,
  });
}

interface Preset {
  name: string;
  description: string;
  config: Partial<TestDataConfig>;
}

const PRESETS: Preset[] = [
  {
    name: '标准竞赛',
    description: '模型按边界情况选择 10–20 个测试点，2 组样例，2s 时限',
    config: {
      adaptive_count: true,
      min_count: ADAPTIVE_TEST_CASE_MIN,
      max_count: ADAPTIVE_TEST_CASE_MAX,
      total_count: 0,
      sample_count: 2,
      time_limit: 2000,
      memory_limit: 256,
      checker_type: 'exact',
      groups: [
        {
          name: '小数据',
          points: 30,
          count: 3,
          constraints: [{ min: 1, max: 100, variable: 'n', description: '数组长度' }],
        },
        {
          name: '中等数据',
          points: 30,
          count: 3,
          constraints: [{ min: 100, max: 10000, variable: 'n', description: '数组长度' }],
        },
        {
          name: '极限数据',
          points: 40,
          count: 4,
          constraints: [{ min: 10000, max: 200000, variable: 'n', description: '数组长度' }],
        },
      ],
    },
  },
  {
    name: '语法入门',
    description: '模型按边界情况选择测试点，1 组样例',
    config: {
      adaptive_count: true,
      min_count: ADAPTIVE_TEST_CASE_MIN,
      max_count: ADAPTIVE_TEST_CASE_MAX,
      total_count: 0,
      sample_count: 1,
      time_limit: 1000,
      memory_limit: 256,
      checker_type: 'exact',
      groups: [
        {
          name: '基础',
          points: 100,
          count: 5,
          constraints: [{ min: 1, max: 1000, variable: 'n' }],
        },
      ],
    },
  },
  {
    name: '浮点精度',
    description: '模型按边界情况选择测试点，带精度容差的浮点输出判定',
    config: {
      adaptive_count: true,
      min_count: ADAPTIVE_TEST_CASE_MIN,
      max_count: ADAPTIVE_TEST_CASE_MAX,
      total_count: 0,
      sample_count: 2,
      time_limit: 2000,
      memory_limit: 256,
      checker_type: 'float_tolerance',
      float_tolerance: 1e-6,
      groups: [
        {
          name: '小数据',
          points: 30,
          count: 5,
          constraints: [{ min: 1, max: 100, variable: 'n' }],
        },
        {
          name: '大数据',
          points: 70,
          count: 10,
          constraints: [{ min: 100, max: 100000, variable: 'n' }],
        },
      ],
    },
  },
];

// ---------------------------------------------------------------------------
// Test Data Config Page
// ---------------------------------------------------------------------------

export default function TestDataConfigPage() {
  const {
    testDataConfig,
    updateTestDataConfig,
    resetTestDataConfig,
  } = useAppStore();

  const [saving, setSaving] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [showDistribution, setShowDistribution] = useState(false);
  const [showCustomCases, setShowCustomCases] = useState(false);

  // Handle updating general config fields
  const handleConfigChange = useCallback(
    <K extends keyof TestDataConfig>(key: K, value: TestDataConfig[K]) => {
      updateTestDataConfig({ [key]: value });
    },
    [updateTestDataConfig],
  );

  // Handle groups update
  const handleGroupsChange = useCallback(
    (groups: TestGroup[]) => {
      updateTestDataConfig({ groups });
    },
    [updateTestDataConfig],
  );

  // Handle custom cases update
  const handleCustomCasesChange = useCallback(
    (customCases: CustomTestCase[]) => {
      updateTestDataConfig({ custom_cases: customCases });
    },
    [updateTestDataConfig],
  );

  // Apply preset
  const handleApplyPreset = useCallback(
    (preset: Preset) => {
      if (
        window.confirm(
          `确定要应用预设 "${preset.name}" 吗？当前配置将被覆盖。`,
        )
      ) {
        resetTestDataConfig();
        updateTestDataConfig(preset.config);
      }
    },
    [resetTestDataConfig, updateTestDataConfig],
  );

  // Save configuration (to local storage as example)
  const handleSave = useCallback(async () => {
    setSaving(true);
    try {
      localStorage.setItem(
        TEST_DATA_CONFIG_STORAGE_KEY,
        serializeStoredTestDataConfig(testDataConfig),
      );
      setSaveSuccess(true);
      setTimeout(() => setSaveSuccess(false), 2000);
    } finally {
      setSaving(false);
    }
  }, [testDataConfig]);

  // Load configuration from local storage
  const handleLoad = useCallback(() => {
    const saved = localStorage.getItem(TEST_DATA_CONFIG_STORAGE_KEY);
    if (saved) {
      try {
        const { config, migratedLegacyMemoryLimit } =
          normalizeTestDataConfig(JSON.parse(saved) as unknown);
        resetTestDataConfig();
        updateTestDataConfig(config);
        if (migratedLegacyMemoryLimit) {
          localStorage.setItem(
            TEST_DATA_CONFIG_STORAGE_KEY,
            serializeStoredTestDataConfig(config),
          );
          alert(`已将旧版 KB 内存限制转换为 ${config.memory_limit} MB`);
        }
      } catch (error) {
        alert(
          `加载配置失败：${error instanceof Error ? error.message : '配置无效'}`,
        );
      }
    } else {
      alert('没有已保存的配置');
    }
  }, [resetTestDataConfig, updateTestDataConfig]);

  // Export config as JSON
  const handleExport = useCallback(async () => {
    try {
      await saveTextFile(JSON.stringify(testDataConfig, null, 2), 'testdata-config.json');
    } catch (error) {
      alert(error instanceof Error ? error.message : '导出配置失败');
    }
  }, [testDataConfig]);

  // Import config from JSON file
  const handleImport = useCallback(() => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.json';
    input.onchange = (e) => {
      const file = (e.target as HTMLInputElement).files?.[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = () => {
        try {
          const { config, migratedLegacyMemoryLimit } =
            normalizeTestDataConfig(JSON.parse(reader.result as string) as unknown);
          resetTestDataConfig();
          updateTestDataConfig(config);
          if (migratedLegacyMemoryLimit) {
            alert(`已将旧版 KB 内存限制转换为 ${config.memory_limit} MB`);
          }
        } catch (error) {
          alert(
            `导入配置失败：${error instanceof Error ? error.message : '配置无效'}`,
          );
        }
      };
      reader.readAsText(file);
    };
    input.click();
  }, [resetTestDataConfig, updateTestDataConfig]);

  // Compute total points
  const totalPoints = testDataConfig.groups.reduce(
    (sum, g) => sum + g.points,
    0,
  );

  const countLabel = testDataConfig.adaptive_count
    ? `${testDataConfig.min_count ?? ADAPTIVE_TEST_CASE_MIN}–${testDataConfig.max_count ?? ADAPTIVE_TEST_CASE_MAX}（模型选择）`
    : String(testDataConfig.total_count);
  const checkerLabel = testDataConfig.checker_type === 'exact' ? '精确匹配' : testDataConfig.checker_type === 'float_tolerance' ? '浮点容差' : '特殊判题（SPJ）';
  return (
    <div className="af-page">
      <PageHeader eyebrow="工具 / 测试数据" title="设计测试数据方案" description="集中设置运行限制、测试组与边界，保存后供后续出题使用。"
        actions={<><button type="button" className="forge-btn-secondary" onClick={handleImport}><Upload className="h-4 w-4" />导入</button><button type="button" className="forge-btn-secondary" onClick={handleExport}><Download className="h-4 w-4" />导出</button><button type="button" className="forge-btn-ghost" onClick={handleLoad}><RefreshCw className="h-4 w-4" />加载已保存配置</button></>}>
        <Link href="/problems/new" className="af-link text-sm">返回编程题创作</Link>
      </PageHeader>
      <div className="af-form-layout">
        <div className="af-form-main">
          <FormSection number="01" title="基本参数" description="设置样例与运行限额，也可以从已有预设开始。">
            <details className="af-form-details"><summary><strong>应用预设方案</strong><span className="af-hint">标准竞赛、语法入门、浮点精度</span></summary><div className="grid gap-3 sm:grid-cols-3">{PRESETS.map((preset) => <button key={preset.name} type="button" className="af-choice-card" onClick={() => handleApplyPreset(preset)}><span><strong className="block text-sm font-semibold">{preset.name}</strong><span className="af-hint mt-1 block">{preset.description}</span></span></button>)}</div></details>
            <div className="rounded-lg border border-[var(--dl)] bg-[var(--ds)] p-4"><p className="text-sm font-medium">测试点数量 · {countLabel}</p><p className="af-hint mt-1">{testDataConfig.adaptive_count ? '模型根据边界情况与覆盖需求确定测试点数量，不重复凑数。' : '当前导入配置使用固定数量；生成方式仍以所选出题工作流为准。'}</p></div>
            <div className="grid gap-4 sm:grid-cols-2">
              <label className="af-field"><span>样例数量</span><input className="forge-input" type="number" min={0} max={ADAPTIVE_TEST_CASE_MAX} value={testDataConfig.sample_count} onChange={(event) => handleConfigChange('sample_count', Number(event.target.value))} /></label>
              <label className="af-field"><span>时间限制（ms）</span><input className="forge-input" type="number" min={100} max={10000} step={100} value={testDataConfig.time_limit} onChange={(event) => handleConfigChange('time_limit', Number(event.target.value))} /></label>
              <label className="af-field"><span>内存限制（MB）</span><input className="forge-input" type="number" min={MEMORY_LIMIT_MIN_MB} max={MEMORY_LIMIT_MAX_MB} step={16} value={testDataConfig.memory_limit} onChange={(event) => handleConfigChange('memory_limit', Number(event.target.value))} /><span className="af-hint">{MEMORY_LIMIT_MIN_MB}–{MEMORY_LIMIT_MAX_MB} MB</span></label>
              <label className="af-field"><span>判定类型</span><select className="forge-input" value={testDataConfig.checker_type} onChange={(event) => handleConfigChange('checker_type', event.target.value as TestDataConfig['checker_type'])}><option value="exact">精确匹配</option><option value="float_tolerance">浮点容差</option><option value="special_judge">特殊判题（SPJ）</option></select></label>
              {testDataConfig.checker_type === 'float_tolerance' && <label className="af-field"><span>浮点容差</span><input className="forge-input" type="number" step="0.000001" value={testDataConfig.float_tolerance ?? 1e-6} onChange={(event) => handleConfigChange('float_tolerance', Number(event.target.value))} /></label>}
            </div>
          </FormSection>
          <FormSection number="02" title="分组与覆盖" description="为不同数据规模分配测试点与分值，再细化变量约束和边界条件。">
            <div className="flex items-center justify-between gap-3"><span className="af-hint">{testDataConfig.groups.length} 个测试组</span><span className={cn('text-sm font-medium tabular-nums', totalPoints === 100 ? 'text-success-600' : 'text-warning-600')}>当前总分 {totalPoints} / 100</span></div>
            <GroupEditor groups={testDataConfig.groups} onChange={handleGroupsChange} />
            {testDataConfig.groups.length > 0 && <div className="space-y-4 border-t border-[var(--dl)] pt-5">
              <details className="af-form-details" open><summary><strong>变量约束</strong><span className="af-hint">逐组定义变量取值范围</span></summary><ConstraintPanel groups={testDataConfig.groups} onGroupsChange={handleGroupsChange} /></details>
              <details className="af-form-details"><summary><strong>边界条件</strong><span className="af-hint">最小值、最大值、零值与负数</span></summary><BoundaryToggle groups={testDataConfig.groups} onGroupsChange={handleGroupsChange} /></details>
              <details className="af-form-details" onToggle={(event) => setShowDistribution(event.currentTarget.open)}><summary><strong>查看分值分布</strong><span className="af-hint">{totalPoints} 分 · 图表与分组明细</span></summary>{showDistribution && <ScoreDistribution groups={testDataConfig.groups} />}</details>
            </div>}
          </FormSection>
          <details className="af-form-details" onToggle={(event) => setShowCustomCases(event.currentTarget.open)}>
            <summary><strong>自定义输入与预期输出</strong><span className="af-hint">已配置 {testDataConfig.custom_cases.length} 个用例；按需补充特殊场景</span></summary>
            {showCustomCases && <CustomCaseEditor cases={testDataConfig.custom_cases} onChange={handleCustomCasesChange} />}
          </details>
        </div>
        <aside className="af-form-rail"><div className="af-summary">
          <div><p className="af-hint">当前方案</p><h2 className="mt-2 text-lg font-semibold">配置预览</h2></div>
          <DataPreview config={testDataConfig} />
          <dl className="space-y-3 border-t border-[var(--dl)] pt-4 text-sm"><div className="af-summary-row"><dt>运行限制</dt><dd>{testDataConfig.time_limit} ms / {testDataConfig.memory_limit} MB</dd></div><div className="af-summary-row"><dt>判定方式</dt><dd>{checkerLabel}</dd></div><div className="af-summary-row"><dt>分组总分</dt><dd className={cn('font-medium', totalPoints === 100 ? 'text-success-600' : 'text-warning-600')}>{totalPoints}</dd></div></dl>
          <div className="af-sticky-actions"><button type="button" className={cn('forge-btn-primary w-full', saveSuccess && 'bg-success-600 hover:bg-success-700')} onClick={handleSave} disabled={saving}>{saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}{saveSuccess ? '已保存' : '保存配置'}</button><Link href="/problems/new" className="forge-btn-secondary w-full">返回出题</Link></div>
          <p className="af-hint">修改会更新当前工作台配置；保存后可在本地重新加载，也可导出 JSON 备份。</p>
          <button type="button" className="forge-btn-ghost w-full text-danger-500 hover:text-danger-600" onClick={() => { if (window.confirm('确定要重置所有配置吗？')) { resetTestDataConfig(); } }}><Trash2 className="h-4 w-4" />重置配置</button>
        </div></aside>
      </div>
    </div>
  );
}
