'use client';

import { Clock, HardDrive } from 'lucide-react';
import { cn, difficultyColor } from '@/lib/utils';
import { getDifficultyLabel } from '@/lib/constants';
import MarkdownRenderer from '@/components/MarkdownRenderer';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface ProblemPreviewProps {
  statement: string;
  title: string;
  difficulty: number;
  tags: string[];
  timeLimit: number;
  memoryLimit: number;
}

// ---------------------------------------------------------------------------
// Difficulty badge
// ---------------------------------------------------------------------------

function DifficultyBadge({ difficulty }: { difficulty: number }) {
  const color = difficultyColor(difficulty);
  const label = getDifficultyLabel(difficulty);

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-bold',
        color,
        'border-current/20 bg-current/5',
      )}
    >
      {difficulty} &middot; {label}
    </span>
  );
}

// ---------------------------------------------------------------------------
// ProblemPreview
// ---------------------------------------------------------------------------

export default function ProblemPreview({
  statement,
  title,
  difficulty,
  tags,
  timeLimit,
  memoryLimit,
}: ProblemPreviewProps) {
  return (
    <article className="rounded-xl border border-anvil-200 bg-white p-6 shadow-sm dark:border-anvil-700 dark:bg-anvil-900">
      {/* Header */}
      <header className="mb-6 border-b border-anvil-100 pb-4 dark:border-anvil-800">
        <h1 className="mb-2 text-xl font-bold text-anvil-900 dark:text-anvil-50">
          {title}
        </h1>

        <div className="flex flex-wrap items-center gap-3">
          <DifficultyBadge difficulty={difficulty} />

          {tags.map((tag) => (
            <span
              key={tag}
              className="rounded-md bg-forge-100 px-2 py-0.5 text-xs font-medium text-forge-700 dark:bg-forge-900/40 dark:text-forge-300"
            >
              {tag}
            </span>
          ))}
        </div>

        <div className="mt-3 flex items-center gap-4 text-xs text-anvil-500 dark:text-anvil-400">
          <span className="inline-flex items-center gap-1">
            <Clock className="h-3.5 w-3.5" />
            {timeLimit} ms
          </span>
          <span className="inline-flex items-center gap-1">
            <HardDrive className="h-3.5 w-3.5" />
            {memoryLimit} MB
          </span>
        </div>
      </header>

      {/* Statement */}
      <MarkdownRenderer content={statement} />
    </article>
  );
}
