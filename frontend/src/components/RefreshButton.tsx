'use client';

import { RefreshCw } from 'lucide-react';

import { cn } from '@/lib/utils';

interface Props {
  onClick: () => void | Promise<void>;
  loading?: boolean;
  label?: string;
  size?: 'sm' | 'md';
  variant?: 'primary' | 'secondary' | 'ghost';
  className?: string;
}

export default function RefreshButton({
  onClick,
  loading = false,
  label = '刷新',
  size = 'md',
  variant = 'secondary',
  className,
}: Props) {
  const variantClass =
    variant === 'primary'
      ? 'forge-btn-primary'
      : variant === 'ghost'
        ? 'forge-btn-ghost'
        : 'forge-btn-secondary';

  return (
    <button
      type="button"
      className={cn(
        variantClass,
        size === 'sm' && 'px-2 py-1 text-xs',
        className,
      )}
      onClick={() => {
        void onClick();
      }}
      disabled={loading}
      aria-label={label}
    >
      <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />
      <span className={cn(size === 'sm' && 'sr-only')}>{label}</span>
    </button>
  );
}
