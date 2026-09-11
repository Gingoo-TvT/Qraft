'use client';

import Link from 'next/link';

import type { SimilarityNeighbor, SimilarityResult } from '@/lib/types';
import { cn } from '@/lib/utils';

interface Props {
  result?: SimilarityResult;
}

function isNeighbor(value: unknown): value is SimilarityNeighbor {
  if (!value || typeof value !== 'object') return false;
  const item = value as Record<string, unknown>;
  return (
    typeof item.id === 'string' &&
    typeof item.title === 'string' &&
    typeof item.similarity === 'number'
  );
}

function normalizeResult(result?: SimilarityResult): SimilarityResult | null {
  if (!result || typeof result !== 'object') return null;

  const maxSimilarity =
    typeof result.max_similarity === 'number'
      ? result.max_similarity
      : undefined;
  const hardReject =
    typeof result.hard_reject === 'boolean' ? result.hard_reject : undefined;
  const warning =
    typeof result.warning === 'boolean' ? result.warning : undefined;
  const isDuplicate =
    typeof result.is_duplicate === 'boolean' ? result.is_duplicate : undefined;
  const duplicateOf =
    typeof result.duplicate_of === 'string' ? result.duplicate_of : undefined;
  const duplicateReason =
    typeof result.duplicate_reason === 'string'
      ? result.duplicate_reason
      : undefined;
  const neighbors = Array.isArray(result.neighbors)
    ? result.neighbors.filter(isNeighbor)
    : undefined;

  if (
    maxSimilarity === undefined &&
    hardReject === undefined &&
    warning === undefined &&
    isDuplicate === undefined &&
    duplicateOf === undefined &&
    duplicateReason === undefined &&
    (!neighbors || neighbors.length === 0)
  ) {
    return null;
  }

  return {
    max_similarity: maxSimilarity,
    hard_reject: hardReject,
    warning,
    is_duplicate: isDuplicate,
    duplicate_of: duplicateOf,
    duplicate_reason: duplicateReason,
    neighbors,
  };
}

function similarityColor(value: number) {
  if (value >= 0.88) return 'text-danger-600 dark:text-danger-400';
  if (value >= 0.8) return 'text-warning-600 dark:text-warning-400';
  return 'text-success-600 dark:text-success-400';
}

export default function SimilarityPanel({ result }: Props) {
  const safe = normalizeResult(result);
  if (!safe) return null;

  const percent =
    safe.max_similarity === undefined
      ? '-'
      : `${(safe.max_similarity * 100).toFixed(1)}%`;

  return (
    <div className="forge-card lg:col-span-2">
      <h2 className="forge-section-title mb-4">相似度检测</h2>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
        <div className="rounded-lg bg-anvil-50 p-4 dark:bg-anvil-800">
          <p className="text-xs text-anvil-500 dark:text-anvil-400">
            Max Similarity
          </p>
          <p
            className={cn(
              'mt-1 text-2xl font-bold',
              typeof safe.max_similarity === 'number' &&
                similarityColor(safe.max_similarity),
            )}
          >
            {percent}
          </p>
        </div>
        <div className="rounded-lg bg-anvil-50 p-4 dark:bg-anvil-800">
          <p className="text-xs text-anvil-500 dark:text-anvil-400">硬阻断</p>
          <span
            className={cn(
              'forge-badge mt-2',
              safe.hard_reject
                ? 'bg-danger-100 text-danger-700 dark:bg-danger-900/30 dark:text-danger-300'
                : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-700 dark:text-anvil-300',
            )}
          >
            {safe.hard_reject ? '硬阻断' : '通过'}
          </span>
        </div>
        <div className="rounded-lg bg-anvil-50 p-4 dark:bg-anvil-800">
          <p className="text-xs text-anvil-500 dark:text-anvil-400">警告</p>
          <span
            className={cn(
              'forge-badge mt-2',
              safe.warning
                ? 'bg-warning-100 text-warning-700 dark:bg-warning-900/30 dark:text-warning-300'
                : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-700 dark:text-anvil-300',
            )}
          >
            {safe.warning ? '警告' : '无'}
          </span>
        </div>
        <div className="rounded-lg bg-anvil-50 p-4 dark:bg-anvil-800">
          <p className="text-xs text-anvil-500 dark:text-anvil-400">重复判定</p>
          <span
            className={cn(
              'forge-badge mt-2',
              safe.is_duplicate
                ? 'bg-danger-100 text-danger-700 dark:bg-danger-900/30 dark:text-danger-300'
                : 'bg-success-100 text-success-700 dark:bg-success-900/30 dark:text-success-300',
            )}
          >
            {safe.is_duplicate ? '判定为重复' : '未重复'}
          </span>
        </div>
      </div>

      {safe.is_duplicate && (
        <div className="mt-4 rounded-lg border border-danger-300 bg-danger-50 p-4 text-sm dark:border-danger-500/40 dark:bg-danger-500/10">
          {safe.duplicate_of && (
            <p>
              重复对象：
              <Link
                className="font-mono text-forge-600 hover:underline dark:text-forge-400"
                href={`/problems/${safe.duplicate_of}`}
              >
                {safe.duplicate_of}
              </Link>
            </p>
          )}
          {safe.duplicate_reason && (
            <p className="mt-2 whitespace-pre-wrap text-danger-700 dark:text-danger-300">
              {safe.duplicate_reason}
            </p>
          )}
        </div>
      )}

      {safe.neighbors && safe.neighbors.length > 0 && (
        <div className="mt-5 overflow-x-auto rounded-lg border border-anvil-200 dark:border-anvil-700">
          <table className="forge-table">
            <thead>
              <tr>
                <th>标题</th>
                <th>Hint</th>
                <th>Tags</th>
                <th className="w-28">相似度</th>
              </tr>
            </thead>
            <tbody>
              {safe.neighbors.map((neighbor) => (
                <tr key={neighbor.id}>
                  <td>
                    <Link
                      href={`/problems/${neighbor.id}`}
                      className="font-medium text-forge-600 hover:underline dark:text-forge-400"
                    >
                      {neighbor.title}
                    </Link>
                  </td>
                  <td className="text-sm text-anvil-500 dark:text-anvil-400">
                    {neighbor.one_line_hint ?? '-'}
                  </td>
                  <td>
                    <div className="flex flex-wrap gap-1">
                      {(neighbor.tags ?? []).map((tag) => (
                        <span
                          key={tag}
                          className="forge-badge bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300"
                        >
                          {tag}
                        </span>
                      ))}
                    </div>
                  </td>
                  <td className={cn('font-semibold', similarityColor(neighbor.similarity))}>
                    {(neighbor.similarity * 100).toFixed(1)}%
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
