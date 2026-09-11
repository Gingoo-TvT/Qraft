'use client';

import React, { useMemo } from 'react';
import {
  PieChart,
  Pie,
  Cell,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  Legend,
  ResponsiveContainer,
} from 'recharts';
import type { TestGroup } from '@/lib/types';
import { cn } from '@/lib/utils';

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

export interface ScoreDistributionProps {
  groups: TestGroup[];
}

// ---------------------------------------------------------------------------
// Color palette using the forge/ember/anvil theme
// ---------------------------------------------------------------------------

const CHART_COLORS = [
  '#2d73ff', // forge-500
  '#f97316', // ember-500
  '#10b981', // success-500
  '#8b5cf6', // purple
  '#ec4899', // pink
  '#14b8a6', // teal
  '#eab308', // yellow
  '#ef4444', // red
  '#06b6d4', // cyan
  '#6366f1', // indigo
];

function getColor(index: number): string {
  return CHART_COLORS[index % CHART_COLORS.length];
}

// ---------------------------------------------------------------------------
// Component
// ---------------------------------------------------------------------------

export default function ScoreDistribution({ groups }: ScoreDistributionProps) {
  const totalScore = useMemo(
    () => groups.reduce((sum, g) => sum + g.points, 0),
    [groups],
  );

  const chartData = useMemo(
    () =>
      groups.map((g, i) => ({
        name: g.name || `组 ${i + 1}`,
        score: g.points,
        count: g.count,
        percentage: totalScore > 0 ? Math.round((g.points / totalScore) * 100) : 0,
        fill: getColor(i),
      })),
    [groups, totalScore],
  );

  if (groups.length === 0) {
    return (
      <div className="rounded-lg border-2 border-dashed border-anvil-200 dark:border-anvil-700 p-8 text-center">
        <p className="text-sm text-anvil-500 dark:text-anvil-400">
          添加测试组后，可查看分值分布。
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-anvil-800 dark:text-anvil-200">
          分值分布
        </h3>
        <span className="text-sm font-bold text-forge-600 dark:text-forge-400">
          总分：{totalScore} 分
        </span>
      </div>

      {/* Charts container */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {/* Pie Chart */}
        <div className="rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-900 p-4">
          <ResponsiveContainer width="100%" height={220}>
            <PieChart>
              <Pie
                data={chartData}
                cx="50%"
                cy="50%"
                innerRadius={50}
                outerRadius={80}
                paddingAngle={2}
                dataKey="score"
                label={({ name, percentage }) => `${name} (${percentage}%)`}
                labelLine={{ stroke: 'var(--dl, #9fa2a9)', strokeWidth: 1 }}
              >
                {chartData.map((entry, index) => (
                  <Cell key={`cell-${index}`} fill={entry.fill} />
                ))}
              </Pie>
              <Tooltip
                formatter={(value: number, name: string) => [`${value} 分`, name]}
                contentStyle={{
                  backgroundColor: 'var(--dp, #34363b)',
                  border: '1px solid var(--dl, #4b4e57)',
                  borderRadius: '6px',
                  fontSize: '12px',
                  color: 'var(--dt, #e2e3e5)',
                }}
              />
            </PieChart>
          </ResponsiveContainer>
        </div>

        {/* Bar Chart */}
        <div className="rounded-lg border border-anvil-200 dark:border-anvil-700 bg-white dark:bg-anvil-900 p-4">
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={chartData} margin={{ top: 5, right: 10, left: 0, bottom: 5 }}>
              <XAxis
                dataKey="name"
                tick={{ fontSize: 12, fill: 'var(--dm, #7a7e87)' }}
                axisLine={{ stroke: 'var(--dl, #c4c6cb)' }}
                tickLine={false}
              />
              <YAxis
                tick={{ fontSize: 12, fill: 'var(--dm, #7a7e87)' }}
                axisLine={{ stroke: 'var(--dl, #c4c6cb)' }}
                tickLine={false}
              />
              <Tooltip
                formatter={(value: number, name: string) => {
                  if (name === 'score') return [`${value} 分`, '分值'];
                  return [value, name];
                }}
                contentStyle={{
                  backgroundColor: 'var(--dp, #34363b)',
                  border: '1px solid var(--dl, #4b4e57)',
                  borderRadius: '6px',
                  fontSize: '12px',
                  color: 'var(--dt, #e2e3e5)',
                }}
              />
              <Bar dataKey="score" radius={[4, 4, 0, 0]}>
                {chartData.map((entry, index) => (
                  <Cell key={`bar-${index}`} fill={entry.fill} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Legend / details table */}
      <div className="rounded-lg border border-anvil-200 dark:border-anvil-700 overflow-hidden">
        <table className="w-full text-left">
          <thead>
            <tr className="bg-anvil-50 dark:bg-anvil-800 border-b border-anvil-200 dark:border-anvil-700">
              <th className="px-3 py-2 text-xs font-semibold text-anvil-500 dark:text-anvil-400">
                测试组
              </th>
              <th className="px-3 py-2 text-xs font-semibold text-anvil-500 dark:text-anvil-400 text-right">
                测试点数
              </th>
              <th className="px-3 py-2 text-xs font-semibold text-anvil-500 dark:text-anvil-400 text-right">
                分值
              </th>
              <th className="px-3 py-2 text-xs font-semibold text-anvil-500 dark:text-anvil-400 text-right">
                占比
              </th>
              <th className="px-3 py-2 text-xs font-semibold text-anvil-500 dark:text-anvil-400 w-32">
                分布
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-anvil-100 dark:divide-anvil-800">
            {chartData.map((row, index) => (
              <tr key={index} className="hover:bg-anvil-50 dark:hover:bg-anvil-800/50 transition-colors">
                <td className="px-3 py-2 text-sm text-anvil-800 dark:text-anvil-200">
                  <div className="flex items-center gap-2">
                    <span
                      className="inline-block h-3 w-3 rounded-sm shrink-0"
                      style={{ backgroundColor: row.fill }}
                    />
                    {row.name}
                  </div>
                </td>
                <td className="px-3 py-2 text-sm text-anvil-600 dark:text-anvil-400 text-right font-mono">
                  {row.count}
                </td>
                <td className="px-3 py-2 text-sm text-anvil-800 dark:text-anvil-200 text-right font-semibold font-mono">
                  {row.score}
                </td>
                <td className="px-3 py-2 text-sm text-anvil-600 dark:text-anvil-400 text-right font-mono">
                  {row.percentage}%
                </td>
                <td className="px-3 py-2">
                  <div className="h-2 w-full rounded-full bg-anvil-100 dark:bg-anvil-800 overflow-hidden">
                    <div
                      className="h-full rounded-full transition-all duration-300"
                      style={{
                        width: `${row.percentage}%`,
                        backgroundColor: row.fill,
                      }}
                    />
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="bg-anvil-50 dark:bg-anvil-800 border-t border-anvil-200 dark:border-anvil-700">
              <td className="px-3 py-2 text-xs font-semibold text-anvil-600 dark:text-anvil-400">
                合计
              </td>
              <td className="px-3 py-2 text-xs text-anvil-600 dark:text-anvil-400 text-right font-mono font-semibold">
                {groups.reduce((s, g) => s + g.count, 0)}
              </td>
              <td className="px-3 py-2 text-xs text-forge-600 dark:text-forge-400 text-right font-mono font-bold">
                {totalScore}
              </td>
              <td className="px-3 py-2 text-xs text-anvil-600 dark:text-anvil-400 text-right font-mono">
                100%
              </td>
              <td />
            </tr>
          </tfoot>
        </table>
      </div>
    </div>
  );
}
