'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  Cpu,
  Database,
  Loader2,
  Plug,
  RefreshCw,
  Rocket,
  ShieldCheck,
} from 'lucide-react';

import {
  activateLocalEmbeddingModel,
  backfillLocalEmbeddingModel,
  deployLocalEmbeddingModel,
  getEmbeddingRuntimeSettings,
  getEmbeddingStatus,
  testLocalEmbeddingEndpoint,
} from '@/lib/api';
import type {
  ActiveEmbeddingModelStatus,
  EmbeddingKind,
  EmbeddingRuntimeSettings,
  LocalEmbeddingActivateResult,
  LocalEmbeddingBackfillResult,
  LocalEmbeddingDeployResult,
  LocalEmbeddingEndpointConfig,
  LocalEmbeddingTestResult,
} from '@/lib/types';
import { PageHeader, SectionHeading } from '@/components/ui/Workspace';
import { cn } from '@/lib/utils';

type PendingAction =
  | 'status'
  | 'test'
  | 'deploy'
  | 'deploy-activate'
  | 'backfill'
  | 'activate-dry-run'
  | 'activate'
  | null;

const DEFAULT_ENDPOINT: LocalEmbeddingEndpointConfig = {
  base_url: 'http://127.0.0.1:8000/v1',
  model: 'local-embedding',
  api_key: '',
};

const KIND_LABELS: Record<EmbeddingKind, string> = {
  statement: '题面去重',
  solution: '题解去重',
};

function toErrorMessage(err: unknown): string {
  return err instanceof Error ? err.message : '操作失败';
}

function formatTime(value?: string): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { hour12: false });
}

function formatNumber(value: number | undefined): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '-';
  return new Intl.NumberFormat('zh-CN').format(value);
}

function StatusBadge({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone: 'success' | 'warning' | 'neutral' | 'danger';
}) {
  return (
    <span
      className={cn(
        'inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium',
        tone === 'success' &&
          'bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400',
        tone === 'warning' &&
          'bg-warning-50 text-warning-600 dark:bg-warning-500/10 dark:text-warning-400',
        tone === 'danger' &&
          'bg-danger-50 text-danger-600 dark:bg-danger-500/10 dark:text-danger-400',
        tone === 'neutral' &&
          'bg-anvil-100 text-anvil-600 dark:bg-anvil-800 dark:text-anvil-300',
      )}
    >
      {children}
    </span>
  );
}

function FieldLabel({
  htmlFor,
  children,
}: {
  htmlFor: string;
  children: React.ReactNode;
}) {
  return (
    <label
      htmlFor={htmlFor}
      className="mb-2 block text-sm font-medium text-[var(--dt)]"
    >
      {children}
    </label>
  );
}

function ResultBlock({
  title,
  value,
}: {
  title: string;
  value: unknown;
}) {
  return (
    <details className="af-form-details">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3 text-sm font-medium text-anvil-800 dark:text-anvil-100">
        <span>{title}</span>
        <span className="text-xs text-anvil-400 group-open:hidden">展开</span>
        <span className="hidden text-xs text-anvil-400 group-open:inline">
          收起
        </span>
      </summary>
      <pre className="max-h-72 overflow-auto border-t border-anvil-200 bg-anvil-50 p-4 text-xs text-anvil-700 dark:border-anvil-700 dark:bg-anvil-950 dark:text-anvil-300">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  );
}

function ActionButton({
  icon: Icon,
  label,
  pending,
  variant,
  disabled,
  onClick,
}: {
  icon: typeof Plug;
  label: string;
  pending: boolean;
  variant: 'primary' | 'secondary' | 'danger';
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={pending || disabled}
      className={cn(
        variant === 'primary' && 'forge-btn-primary',
        variant === 'secondary' && 'forge-btn-secondary',
        variant === 'danger' && 'forge-btn-danger',
        'w-full min-w-0',
      )}
    >
      {pending ? (
        <Loader2 className="h-4 w-4 flex-shrink-0 animate-spin" />
      ) : (
        <Icon className="h-4 w-4 flex-shrink-0" />
      )}
      <span className="truncate">{label}</span>
    </button>
  );
}

export default function EmbeddingAdminPage() {
  const [endpoint, setEndpoint] =
    useState<LocalEmbeddingEndpointConfig>(DEFAULT_ENDPOINT);

  const [statuses, setStatuses] = useState<ActiveEmbeddingModelStatus[]>([]);
  const [runtimeSettings, setRuntimeSettings] =
    useState<EmbeddingRuntimeSettings | null>(null);
  const [pendingAction, setPendingAction] = useState<PendingAction>(null);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [testResult, setTestResult] =
    useState<LocalEmbeddingTestResult | null>(null);
  const [deployResult, setDeployResult] =
    useState<LocalEmbeddingDeployResult | null>(null);
  const [backfillResult, setBackfillResult] =
    useState<LocalEmbeddingBackfillResult | null>(null);
  const [activateResult, setActivateResult] =
    useState<LocalEmbeddingActivateResult | null>(null);

  const endpointPayload = useMemo<LocalEmbeddingEndpointConfig>(
    () => ({
      base_url: endpoint.base_url.trim(),
      model: endpoint.model.trim(),
      api_key: endpoint.api_key?.trim() || undefined,
    }),
    [endpoint],
  );

  const latestModelVersionID =
    deployResult?.model_version.id ||
    runtimeSettings?.statement_model_version_id ||
    '';

  const loadStatus = useCallback(async () => {
    setPendingAction('status');
    setErrorMessage(null);
    try {
      const [statusResponse, runtimeResponse] = await Promise.all([
        getEmbeddingStatus(),
        getEmbeddingRuntimeSettings(),
      ]);
      setStatuses(statusResponse.data ?? []);
      const savedRuntime = runtimeResponse.data ?? null;
      setRuntimeSettings(savedRuntime);
      if (savedRuntime) {
        setEndpoint((current) => ({
          ...current,
          base_url: savedRuntime.base_url,
          model: savedRuntime.model,
          api_key:
            current.api_key === DEFAULT_ENDPOINT.api_key
              ? ''
              : current.api_key,
        }));
      }
    } catch (err) {
      setErrorMessage(toErrorMessage(err));
      setStatuses([]);
      setRuntimeSettings(null);
    } finally {
      setPendingAction(null);
    }
  }, []);

  useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  function updateEndpoint<K extends keyof LocalEmbeddingEndpointConfig>(
    key: K,
    value: LocalEmbeddingEndpointConfig[K],
  ) {
    setEndpoint((current) => ({ ...current, [key]: value }));
  }

  async function runAction<T>(
    action: Exclude<PendingAction, null>,
    task: () => Promise<T>,
  ): Promise<T | null> {
    setPendingAction(action);
    setErrorMessage(null);
    try {
      return await task();
    } catch (err) {
      setErrorMessage(toErrorMessage(err));
      return null;
    } finally {
      setPendingAction(null);
    }
  }

  async function handleTest() {
    const result = await runAction('test', async () => {
      const res = await testLocalEmbeddingEndpoint(endpointPayload);
      return res.data ?? null;
    });
    if (result) setTestResult(result);
  }

  async function handleDeploy(activate: boolean) {
    const result = await runAction(activate ? 'deploy-activate' : 'deploy', async () => {
      const res = await deployLocalEmbeddingModel({
        endpoint: endpointPayload,
        activate,
      });
      return res.data ?? null;
    });
    if (result) {
      setDeployResult(result);
      setTestResult(result.test);
      if (result.runtime_settings_saved) {
        setEndpoint((current) => ({ ...current, api_key: '' }));
      }
      if (result.activation_reports) {
        setActivateResult({
          reports: result.activation_reports,
          runtime_matches: result.runtime_matches,
          requires_runtime_restart: result.requires_runtime_restart,
          activation_blocked_reason: result.activation_blocked_reason,
        });
      }
      await loadStatus();
    }
  }

  async function handleBackfill() {
    const id = latestModelVersionID;
    if (!id) {
      setErrorMessage('请先部署模型；模型版本 ID 会由系统自动生成');
      return;
    }
    const result = await runAction('backfill', async () => {
      const res = await backfillLocalEmbeddingModel({
        endpoint: endpointPayload,
        model_version_id: id,
      });
      return res.data ?? null;
    });
    if (result) setBackfillResult(result);
  }

  async function handleActivate(dryRun: boolean) {
    const id = latestModelVersionID;
    if (!id) {
      setErrorMessage('请先部署模型；模型版本 ID 会由系统自动生成');
      return;
    }
    const result = await runAction(dryRun ? 'activate-dry-run' : 'activate', async () => {
      const res = await activateLocalEmbeddingModel({
        model_version_id: id,
        dry_run: dryRun,
      });
      return res.data ?? null;
    });
    if (result) {
      setActivateResult(result);
      await loadStatus();
    }
  }

  const busy = pendingAction !== null;

  return (
    <div className="af-page">
      <PageHeader title="去重服务" description="配置向量模型，为题面和题解提供去重能力。版本身份由系统自动解析。" actions={
        <button type="button" onClick={loadStatus} disabled={busy} className="forge-btn-secondary">
          {pendingAction === 'status' ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}刷新状态
        </button>
      } />

      {errorMessage && (
        <div role="alert" className="mb-6 flex min-w-0 items-start gap-3 rounded-lg border border-danger-400/40 bg-danger-50 p-4 text-sm text-danger-600 dark:bg-danger-500/10 dark:text-danger-400">
          <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0" />
          <p className="min-w-0 break-words">{errorMessage}</p>
        </div>
      )}

      <div className="af-form-layout">
        <div className="af-form-main">
          <div className="af-panel">
            <SectionHeading title="连接配置" description="填写服务地址、模型名称与 API Key。其余参数由系统处理。" />
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div className="md:col-span-2">
                <FieldLabel htmlFor="base-url">Base URL</FieldLabel>
                <input
                  id="base-url"
                  className="forge-input font-mono"
                  value={endpoint.base_url}
                  onChange={(e) => updateEndpoint('base_url', e.target.value)}
                  placeholder="http://127.0.0.1:8000/v1"
                />
              </div>
              <div>
                <FieldLabel htmlFor="endpoint-model">模型名</FieldLabel>
                <input
                  id="endpoint-model"
                  className="forge-input"
                  value={endpoint.model}
                  onChange={(e) => updateEndpoint('model', e.target.value)}
                />
              </div>
              <div>
                <FieldLabel htmlFor="api-key">API Key</FieldLabel>
                <input
                  id="api-key"
                  className="forge-input font-mono"
                  type="password"
                  value={endpoint.api_key ?? ''}
                  onChange={(e) => updateEndpoint('api_key', e.target.value)}
                  autoComplete="off"
                />
                {runtimeSettings?.api_key_configured && (
                  <p className="mt-1 text-xs text-success-600 dark:text-success-400">
                    已配置匹配密钥；留空时自动使用
                  </p>
                )}
              </div>
            </div>
            <p className="mt-4 text-xs text-anvil-500 dark:text-anvil-400">
              向量维度、超时、测试文本和模型版本元数据由系统自动生成。
            </p>
          </div>

          <details className="af-form-details"><summary>高级运行信息</summary><div>
            <dl className="grid grid-cols-1 gap-4 text-sm md:grid-cols-2">
              <div>
                <dt className="text-anvil-400">提供方标识</dt>
                <dd className="mt-1 break-all font-mono text-xs">
                  {runtimeSettings?.provider_id || '连接测试后自动绑定'}
                </dd>
              </div>
              <div>
                <dt className="text-anvil-400">运行时维度 / 超时</dt>
                <dd className="mt-1 font-medium">
                  {runtimeSettings
                    ? `${runtimeSettings.dimensions} / ${runtimeSettings.timeout_sec}s`
                    : '1536 / 30s（默认）'}
                </dd>
              </div>
              <div className="md:col-span-2">
                <dt className="text-anvil-400">当前操作的模型版本 ID</dt>
                <dd className="mt-1 break-all font-mono text-xs">
                  {latestModelVersionID || '部署后自动生成；或从启动配置读取'}
                </dd>
              </div>
            </dl>
            <p className="mt-4 text-xs text-anvil-500 dark:text-anvil-400">
              回填与启用默认作用于 statement；批次上限、actor、reason 和审计哈希由后端生成。
            </p>
          </div></details>
        </div>

        <div className="af-form-rail">
          <div className="af-summary">
            <SectionHeading title="保存与启用" description="先测试连接，再部署所选模型。" />
            <div className="space-y-3"><ActionButton
                icon={Plug}
                label="测试连接"
                pending={pendingAction === 'test'}
                variant="secondary"
                onClick={handleTest}
              />
<ActionButton
                icon={CheckCircle2}
                label="部署并启用"
                pending={pendingAction === 'deploy-activate'}
                variant="primary"
                onClick={() => handleDeploy(true)}
              /></div>
            <details className="af-form-details mt-5"><summary>分步部署与维护</summary><div className="space-y-3"><ActionButton
                icon={Rocket}
                label="仅部署"
                pending={pendingAction === 'deploy'}
                variant="secondary"
                onClick={() => handleDeploy(false)}
              />
<ActionButton
                icon={Database}
                label="回填向量"
                pending={pendingAction === 'backfill'}
                variant="secondary"
                disabled={!latestModelVersionID}
                onClick={handleBackfill}
              />
<ActionButton
                icon={ShieldCheck}
                label="预演启用"
                pending={pendingAction === 'activate-dry-run'}
                variant="secondary"
                disabled={!latestModelVersionID}
                onClick={() => handleActivate(true)}
              />
<ActionButton
                icon={CheckCircle2}
                label="正式启用"
                pending={pendingAction === 'activate'}
                variant="danger"
                disabled={!latestModelVersionID}
                onClick={() => handleActivate(false)}
              /></div></details>

            {deployResult?.activation_blocked_reason && (
              <div className="mt-4 rounded-lg border border-warning-400/40 bg-warning-50 p-3 text-sm text-warning-600 dark:bg-warning-500/10 dark:text-warning-400">
                {deployResult.activation_blocked_reason}
              </div>
            )}
            {activateResult?.activation_blocked_reason && (
              <div className="mt-4 rounded-lg border border-warning-400/40 bg-warning-50 p-3 text-sm text-warning-600 dark:bg-warning-500/10 dark:text-warning-400">
                {activateResult.activation_blocked_reason}
              </div>
            )}
          </div>

          <div className="af-panel">
            <SectionHeading title="最近操作" />
            <div className="space-y-4">
              {testResult && (
                <div className="rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <span className="font-medium">连接测试</span>
                    <StatusBadge tone={testResult.ok ? 'success' : 'danger'}>
                      {testResult.ok ? '成功' : '失败'}
                    </StatusBadge>
                  </div>
                  <dl className="grid grid-cols-2 gap-3 text-sm">
                    <div>
                      <dt className="text-anvil-400">维度</dt>
                      <dd className="font-semibold">
                        {testResult.dimensions}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-anvil-400">延迟</dt>
                      <dd className="font-semibold">
                        {testResult.latency_ms} ms
                      </dd>
                    </div>
                    <div>
                      <dt className="text-anvil-400">向量范数</dt>
                      <dd className="font-semibold">
                        {testResult.vector_norm.toFixed(4)}
                      </dd>
                    </div>
                    <div className="min-w-0">
                      <dt className="text-anvil-400">样本摘要</dt>
                      <dd className="break-all font-mono text-xs">
                        {testResult.sample_sha256.slice(0, 16)}
                      </dd>
                    </div>
                  </dl>
                </div>
              )}

              {deployResult && (
                <div className="space-y-3 rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
                  <div className="flex items-center justify-between gap-3">
                    <span className="font-medium">部署记录</span>
                    <StatusBadge
                      tone={
                        deployResult.runtime_matches ? 'success' : 'warning'
                      }
                    >
                      {deployResult.runtime_matches
                        ? '运行配置匹配'
                        : '需要重启服务'}
                    </StatusBadge>
                  </div>
                  <p className="break-all font-mono text-xs text-anvil-500 dark:text-anvil-400">
                    {deployResult.model_version.id}
                  </p>
                  <p className={cn(
                    'text-sm',
                    deployResult.runtime_settings_saved
                      ? 'text-success-600 dark:text-success-400'
                      : 'text-warning-600 dark:text-warning-400',
                  )}>
                    {deployResult.runtime_settings_saved
                      ? '配置已加密保存；重启服务后按 Base URL 和模型名自动解析版本'
                      : 'API Key 未保存；当前服务未配置持久设置存储'}
                  </p>
                  <div className="grid grid-cols-2 gap-3 text-sm">
                    <div>
                      <dt className="text-anvil-400">回填计划</dt>
                      <dd className="font-semibold">
                        {deployResult.backfill_plans?.length ?? 0}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-anvil-400">候选数</dt>
                      <dd className="font-semibold">
                        {formatNumber(
                          deployResult.backfill_plans?.reduce(
                            (sum, plan) => sum + plan.candidate_count,
                            0,
                          ),
                        )}
                      </dd>
                    </div>
                  </div>
                </div>
              )}

              {backfillResult && (
                <div className="rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <span className="font-medium">回填</span>
                    <StatusBadge
                      tone={backfillResult.failed_count > 0 ? 'warning' : 'success'}
                    >
                      {backfillResult.dry_run ? '预演' : '已完成'}
                    </StatusBadge>
                  </div>
                  <dl className="grid grid-cols-3 gap-3 text-sm">
                    <div>
                      <dt className="text-anvil-400">候选</dt>
                      <dd className="font-semibold">
                        {backfillResult.plan.candidate_count}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-anvil-400">写入</dt>
                      <dd className="font-semibold">
                        {backfillResult.embedded_count}
                      </dd>
                    </div>
                    <div>
                      <dt className="text-anvil-400">失败</dt>
                      <dd className="font-semibold">
                        {backfillResult.failed_count}
                      </dd>
                    </div>
                  </dl>
                </div>
              )}

              {activateResult && (
                <div className="rounded-lg border border-anvil-200 p-4 dark:border-anvil-700">
                  <div className="mb-3 flex items-center justify-between gap-3">
                    <span className="font-medium">启用</span>
                    <StatusBadge
                      tone={
                        activateResult.reports.every(
                          (report) => report.decision === 'go',
                        )
                          ? 'success'
                          : 'warning'
                      }
                    >
                      {activateResult.runtime_matches
                        ? '运行配置匹配'
                        : '运行配置不一致'}
                    </StatusBadge>
                  </div>
                  <div className="space-y-2">
                    {activateResult.reports.map((report) => (
                      <div
                        key={`${report.embedding_kind}-${report.generated_at}`}
                        className="flex min-w-0 items-center justify-between gap-3 text-sm"
                      >
                        <span className="truncate">
                          {KIND_LABELS[
                            report.embedding_kind as EmbeddingKind
                          ] ?? report.embedding_kind}
                        </span>
                        <StatusBadge
                          tone={report.committed ? 'success' : 'neutral'}
                        >
                          {report.committed ? '已生效' : report.decision}
                        </StatusBadge>
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {!testResult &&
                !deployResult &&
                !backfillResult &&
                !activateResult && (
                  <p className="py-8 text-center text-sm text-anvil-400">
                    暂无操作结果
                  </p>
                )}
            </div>
          </div>
        </div>
      </div>

            <details className="af-form-details mt-6"><summary>当前启用版本与状态</summary><div>
        <div className="mb-4 flex min-w-0 items-center justify-between gap-3">
          <h2 className="forge-section-title flex min-w-0 items-center gap-2">
            <Cpu className="h-5 w-5 flex-shrink-0 text-anvil-400" />
            <span className="truncate">当前启用模型</span>
          </h2>
          <StatusBadge tone={statuses.length > 0 ? 'success' : 'neutral'}>
            {statuses.length > 0 ? `${statuses.length} 项` : '暂无数据'}
          </StatusBadge>
        </div>
        <div className="overflow-x-auto">
          <table className="forge-table min-w-[760px]">
            <thead>
              <tr>
                <th>用途</th>
                <th>模型版本</th>
                <th>模型</th>
                <th>维度</th>
                <th>状态</th>
                <th>更新时间</th>
              </tr>
            </thead>
            <tbody>
              {statuses.length === 0 ? (
                <tr>
                  <td colSpan={6} className="text-center text-anvil-400">
                    暂无启用模型数据
                  </td>
                </tr>
              ) : (
                statuses.map((status) => (
                  <tr key={status.embedding_kind}>
                    <td className="font-medium">
                      {KIND_LABELS[status.embedding_kind as EmbeddingKind] ??
                        status.embedding_kind}
                    </td>
                    <td className="max-w-[220px] break-all font-mono text-xs">
                      {status.model_version.id}
                    </td>
                    <td className="max-w-[220px] break-all">
                      <div className="font-medium">
                        {status.model_version.model_id}
                      </div>
                      <div className="text-xs text-anvil-400">
                        {status.model_version.provider}
                      </div>
                    </td>
                    <td>{status.expected_dimensions}</td>
                    <td>
                      <StatusBadge
                        tone={
                          status.model_version.status === 'active'
                            ? 'success'
                            : 'warning'
                        }
                      >
                        {status.model_version.status}
                      </StatusBadge>
                    </td>
                    <td>{formatTime(status.updated_at)}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div></details>

      <div className="mt-6 grid grid-cols-1 gap-4 xl:grid-cols-2">
        {deployResult && <ResultBlock title="部署响应" value={deployResult} />}
        {backfillResult && (
          <ResultBlock title="回填响应" value={backfillResult} />
        )}
        {activateResult && (
          <ResultBlock title="启用响应" value={activateResult} />
        )}
      </div>
    </div>
  );
}
