'use client';

import { useEffect } from 'react';
import Link from 'next/link';
import { AlertTriangle, ArrowLeft, RotateCcw } from 'lucide-react';

export default function ProblemDetailError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error('[ProblemDetail] Uncaught error:', error);
  }, [error]);

  return (
    <div className="forge-page">
      <div className="flex flex-col items-center justify-center gap-4 py-20">
        <AlertTriangle className="h-12 w-12 text-danger-400" />
        <p className="text-lg font-semibold text-danger-600">页面发生错误</p>
        <pre className="max-w-xl overflow-x-auto rounded-lg bg-anvil-100 p-4 text-sm text-danger-600 dark:bg-anvil-800 dark:text-danger-400">
          {error.message}
        </pre>
        <div className="flex gap-3">
          <button onClick={reset} className="forge-btn-secondary">
            <RotateCcw className="h-4 w-4" />
            重试
          </button>
          <Link href="/problems" className="forge-btn-secondary">
            <ArrowLeft className="h-4 w-4" />
            返回列表
          </Link>
        </div>
      </div>
    </div>
  );
}
