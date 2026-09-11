'use client';

import {
  ArrowRight,
  BookOpen,
  Check,
  CheckCircle2,
  ClipboardCheck,
  Loader2,
  RefreshCw,
} from 'lucide-react';
import { useParams } from 'next/navigation';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import AnnotationPayload, {
  sanitizeAnnotationPayload,
} from '@/components/annotation/AnnotationPayload';
import MarkdownRenderer from '@/components/MarkdownRenderer';
import {
  APIError,
  getNextAnnotation,
  submitAnnotation,
} from '@/lib/api';
import type {
  AnnotationItem,
  AnnotationOptionsSchema,
} from '@/lib/types';
import { cn } from '@/lib/utils';

const TASK_LABELS: Record<string, string> = {
  dedup: '去重分级',
  dedup_grade: '去重分级',
  difficulty: '难度分级',
  difficulty_band: '难度分级',
  fidelity: '条款保真',
  term_fidelity: '条款保真',
  defect: '缺陷标注',
  defect_multi: '缺陷标注',
  tag: '标签核验',
  tag_correctness: '标签核验',
};

function taskLabel(taskType: string): string {
  return TASK_LABELS[taskType.toLowerCase()] ?? '标注任务';
}

function safeCodebookURL(value?: string): string | null {
  if (!value) return null;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' ? url.href : null;
  } catch {
    return null;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isAnnotationItem(value: unknown): value is AnnotationItem {
  if (!isRecord(value)) return false;
  if (
    typeof value.item_id !== 'string' ||
    !value.item_id ||
    typeof value.batch_code !== 'string' ||
    typeof value.task_type !== 'string' ||
    !('payload' in value) ||
    !isRecord(value.options_schema) ||
    !isRecord(value.progress)
  ) {
    return false;
  }

  return (
    typeof value.progress.completed === 'number' &&
    Number.isFinite(value.progress.completed) &&
    typeof value.progress.total === 'number' &&
    Number.isFinite(value.progress.total) &&
    typeof value.progress.position === 'number' &&
    Number.isFinite(value.progress.position) &&
    (value.codebook_url === undefined || typeof value.codebook_url === 'string')
  );
}

function schemaError(schema: AnnotationOptionsSchema): string | null {
  if (schema.mode !== 'single' && schema.mode !== 'multi') {
    return '当前标注项暂不可用，请联系任务管理员。';
  }
  if (!Array.isArray(schema.options) || schema.options.length === 0) {
    return '当前标注项暂不可用，请联系任务管理员。';
  }

  const values = new Set<string>();
  for (const option of schema.options) {
    if (
      !option ||
      typeof option.value !== 'string' ||
      !option.value.trim() ||
      typeof option.label !== 'string' ||
      !option.label.trim() ||
      (option.description !== undefined && typeof option.description !== 'string')
    ) {
      return '当前标注项暂不可用，请联系任务管理员。';
    }
    if (values.has(option.value)) {
      return '当前标注项暂不可用，请联系任务管理员。';
    }
    values.add(option.value);
  }

  const min = schema.mode === 'single' ? 1 : (schema.min_selections ?? 1);
  const max =
    schema.mode === 'single'
      ? 1
      : (schema.max_selections ?? schema.options.length);
  if (
    (schema.mode === 'single' &&
      ((schema.min_selections !== undefined && schema.min_selections !== 1) ||
        (schema.max_selections !== undefined && schema.max_selections !== 1))) ||
    !Number.isInteger(min) ||
    !Number.isInteger(max) ||
    min < 1 ||
    max < min ||
    max > schema.options.length
  ) {
    return '当前标注项暂不可用，请联系任务管理员。';
  }
  if (schema.prompt !== undefined && typeof schema.prompt !== 'string') {
    return '当前标注项暂不可用，请联系任务管理员。';
  }
  if (schema.note !== undefined) {
    if (!isRecord(schema.note)) {
      return '当前标注项暂不可用，请联系任务管理员。';
    }
    if (
      (schema.note.enabled !== undefined && typeof schema.note.enabled !== 'boolean') ||
      (schema.note.required !== undefined && typeof schema.note.required !== 'boolean') ||
      (schema.note.label !== undefined && typeof schema.note.label !== 'string') ||
      (schema.note.placeholder !== undefined &&
        typeof schema.note.placeholder !== 'string')
    ) {
      return '当前标注项暂不可用，请联系任务管理员。';
    }
    if (schema.note.enabled === false && schema.note.required === true) {
      return '当前标注项暂不可用，请联系任务管理员。';
    }
  }

  return null;
}

function userError(error: unknown): string {
  if (error instanceof APIError) {
    if ([401, 403, 404].includes(error.status)) {
      return '此标注链接无效或已失效，请向任务管理员确认。';
    }
    if (error.status === 409) {
      return '该标注批次已锁定，暂时无法继续提交。';
    }
    if (error.status === 429) {
      return '操作过于频繁，请稍后再试。';
    }
  }
  return '暂时无法连接标注服务，请稍后重试。';
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError';
}

function AnnotationLoading() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center" aria-live="polite">
      <div className="flex items-center gap-3 text-sm text-anvil-600">
        <Loader2 className="h-5 w-5 animate-spin" aria-hidden="true" />
        正在载入标注项...
      </div>
    </div>
  );
}

function AnnotationDone() {
  return (
    <div className="mx-auto flex min-h-[70vh] max-w-xl flex-col items-center justify-center px-6 text-center">
      <CheckCircle2 className="h-12 w-12 text-success-600" aria-hidden="true" />
      <h1 className="mt-5 text-2xl font-semibold text-anvil-900">本批次已完成</h1>
      <p className="mt-2 text-sm text-anvil-600">所有提交均已记录，可以关闭此页面。</p>
    </div>
  );
}

export default function AnnotationPage() {
  const params = useParams<{ token: string }>();
  const token = params.token;
  const [item, setItem] = useState<AnnotationItem | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [note, setNote] = useState('');
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);
  const [pageError, setPageError] = useState<string | null>(null);
  const [validationError, setValidationError] = useState<string | null>(null);
  const [submissionBlocked, setSubmissionBlocked] = useState(false);
  const startedAt = useRef(Date.now());
  const activeRequest = useRef<AbortController | null>(null);
  const requestSequence = useRef(0);
  const submittingRef = useRef(false);
  const taskHeadingRef = useRef<HTMLHeadingElement>(null);

  const loadNext = useCallback(async () => {
    activeRequest.current?.abort();
    const controller = new AbortController();
    activeRequest.current = controller;
    const sequence = ++requestSequence.current;

    setLoading(true);
    setPageError(null);
    setValidationError(null);
    setSubmissionBlocked(false);
    setItem(null);

    try {
      const response = await getNextAnnotation(token, controller.signal);
      if (sequence !== requestSequence.current) return;
      if (!response.data || typeof response.data.done !== 'boolean') {
        throw new Error('Invalid annotation data');
      }

      if (response.data.done) {
        setDone(true);
        return;
      }
      if (!isAnnotationItem(response.data.item)) {
        throw new Error('Invalid annotation item');
      }

      setDone(false);
      setSelected([]);
      setNote('');
      setItem(response.data.item);
      startedAt.current = Date.now();
    } catch (error) {
      if (!isAbortError(error) && sequence === requestSequence.current) {
        setPageError(userError(error));
      }
    } finally {
      if (sequence === requestSequence.current) setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void loadNext();
    return () => activeRequest.current?.abort();
  }, [loadNext]);

  useEffect(() => {
    if (!item) return;
    taskHeadingRef.current?.focus();
  }, [item]);

  const currentSchemaError = useMemo(
    () => (item ? schemaError(item.options_schema) : null),
    [item],
  );
  const payloadAvailable = useMemo(
    () => (item ? sanitizeAnnotationPayload(item.payload) !== undefined : false),
    [item],
  );

  const toggleSelection = useCallback(
    (value: string) => {
      if (
        !item ||
        submittingRef.current ||
        submissionBlocked ||
        currentSchemaError
      ) {
        return;
      }
      setValidationError(null);

      if (item.options_schema.mode === 'single') {
        setSelected([value]);
        return;
      }

      setSelected((current) => {
        if (current.includes(value)) {
          return current.filter((entry) => entry !== value);
        }
        const max =
          item.options_schema.max_selections ?? item.options_schema.options.length;
        if (current.length >= max) {
          setValidationError(`最多选择 ${max} 项。`);
          return current;
        }
        return [...current, value];
      });
    },
    [currentSchemaError, item, submissionBlocked],
  );

  const handleSubmit = useCallback(async () => {
    if (
      !item ||
      submittingRef.current ||
      submissionBlocked ||
      currentSchemaError ||
      !payloadAvailable
    ) {
      return;
    }

    const min =
      item.options_schema.mode === 'single'
        ? 1
        : (item.options_schema.min_selections ?? 1);
    const max =
      item.options_schema.mode === 'single'
        ? 1
        : (item.options_schema.max_selections ?? item.options_schema.options.length);

    if (selected.length < min || selected.length > max) {
      setValidationError(
        min === max ? `请选择 ${min} 项。` : `请选择 ${min} 至 ${max} 项。`,
      );
      return;
    }
    if (item.options_schema.note?.required && !note.trim()) {
      setValidationError('请填写备注。');
      return;
    }

    submittingRef.current = true;
    setSubmitting(true);
    setValidationError(null);
    setPageError(null);

    try {
      const controller = new AbortController();
      activeRequest.current = controller;
      await submitAnnotation(token, {
        item_id: item.item_id,
        response: {
          selections: selected,
          ...(note.trim() ? { note: note.trim() } : {}),
        },
        duration_ms: Math.max(0, Date.now() - startedAt.current),
      }, controller.signal);
      await loadNext();
    } catch (error) {
      if (!isAbortError(error)) {
        setPageError(userError(error));
        if (
          error instanceof APIError &&
          [401, 403, 404, 409].includes(error.status)
        ) {
          setSubmissionBlocked(true);
        }
      }
    } finally {
      submittingRef.current = false;
      setSubmitting(false);
    }
  }, [
    currentSchemaError,
    item,
    loadNext,
    note,
    payloadAvailable,
    selected,
    submissionBlocked,
    token,
  ]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (!item || loading || submittingRef.current || event.repeat) return;
      const target = event.target as HTMLElement | null;
      if (
        target?.matches('input, textarea, select, button, a') ||
        target?.isContentEditable
      ) {
        return;
      }

      if (/^[1-9]$/.test(event.key)) {
        const option = item.options_schema.options[Number(event.key) - 1];
        if (option) {
          event.preventDefault();
          toggleSelection(option.value);
        }
        return;
      }

      if (event.key === 'Enter') {
        event.preventDefault();
        void handleSubmit();
      }
    }

    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [handleSubmit, item, loading, toggleSelection]);

  if (loading && !item) return <AnnotationLoading />;
  if (done) return <AnnotationDone />;

  if (pageError && !item) {
    return (
      <div className="mx-auto flex min-h-[70vh] max-w-xl flex-col items-center justify-center px-6 text-center">
        <div role="alert" className="text-sm text-danger-600">{pageError}</div>
        <button type="button" className="forge-btn-secondary mt-5" onClick={() => void loadNext()}>
          <RefreshCw className="h-4 w-4" aria-hidden="true" />
          重试
        </button>
      </div>
    );
  }

  if (!item) return <AnnotationLoading />;

  const schema = item.options_schema;
  const codebookURL = safeCodebookURL(item.codebook_url);
  const progressTotal = Math.max(0, item.progress.total);
  const progressCompleted = Math.min(
    Math.max(0, item.progress.completed),
    progressTotal,
  );
  const progressPercent =
    progressTotal > 0 ? (progressCompleted / progressTotal) * 100 : 0;
  const noteSchema = schema.note;
  const showNote = noteSchema?.enabled !== false;
  const unavailableError =
    currentSchemaError ||
    (!payloadAvailable
      ? '当前标注项暂不可用，请联系任务管理员。'
      : null);
  const controlsDisabled = submitting || Boolean(unavailableError) || submissionBlocked;

  return (
    <div className="min-h-screen bg-anvil-50 text-anvil-900">
      <header className="border-b border-anvil-200 bg-white">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-4 px-4 py-4 sm:px-6">
          <div className="flex min-w-0 items-center gap-3">
            <span className="flex h-9 w-9 flex-none items-center justify-center rounded-lg bg-forge-600 text-white">
              <ClipboardCheck className="h-5 w-5" aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <p className="truncate text-sm font-semibold">{taskLabel(item.task_type)}</p>
              <p className="truncate text-xs text-anvil-500">批次 {item.batch_code}</p>
            </div>
          </div>
          {codebookURL && (
            <a
              href={codebookURL}
              target="_blank"
              rel="noopener noreferrer"
              referrerPolicy="no-referrer"
              className="forge-btn-secondary flex-none"
            >
              <BookOpen className="h-4 w-4" aria-hidden="true" />
              <span className="hidden sm:inline">查看标注规范</span>
              <span className="sm:hidden">规范</span>
            </a>
          )}
        </div>
      </header>

      <div className="border-b border-anvil-200 bg-white">
        <div className="mx-auto max-w-5xl px-4 py-3 sm:px-6">
          <div className="mb-2 flex items-center justify-between gap-4 text-xs text-anvil-600">
            <span>已完成 {progressCompleted} / {progressTotal}</span>
            <span>第 {Math.max(1, item.progress.position)} 项</span>
          </div>
          <div
            className="h-2 overflow-hidden rounded-full bg-anvil-100"
            role="progressbar"
            aria-label="标注进度"
            aria-valuemin={0}
            aria-valuemax={progressTotal}
            aria-valuenow={progressCompleted}
          >
            <div
              className="h-full rounded-full bg-success-500 transition-[width] duration-300"
              style={{ width: `${progressPercent}%` }}
            />
          </div>
        </div>
      </div>

      <main className="mx-auto w-full max-w-5xl px-4 py-8 sm:px-6 sm:py-10">
        <h1
          ref={taskHeadingRef}
          tabIndex={-1}
          className="mb-6 text-lg font-semibold outline-none sm:text-xl"
        >
          {schema.prompt || '请完成本项标注'}
        </h1>

        <section className="border-y border-anvil-200 bg-white px-4 py-6 sm:px-6 sm:py-8">
          {payloadAvailable ? (
            <AnnotationPayload payload={item.payload} />
          ) : (
            <p role="alert" className="text-sm text-danger-600">
              当前标注项暂不可用，请联系任务管理员。
            </p>
          )}
        </section>

        <fieldset className="mt-8" disabled={controlsDisabled}>
          <legend className="mb-3 text-sm font-semibold text-anvil-800">
            {schema.mode === 'multi' ? '选择所有符合项' : '选择一项'}
          </legend>
          <div className="grid gap-3" role={schema.mode === 'single' ? 'radiogroup' : 'group'}>
            {!currentSchemaError && schema.options.map((option, index) => {
              const checked = selected.includes(option.value);
              return (
                <button
                  key={option.value}
                  type="button"
                  role={schema.mode === 'single' ? 'radio' : 'checkbox'}
                  aria-checked={checked}
                  aria-keyshortcuts={index < 9 ? String(index + 1) : undefined}
                  className={cn(
                    'flex min-h-16 w-full items-start gap-3 border px-4 py-3 text-left transition-colors sm:px-5',
                    checked
                      ? 'border-forge-600 bg-forge-50 text-anvil-900'
                      : 'border-anvil-300 bg-white text-anvil-800 hover:border-anvil-500 hover:bg-anvil-50',
                  )}
                  onClick={() => toggleSelection(option.value)}
                >
                  <span
                    aria-hidden="true"
                    className={cn(
                      'mt-0.5 flex h-5 w-5 flex-none items-center justify-center border',
                      schema.mode === 'single' ? 'rounded-full' : 'rounded',
                      checked
                        ? 'border-forge-600 bg-forge-600 text-white'
                        : 'border-anvil-400 bg-white',
                    )}
                  >
                    {checked && <Check className="h-3.5 w-3.5" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium sm:text-base">{option.label}</span>
                    {option.description && (
                      <MarkdownRenderer
                        content={option.description}
                        className="mt-1 text-sm text-anvil-600"
                      />
                    )}
                  </span>
                </button>
              );
            })}
          </div>
        </fieldset>

        {showNote && !currentSchemaError && (
          <label className="mt-6 block">
            <span className="mb-2 block text-sm font-medium text-anvil-800">
              {noteSchema?.label ?? '备注'}
              {!noteSchema?.required && <span className="ml-1 font-normal text-anvil-500">（可选）</span>}
            </span>
            <textarea
              value={note}
              onChange={(event) => {
                setNote(event.target.value);
                setValidationError(null);
              }}
              rows={3}
              disabled={controlsDisabled}
              placeholder={noteSchema?.placeholder}
              className="forge-input resize-y"
            />
          </label>
        )}

        {(unavailableError || validationError || pageError) && (
          <div
            role="alert"
            className="mt-5 border border-danger-400/40 bg-danger-50 px-4 py-3 text-sm text-danger-600"
          >
            {unavailableError || validationError || pageError}
          </div>
        )}

        <div className="mt-6 flex justify-end">
          <button
            type="button"
            className="forge-btn-primary min-h-11 min-w-36"
            disabled={controlsDisabled}
            onClick={() => void handleSubmit()}
          >
            {submitting ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                提交中...
              </>
            ) : (
              <>
                提交并继续
                <ArrowRight className="h-4 w-4" aria-hidden="true" />
              </>
            )}
          </button>
        </div>
      </main>
    </div>
  );
}
