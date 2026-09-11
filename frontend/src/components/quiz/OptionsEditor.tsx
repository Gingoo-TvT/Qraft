'use client';

import { Plus, Trash2 } from 'lucide-react';

import type { QuizOption } from '@/lib/types';

interface Props {
  value: QuizOption[];
  onChange: (options: QuizOption[]) => void;
}

function relabel(options: QuizOption[]): QuizOption[] {
  return options.map((option, index) => ({
    ...option,
    label: String.fromCharCode(65 + index),
  }));
}

function normalized(value: QuizOption[]): QuizOption[] {
  const base = value.length >= 2 ? value : [
    { label: 'A', content: '' },
    { label: 'B', content: '' },
  ];
  return relabel(base);
}

export default function OptionsEditor({ value, onChange }: Props) {
  const options = normalized(value);

  function updateContent(index: number, content: string) {
    const next = options.map((option, i) =>
      i === index ? { ...option, content } : option,
    );
    onChange(relabel(next));
  }

  function addOption() {
    onChange(relabel([...options, { label: '', content: '' }]));
  }

  function removeOption(index: number) {
    if (options.length <= 2) return;
    onChange(relabel(options.filter((_, i) => i !== index)));
  }

  return (
    <div className="space-y-3">
      {options.map((option, index) => (
        <div key={option.label} className="flex items-center gap-2">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-anvil-100 font-mono text-sm font-semibold text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300">
            {option.label}
          </span>
          <input
            className="forge-input"
            value={option.content}
            onChange={(e) => updateContent(index, e.target.value)}
            placeholder={`选项 ${option.label}`}
          />
          <button
            type="button"
            className="forge-btn-ghost px-2 text-danger-500"
            disabled={options.length <= 2}
            onClick={() => removeOption(index)}
            aria-label={`删除选项 ${option.label}`}
          >
            <Trash2 className="h-4 w-4" />
          </button>
        </div>
      ))}
      <button type="button" className="forge-btn-secondary" onClick={addOption}>
        <Plus className="h-4 w-4" />
        新增选项
      </button>
    </div>
  );
}
