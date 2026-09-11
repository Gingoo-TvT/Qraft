'use client';

import { ChevronLeft, ChevronRight } from 'lucide-react';

import { PAGE_SIZE_OPTIONS } from '@/lib/constants';
import { cn } from '@/lib/utils';

interface PaginationProps {
  page: number;
  total: number;
  size: number;
  onPageChange: (page: number) => void;
  onSizeChange: (size: number) => void;
}

export default function Pagination({
  page,
  total,
  size,
  onPageChange,
  onSizeChange,
}: PaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / size));

  return (
    <div className="flex flex-wrap items-center justify-between gap-4 pt-4">
      <div className="flex items-center gap-2 text-sm text-anvil-500 dark:text-anvil-400">
        <span>
          共 {total} 条，第 {page}/{totalPages} 页
        </span>
        <select
          className="forge-input w-auto py-1 text-xs"
          value={size}
          onChange={(e) => onSizeChange(Number(e.target.value))}
        >
          {PAGE_SIZE_OPTIONS.map((s) => (
            <option key={s} value={s}>
              {s} 条/页
            </option>
          ))}
        </select>
      </div>

      <div className="flex items-center gap-1">
        <button
          className="forge-btn-ghost px-2 py-1"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          <ChevronLeft className="h-4 w-4" />
        </button>

        {Array.from({ length: Math.min(5, totalPages) }, (_, i) => {
          let pageNum: number;
          if (totalPages <= 5) {
            pageNum = i + 1;
          } else if (page <= 3) {
            pageNum = i + 1;
          } else if (page >= totalPages - 2) {
            pageNum = totalPages - 4 + i;
          } else {
            pageNum = page - 2 + i;
          }
          return (
            <button
              key={pageNum}
              className={cn(
                'forge-btn-ghost px-3 py-1 text-sm',
                pageNum === page &&
                  'bg-forge-600 text-white hover:bg-forge-700 dark:bg-forge-600 dark:text-white',
              )}
              onClick={() => onPageChange(pageNum)}
            >
              {pageNum}
            </button>
          );
        })}

        <button
          className="forge-btn-ghost px-2 py-1"
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}
