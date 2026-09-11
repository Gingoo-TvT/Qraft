// ============================================================================
// Qraft - Utility Functions
// ============================================================================

import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';
import type { ProblemLevel, ProblemStatus } from './types';

/**
 * Merge class names using clsx + tailwind-merge to handle Tailwind conflicts.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/**
 * Format an ISO date string into a human-readable locale string.
 */
export function formatDate(
  dateString: string,
  options?: Intl.DateTimeFormatOptions,
): string {
  try {
    const date = new Date(dateString);
    return date.toLocaleDateString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      ...options,
    });
  } catch {
    return dateString;
  }
}

/**
 * Returns a relative time string (e.g. "3 分钟前").
 */
export function formatRelativeTime(dateString: string): string {
  const now = Date.now();
  const then = new Date(dateString).getTime();
  const diffMs = now - then;

  if (diffMs < 0) return '刚刚';

  const seconds = Math.floor(diffMs / 1000);
  if (seconds < 60) return '刚刚';

  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;

  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;

  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;

  return formatDate(dateString, { hour: undefined, minute: undefined });
}

/**
 * Returns a Tailwind color class string based on the difficulty rating.
 */
export function difficultyColor(difficulty: number): string {
  if (difficulty < 800) return 'text-gray-500';
  if (difficulty < 1200) return 'text-green-500';
  if (difficulty < 1600) return 'text-cyan-500';
  if (difficulty < 2100) return 'text-blue-500';
  if (difficulty < 2600) return 'text-purple-500';
  if (difficulty < 3000) return 'text-orange-500';
  return 'text-red-600';
}

/**
 * Returns a Tailwind bg + text color class for the problem level badge.
 */
export function levelBadgeColor(level: ProblemLevel): string {
  switch (level) {
    case 'syntax':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400';
    case 'algorithm':
      return 'bg-violet-100 text-violet-700 dark:bg-violet-900/30 dark:text-violet-400';
    case 'gplt_l1':
      return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400';
    case 'gplt_l2':
      return 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-400';
    case 'gplt_l3':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400';
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300';
  }
}

/**
 * Returns a Tailwind bg + text color class for the problem status badge.
 */
export function statusBadgeColor(status: ProblemStatus | string): string {
  switch (status) {
    case 'draft':
      return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400';
    case 'generating':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400';
    case 'review':
    case 'waiting_review':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400';
    case 'quarantined':
    case 'rejected_quarantined':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400';
    case 'published':
    case 'approved':
      return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400';
    case 'rejected':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400';
    case 'running':
      return 'bg-cyan-100 text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400';
    case 'failed':
      return 'bg-red-100 text-red-600 dark:bg-red-900/30 dark:text-red-400';
    case 'cancelled':
      return 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-500';
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400';
  }
}

/**
 * Truncate a string to a maximum length, appending ellipsis if truncated.
 */
export function truncate(text: string, len: number): string {
  if (!text) return '';
  if (text.length <= len) return text;
  return text.slice(0, len).trimEnd() + '...';
}

/**
 * Generate a short random ID (for notifications, temp keys, etc.).
 */
export function generateId(): string {
  return Math.random().toString(36).substring(2, 10) + Date.now().toString(36);
}

/**
 * Sleep helper for async flows.
 */
export function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Build a query string from a params object, omitting undefined/null values.
 */
export function buildQueryString(params: Record<string, unknown>): string {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    if (Array.isArray(value)) {
      value.forEach((v) => searchParams.append(key, String(v)));
    } else {
      searchParams.set(key, String(value));
    }
  }
  const qs = searchParams.toString();
  return qs ? `?${qs}` : '';
}
