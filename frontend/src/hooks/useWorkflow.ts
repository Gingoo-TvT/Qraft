// ============================================================================
// Qraft - useWorkflow & useWorkflowList Hooks
// Real-time workflow management with SSE event subscription
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  approveWorkflow as apiApprove,
  cancelWorkflow as apiCancel,
  getWorkflow as apiGetWorkflow,
  listWorkflows as apiListWorkflows,
  rejectWorkflow as apiReject,
  retryWorkflow as apiRetry,
  subscribeWorkflowEvents,
} from '../lib/api';
import type {
  WorkflowEvent,
  WorkflowReviewActionResponse,
  WorkflowReviewReference,
  WorkflowRetryResponse,
  WorkflowState,
} from '../lib/types';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MAX_RECONNECT_DELAY = 30_000;
const INITIAL_RECONNECT_DELAY = 1_000;

// ---------------------------------------------------------------------------
// useWorkflow - single workflow detail (REST fetch, no SSE)
// ---------------------------------------------------------------------------

interface UseWorkflowReturn {
  workflow: WorkflowState | null;
  loading: boolean;
  error: string | null;
  refresh: () => Promise<void>;
  refetch: () => Promise<void>;
}

export function useWorkflow(workflowId: string | undefined): UseWorkflowReturn {
  const [workflow, setWorkflow] = useState<WorkflowState | null>(null);
  const [loading, setLoading] = useState(() => Boolean(workflowId));
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  const fetchWorkflow = useCallback(async () => {
    if (!workflowId) return;
    setLoading(true);
    setError(null);

    try {
      const res = await apiGetWorkflow(workflowId);
      if (mountedRef.current) {
        setWorkflow(res.data ?? null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(
          err instanceof Error ? err.message : 'Failed to fetch workflow',
        );
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false);
      }
    }
  }, [workflowId]);

  useEffect(() => {
    mountedRef.current = true;
    fetchWorkflow();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchWorkflow]);

  return {
    workflow,
    loading,
    error,
    refresh: fetchWorkflow,
    refetch: fetchWorkflow,
  };
}

// ---------------------------------------------------------------------------
// useWorkflowActions - approve, reject, retry, cancel
// ---------------------------------------------------------------------------

interface UseWorkflowActionsReturn {
  approve: (
    review: WorkflowReviewReference,
  ) => Promise<WorkflowReviewActionResponse | null>;
  reject: (
    review: WorkflowReviewReference,
    feedback: string,
  ) => Promise<WorkflowReviewActionResponse | null>;
  retry: () => Promise<WorkflowRetryResponse | null>;
  cancel: () => Promise<WorkflowState | null>;
  loading: boolean;
  error: string | null;
}

export function useWorkflowActions(workflowId: string | undefined): UseWorkflowActionsReturn {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const approve = useCallback(
    async (
      review: WorkflowReviewReference,
    ): Promise<WorkflowReviewActionResponse | null> => {
      if (!workflowId) {
        setError('No workflow ID provided');
        return null;
      }
      setLoading(true);
      setError(null);
      try {
        const res = await apiApprove(workflowId, review);
        return res.data ?? null;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Approve failed');
        return null;
      } finally {
        setLoading(false);
      }
    },
    [workflowId],
  );

  const reject = useCallback(
    async (
      review: WorkflowReviewReference,
      feedback: string,
    ): Promise<WorkflowReviewActionResponse | null> => {
      if (!workflowId) {
        setError('No workflow ID provided');
        return null;
      }
      const normalizedFeedback = feedback.trim();
      if (!normalizedFeedback) {
        setError('Feedback is required when rejecting');
        return null;
      }
      setLoading(true);
      setError(null);
      try {
        const res = await apiReject(workflowId, review, normalizedFeedback);
        return res.data ?? null;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Reject failed');
        return null;
      } finally {
        setLoading(false);
      }
    },
    [workflowId],
  );

  const retry = useCallback(async (): Promise<WorkflowRetryResponse | null> => {
    if (!workflowId) {
      setError('No workflow ID provided');
      return null;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await apiRetry(workflowId);
      return res.data ?? null;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Retry failed');
      return null;
    } finally {
      setLoading(false);
    }
  }, [workflowId]);

  const cancel = useCallback(async (): Promise<WorkflowState | null> => {
    if (!workflowId) {
      setError('No workflow ID provided');
      return null;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await apiCancel(workflowId);
      return res.data ?? null;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cancel failed');
      return null;
    } finally {
      setLoading(false);
    }
  }, [workflowId]);

  return { approve, reject, retry, cancel, loading, error };
}

// ---------------------------------------------------------------------------
// useWorkflowEvents - SSE event subscription
// ---------------------------------------------------------------------------

interface UseWorkflowEventsReturn {
  events: WorkflowEvent[];
  latestEvent: WorkflowEvent | null;
  connected: boolean;
  error: string | null;
}

export function useWorkflowEvents(workflowId: string | undefined): UseWorkflowEventsReturn {
  const [events, setEvents] = useState<WorkflowEvent[]>([]);
  const [latestEvent, setLatestEvent] = useState<WorkflowEvent | null>(null);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectDelayRef = useRef(INITIAL_RECONNECT_DELAY);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;

    if (!workflowId) return;

    function connect() {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
      }

      try {
        const es = subscribeWorkflowEvents(
          workflowId!,
          (messageEvent: MessageEvent) => {
            if (!mountedRef.current) return;

            try {
              const event: WorkflowEvent = JSON.parse(messageEvent.data);
              setEvents((prev) => [...prev, event]);
              setLatestEvent(event);
              reconnectDelayRef.current = INITIAL_RECONNECT_DELAY;

              if (
                event.type === 'workflow_completed' ||
                event.type === 'workflow_failed'
              ) {
                eventSourceRef.current?.close();
                setConnected(false);
              }
            } catch {
              // Ignore malformed events
            }
          },
          (_errorEvent: Event) => {
            if (!mountedRef.current) return;
            setConnected(false);
            setError('SSE connection lost, reconnecting...');

            reconnectTimerRef.current = setTimeout(() => {
              if (mountedRef.current) {
                reconnectDelayRef.current = Math.min(
                  reconnectDelayRef.current * 2,
                  MAX_RECONNECT_DELAY,
                );
                connect();
              }
            }, reconnectDelayRef.current);
          },
        );

        eventSourceRef.current = es;

        es.onopen = () => {
          if (!mountedRef.current) return;
          setConnected(true);
          setError(null);
          reconnectDelayRef.current = INITIAL_RECONNECT_DELAY;
        };
      } catch {
        // SSE not available - not fatal
        setError('Unable to connect to event stream');
      }
    }

    connect();

    return () => {
      mountedRef.current = false;
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowId]);

  return { events, latestEvent, connected, error };
}

// ---------------------------------------------------------------------------
// useWorkflowList - paginated workflow listing
// ---------------------------------------------------------------------------

interface WorkflowListParams {
  page?: number;
  size?: number;
  status?: string;
}

interface UseWorkflowListReturn {
  workflows: WorkflowState[];
  total: number;
  loading: boolean;
  error: string | null;
  refetch: () => void;
  refresh: () => void;
  setParams: (params: WorkflowListParams) => void;
}

export function useWorkflowList(
  initialParams?: WorkflowListParams,
): UseWorkflowListReturn {
  const [params, setParams] = useState<WorkflowListParams>(initialParams ?? {});
  const [workflows, setWorkflows] = useState<WorkflowState[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const paramsKey = JSON.stringify(params);
  const fetchIdRef = useRef(0);

  const fetchWorkflows = useCallback(async () => {
    const fetchId = ++fetchIdRef.current;
    setLoading(true);
    setError(null);

    try {
      const res = await apiListWorkflows(params);
      if (fetchId !== fetchIdRef.current) return;
      setWorkflows(res.data ?? []);
      setTotal(res.meta?.total ?? 0);
    } catch (err) {
      if (fetchId !== fetchIdRef.current) return;
      setError(
        err instanceof Error ? err.message : 'Failed to fetch workflows',
      );
    } finally {
      if (fetchId === fetchIdRef.current) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paramsKey]);

  useEffect(() => {
    fetchWorkflows();
  }, [fetchWorkflows]);

  return {
    workflows,
    total,
    loading,
    error,
    refetch: fetchWorkflows,
    refresh: fetchWorkflows,
    setParams,
  };
}

// ---------------------------------------------------------------------------
// useWorkflows — alias for useWorkflowList for convenience
// ---------------------------------------------------------------------------

export function useWorkflows(
  initialParams?: WorkflowListParams,
): UseWorkflowListReturn {
  return useWorkflowList(initialParams);
}
