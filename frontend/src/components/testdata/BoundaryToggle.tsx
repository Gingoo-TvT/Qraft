'use client';

import React, { useCallback } from 'react';
import type { BoundaryConfig, TestGroup } from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface BoundaryToggleProps {
  groups: TestGroup[];
  onGroupsChange: (groups: TestGroup[]) => void;
}

// ---------------------------------------------------------------------------
// Default boundary config
// ---------------------------------------------------------------------------

const DEFAULT_BOUNDARY: BoundaryConfig = {
  include_min: true,
  include_max: true,
  include_zero: false,
  include_negative: false,
  custom_boundaries: [],
};

// ---------------------------------------------------------------------------
// Toggle switch
// ---------------------------------------------------------------------------

function Toggle({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex items-center justify-between cursor-pointer py-1">
      <span className="text-sm text-anvil-600 dark:text-anvil-300">{label}</span>
      <button
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={cn(
          'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
          checked ? 'bg-forge-500' : 'bg-anvil-300 dark:bg-anvil-600',
        )}
      >
        <span
          className={cn(
            'inline-block h-3.5 w-3.5 rounded-full bg-white transition-transform',
            checked ? 'translate-x-[18px]' : 'translate-x-[3px]',
          )}
        />
      </button>
    </label>
  );
}

// ---------------------------------------------------------------------------
// BoundaryToggle
// ---------------------------------------------------------------------------

export default function BoundaryToggle({ groups, onGroupsChange }: BoundaryToggleProps) {
  const handleToggle = useCallback(
    (groupIdx: number, field: keyof BoundaryConfig, value: boolean) => {
      const newGroups = groups.map((g, i) => {
        if (i !== groupIdx) return g;
        const config = g.boundary_config ?? { ...DEFAULT_BOUNDARY };
        return { ...g, boundary_config: { ...config, [field]: value } };
      });
      onGroupsChange(newGroups);
    },
    [groups, onGroupsChange],
  );

  if (groups.length === 0) {
    return (
      <p className="text-sm text-anvil-400 text-center py-4">
        请先添加测试组
      </p>
    );
  }

  return (
    <div className="space-y-4">
      {groups.map((group, idx) => {
        const config = group.boundary_config ?? DEFAULT_BOUNDARY;
        return (
          <div key={idx} className="rounded-lg border border-anvil-200 dark:border-anvil-700 p-3">
            <h4 className="text-sm font-medium text-anvil-700 dark:text-anvil-200 mb-2">
              {group.name || `组 ${idx + 1}`}
            </h4>
            <div className="space-y-1">
              <Toggle
                label="包含最小值边界"
                checked={config.include_min}
                onChange={(v) => handleToggle(idx, 'include_min', v)}
              />
              <Toggle
                label="包含最大值边界"
                checked={config.include_max}
                onChange={(v) => handleToggle(idx, 'include_max', v)}
              />
              <Toggle
                label="包含零值"
                checked={config.include_zero}
                onChange={(v) => handleToggle(idx, 'include_zero', v)}
              />
              <Toggle
                label="包含负数"
                checked={config.include_negative}
                onChange={(v) => handleToggle(idx, 'include_negative', v)}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}
