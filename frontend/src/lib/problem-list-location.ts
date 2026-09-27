import { DEFAULT_PAGE_SIZE } from './constants';
import type { ProblemFilter } from './types';

export function problemFilterFromQuery(query: string): ProblemFilter {
  const params = new URLSearchParams(query);
  const integer = (key: string, min: number, max: number) => {
    const raw = params.get(key);
    if (!raw || !/^\d+$/.test(raw)) return undefined;
    const value = Number(raw);
    return Number.isSafeInteger(value) && value >= min && value <= max ? value : undefined;
  };
  const level = params.get('level');
  const status = params.get('status');
  const sort = params.get('sort_by');
  return {
    page: integer('page', 1, 1000000) ?? 1,
    size: integer('size', 1, 100) ?? DEFAULT_PAGE_SIZE,
    search: params.get('search') || undefined,
    level: ['syntax', 'algorithm', 'gplt_l1', 'gplt_l2', 'gplt_l3'].includes(level ?? '') ? level as ProblemFilter['level'] : undefined,
    status: ['draft', 'generating', 'review', 'published', 'rejected', 'quarantined'].includes(status ?? '') ? status as ProblemFilter['status'] : undefined,
    difficulty_min: integer('difficulty_min', 800, 3500),
    difficulty_max: integer('difficulty_max', 800, 3500),
    tags: params.getAll('tags').filter(Boolean),
    sort_by: ['created_at', 'difficulty', 'title', 'serial_number'].includes(sort ?? '') ? sort as ProblemFilter['sort_by'] : 'created_at',
    sort_order: params.get('sort_order') === 'asc' ? 'asc' : 'desc',
  };
}

export function problemListHref(filter: ProblemFilter, targetSet?: string): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) {
    if (key === 'tags') {
      for (const tag of filter.tags ?? []) query.append(key, tag);
    } else if (value !== undefined && value !== '') {
      query.set(key, String(value));
    }
  }
  if (targetSet) query.set('set', targetSet);
  return '/problems' + (query.size ? '?' + query.toString() : '');
}

export function problemListReturnTo(value: string | null): string {
  // Only this list may be used as a return destination, never an external URL.
  return value && /^\/problems(?:\?|$)/.test(value) && !/[\\\r\n]/.test(value) ? value : '/problems';
}

export function problemDetailHref(id: string, returnTo: string, edit = false): string {
  return '/problems/' + encodeURIComponent(id) + (edit ? '/edit' : '')
    + '?returnTo=' + encodeURIComponent(problemListReturnTo(returnTo));
}
