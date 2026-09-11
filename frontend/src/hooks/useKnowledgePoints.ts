// ============================================================================
// Qraft - useKnowledgePoints Hook
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react';

import { listKnowledgePoints as apiListKnowledgePoints } from '@/lib/api';
import type { KnowledgePoint } from '@/lib/types';

export function useKnowledgePoints(subject?: string): {
  points: KnowledgePoint[];
  loading: boolean;
  error: string | null;
  refresh: () => void;
} {
  const [points, setPoints] = useState<KnowledgePoint[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fetchIdRef = useRef(0);
  const mountedRef = useRef(true);

  const fetchPoints = useCallback(async () => {
    const fetchId = ++fetchIdRef.current;

    if (!subject) {
      setPoints([]);
      setLoading(false);
      setError(null);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const res = await apiListKnowledgePoints(subject);
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setPoints(res.data ?? []);
    } catch (err) {
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setError(
        err instanceof Error ? err.message : 'Failed to fetch knowledge points',
      );
      setPoints([]);
    } finally {
      if (mountedRef.current && fetchId === fetchIdRef.current) {
        setLoading(false);
      }
    }
  }, [subject]);

  useEffect(() => {
    mountedRef.current = true;
    fetchPoints();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchPoints]);

  return { points, loading, error, refresh: fetchPoints };
}
