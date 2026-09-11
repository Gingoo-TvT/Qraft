// ============================================================================
// Qraft - useProblems Hook
// Custom React hooks for problem CRUD operations with filtering and pagination
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  deleteProblem as apiDeleteProblem,
  generateGPLTBatch as apiGenerateGPLTBatch,
  generateProblem as apiGenerateProblem,
  getProblem as apiGetProblem,
  getProblemSolutions as apiGetProblemSolutions,
  getTestcases as apiGetTestcases,
  listProblems as apiListProblems,
  listTags as apiListTags,
  updateProblem as apiUpdateProblem,
} from '../lib/api';
import { DEFAULT_PAGE_SIZE } from '../lib/constants';
import type {
  GPLTBatchParams,
  GPLTBatchTriggerResponse,
  Problem,
  ProblemFilter,
  ProblemGenerateResponse,
  ProblemGenParams,
  ProblemSolution,
  ProblemUpdateRequest,
  TagCategory,
  TestCase,
} from '../lib/types';

// ---------------------------------------------------------------------------
// useProblems — list with filter, pagination, CRUD
// ---------------------------------------------------------------------------

interface UseProblemsReturn {
  problems: Problem[];
  total: number;
  page: number;
  loading: boolean;
  error: string | null;
  filter: ProblemFilter;
  setFilter: (filter: ProblemFilter) => void;
  refresh: () => void;
  refetch: () => void;
  createProblem: (params: ProblemGenParams) => Promise<ProblemGenerateResponse | null>;
  updateProblem: (id: string, data: ProblemUpdateRequest) => Promise<Problem | null>;
  deleteProblem: (id: string) => Promise<boolean>;
}

export function useProblems(initialFilter?: ProblemFilter): UseProblemsReturn {
  const [filter, setFilter] = useState<ProblemFilter>({
    page: 1,
    size: DEFAULT_PAGE_SIZE,
    ...initialFilter,
  });
  const [problems, setProblems] = useState<Problem[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const filterKey = JSON.stringify(filter);
  const fetchIdRef = useRef(0);

  const fetchProblems = useCallback(async () => {
    const fetchId = ++fetchIdRef.current;
    setLoading(true);
    setError(null);

    try {
      const res = await apiListProblems(filter);
      if (fetchId !== fetchIdRef.current) return;
      setProblems(res.data ?? []);
      setTotal(res.meta?.total ?? 0);
    } catch (err) {
      if (fetchId !== fetchIdRef.current) return;
      setError(err instanceof Error ? err.message : 'Failed to fetch problems');
    } finally {
      if (fetchId === fetchIdRef.current) {
        setLoading(false);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterKey]);

  useEffect(() => {
    fetchProblems();
  }, [fetchProblems]);

  const createProblem = useCallback(
    async (params: ProblemGenParams): Promise<ProblemGenerateResponse | null> => {
      setError(null);
      try {
        const res = await apiGenerateProblem(params);
        const created = res.data ?? null;
        fetchProblems();
        return created;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to create problem';
        setError(message);
        return null;
      }
    },
    [fetchProblems],
  );

  const updateProblem = useCallback(
    async (id: string, data: ProblemUpdateRequest): Promise<Problem | null> => {
      setError(null);
      try {
        const res = await apiUpdateProblem(id, data);
        const updated = res.data ?? null;
        fetchProblems();
        return updated;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to update problem';
        setError(message);
        return null;
      }
    },
    [fetchProblems],
  );

  const deleteProblem = useCallback(
    async (id: string): Promise<boolean> => {
      setError(null);
      try {
        await apiDeleteProblem(id);
        fetchProblems();
        return true;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to delete problem';
        setError(message);
        return false;
      }
    },
    [fetchProblems],
  );

  return {
    problems,
    total,
    page: filter.page ?? 1,
    loading,
    error,
    filter,
    setFilter,
    refresh: fetchProblems,
    refetch: fetchProblems,
    createProblem,
    updateProblem,
    deleteProblem,
  };
}

// ---------------------------------------------------------------------------
// useProblem — single problem detail
// ---------------------------------------------------------------------------

interface UseProblemReturn {
  problem: Problem | null;
  loading: boolean;
  error: string | null;
  refresh: () => void;
}

export function useProblem(id: string | undefined): UseProblemReturn {
  const [problem, setProblem] = useState<Problem | null>(null);
  const [loading, setLoading] = useState(() => Boolean(id));
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  const fetchProblem = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    setError(null);

    try {
      const res = await apiGetProblem(id);
      if (mountedRef.current) {
        setProblem(res.data ?? null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err.message : 'Failed to fetch problem');
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false);
      }
    }
  }, [id]);

  useEffect(() => {
    mountedRef.current = true;
    fetchProblem();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchProblem]);

  return { problem, loading, error, refresh: fetchProblem };
}

// ---------------------------------------------------------------------------
// useTestcases — test cases for a problem
// ---------------------------------------------------------------------------

interface UseTestcasesReturn {
  testcases: TestCase[];
  loading: boolean;
  error: string | null;
}

export function useTestcases(problemId: string | undefined): UseTestcasesReturn {
  const [testcases, setTestcases] = useState<TestCase[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!problemId) return;
    let cancelled = false;

    setLoading(true);
    setError(null);

    apiGetTestcases(problemId)
      .then((res) => {
        if (!cancelled) {
          setTestcases(res.data ?? []);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to fetch testcases');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [problemId]);

  return { testcases, loading, error };
}

// ---------------------------------------------------------------------------
// useProblemSolutions — executable source artefacts for a problem
// ---------------------------------------------------------------------------

interface UseProblemSolutionsReturn {
  solutions: ProblemSolution[];
  loading: boolean;
  error: string | null;
}

export function useProblemSolutions(
  problemId: string | undefined,
): UseProblemSolutionsReturn {
  const [solutions, setSolutions] = useState<ProblemSolution[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!problemId) return;
    let cancelled = false;

    setLoading(true);
    setError(null);

    apiGetProblemSolutions(problemId)
      .then((res) => {
        if (!cancelled) {
          setSolutions(res.data ?? []);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to fetch solutions');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [problemId]);

  return { solutions, loading, error };
}

// ---------------------------------------------------------------------------
// useTags — fetch tag categories
// ---------------------------------------------------------------------------

interface UseTagsReturn {
  tags: TagCategory[];
  loading: boolean;
  error: string | null;
}

export function useTags(): UseTagsReturn {
  const [tags, setTags] = useState<TagCategory[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);

    apiListTags()
      .then((res) => {
        if (!cancelled) {
          setTags(res.data ?? []);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to fetch tags');
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, []);

  return { tags, loading, error };
}

// ---------------------------------------------------------------------------
// useGenerateProblem — trigger problem generation
// ---------------------------------------------------------------------------

interface UseGenerateProblemReturn {
  generate: (params: ProblemGenParams) => Promise<ProblemGenerateResponse | null>;
  loading: boolean;
  error: string | null;
  reset: () => void;
}

export function useGenerateProblem(): UseGenerateProblemReturn {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const generate = useCallback(
    async (params: ProblemGenParams): Promise<ProblemGenerateResponse | null> => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGenerateProblem(params);
        return res.data ?? null;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to generate problem';
        setError(message);
        return null;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reset = useCallback(() => {
    setError(null);
    setLoading(false);
  }, []);

  return { generate, loading, error, reset };
}

// ---------------------------------------------------------------------------
// useGenerateGPLTBatch — trigger 天梯赛 one-click batch (15 problems)
// ---------------------------------------------------------------------------

interface UseGenerateGPLTBatchReturn {
  generate: (params: GPLTBatchParams) => Promise<GPLTBatchTriggerResponse | null>;
  loading: boolean;
  error: string | null;
  reset: () => void;
}

export function useGenerateGPLTBatch(): UseGenerateGPLTBatchReturn {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const generate = useCallback(
    async (params: GPLTBatchParams): Promise<GPLTBatchTriggerResponse | null> => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGenerateGPLTBatch(params);
        return res.data ?? null;
      } catch (err) {
        const message =
          err instanceof Error ? err.message : 'Failed to start GPLT batch generation';
        setError(message);
        return null;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reset = useCallback(() => {
    setError(null);
    setLoading(false);
  }, []);

  return { generate, loading, error, reset };
}
