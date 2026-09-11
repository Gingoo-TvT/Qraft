'use client';

import { useCallback, useEffect, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  RefreshCw,
} from 'lucide-react';

import { getApiBaseUrl, getApiHealth } from '@/lib/api';
import { cn } from '@/lib/utils';

type HealthState = 'checking' | 'online' | 'offline';

export default function ApiHealthBanner() {
  const [state, setState] = useState<HealthState>('checking');
  const [message, setMessage] = useState('正在检查 API 服务');
  const apiBaseUrl = getApiBaseUrl();

  const check = useCallback(async (signal?: AbortSignal) => {
    setState('checking');
    setMessage('正在检查 API 服务');
    try {
      const health = await getApiHealth(signal);
      if (health.status !== 'ok') {
        throw new Error(`API 状态异常: ${health.status || 'unknown'}`);
      }
      setState('online');
      setMessage(apiBaseUrl ? `API 服务已连接：${apiBaseUrl}` : 'API 服务已连接');
    } catch (err) {
      if (signal?.aborted) return;
      setState('offline');
      setMessage(err instanceof Error ? err.message : 'API 服务未连接');
    }
  }, [apiBaseUrl]);

  useEffect(() => {
    const controller = new AbortController();
    void check(controller.signal);
    return () => controller.abort();
  }, [check]);

  const Icon =
    state === 'online'
      ? CheckCircle2
      : state === 'checking'
        ? Loader2
        : AlertTriangle;

  return (
    <div role={state === 'offline' ? 'alert' : 'status'} className={cn(
      state === 'offline'
        ? 'flex min-w-0 flex-wrap items-start justify-between gap-4 rounded-lg border border-warning-200 bg-warning-50 p-4 text-sm text-warning-800 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-200'
        : 'flex min-h-8 min-w-0 items-center gap-2 text-xs text-[var(--dm)]',
    )}>
      <div className={cn('flex min-w-0 gap-2', state === 'offline' ? 'items-start' : 'items-center')}>
        <Icon className={cn('h-4 w-4 flex-shrink-0', state === 'checking' && 'animate-spin', state === 'offline' && 'mt-0.5')} />
        {state === 'offline' ? <div className="min-w-0"><p className="font-medium">服务未连接</p><p className="mt-1 break-words">{message}</p></div>
          : <span title={apiBaseUrl || '当前站点的 API 服务'}>{state === 'online' ? '服务已连接' : '正在检查服务…'}</span>}
      </div>
      <button type="button" onClick={() => void check()} disabled={state === 'checking'}
        aria-label="重新检查服务连接" title="重新检查服务连接"
        className={state === 'offline' ? 'forge-btn-secondary self-start' : 'inline-flex h-7 w-7 items-center justify-center rounded-md hover:bg-[var(--dh)] disabled:opacity-50'}>
        <RefreshCw className={cn('h-3.5 w-3.5', state === 'checking' && 'animate-spin')} />
        {state === 'offline' && '重试'}
      </button>
    </div>
  );
}
