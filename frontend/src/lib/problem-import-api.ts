import { APIError, getApiBaseUrl } from './api';
import { serviceFetch } from './auth-session';

export type SourceItem = { id: string; url: string; title: string; statement?: string; statement_sha256?: string; warnings?: string[] };
export type SourceDocument = { url: string; final_url: string; title: string; kind: 'problem' | 'collection'; items: SourceItem[]; warnings?: string[] };
export type ImportMode = 'inspiration' | 'preserve_statement';
export type ImportInputItem = { item_id: string; title: string; statement: string; source_url?: string; source_id?: string };
export type ImportRequest = { title?: string; mode: ImportMode; items: ImportInputItem[]; create_set: boolean; difficulty?: number; language?: string; locale?: string };
export type ImportItemResult = { item_id: string; title: string; status: 'pending' | 'running' | 'imported' | 'skipped_duplicate' | 'failed' | 'assessment_failed'; problem_id?: string; workflow_id?: string; duplicate_of?: string; rating_assessment_id?: string; estimated_difficulty?: number; difficulty_reason?: string; warning?: string; error?: string; statement_changed?: boolean; clarification_reason?: string };
export type ImportReport = { workflow_id: string; status: string; problem_set_id?: string; items: ImportItemResult[]; error?: string };

async function request<T>(path: string, payload?: unknown): Promise<T> {
 const response = await serviceFetch(getApiBaseUrl() + path, {
  method: payload === undefined ? 'GET' : 'POST',
  headers: { 'Content-Type': 'application/json' },
  ...(payload === undefined ? {} : { body: JSON.stringify(payload) }),
 });
 const body = await response.json().catch(() => null);
 if (!response.ok || body?.success === false) throw new APIError(body?.error?.message ?? '请求失败，请稍后重试。', body?.error?.code ?? 'IMPORT_ERROR', response.status);
 return body.data as T;
}
export const previewProblemSource = (url: string) => request<SourceDocument>('/api/v1/sources/preview', { url });
export const startProblemImport = (value: ImportRequest) => request<{ workflow_id: string }>('/api/v1/problem-imports', value);
export const getProblemImport = (id: string) => request<ImportReport>('/api/v1/problem-imports/' + encodeURIComponent(id));
export const importActive = (status: string) => ['pending', 'queued', 'running'].includes(status);

export const resumeProblemImport = (id: string) => request<{ workflow_id: string }>('/api/v1/problem-imports/' + encodeURIComponent(id) + '/resume', {});
