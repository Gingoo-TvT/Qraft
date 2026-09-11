'use client';

import React, { useCallback } from 'react';
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
  arrayMove,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { GripVertical, Trash2, Plus } from 'lucide-react';
import type { TestGroup } from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface GroupEditorProps {
  groups: TestGroup[];
  onChange: (groups: TestGroup[]) => void;
}

// ---------------------------------------------------------------------------
// Sortable Group Card
// ---------------------------------------------------------------------------

interface SortableGroupCardProps {
  group: TestGroup;
  index: number;
  onUpdate: (index: number, group: TestGroup) => void;
  onDelete: (index: number) => void;
}

function SortableGroupCard({ group, index, onUpdate, onDelete }: SortableGroupCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: `group-${index}` });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  const handleField = (field: keyof TestGroup, value: string | number) => {
    onUpdate(index, { ...group, [field]: value });
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={cn(
        'rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-900 p-4 transition-shadow',
        isDragging && 'shadow-lg ring-2 ring-forge-400 opacity-90',
      )}
    >
      <div className="flex items-start gap-3">
        {/* Drag handle */}
        <button
          {...attributes}
          {...listeners}
          className="mt-1 cursor-grab touch-none text-anvil-400 hover:text-anvil-600 dark:hover:text-anvil-300 active:cursor-grabbing"
          aria-label="拖动调整顺序"
        >
          <GripVertical className="h-5 w-5" />
        </button>

        {/* Fields */}
        <div className="flex-1 space-y-3">
          {/* Group name */}
          <div>
            <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
              测试组名称
            </label>
            <input
              type="text"
              value={group.name}
              onChange={(e) => handleField('name', e.target.value)}
              placeholder="例如：小规模输入"
              className={cn(
                'w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800',
                'px-3 py-1.5 text-sm text-anvil-900 dark:text-anvil-100',
                'placeholder:text-anvil-400 focus:outline-none focus:ring-2 focus:ring-forge-400 focus:border-transparent',
              )}
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            {/* Test case count */}
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
                测试点数量
              </label>
              <input
                type="number"
                min={1}
                max={1000}
                value={group.count}
                onChange={(e) => handleField('count', Math.max(1, parseInt(e.target.value, 10) || 1))}
                className={cn(
                  'w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800',
                  'px-3 py-1.5 text-sm text-anvil-900 dark:text-anvil-100',
                  'focus:outline-none focus:ring-2 focus:ring-forge-400 focus:border-transparent',
                )}
              />
            </div>

            {/* Points / Score */}
            <div>
              <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
                分值
              </label>
              <input
                type="number"
                min={0}
                value={group.points}
                onChange={(e) => handleField('points', Math.max(0, parseInt(e.target.value, 10) || 0))}
                className={cn(
                  'w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800',
                  'px-3 py-1.5 text-sm text-anvil-900 dark:text-anvil-100',
                  'focus:outline-none focus:ring-2 focus:ring-forge-400 focus:border-transparent',
                )}
              />
            </div>
          </div>

          {/* Constraints summary */}
          {group.constraints.length > 0 && (
            <div className="text-xs text-anvil-500 dark:text-anvil-400">
              <span className="font-medium">约束：</span>{' '}
              {group.constraints
                .map((c) => `${c.variable}: [${c.min}, ${c.max}]`)
                .join(', ')}
            </div>
          )}
        </div>

        {/* Delete button */}
        <button
          onClick={() => onDelete(index)}
          className="mt-1 text-anvil-400 hover:text-danger-500 transition-colors"
          aria-label={`删除测试组 ${group.name}`}
        >
          <Trash2 className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// GroupEditor
// ---------------------------------------------------------------------------

export default function GroupEditor({ groups, onChange }: GroupEditorProps) {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;

      const oldIndex = groups.findIndex((_, i) => `group-${i}` === active.id);
      const newIndex = groups.findIndex((_, i) => `group-${i}` === over.id);

      if (oldIndex !== -1 && newIndex !== -1) {
        onChange(arrayMove(groups, oldIndex, newIndex));
      }
    },
    [groups, onChange],
  );

  const handleUpdate = useCallback(
    (index: number, updated: TestGroup) => {
      const next = [...groups];
      next[index] = updated;
      onChange(next);
    },
    [groups, onChange],
  );

  const handleDelete = useCallback(
    (index: number) => {
      onChange(groups.filter((_, i) => i !== index));
    },
    [groups, onChange],
  );

  const handleAdd = useCallback(() => {
    const newGroup: TestGroup = {
      name: `Group ${groups.length + 1}`,
      points: 10,
      count: 5,
      constraints: [],
    };
    onChange([...groups, newGroup]);
  }, [groups, onChange]);

  const sortableIds = groups.map((_, i) => `group-${i}`);

  return (
    <div className="space-y-3">
      {groups.length === 0 ? (
        <div className="rounded-lg border-2 border-dashed border-anvil-200 dark:border-anvil-700 p-8 text-center">
          <p className="text-sm text-anvil-500 dark:text-anvil-400">
            还没有测试组，添加后可分别设置数量、分值与约束。
          </p>
        </div>
      ) : (
        <DndContext
          sensors={sensors}
          collisionDetection={closestCenter}
          onDragEnd={handleDragEnd}
        >
          <SortableContext items={sortableIds} strategy={verticalListSortingStrategy}>
            <div className="space-y-2">
              {groups.map((group, index) => (
                <SortableGroupCard
                  key={`group-${index}`}
                  group={group}
                  index={index}
                  onUpdate={handleUpdate}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          </SortableContext>
        </DndContext>
      )}

      <button
        onClick={handleAdd}
        className={cn(
          'inline-flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed',
          'border-anvil-300 dark:border-anvil-600 py-2.5 text-sm font-medium',
          'text-anvil-600 dark:text-anvil-400 hover:border-forge-400 hover:text-forge-600',
          'dark:hover:border-forge-500 dark:hover:text-forge-400 transition-colors',
        )}
      >
        <Plus className="h-4 w-4" />
        添加测试组
      </button>
    </div>
  );
}
