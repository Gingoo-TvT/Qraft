'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import { ChevronDown } from 'lucide-react';

import type { ImportReport } from '@/lib/types';
import { cn } from '@/lib/utils';

interface Props {
  report: ImportReport;
  subject: string;
}

export default function ImportReportPanel({ report, subject }: Props) {
  const router = useRouter();
  const [open, setOpen] = useState(report.failed_rows.length > 0);
  const total = report.success_count + report.failed_count;

  return (
    <div className="forge-card space-y-5">
      <div>
        <h2 className="forge-section-title">导入结果</h2>
        <p className="mt-1 text-sm text-anvil-500 dark:text-anvil-400">
          已完成 Excel 解析与写入。
        </p>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="rounded-lg bg-success-50 p-4 text-success-700 dark:bg-success-500/10 dark:text-success-300">
          <p className="text-xs font-medium">成功</p>
          <p className="mt-1 text-2xl font-bold">{report.success_count}</p>
        </div>
        <div className="rounded-lg bg-danger-50 p-4 text-danger-700 dark:bg-danger-500/10 dark:text-danger-300">
          <p className="text-xs font-medium">失败</p>
          <p className="mt-1 text-2xl font-bold">{report.failed_count}</p>
        </div>
        <div className="rounded-lg bg-anvil-100 p-4 text-anvil-700 dark:bg-anvil-800 dark:text-anvil-200">
          <p className="text-xs font-medium">总计</p>
          <p className="mt-1 text-2xl font-bold">{total}</p>
        </div>
      </div>

      {report.failed_rows.length > 0 && (
        <div className="rounded-lg border border-anvil-200 dark:border-anvil-700">
          <button
            type="button"
            className="flex w-full items-center justify-between px-4 py-3 text-left text-sm font-medium"
            onClick={() => setOpen((prev) => !prev)}
          >
            失败明细
            <ChevronDown
              className={cn('h-4 w-4 transition-transform', open && 'rotate-180')}
            />
          </button>
          {open && (
            <div className="overflow-x-auto border-t border-anvil-200 dark:border-anvil-700">
              <table className="forge-table">
                <thead>
                  <tr>
                    <th className="w-24">行号</th>
                    <th className="w-32">编号</th>
                    <th>原因</th>
                  </tr>
                </thead>
                <tbody>
                  {report.failed_rows.map((row) => (
                    <tr key={`${row.row_index}-${row.code}`}>
                      <td>{row.row_index}</td>
                      <td className="font-mono text-sm">{row.code || '-'}</td>
                      <td className="text-danger-600 dark:text-danger-400">
                        {row.reason}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      <div className="flex justify-end">
        <button
          type="button"
          className="forge-btn-primary"
          onClick={() =>
            router.push(`/quizzes?subject=${encodeURIComponent(subject)}`)
          }
        >
          前往题库 →
        </button>
      </div>
    </div>
  );
}
