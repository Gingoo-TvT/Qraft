// ============================================================================
// Qraft - useQuiz Hooks
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react';

import {
  deleteQuiz as apiDeleteQuiz,
  generateQuiz as apiGenerateQuiz,
  getQuiz as apiGetQuiz,
  importQuizzes as apiImportQuizzes,
  listQuizzes as apiListQuizzes,
  updateQuiz as apiUpdateQuiz,
} from '@/lib/api';
import { DEFAULT_PAGE_SIZE } from '@/lib/constants';
import type {
  ImportReport,
  OnConflict,
  QuizFilter,
  QuizGenerateParams,
  QuizGenerateResponse,
  QuizProblem,
} from '@/lib/types';

export function useQuizzes(initial?: QuizFilter): {
  quizzes: QuizProblem[];
  total: number;
  page: number;
  loading: boolean;
  error: string | null;
  filter: QuizFilter;
  setFilter: (f: QuizFilter) => void;
  refresh: () => void;
} {
  const [filter, setFilter] = useState<QuizFilter>({
    page: 1,
    size: DEFAULT_PAGE_SIZE,
    ...initial,
  });
  const [quizzes, setQuizzes] = useState<QuizProblem[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fetchIdRef = useRef(0);
  const mountedRef = useRef(true);
  const filterKey = JSON.stringify(filter);

  const fetchQuizzes = useCallback(async () => {
    const fetchId = ++fetchIdRef.current;
    setLoading(true);
    setError(null);

    try {
      const res = await apiListQuizzes(filter);
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setQuizzes(res.data ?? []);
      setTotal(res.meta?.total ?? 0);
    } catch (err) {
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setError(err instanceof Error ? err.message : 'Failed to fetch quizzes');
      setQuizzes([]);
      setTotal(0);
    } finally {
      if (mountedRef.current && fetchId === fetchIdRef.current) {
        setLoading(false);
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterKey]);

  useEffect(() => {
    mountedRef.current = true;
    fetchQuizzes();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchQuizzes]);

  return {
    quizzes,
    total,
    page: filter.page ?? 1,
    loading,
    error,
    filter,
    setFilter,
    refresh: fetchQuizzes,
  };
}

export function useQuiz(id: string | undefined): {
  quiz: QuizProblem | null;
  loading: boolean;
  error: string | null;
  refresh: () => void;
} {
  const [quiz, setQuiz] = useState<QuizProblem | null>(null);
  const [loading, setLoading] = useState(() => Boolean(id));
  const [error, setError] = useState<string | null>(null);
  const fetchIdRef = useRef(0);
  const mountedRef = useRef(true);

  const fetchQuiz = useCallback(async () => {
    const fetchId = ++fetchIdRef.current;
    if (!id) return;
    setLoading(true);
    setError(null);

    try {
      const res = await apiGetQuiz(id);
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setQuiz(res.data ?? null);
    } catch (err) {
      if (!mountedRef.current || fetchId !== fetchIdRef.current) return;
      setError(err instanceof Error ? err.message : 'Failed to fetch quiz');
      setQuiz(null);
    } finally {
      if (mountedRef.current && fetchId === fetchIdRef.current) {
        setLoading(false);
      }
    }
  }, [id]);

  useEffect(() => {
    mountedRef.current = true;
    fetchQuiz();
    return () => {
      mountedRef.current = false;
    };
  }, [fetchQuiz]);

  return { quiz, loading, error, refresh: fetchQuiz };
}

export function useGenerateQuiz(): {
  generate: (p: QuizGenerateParams) => Promise<QuizGenerateResponse | null>;
  loading: boolean;
  error: string | null;
  reset: () => void;
} {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const generate = useCallback(
    async (params: QuizGenerateParams): Promise<QuizGenerateResponse | null> => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiGenerateQuiz(params);
        return res.data ?? null;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to generate quiz');
        return null;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reset = useCallback(() => setError(null), []);

  return { generate, loading, error, reset };
}

export function useImportQuizzes(): {
  importQ: (
    file: File,
    subject: string,
    onConflict: OnConflict,
  ) => Promise<ImportReport | null>;
  loading: boolean;
  error: string | null;
  report: ImportReport | null;
  reset: () => void;
} {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);

  const importQ = useCallback(
    async (
      file: File,
      subject: string,
      onConflict: OnConflict,
    ): Promise<ImportReport | null> => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiImportQuizzes(file, subject, onConflict);
        const nextReport = res.data ?? null;
        setReport(nextReport);
        return nextReport;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to import quizzes');
        setReport(null);
        return null;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reset = useCallback(() => {
    setError(null);
    setReport(null);
  }, []);

  return { importQ, loading, error, report, reset };
}

export function useUpdateQuiz(): {
  update: (id: string, body: Partial<QuizProblem>) => Promise<QuizProblem | null>;
  loading: boolean;
  error: string | null;
  reset: () => void;
} {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const update = useCallback(
    async (
      id: string,
      body: Partial<QuizProblem>,
    ): Promise<QuizProblem | null> => {
      setLoading(true);
      setError(null);
      try {
        const res = await apiUpdateQuiz(id, body);
        return res.data ?? null;
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Failed to update quiz');
        return null;
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  const reset = useCallback(() => setError(null), []);

  return { update, loading, error, reset };
}

export function useDeleteQuiz(): {
  remove: (id: string) => Promise<boolean>;
  loading: boolean;
  error: string | null;
  reset: () => void;
} {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const remove = useCallback(async (id: string): Promise<boolean> => {
    setLoading(true);
    setError(null);
    try {
      await apiDeleteQuiz(id);
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete quiz');
      return false;
    } finally {
      setLoading(false);
    }
  }, []);

  const reset = useCallback(() => setError(null), []);

  return { remove, loading, error, reset };
}
