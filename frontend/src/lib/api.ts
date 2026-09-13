// ============================================================================
// Qraft - API Client
// ============================================================================

import { desktopRuntime } from './desktop-runtime';

import type {
  APIResponse,
  ActiveEmbeddingModelStatus,
  EmbeddingModelVersion,
  EmbeddingRuntimeSettings,
  AnnotationNext,
  AnnotationSubmitRequest,
  AnnotationSubmitResult,
  DashboardStats,
  GPLTBatchParams,
  GPLTBatchTriggerResponse,
  HydroValidationReport,
  IntegrationCapabilities,
  ImportReport,
  KnowledgePoint,
  LocalEmbeddingActivateRequest,
  LocalEmbeddingActivateResult,
  LocalEmbeddingBackfillRequest,
  LocalEmbeddingBackfillResult,
  LocalEmbeddingDeployRequest,
  LocalEmbeddingDeployResult,
  LocalEmbeddingEndpointConfig,
  LocalEmbeddingTestResult,
  OnConflict,
  PersistentLLMProviderSetting,
  PersistentLLMProviderSettings,
  PersistentLLMProviderSettingUpdate,
  LLMOverridePurpose,
  LLMProviderPurpose,
  Problem,
  ProblemEditRefreshReport,
  ProblemEditRefreshRequest,
  ProblemFilter,
  ProblemGenerateResponse,
  ProblemGenParams,
  ProblemSolution,
  ProblemUpdateRequest,
  PublicReleaseApprovalReport,
  PublicReleaseApprovalRequest,
  QuizFilter,
  QuestionSearchFilter,
  QuestionSearchItem,
  ReviewSettings,
  ReviewSettingsUpdate,
  QuizGenerateParams,
  QuizGenerateResponse,
  QuizProblem,
  ProblemSet,
  ProblemSetGenerationState,
  ProblemSetAssemblyRequest,
  ProblemSetAssembleRequest,
  ProblemSetAssemblyPreview,
  ProblemSetAddItemRequest,
  ProblemSetCreateRequest,
  ProblemSetListFilter,
  ProblemSetQuality,
  ProblemSetUpdateRequest,
  TagCategory,
  TestCase,
  WorkflowReviewActionResponse,
  WorkflowReviewReference,
  WorkflowRetryResponse,
  WorkflowState,
} from './types';
import { buildQueryString } from './utils';

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const API_BASE_URL = (
  process.env.NEXT_PUBLIC_API_URL ?? ''
).trim();

export function getApiBaseUrl(): string {
  const desktop = desktopRuntime();
  if (desktop) return desktop.api_base;
  return API_BASE_URL.endsWith('/')
    ? API_BASE_URL.slice(0, -1)
    : API_BASE_URL;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function apiUrl(path: string): string {
  const base = getApiBaseUrl();
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return `${base}${normalizedPath}`;
}

function appendQuery(url: string, params: URLSearchParams): string {
  const query = params.toString();
  if (!query) return url;
  return `${url}${url.includes('?') ? '&' : '?'}${query}`;
}

function getAuthHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  };

  if (typeof window !== 'undefined') {
    const token = localStorage.getItem('algoforge_token');
    if (token) {
      headers['Authorization'] = `Bearer ${token}`;
    }
  }

  return headers;
}

class APIError extends Error {
  code: string;
  status: number;

  constructor(message: string, code: string, status: number) {
    super(message);
    this.name = 'APIError';
    this.code = code;
    this.status = status;
  }
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<APIResponse<T>> {
  const url = apiUrl(path);

  let response: Response;
  try {
    response = await fetch(url, {
      ...options,
      headers: {
        ...getAuthHeaders(),
        ...(options.headers as Record<string, string>),
      },
    });
  } catch {
    throw new APIError(
      `无法连接 API 服务（${getApiBaseUrl()}）`,
      'NETWORK_ERROR',
      0,
    );
  }

  // Handle non-JSON responses (e.g. file downloads)
  const contentType = response.headers.get('content-type') || '';
  if (!contentType.includes('application/json')) {
    if (!response.ok) {
      throw new APIError(
        `Request failed: ${response.statusText}`,
        'NETWORK_ERROR',
        response.status,
      );
    }
    // For blob downloads, the caller handles the response directly
    return { success: true, data: response as unknown as T };
  }

  const body: APIResponse<T> = await response.json();

  if (!response.ok || !body.success) {
    throw new APIError(
      body.error?.message || `Request failed with status ${response.status}`,
      body.error?.code || 'UNKNOWN_ERROR',
      response.status,
    );
  }

  return body;
}

async function anonymousRequest<T>(
  path: string,
  options: RequestInit = {},
): Promise<APIResponse<T>> {
  let response: Response;
  try {
    response = await fetch(apiUrl(path), {
      ...options,
      credentials: 'omit',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {}),
        ...(options.headers as Record<string, string>),
      },
    });
  } catch {
    throw new APIError(
      `无法连接 API 服务（${getApiBaseUrl()}）`,
      'NETWORK_ERROR',
      0,
    );
  }

  const contentType = response.headers.get('content-type') || '';
  if (!contentType.includes('application/json')) {
    throw new APIError(
      response.ok ? 'Invalid server response' : `Request failed: ${response.statusText}`,
      'INVALID_RESPONSE',
      response.status,
    );
  }

  const body: APIResponse<T> = await response.json();
  if (!response.ok || !body.success) {
    throw new APIError(
      body.error?.message || `Request failed with status ${response.status}`,
      body.error?.code || 'UNKNOWN_ERROR',
      response.status,
    );
  }

  return body;
}

// ---------------------------------------------------------------------------
// External annotation (token is the identity; internal JWT is never sent)
// ---------------------------------------------------------------------------

export async function getNextAnnotation(
  token: string,
  signal?: AbortSignal,
): Promise<APIResponse<AnnotationNext>> {
  return anonymousRequest<AnnotationNext>(
    `/api/v1/annotate/${encodeURIComponent(token)}/next`,
    { signal },
  );
}

export async function submitAnnotation(
  token: string,
  payload: AnnotationSubmitRequest,
  signal?: AbortSignal,
): Promise<APIResponse<AnnotationSubmitResult>> {
  return anonymousRequest<AnnotationSubmitResult>(
    `/api/v1/annotate/${encodeURIComponent(token)}/submit`,
    {
      method: 'POST',
      body: JSON.stringify(payload),
      signal,
    },
  );
}

// ---------------------------------------------------------------------------
// Service health
// ---------------------------------------------------------------------------

export async function getApiHealth(
  signal?: AbortSignal,
): Promise<{ status: string }> {
  let response: Response;
  try {
    response = await fetch(apiUrl('/health'), {
      signal,
      cache: 'no-store',
      headers: {
        Accept: 'application/json',
      },
    });
  } catch {
    throw new APIError(
      `无法连接 API 服务（${getApiBaseUrl()}）`,
      'NETWORK_ERROR',
      0,
    );
  }

  const contentType = response.headers.get('content-type') || '';
  if (!response.ok || !contentType.includes('application/json')) {
    throw new APIError(
      response.ok ? 'API 服务响应格式异常' : `API 服务异常: ${response.statusText}`,
      'HEALTH_CHECK_FAILED',
      response.status,
    );
  }

  return response.json() as Promise<{ status: string }>;
}

// The capabilities endpoint intentionally returns its versioned document
// directly instead of the usual { success, data } envelope.
export async function getIntegrationCapabilities(
  signal?: AbortSignal,
): Promise<IntegrationCapabilities> {
  let response: Response;
  try {
    response = await fetch(apiUrl('/api/v1/integration/capabilities'), {
      signal,
      cache: 'no-store',
      headers: {
        Accept: 'application/json',
      },
    });
  } catch {
    throw new APIError(
      `无法连接 API 服务（${getApiBaseUrl()}）`,
      'NETWORK_ERROR',
      0,
    );
  }

  const contentType = response.headers.get('content-type') || '';
  if (!response.ok || !contentType.includes('application/json')) {
    throw new APIError(
      response.ok
        ? '能力接口响应格式异常'
        : `能力接口异常: ${response.statusText}`,
      'CAPABILITIES_CHECK_FAILED',
      response.status,
    );
  }

  const body = (await response.json()) as IntegrationCapabilities;
  if (body.schema_version !== 'algoforge.integration-capabilities.v1') {
    throw new APIError('能力接口版本不受支持', 'CAPABILITIES_VERSION_UNSUPPORTED', response.status);
  }
  return body;
}

// ---------------------------------------------------------------------------
// Problems
// ---------------------------------------------------------------------------

export async function generateProblem(
  params: ProblemGenParams,
): Promise<APIResponse<ProblemGenerateResponse>> {
  return request<ProblemGenerateResponse>('/api/v1/problems/generate', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function generateGPLTBatch(
  params: GPLTBatchParams,
): Promise<APIResponse<GPLTBatchTriggerResponse>> {
  return request<GPLTBatchTriggerResponse>('/api/v1/problems/gplt/generate', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function listProblems(
  filter?: ProblemFilter,
): Promise<APIResponse<Problem[]>> {
  const qs = filter ? buildQueryString(filter as Record<string, unknown>) : '';
  return request<Problem[]>(`/api/v1/problems${qs}`);
}

export async function getProblem(id: string): Promise<APIResponse<Problem>> {
  return request<Problem>(`/api/v1/problems/${id}`);
}

export async function updateProblem(
  id: string,
  data: ProblemUpdateRequest,
): Promise<APIResponse<Problem>> {
  return request<Problem>(`/api/v1/problems/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
}

export async function deleteProblem(id: string): Promise<APIResponse<void>> {
  return request<void>(`/api/v1/problems/${id}`, { method: 'DELETE' });
}

export async function completeProblemEditRefresh(
  problemId: string,
  payload: ProblemEditRefreshRequest,
): Promise<APIResponse<ProblemEditRefreshReport>> {
  return request<ProblemEditRefreshReport>(
    `/api/v1/problems/${problemId}/edit-refresh`,
    {
      method: 'POST',
      body: JSON.stringify(payload),
    },
  );
}

export async function approveProblemPublicRelease(
  problemId: string,
  payload: PublicReleaseApprovalRequest = { approved: true },
): Promise<APIResponse<PublicReleaseApprovalReport>> {
  return request<PublicReleaseApprovalReport>(
    `/api/v1/problems/${problemId}/public-release-approval`,
    {
      method: 'POST',
      body: JSON.stringify(payload),
    },
  );
}

// ---------------------------------------------------------------------------
// Test Cases
// ---------------------------------------------------------------------------

export async function getTestcases(
  problemId: string,
): Promise<APIResponse<TestCase[]>> {
  return request<TestCase[]>(`/api/v1/problems/${problemId}/testcases`);
}

export function downloadTestcaseInput(
  problemId: string,
  testcaseId: string,
): string {
  return apiUrl(
    `/api/v1/problems/${problemId}/testcases/${testcaseId}/input`,
  );
}

export function downloadTestcaseOutput(
  problemId: string,
  testcaseId: string,
): string {
  return apiUrl(
    `/api/v1/problems/${problemId}/testcases/${testcaseId}/output`,
  );
}

export function downloadAllTestData(problemId: string): string {
  return apiUrl(`/api/v1/problems/${problemId}/testdata.zip`);
}

export function downloadHydroPackage(problemId: string): string {
  return apiUrl(`/api/v1/problems/${problemId}/hydro.zip`);
}

export function downloadHydroBatch(problemIds: string[]): string {
  const qs = new URLSearchParams();
  problemIds.forEach((id) => qs.append('ids', id));
  return apiUrl(`/api/v1/problems/hydro.zip?${qs.toString()}`);
}

export async function validateHydroPackage(
  file: File,
): Promise<APIResponse<HydroValidationReport>> {
  const fd = new FormData();
  fd.append('file', file);
  const token =
    typeof window !== 'undefined'
      ? localStorage.getItem('algoforge_token')
      : null;
  const res = await fetch(apiUrl('/api/v1/problems/hydro/validate'), {
    method: 'POST',
    body: fd,
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  const body: APIResponse<HydroValidationReport> = await res.json();
  if (!res.ok || !body.success) {
    throw new APIError(
      body.error?.message || `Hydro validation failed (${res.status})`,
      body.error?.code || 'HYDRO_VALIDATE_FAILED',
      res.status,
    );
  }
  return body;
}

// ---------------------------------------------------------------------------
// Metadata & Editorial
// ---------------------------------------------------------------------------

export async function getMetadata(
  problemId: string,
): Promise<APIResponse<Record<string, unknown>>> {
  return request<Record<string, unknown>>(
    `/api/v1/problems/${problemId}/metadata`,
  );
}

export async function getEditorial(
  problemId: string,
): Promise<APIResponse<{ editorial: string }>> {
  return request<{ editorial: string }>(
    `/api/v1/problems/${problemId}/editorial`,
  );
}

export async function getProblemSolutions(
  problemId: string,
): Promise<APIResponse<ProblemSolution[]>> {
  return request<ProblemSolution[]>(
    `/api/v1/problems/${problemId}/solutions`,
  );
}

// ---------------------------------------------------------------------------
// Validation & Similarity
// ---------------------------------------------------------------------------

export async function validateProblem(
  problemId: string,
): Promise<APIResponse<{ valid: boolean; issues: string[]; workflow_id?: string; run_id?: string }>> {
  return request<{ valid: boolean; issues: string[]; workflow_id?: string; run_id?: string }>(
    `/api/v1/problems/${problemId}/validate`,
    { method: 'POST' },
  );
}

export async function getSimilarProblems(
  problemId: string,
  limit: number = 10,
): Promise<APIResponse<{ problem: Problem; similarity: number }[]>> {
  const qs = buildQueryString({ problem_id: problemId, limit });
  return request<{ problem: Problem; similarity: number }[]>(`/api/v1/problems/similar${qs}`);
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

export async function listTags(): Promise<APIResponse<TagCategory[]>> {
  return request<TagCategory[]>('/api/v1/tags');
}

export async function listTagsByLevel(
  level: string,
  difficulty?: number,
): Promise<APIResponse<TagCategory[]>> {
  let url = `/api/v1/tags?level=${encodeURIComponent(level)}`;
  if (difficulty) {
    url += `&difficulty=${difficulty}`;
  }
  return request<TagCategory[]>(url);
}

// ---------------------------------------------------------------------------
// Workflows
// ---------------------------------------------------------------------------

export async function listWorkflows(
  params?: { page?: number; size?: number; status?: string },
): Promise<APIResponse<WorkflowState[]>> {
  const qs = params
    ? buildQueryString(params as Record<string, unknown>)
    : '';
  return request<WorkflowState[]>(`/api/v1/workflows${qs}`);
}

export async function getWorkflow(
  id: string,
): Promise<APIResponse<WorkflowState>> {
  return request<WorkflowState>(`/api/v1/workflows/${id}`);
}

export async function approveWorkflow(
  id: string,
  review: WorkflowReviewReference,
): Promise<APIResponse<WorkflowReviewActionResponse>> {
  return request<WorkflowReviewActionResponse>(`/api/v1/workflows/${id}/approve`, {
    method: 'POST',
    body: JSON.stringify({
      token: review.token,
      run_id: review.run_id,
      review_attempt: review.review_attempt,
    }),
  });
}

export async function rejectWorkflow(
  id: string,
  review: WorkflowReviewReference,
  feedback: string,
): Promise<APIResponse<WorkflowReviewActionResponse>> {
  return request<WorkflowReviewActionResponse>(`/api/v1/workflows/${id}/reject`, {
    method: 'POST',
    body: JSON.stringify({
      token: review.token,
      run_id: review.run_id,
      review_attempt: review.review_attempt,
      feedback,
    }),
  });
}

export async function retryWorkflow(
  id: string,
): Promise<APIResponse<WorkflowRetryResponse>> {
  return request<WorkflowRetryResponse>(`/api/v1/workflows/${id}/retry`, {
    method: 'POST',
  });
}

export async function cancelWorkflow(
  id: string,
): Promise<APIResponse<WorkflowState>> {
  return request<WorkflowState>(`/api/v1/workflows/${id}`, {
    method: 'DELETE',
  });
}

/**
 * Subscribe to real-time workflow events via Server-Sent Events.
 * Returns the EventSource instance so the caller can close it.
 */
export function subscribeWorkflowEvents(
  workflowId: string,
  onEvent: (event: MessageEvent) => void,
  onError?: (event: Event) => void,
): EventSource {
  const token =
    typeof window !== 'undefined'
      ? localStorage.getItem('algoforge_token')
      : null;

  let url = apiUrl(
    `/api/v1/workflows/${encodeURIComponent(workflowId)}/events`,
  );
  if (token) {
    url = appendQuery(url, new URLSearchParams({ token }));
  }

  const eventSource = new EventSource(url);
  eventSource.onmessage = onEvent;
  if (onError) {
    eventSource.onerror = onError;
  }

  return eventSource;
}

// ---------------------------------------------------------------------------
// Dashboard Stats
// ---------------------------------------------------------------------------

export async function getStats(): Promise<APIResponse<DashboardStats>> {
  return request<DashboardStats>('/api/v1/stats');
}

export async function getPermanentLLMSettings(): Promise<APIResponse<PersistentLLMProviderSettings>> {
  return request<PersistentLLMProviderSettings>('/api/v1/settings/llm');
}

export async function updatePermanentLLMSetting(
  purpose: LLMProviderPurpose,
  payload: PersistentLLMProviderSettingUpdate,
): Promise<APIResponse<PersistentLLMProviderSetting>> {
  return request<PersistentLLMProviderSetting>(
    `/api/v1/settings/llm/${encodeURIComponent(purpose)}`,
    {
      method: 'PUT',
      body: JSON.stringify(payload),
    },
  );
}

export async function resetPermanentLLMSetting(
  purpose: LLMOverridePurpose,
): Promise<APIResponse<PersistentLLMProviderSetting>> {
  return request<PersistentLLMProviderSetting>(
    `/api/v1/settings/llm/${encodeURIComponent(purpose)}`,
    { method: 'DELETE' },
  );
}

export async function getReviewSettings(): Promise<APIResponse<ReviewSettings>> {
  return request<ReviewSettings>('/api/v1/settings/review');
}

export async function updateReviewSettings(
  payload: ReviewSettingsUpdate,
): Promise<APIResponse<ReviewSettings>> {
  return request<ReviewSettings>('/api/v1/settings/review', {
    method: 'PUT',
    body: JSON.stringify(payload),
  });
}

export async function getEmbeddingStatus(): Promise<APIResponse<ActiveEmbeddingModelStatus[]>> {
  return request<ActiveEmbeddingModelStatus[]>('/api/v1/embedding/status');
}

export async function listEmbeddingModels(): Promise<APIResponse<EmbeddingModelVersion[]>> {
  return request<EmbeddingModelVersion[]>('/api/v1/embedding/models');
}

export async function getEmbeddingRuntimeSettings(): Promise<APIResponse<EmbeddingRuntimeSettings>> {
  return request<EmbeddingRuntimeSettings>('/api/v1/embedding/runtime-settings');
}

export async function testLocalEmbeddingEndpoint(
  payload: LocalEmbeddingEndpointConfig,
): Promise<APIResponse<LocalEmbeddingTestResult>> {
  return request<LocalEmbeddingTestResult>('/api/v1/embedding/local/test', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function deployLocalEmbeddingModel(
  payload: LocalEmbeddingDeployRequest,
): Promise<APIResponse<LocalEmbeddingDeployResult>> {
  return request<LocalEmbeddingDeployResult>('/api/v1/embedding/local/deploy', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function backfillLocalEmbeddingModel(
  payload: LocalEmbeddingBackfillRequest,
): Promise<APIResponse<LocalEmbeddingBackfillResult>> {
  return request<LocalEmbeddingBackfillResult>('/api/v1/embedding/local/backfill', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function activateLocalEmbeddingModel(
  payload: LocalEmbeddingActivateRequest,
): Promise<APIResponse<LocalEmbeddingActivateResult>> {
  return request<LocalEmbeddingActivateResult>('/api/v1/embedding/local/activate', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

// ---------------------------------------------------------------------------
// Quizzes
// ---------------------------------------------------------------------------

export async function listQuizzes(
  filter?: QuizFilter,
): Promise<APIResponse<QuizProblem[]>> {
  const qs = filter ? buildQueryString(filter as Record<string, unknown>) : '';
  return request<QuizProblem[]>(`/api/v1/quizzes${qs}`);
}

export async function getQuiz(id: string): Promise<APIResponse<QuizProblem>> {
  return request<QuizProblem>(`/api/v1/quizzes/${id}`);
}

export async function createQuiz(
  body: Partial<QuizProblem>,
): Promise<APIResponse<QuizProblem>> {
  return request<QuizProblem>('/api/v1/quizzes', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export async function updateQuiz(
  id: string,
  body: Partial<QuizProblem>,
): Promise<APIResponse<QuizProblem>> {
  return request<QuizProblem>(`/api/v1/quizzes/${id}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
}

export async function deleteQuiz(id: string): Promise<APIResponse<void>> {
  return request<void>(`/api/v1/quizzes/${id}`, { method: 'DELETE' });
}

export async function generateQuiz(
  params: QuizGenerateParams,
): Promise<APIResponse<QuizGenerateResponse>> {
  return request<QuizGenerateResponse>('/api/v1/quizzes/generate', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function importQuizzes(
  file: File,
  subject: string,
  onConflict: OnConflict,
): Promise<APIResponse<ImportReport>> {
  const fd = new FormData();
  fd.append('file', file);
  fd.append('subject', subject);
  const url =
    `/api/v1/quizzes/import?` +
    `subject=${encodeURIComponent(subject)}` +
    `&on_conflict=${encodeURIComponent(onConflict)}`;
  const token =
    typeof window !== 'undefined'
      ? localStorage.getItem('algoforge_token')
      : null;
  const res = await fetch(apiUrl(url), {
    method: 'POST',
    body: fd,
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  });
  const body: APIResponse<ImportReport> = await res.json();
  if (!res.ok || !body.success) {
    throw new APIError(
      body.error?.message || `Import failed (${res.status})`,
      body.error?.code || 'IMPORT_FAILED',
      res.status,
    );
  }
  return body;
}

export function exportQuizzesURL(filter: QuizFilter): string {
  const qs = buildQueryString(filter as Record<string, unknown>);
  return apiUrl(`/api/v1/quizzes/export${qs}`);
}

// ---------------------------------------------------------------------------
// Contest / problem sets
// ---------------------------------------------------------------------------

export async function listProblemSets(
  filter?: ProblemSetListFilter,
): Promise<APIResponse<ProblemSet[]>> {
  const qs = filter ? buildQueryString(filter as Record<string, unknown>) : '';
  return request<ProblemSet[]>(`/api/v1/problem-sets${qs}`);
}

export async function getProblemSet(
  id: string,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>(`/api/v1/problem-sets/${id}`);
}

export async function createProblemSet(
  payload: ProblemSetCreateRequest,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>('/api/v1/problem-sets', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function reorderProblemSetItems(id: string, itemIDs: string[]): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>('/api/v1/problem-sets/' + id + '/items/reorder', { method: 'PUT', body: JSON.stringify({ item_ids: itemIDs }) });
}

export async function previewProblemSetAssembly(payload: ProblemSetAssemblyRequest): Promise<APIResponse<ProblemSetAssemblyPreview>> {
  return request<ProblemSetAssemblyPreview>('/api/v1/problem-sets/assembly-preview', { method: 'POST', body: JSON.stringify(payload) });
}

export async function assembleProblemSet(payload: ProblemSetAssembleRequest): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>('/api/v1/problem-sets/assemble', { method: 'POST', body: JSON.stringify(payload) });
}

export async function updateProblemSet(
  id: string,
  payload: ProblemSetUpdateRequest,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>(`/api/v1/problem-sets/${id}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  });
}

export async function startProblemSetGeneration(id: string): Promise<APIResponse<ProblemSetGenerationState>> {
  return request<ProblemSetGenerationState>(`/api/v1/problem-sets/${id}/generation`, { method: 'POST' });
}

export async function getProblemSetGeneration(id: string): Promise<APIResponse<ProblemSetGenerationState | null>> {
  return request<ProblemSetGenerationState | null>(`/api/v1/problem-sets/${id}/generation`);
}

export async function cancelProblemSetGeneration(id: string): Promise<APIResponse<{ stop_requested: boolean }>> {
  return request<{ stop_requested: boolean }>(`/api/v1/problem-sets/${id}/generation/cancel`, { method: 'POST' });
}

export async function addProblemSetItem(
  id: string,
  payload: ProblemSetAddItemRequest,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>(`/api/v1/problem-sets/${id}/items`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export async function removeProblemSetItem(
  setId: string,
  itemId: string,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>(
    `/api/v1/problem-sets/${setId}/items/${itemId}`,
    { method: 'DELETE' },
  );
}

export async function getProblemSetQuality(
  id: string,
): Promise<APIResponse<ProblemSetQuality>> {
  return request<ProblemSetQuality>(`/api/v1/problem-sets/${id}/quality`);
}

export async function generateProblemSetPrompt(
  id: string,
): Promise<APIResponse<ProblemSet>> {
  return request<ProblemSet>(`/api/v1/problem-sets/${id}/generate-prompt`, {
    method: 'POST',
  });
}

export function exportProblemSetURL(id: string, allowReuse = false): string {
  const suffix = allowReuse ? '?allow_reuse=true' : '';
  return apiUrl(`/api/v1/problem-sets/${id}/export.zip${suffix}`);
}

// ---------------------------------------------------------------------------
// Knowledge Points
// ---------------------------------------------------------------------------

export async function listKnowledgePoints(
  subject?: string,
): Promise<APIResponse<KnowledgePoint[]>> {
  const qs = subject ? `?subject=${encodeURIComponent(subject)}` : '';
  return request<KnowledgePoint[]>(`/api/v1/knowledge-points${qs}`);
}

// ---------------------------------------------------------------------------
// Export the error class for consumers
// ---------------------------------------------------------------------------

export { APIError };

export function quizTemplateURL(): string {
  return apiUrl("/api/v1/quizzes/template.xlsx");
}


export async function searchQuestions(
  filter: QuestionSearchFilter,
  signal?: AbortSignal,
): Promise<APIResponse<QuestionSearchItem[]>> {
  return request<QuestionSearchItem[]>(
    '/api/v1/questions/search' + buildQueryString(filter as Record<string, unknown>),
    { signal },
  );
}
