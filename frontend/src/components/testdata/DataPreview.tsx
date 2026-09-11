'use client';

import React from 'react';
import type { TestDataConfig } from '@/lib/types';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface DataPreviewProps {
  config: TestDataConfig;
}

// ---------------------------------------------------------------------------
// DataPreview
// ---------------------------------------------------------------------------

export default function DataPreview({ config }: DataPreviewProps) {
  const totalGroupTests = config.groups.reduce((sum, g) => sum + g.count, 0);
  const countLabel = config.adaptive_count
    ? String(config.min_count ?? 10) + '–' + String(config.max_count ?? 20) + '（模型选择）'
    : String(config.total_count);

  return (
    <div className="space-y-3 text-sm">
      <div className="rounded-lg bg-anvil-50 dark:bg-anvil-800/50 p-3 space-y-2">
        <p className="text-anvil-600 dark:text-anvil-300">
          <span className="font-medium">测试点数量：</span> {countLabel}
        </p>
        <p className="text-anvil-600 dark:text-anvil-300">
          <span className="font-medium">样例数：</span> {config.sample_count}
        </p>
        <p className="text-anvil-600 dark:text-anvil-300">
          <span className="font-medium">分组提示合计：</span> {totalGroupTests}
        </p>
        <p className="text-anvil-600 dark:text-anvil-300">
          <span className="font-medium">自定义用例：</span> {config.custom_cases.length}
        </p>
      </div>

      {config.groups.length > 0 && (
        <div className="space-y-2">
          <h4 className="text-xs font-semibold text-anvil-500 dark:text-anvil-400 uppercase">
            分组概览
          </h4>
          {config.groups.map((group, idx) => (
            <div
              key={idx}
              className="flex items-center justify-between rounded border border-anvil-200 dark:border-anvil-700 px-3 py-2"
            >
              <span className="text-anvil-700 dark:text-anvil-200">
                {group.name || `组 ${idx + 1}`}
              </span>
              <div className="flex items-center gap-3 text-xs text-anvil-500">
                <span>{group.count} 个测试点</span>
                <span>{group.points} 分</span>
              </div>
            </div>
          ))}
        </div>
      )}

      {config.custom_cases.length > 0 && (
        <div className="space-y-2">
          <h4 className="text-xs font-semibold text-anvil-500 dark:text-anvil-400 uppercase">
            自定义用例预览
          </h4>
          {config.custom_cases.slice(0, 3).map((tc, idx) => (
            <div
              key={idx}
              className="rounded border border-anvil-200 dark:border-anvil-700 p-2"
            >
              <p className="text-xs text-anvil-500">{tc.description || `用例 ${idx + 1}`}</p>
              <pre className="mt-1 max-h-16 overflow-hidden text-xs text-anvil-600 dark:text-anvil-300 font-mono">
                {tc.input.slice(0, 100)}{tc.input.length > 100 ? '...' : ''}
              </pre>
            </div>
          ))}
          {config.custom_cases.length > 3 && (
            <p className="text-xs text-anvil-400">
              还有 {config.custom_cases.length - 3} 个用例...
            </p>
          )}
        </div>
      )}
    </div>
  );
}
