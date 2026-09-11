'use client';

import React, { useCallback } from 'react';
import { Trash2, Plus } from 'lucide-react';
import type { ConstraintRange, TestGroup } from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Props - accepts groups with constraints inside them
// ---------------------------------------------------------------------------

export interface ConstraintPanelProps {
  groups: TestGroup[];
  onGroupsChange: (groups: TestGroup[]) => void;
}

// ---------------------------------------------------------------------------
// Single constraint row
// ---------------------------------------------------------------------------

function ConstraintRow({
  constraint,
  onUpdate,
  onDelete,
}: {
  constraint: ConstraintRange;
  onUpdate: (constraint: ConstraintRange) => void;
  onDelete: () => void;
}) {
  const handleChange = (field: keyof ConstraintRange, value: string | number) => {
    onUpdate({ ...constraint, [field]: value });
  };

  const visualMax = Math.max(Math.abs(constraint.max) * 1.2, Math.abs(constraint.min) * 1.2, 100);
  const visualMin = -visualMax;
  const range = visualMax - visualMin;
  const leftPercent = Math.max(0, Math.min(100, ((constraint.min - visualMin) / range) * 100));
  const rightPercent = Math.max(0, Math.min(100, ((constraint.max - visualMin) / range) * 100));

  return (
    <div className="rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-900 p-3 space-y-3">
      <div className="flex items-start gap-3">
        <div className="flex-1 space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">变量名</label>
              <input
                type="text"
                value={constraint.variable}
                onChange={(e) => handleChange('variable', e.target.value)}
                placeholder="例如：N"
                className="w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-forge-400"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">描述</label>
              <input
                type="text"
                value={constraint.description ?? ''}
                onChange={(e) => handleChange('description', e.target.value)}
                placeholder="可选描述"
                className="w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800 px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-forge-400"
              />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">最小值</label>
              <input
                type="number"
                value={constraint.min}
                onChange={(e) => handleChange('min', parseFloat(e.target.value) || 0)}
                className="w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-forge-400"
              />
            </div>
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">最大值</label>
              <input
                type="number"
                value={constraint.max}
                onChange={(e) => handleChange('max', parseFloat(e.target.value) || 0)}
                className="w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800 px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-forge-400"
              />
            </div>
          </div>
          <div className="relative h-3 rounded-full bg-anvil-100 dark:bg-anvil-800 overflow-hidden">
            <div
              className="absolute top-0 h-full rounded-full bg-forge-400/40 dark:bg-forge-500/30"
              style={{ left: `${leftPercent}%`, width: `${Math.max(0, rightPercent - leftPercent)}%` }}
            />
          </div>
        </div>
        <button onClick={onDelete} className="mt-1 text-anvil-400 hover:text-danger-500 transition-colors">
          <Trash2 className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ConstraintPanel - shows constraints per group
// ---------------------------------------------------------------------------

export default function ConstraintPanel({ groups, onGroupsChange }: ConstraintPanelProps) {
  const handleConstraintUpdate = useCallback(
    (groupIdx: number, constraintIdx: number, updated: ConstraintRange) => {
      const newGroups = groups.map((g, gi) => {
        if (gi !== groupIdx) return g;
        const newConstraints = [...g.constraints];
        newConstraints[constraintIdx] = updated;
        return { ...g, constraints: newConstraints };
      });
      onGroupsChange(newGroups);
    },
    [groups, onGroupsChange],
  );

  const handleConstraintDelete = useCallback(
    (groupIdx: number, constraintIdx: number) => {
      const newGroups = groups.map((g, gi) => {
        if (gi !== groupIdx) return g;
        return { ...g, constraints: g.constraints.filter((_, ci) => ci !== constraintIdx) };
      });
      onGroupsChange(newGroups);
    },
    [groups, onGroupsChange],
  );

  const handleConstraintAdd = useCallback(
    (groupIdx: number) => {
      const newConstraint: ConstraintRange = { variable: '', min: 1, max: 100, description: '' };
      const newGroups = groups.map((g, gi) => {
        if (gi !== groupIdx) return g;
        return { ...g, constraints: [...g.constraints, newConstraint] };
      });
      onGroupsChange(newGroups);
    },
    [groups, onGroupsChange],
  );

  return (
    <div className="space-y-6">
      {groups.map((group, groupIdx) => (
        <div key={groupIdx}>
          <h3 className="text-sm font-semibold text-anvil-700 dark:text-anvil-200 mb-2">
            {group.name || `组 ${groupIdx + 1}`}
          </h3>
          <div className="space-y-2">
            {group.constraints.map((constraint, ci) => (
              <ConstraintRow
                key={ci}
                constraint={constraint}
                onUpdate={(updated) => handleConstraintUpdate(groupIdx, ci, updated)}
                onDelete={() => handleConstraintDelete(groupIdx, ci)}
              />
            ))}
          </div>
          <button
            onClick={() => handleConstraintAdd(groupIdx)}
            className={cn(
              'mt-2 inline-flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed',
              'border-anvil-300 dark:border-anvil-600 py-2 text-xs font-medium',
              'text-anvil-600 dark:text-anvil-400 hover:border-forge-400 hover:text-forge-600 transition-colors',
            )}
          >
            <Plus className="h-3.5 w-3.5" />
            添加约束
          </button>
        </div>
      ))}
    </div>
  );
}
