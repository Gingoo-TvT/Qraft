'use client';

import { useEditorTheme } from '@/hooks/useEditorTheme';

import React, { useState, useCallback } from 'react';
import { Trash2, Plus, ChevronDown, ChevronRight } from 'lucide-react';
import Editor from '@monaco-editor/react';
import type { CustomTestCase } from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface CustomCaseEditorProps {
  cases: CustomTestCase[];
  onChange: (cases: CustomTestCase[]) => void;
}

// ---------------------------------------------------------------------------
// Single case card
// ---------------------------------------------------------------------------

interface CaseCardProps {
  testCase: CustomTestCase;
  index: number;
  onUpdate: (index: number, testCase: CustomTestCase) => void;
  onDelete: (index: number) => void;
}

function CaseCard({ testCase, index, onUpdate, onDelete }: CaseCardProps) {
  const editorTheme = useEditorTheme();
  const [expanded, setExpanded] = useState(true);

  const handleField = (field: keyof CustomTestCase, value: string) => {
    onUpdate(index, { ...testCase, [field]: value });
  };

  return (
    <div className="rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-900 overflow-hidden">
      {/* Header */}
      <div
        className={cn(
          'flex items-center gap-2 px-3 py-2 cursor-pointer select-none',
          'bg-anvil-50 dark:bg-anvil-800/50 border-b border-anvil-100 dark:border-anvil-800',
          'hover:bg-anvil-100 dark:hover:bg-anvil-800 transition-colors',
        )}
      >
        <button type="button" onClick={() => setExpanded(!expanded)} aria-expanded={expanded} aria-label={'展开或收起测试用例 ' + (index + 1)} className="flex min-w-0 flex-1 items-center gap-2 text-left">
        {expanded ? (
          <ChevronDown className="h-4 w-4 text-anvil-400" />
        ) : (
          <ChevronRight className="h-4 w-4 text-anvil-400" />
        )}
        <span className="text-sm font-medium text-anvil-700 dark:text-anvil-300 flex-1">
          测试用例 {index + 1}
          {testCase.description && (
            <span className="ml-2 text-xs font-normal text-anvil-400 dark:text-anvil-500">
              - {testCase.description}
            </span>
          )}
        </span>
        </button>
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onDelete(index);
          }}
          className="text-anvil-400 hover:text-danger-500 transition-colors"
          aria-label={`删除测试用例 ${index + 1}`}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>

      {/* Body */}
      {expanded && (
        <div className="p-3 space-y-3">
          {/* Description */}
          <div>
            <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
              用例说明
            </label>
            <input
              type="text"
              aria-label={'测试用例 ' + (index + 1) + ' 说明'}
              value={testCase.description ?? ''}
              onChange={(e) => handleField('description', e.target.value)}
              placeholder="可选，说明这组数据的用途"
              className={cn(
                'w-full rounded-md border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-800',
                'px-3 py-1.5 text-sm text-anvil-900 dark:text-anvil-100',
                'placeholder:text-anvil-400 focus:outline-none focus:ring-2 focus:ring-forge-400 focus:border-transparent',
              )}
            />
          </div>

          {/* Input editor */}
          <div>
            <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
              输入数据
            </label>
            <div className="rounded-md border border-anvil-200 dark:border-anvil-700 overflow-hidden">
              <Editor
                height="120px"
                loading="正在加载编辑器…"
                defaultLanguage="plaintext"
                value={testCase.input}
                onChange={(value) => handleField('input', value ?? '')}
                theme={editorTheme.theme}
                beforeMount={editorTheme.beforeMount}
                options={{
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  lineNumbers: 'on',
                  fontSize: 14,
                  fontFamily: 'JetBrains Mono, Fira Code, monospace',
                  wordWrap: 'off',
                  automaticLayout: true,
                  padding: { top: 4, bottom: 4 },
                  scrollbar: { vertical: 'auto', horizontal: 'auto' },
                  renderLineHighlight: 'none',
                  overviewRulerLanes: 0,
                  hideCursorInOverviewRuler: true,
                  overviewRulerBorder: false,
                  folding: false,
                  glyphMargin: false,
                }}
              />
            </div>
          </div>

          {/* Expected output editor */}
          <div>
            <label className="block text-xs font-medium text-anvil-500 dark:text-anvil-400 mb-1">
              预期输出{' '}
              <span className="text-anvil-400 dark:text-anvil-500 font-normal">（可选）</span>
            </label>
            <div className="rounded-md border border-anvil-200 dark:border-anvil-700 overflow-hidden">
              <Editor
                height="80px"
                loading="正在加载编辑器…"
                defaultLanguage="plaintext"
                value={testCase.expected_output ?? ''}
                onChange={(value) => handleField('expected_output', value ?? '')}
                theme={editorTheme.theme}
                beforeMount={editorTheme.beforeMount}
                options={{
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  lineNumbers: 'on',
                  fontSize: 14,
                  fontFamily: 'JetBrains Mono, Fira Code, monospace',
                  wordWrap: 'off',
                  automaticLayout: true,
                  padding: { top: 4, bottom: 4 },
                  scrollbar: { vertical: 'auto', horizontal: 'auto' },
                  renderLineHighlight: 'none',
                  overviewRulerLanes: 0,
                  hideCursorInOverviewRuler: true,
                  overviewRulerBorder: false,
                  folding: false,
                  glyphMargin: false,
                }}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// CustomCaseEditor
// ---------------------------------------------------------------------------

export default function CustomCaseEditor({ cases, onChange }: CustomCaseEditorProps) {
  const handleUpdate = useCallback(
    (index: number, updated: CustomTestCase) => {
      const next = [...cases];
      next[index] = updated;
      onChange(next);
    },
    [cases, onChange],
  );

  const handleDelete = useCallback(
    (index: number) => {
      onChange(cases.filter((_, i) => i !== index));
    },
    [cases, onChange],
  );

  const handleAdd = useCallback(() => {
    const newCase: CustomTestCase = {
      input: '',
      expected_output: '',
      description: '',
    };
    onChange([...cases, newCase]);
  }, [cases, onChange]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          自定义测试用例
        </h3>
        <span className="text-xs text-anvil-400 dark:text-anvil-500">
          {cases.length} 组用例
        </span>
      </div>

      {cases.length === 0 ? (
        <div className="rounded-lg border-2 border-dashed border-anvil-200 dark:border-anvil-700 p-6 text-center">
          <p className="text-xs text-anvil-500 dark:text-anvil-400">
            还没有自定义用例，可添加边界数据或手工构造的输入。
          </p>
        </div>
      ) : (
        <div className="space-y-2">
          {cases.map((testCase, index) => (
            <CaseCard
              key={index}
              testCase={testCase}
              index={index}
              onUpdate={handleUpdate}
              onDelete={handleDelete}
            />
          ))}
        </div>
      )}

      <button
        type="button"
        onClick={handleAdd}
        className={cn(
          'inline-flex w-full items-center justify-center gap-2 rounded-lg border-2 border-dashed',
          'border-anvil-300 dark:border-anvil-600 py-2.5 text-sm font-medium',
          'text-anvil-600 dark:text-anvil-400 hover:border-forge-400 hover:text-forge-600',
          'dark:hover:border-forge-500 dark:hover:text-forge-400 transition-colors',
        )}
      >
        <Plus className="h-4 w-4" />
        添加测试用例
      </button>
    </div>
  );
}
