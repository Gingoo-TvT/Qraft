'use client';

import { Plus, Trash2, ExternalLink } from 'lucide-react';
import Link from 'next/link';
import { cn } from '@/lib/utils';
import type { TestDataConfig as TDConfig, TestGroup } from '@/lib/types';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface TestDataConfigProps {
  config: TDConfig;
  onChange: (config: TDConfig) => void;
}

// ---------------------------------------------------------------------------
// TestDataConfig (inline / simplified)
// ---------------------------------------------------------------------------

export default function TestDataConfig({ config, onChange }: TestDataConfigProps) {
  // ---- Field updaters ----
  function updateField<K extends keyof TDConfig>(key: K, value: TDConfig[K]) {
    onChange({ ...config, [key]: value });
  }

  function updateGroup(index: number, patch: Partial<TestGroup>) {
    const groups = config.groups.map((g, i) => (i === index ? { ...g, ...patch } : g));
    onChange({ ...config, groups });
  }

  function addGroup() {
    const newGroup: TestGroup = {
      name: `Group ${config.groups.length + 1}`,
      points: 0,
      count: 5,
      constraints: [],
    };
    onChange({ ...config, groups: [...config.groups, newGroup] });
  }

  function removeGroup(index: number) {
    onChange({ ...config, groups: config.groups.filter((_, i) => i !== index) });
  }

  // ---- Boundary helpers ----
  function toggleBoundary(key: 'include_min' | 'include_max' | 'include_zero' | 'include_negative') {
    // Apply boundary config globally (first group used as reference)
    const groups = config.groups.map((g) => {
      const bc = g.boundary_config ?? {
        include_min: false,
        include_max: false,
        include_zero: false,
        include_negative: false,
        custom_boundaries: [],
      };
      return { ...g, boundary_config: { ...bc, [key]: !bc[key] } };
    });
    onChange({ ...config, groups });
  }

  // Read boundary state from the first group (simplified view)
  const boundaryState = config.groups[0]?.boundary_config ?? {
    include_min: false,
    include_max: false,
    include_zero: false,
    include_negative: false,
    custom_boundaries: [],
  };

  return (
    <div className="space-y-5">
      {/* ---- Counts ---- */}
      <div className="grid gap-4 md:grid-cols-2">
        <div className="rounded-lg border border-forge-200 bg-forge-50/60 px-4 py-3 dark:border-forge-800 dark:bg-forge-950/20">
          <p className="text-sm font-medium text-forge-700 dark:text-forge-300">测试点数量</p>
          <p className="mt-1 text-sm text-anvil-600 dark:text-anvil-400">
            由 LLM 按题目 corner case 自主选择 <strong>10–20 个</strong>，只保留覆盖所需的不同用例，不用重复数据凑数。
          </p>
        </div>
        <div>
          <label className="mb-1 block text-sm font-medium text-anvil-700 dark:text-anvil-300">
            Sample cases
          </label>
          <input
            type="number"
            min={0}
            max={20}
            value={config.sample_count}
            onChange={(e) => updateField('sample_count', Number(e.target.value))}
            className="h-9 w-full rounded-lg border border-anvil-200 bg-white px-3 text-sm text-anvil-900 focus:border-forge-400 focus:outline-none focus:ring-2 focus:ring-forge-200 dark:border-anvil-700 dark:bg-anvil-900 dark:text-anvil-100 dark:focus:border-forge-600 dark:focus:ring-forge-900"
          />
        </div>
      </div>

      {/* ---- Quick Groups ---- */}
      <div>
        <div className="mb-2 flex items-center justify-between">
          <label className="text-sm font-medium text-anvil-700 dark:text-anvil-300">
            Test groups
          </label>
          <button
            type="button"
            onClick={addGroup}
            className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-forge-600 transition-colors hover:bg-forge-50 dark:text-forge-400 dark:hover:bg-forge-950/30"
          >
            <Plus className="h-3.5 w-3.5" />
            Add group
          </button>
        </div>

        {config.groups.length === 0 && (
          <p className="rounded-lg border border-dashed border-anvil-200 px-4 py-3 text-center text-sm text-anvil-400 dark:border-anvil-700">
            No groups configured. Test data will be generated as a single batch.
          </p>
        )}

        <div className="space-y-2">
          {config.groups.map((group, idx) => (
            <div
              key={idx}
              className="flex items-center gap-3 rounded-lg border border-anvil-200 bg-anvil-50/50 px-3 py-2 dark:border-anvil-700 dark:bg-anvil-800/50"
            >
              <input
                type="text"
                value={group.name}
                onChange={(e) => updateGroup(idx, { name: e.target.value })}
                className="h-7 w-32 rounded border border-anvil-200 bg-white px-2 text-xs text-anvil-900 focus:border-forge-400 focus:outline-none dark:border-anvil-600 dark:bg-anvil-900 dark:text-anvil-100"
                placeholder="Group name"
              />
              <div className="flex items-center gap-1">
                <span className="text-xs text-anvil-500">Cases:</span>
                <input
                  type="number"
                  min={1}
                  max={200}
                  value={group.count}
                  onChange={(e) =>
                    updateGroup(idx, { count: Number(e.target.value) })
                  }
                  className="h-7 w-16 rounded border border-anvil-200 bg-white px-2 text-xs text-anvil-900 focus:border-forge-400 focus:outline-none dark:border-anvil-600 dark:bg-anvil-900 dark:text-anvil-100"
                />
              </div>
              <div className="flex items-center gap-1">
                <span className="text-xs text-anvil-500">Points:</span>
                <input
                  type="number"
                  min={0}
                  value={group.points}
                  onChange={(e) =>
                    updateGroup(idx, { points: Number(e.target.value) })
                  }
                  className="h-7 w-16 rounded border border-anvil-200 bg-white px-2 text-xs text-anvil-900 focus:border-forge-400 focus:outline-none dark:border-anvil-600 dark:bg-anvil-900 dark:text-anvil-100"
                />
              </div>
              <button
                type="button"
                onClick={() => removeGroup(idx)}
                className="ml-auto rounded p-1 text-anvil-400 transition-colors hover:bg-danger-50 hover:text-danger-500 dark:hover:bg-danger-500/10"
                aria-label={`Remove ${group.name}`}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
          ))}
        </div>
      </div>

      {/* ---- Boundary Toggles ---- */}
      <div>
        <label className="mb-2 block text-sm font-medium text-anvil-700 dark:text-anvil-300">
          Boundary cases
        </label>
        <div className="grid grid-cols-2 gap-2">
          {(
            [
              ['include_min', 'Include minimum'],
              ['include_max', 'Include maximum'],
              ['include_zero', 'Include zero'],
              ['include_negative', 'Include negative'],
            ] as const
          ).map(([key, label]) => (
            <label
              key={key}
              className={cn(
                'flex cursor-pointer items-center gap-2 rounded-lg border px-3 py-2 text-sm transition-colors',
                boundaryState[key]
                  ? 'border-forge-300 bg-forge-50 text-forge-700 dark:border-forge-700 dark:bg-forge-950/30 dark:text-forge-300'
                  : 'border-anvil-200 text-anvil-600 hover:border-anvil-300 dark:border-anvil-700 dark:text-anvil-400 dark:hover:border-anvil-600',
              )}
            >
              <input
                type="checkbox"
                checked={boundaryState[key]}
                onChange={() => toggleBoundary(key)}
                className="sr-only"
              />
              <div
                className={cn(
                  'flex h-4 w-4 items-center justify-center rounded border',
                  boundaryState[key]
                    ? 'border-forge-600 bg-forge-600 dark:border-forge-500 dark:bg-forge-500'
                    : 'border-anvil-300 dark:border-anvil-600',
                )}
              >
                {boundaryState[key] && (
                  <svg
                    className="h-3 w-3 text-white"
                    viewBox="0 0 12 12"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M2 6l3 3 5-5" />
                  </svg>
                )}
              </div>
              <span>{label}</span>
            </label>
          ))}
        </div>
      </div>

      {/* ---- Link to full config page ---- */}
      <Link
        href="/testdata-config"
        className="inline-flex items-center gap-1.5 text-sm font-medium text-forge-600 transition-colors hover:text-forge-700 dark:text-forge-400 dark:hover:text-forge-300"
      >
        <ExternalLink className="h-3.5 w-3.5" />
        Advanced test data settings
      </Link>
    </div>
  );
}
