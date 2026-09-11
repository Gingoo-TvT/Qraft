'use client';

import { useCallback, useEffect, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  CircleSlash2,
  ExternalLink,
  Server,
  ShieldCheck,
  XCircle,
} from 'lucide-react';

import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import RefreshButton from '@/components/RefreshButton';
import { getIntegrationCapabilities } from '@/lib/api';
import type {
  IntegrationCapabilities,
  IntegrationExportCapability,
  IntegrationRolloutCapability,
} from '@/lib/types';
import { cn } from '@/lib/utils';

function StatusBadge({ enabled }: { enabled: boolean }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium',
        enabled
          ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300'
          : 'bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300',
      )}
    >
      {enabled ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
      {enabled ? '已启用' : '未启用'}
    </span>
  );
}

function CapabilityCard({
  title,
  description,
  capability,
}: {
  title: string;
  description: string;
  capability: IntegrationRolloutCapability | IntegrationExportCapability;
}) {
  return (
    <section className="af-panel flex flex-col gap-5">
      <SectionHeading title={title} description={description} actions={<StatusBadge enabled={'enabled' in capability ? capability.enabled : capability.hydro_routes_enabled || capability.portable_sets_enabled} />} />

      <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
        <div>
          <dt className="text-anvil-500 dark:text-anvil-400">当前模式</dt>
          <dd className="mt-0.5 font-mono text-xs">{capability.mode}</dd>
        </div>
        <div>
          <dt className="text-anvil-500 dark:text-anvil-400">回退模式</dt>
          <dd className="mt-0.5 font-mono text-xs">{capability.rollback_mode}</dd>
        </div>
      </dl>

      <details className="af-form-details"><summary>查看接口路径<span className="ml-1 text-xs text-[var(--dm)]">{capability.routes.length} 项</span></summary><div>

        {capability.routes.length > 0 ? (
          <ul className="space-y-1.5">
            {capability.routes.map((route) => (
              <li key={route} className="flex items-start gap-2 text-xs text-anvil-700 dark:text-anvil-300">
                <ExternalLink className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-forge-500" />
                <code className="break-all">{route}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-anvil-400">当前模式未开放产品路由</p>
        )}
      </div></details>

      {capability.flag_name && <p className="border-t border-anvil-100 pt-3 text-xs text-anvil-500 dark:border-anvil-800 dark:text-anvil-400">
        配置项：<code>{capability.flag_name}</code>；修改后需重启服务
      </p>}
    </section>
  );
}

function LoadingState() {
  return (
    <div className="mb-6 grid grid-cols-1 gap-5 lg:grid-cols-2">
      {[0, 1, 2, 3].map((item) => (
        <div key={item} className="af-panel h-56 animate-pulse" />
      ))}
    </div>
  );
}

export default function IntegrationCapabilitiesPanel() {
  const [capabilities, setCapabilities] = useState<IntegrationCapabilities | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError(null);
    try {
      setCapabilities(await getIntegrationCapabilities(signal));
    } catch (err) {
      if (signal?.aborted) return;
      setError(err instanceof Error ? err.message : '加载服务能力失败');
    } finally {
      if (!signal?.aborted) setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  return (
    <div className="af-page">
      <PageHeader title="服务能力" description="查看当前服务开放的生成、组卷与导出能力，按实际接口状态对接。" actions={<RefreshButton onClick={() => load()} loading={loading} />} />

      {error && (
        <div role="alert" className="mb-6 flex items-start gap-3 rounded-lg border border-warning-200 bg-warning-50 p-4 text-sm text-warning-800 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-200">
          <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0" />
          <div>
            <p className="font-medium">服务能力读取失败</p>
            <p className="mt-1">{error}</p>
          </div>
        </div>
      )}

      {loading && !capabilities ? <LoadingState /> : capabilities ? (
        <>
          <div className="af-panel mb-6 flex flex-wrap items-center gap-3 text-sm">
            <Server className="h-5 w-5" />
            <span>
              服务版本：<strong>{capabilities.release_version || '未提供'}</strong>；能力文档：<code>{capabilities.schema_version}</code>
            </span>
            <span className="text-xs text-[var(--dm)]">状态来自服务启动时解析的配置</span>
          </div>

          <div className="mb-6 grid grid-cols-1 gap-5 lg:grid-cols-2">
            <CapabilityCard
              title="题目生成接口"
              description="标准任务、进度与生成证据等级"
              capability={capabilities.generation_jobs}
            />
            <CapabilityCard
              title="多样性批次生成"
              description="同一需求的多候选与批次编排"
              capability={capabilities.generation_micro_batches}
            />
            <CapabilityCard
              title="质量与校验"
              description="质量报告、审计与批次清单"
              capability={capabilities.quality_layer}
            />
            <CapabilityCard
              title="导出与校验"
              description="Hydro 编程包、Qraft 题集与客观题工作簿"
              capability={capabilities.exports}
            />
          </div>

          {capabilities.problem_sets && (
            <section className="af-panel mb-6 space-y-4">
              <SectionHeading title="题集生成与题库组卷" />
              <p className="text-sm text-anvil-500">支持纯编程比赛和编程、选择、填空、判断混合题集，共 {capabilities.problem_sets.min_item_count}–{capabilities.problem_sets.max_item_count} 道。</p>
              <div className="grid gap-4 md:grid-cols-3">
                {[
                  { title: '自动生成', value: capabilities.problem_sets.generation },
                  { title: '题库组卷', value: capabilities.problem_sets.assembly },
                  { title: '整套题集导出', value: capabilities.problem_sets.export },
                ].map(({ title, value }) => (
                  <div key={title} className="space-y-2">
                    <div className="flex items-center gap-3"><h3 className="text-sm font-medium">{title}</h3><StatusBadge enabled={value.enabled} /></div>
                    <ul className="space-y-1">{value.routes.map(route => <li key={route}><code className="break-all text-xs">{route}</code></li>)}</ul>
                  </div>
                ))}
              </div>
              <p className="text-sm text-anvil-500">组卷使用已有题目，无需模型；生成可查询进度、停止并补齐未完成题目。导出包保留题目顺序、分区和分值，包含 JSON 清单、Hydro 编程包与客观题工作簿。</p>
            </section>
          )}

          <div className="mb-6 grid grid-cols-1 gap-5 lg:grid-cols-2">
            <section className="af-panel">
              <h2 className="flex items-center gap-2 text-base font-semibold">
                <ShieldCheck className="h-5 w-5 text-forge-500" />
                证据与校验能力
              </h2>
              <dl className="mt-4 space-y-3 text-sm">
                <div className="flex items-start justify-between gap-4">
                  <dt className="text-anvil-500 dark:text-anvil-400">生成证据等级</dt>
                  <dd className="text-right font-mono text-xs">{capabilities.generation_evidence_levels.join(', ') || '无'}</dd>
                </div>
                <div className="flex items-center justify-between gap-4">
                  <dt className="text-anvil-500 dark:text-anvil-400">Hydro 扩展能力</dt>
                  <dd className="flex items-center gap-1.5 text-xs">
                    {capabilities.hydro_phase2_enabled ? <CheckCircle2 className="h-4 w-4 text-success-500" /> : <CircleSlash2 className="h-4 w-4 text-anvil-400" />}
                    {capabilities.hydro_phase2_enabled ? '已启用' : '未启用'}
                  </dd>
                </div>
              </dl>
            </section>

            <section className="af-panel">
              <h2 className="text-base font-semibold">可携带的内容格式</h2>
              <p className="mt-4 text-sm leading-7 text-anvil-500">编程题可导出 Hydro 包；整套题集以 Qraft ZIP 保存题目顺序、分区、分值与内容；客观题支持 Excel 导入导出。模板由项目自身生成，空白模板不包含示例题。</p>
            </section>
          </div>
          <details className="af-form-details"><summary>完整能力响应<span className="ml-2 text-xs text-[var(--dm)]">供接口对接与排查使用</span></summary><div><pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs text-[var(--dm)]">{JSON.stringify(capabilities, null, 2)}</pre></div></details>
        </>
      ) : null}
    </div>
  );
}
