'use client';

import { useEffect, useMemo, useRef, useState } from 'react';

import { useKnowledgePoints } from '@/hooks/useKnowledgePoints';
import { cn } from '@/lib/utils';

interface Props {
  subject: string;
  value: string[];
  onChange: (codes: string[]) => void;
  max?: number;
}

export default function KnowledgePointPicker({
  subject,
  value,
  onChange,
  max = 8,
}: Props) {
  const { points, loading, error, refresh } = useKnowledgePoints(subject);
  const [keyword, setKeyword] = useState('');
  const previousSubjectRef = useRef(subject);

  useEffect(() => {
    if (previousSubjectRef.current !== subject) {
      previousSubjectRef.current = subject;
      onChange([]);
      setKeyword('');
    }
  }, [onChange, subject]);

  const filteredPoints = useMemo(() => {
    const q = keyword.trim().toLowerCase();
    return [...points]
      .sort((a, b) => {
        if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order;
        return a.code.localeCompare(b.code);
      })
      .filter((point) => {
        if (!q) return true;
        return (
          point.code.toLowerCase().includes(q) ||
          point.name.toLowerCase().includes(q)
        );
      });
  }, [keyword, points]);

  const selectedPoints = useMemo(
    () => points.filter((point) => value.includes(point.code)),
    [points, value],
  );

  function toggle(code: string) {
    if (value.includes(code)) {
      onChange(value.filter((item) => item !== code));
      return;
    }
    if (value.length >= max) return;
    onChange([...value, code]);
  }

  if (!subject) {
    return (
      <div className="rounded-lg border border-dashed border-anvil-300 p-6 text-center text-sm text-anvil-400 dark:border-anvil-700">
        请先选择学科
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-anvil-700 dark:text-anvil-200">
            已选 {value.length}/{max}
          </p>
          {selectedPoints.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-2">
              {selectedPoints.map((point) => (
                <button
                  key={point.code}
                  type="button"
                  className="forge-badge bg-forge-100 text-forge-700 hover:bg-forge-200 dark:bg-forge-900/30 dark:text-forge-300"
                  onClick={() => toggle(point.code)}
                >
                  {point.code} {point.name}
                </button>
              ))}
            </div>
          )}
        </div>
        <button
          type="button"
          className="text-sm text-forge-600 hover:underline dark:text-forge-400"
          onClick={refresh}
          disabled={loading}
        >
          {loading ? '刷新中...' : '刷新知识点'}
        </button>
      </div>

      <input
        className="forge-input"
        placeholder="搜索知识点 code / name"
        value={keyword}
        onChange={(e) => setKeyword(e.target.value)}
      />

      {error && (
        <div className="rounded-lg border border-danger-400/30 bg-danger-50 p-3 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
          {error}
        </div>
      )}

      <div className="max-h-80 overflow-y-auto rounded-lg border border-anvil-200 p-3 dark:border-anvil-700">
        {loading && points.length === 0 ? (
          <div className="grid grid-cols-2 gap-2 md:grid-cols-3">
            {Array.from({ length: 9 }).map((_, i) => (
              <div key={i} className="forge-skeleton h-9" />
            ))}
          </div>
        ) : filteredPoints.length === 0 ? (
          <p className="py-8 text-center text-sm text-anvil-400">
            暂无匹配知识点
          </p>
        ) : (
          <div className="grid grid-cols-1 gap-2 md:grid-cols-2 xl:grid-cols-3">
            {filteredPoints.map((point) => {
              const selected = value.includes(point.code);
              const disabled = !selected && value.length >= max;
              return (
                <button
                  key={point.id}
                  type="button"
                  className={cn(
                    'rounded-lg border px-3 py-2 text-left text-sm transition-colors',
                    selected
                      ? 'border-forge-500 bg-forge-50 text-forge-700 dark:bg-forge-950/40 dark:text-forge-300'
                      : 'border-anvil-200 text-anvil-700 hover:border-anvil-300 hover:bg-anvil-50 dark:border-anvil-700 dark:text-anvil-300 dark:hover:bg-anvil-800',
                    disabled && 'cursor-not-allowed opacity-45',
                  )}
                  onClick={() => toggle(point.code)}
                  disabled={disabled}
                >
                  <span className="block font-mono text-xs text-anvil-400">
                    {point.code}
                  </span>
                  <span className="block font-medium">{point.name}</span>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
