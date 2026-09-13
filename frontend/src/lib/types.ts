// ============================================================================
// Qraft - Shared TypeScript Types
// ============================================================================

// ---------------------------------------------------------------------------
// API Response
// ---------------------------------------------------------------------------

export interface APIResponse<T> {
  success: boolean;
  data?: T;
  error?: { code: string; message: string };
  meta?: { total: number; page: number; size: number };
}

// ---------------------------------------------------------------------------
// Integration capabilities
// ---------------------------------------------------------------------------

export interface IntegrationRolloutCapability {
  mode: string;
  enabled: boolean;
  flag_name: string;
  rollback_mode: string;
  rollback_requires_restart: boolean;
  routes: string[];
}

export interface IntegrationExportCapability extends Omit<IntegrationRolloutCapability, 'enabled'> {
  hydro_routes_enabled: boolean;
  portable_sets_enabled: boolean;
}

export interface IntegrationProblemSetCapability {
  enabled: boolean;
  routes: string[];
  supported_types: QuizType[];
  modes: ('programming' | 'mixed')[];
  min_item_count: number;
  max_item_count: number;
  generation: { enabled: boolean; routes: string[]; flag_name: string; asynchronous: boolean; retains_completed_items: boolean };
  assembly: { enabled: boolean; routes: string[]; requires_model: boolean };
  export: { enabled: boolean; routes: string[]; flag_name: string; format: string; scores_included: boolean };
}

export interface IntegrationCapabilities {
  release_version?: string;
  problem_sets?: IntegrationProblemSetCapability;
  schema_version: string;
  generation_jobs: IntegrationRolloutCapability;
  generation_micro_batches: IntegrationRolloutCapability;
  quality_layer: IntegrationRolloutCapability;
  exports: IntegrationExportCapability;
  generation_evidence_levels: string[];
  hydro_phase2_enabled: boolean;
}

// ---------------------------------------------------------------------------
// External annotation
// ---------------------------------------------------------------------------

export type AnnotationSelectionMode = 'single' | 'multi';

export interface AnnotationOption {
  value: string;
  label: string;
  description?: string;
}

export interface AnnotationNoteSchema {
  enabled?: boolean;
  label?: string;
  placeholder?: string;
  required?: boolean;
}

export interface AnnotationOptionsSchema {
  mode: AnnotationSelectionMode;
  options: AnnotationOption[];
  prompt?: string;
  min_selections?: number;
  max_selections?: number;
  note?: AnnotationNoteSchema;
}

export interface AnnotationProgress {
  completed: number;
  total: number;
  position: number;
}

export interface AnnotationItem {
  item_id: string;
  batch_code: string;
  task_type: string;
  codebook_url?: string;
  payload: unknown;
  options_schema: AnnotationOptionsSchema;
  progress: AnnotationProgress;
}

export interface AnnotationNext {
  done: boolean;
  item?: AnnotationItem;
}

export interface AnnotationSubmitRequest {
  item_id: string;
  response: {
    selections: string[];
    note?: string;
  };
  duration_ms: number;
}

export interface AnnotationSubmitResult {
  accepted?: boolean;
  item_id?: string;
}

// ---------------------------------------------------------------------------
// Problem
// ---------------------------------------------------------------------------

export type ProblemLevel = 'syntax' | 'algorithm' | 'gplt_l1' | 'gplt_l2' | 'gplt_l3';
export type ProblemStatus = 'draft' | 'generating' | 'review' | 'published' | 'rejected' | 'quarantined';

export interface Problem {
  id: string;
  serial_number: string;
  title: string;
  statement: string;
  level: ProblemLevel;
  difficulty: number;
  tags: string[];
  one_line_hint?: string;
  detailed_solution?: string;
  time_limit: number;
  memory_limit: number;
  status: ProblemStatus;
  workflow_id?: string;
  metadata_json?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

// Executable source artefact associated with a problem. This is intentionally
// separate from Problem.detailed_solution, which is the human-readable
// editorial Markdown.
export interface ProblemSolution {
  id: string;
  problem_id: string;
  solution_type: 'main' | 'brute' | 'generator' | 'checker' | string;
  language: string;
  source_code: string;
  compile_status?: string;
  created_at: string;
}

export interface ProblemUpdateRequest {
  expected_updated_at: string;
  title?: string;
  statement?: string;
  level?: ProblemLevel;
  difficulty?: number;
  tags?: string[];
  one_line_hint?: string;
  detailed_solution?: string;
  time_limit?: number;
  memory_limit?: number;
  metadata_json?: Record<string, unknown>;
}

export interface ProblemEditRefreshRequest {
  operation_key?: string;
  validation_report_sha256: string;
  actor?: string;
}

export interface ProblemEditRefreshPreflight {
  problem_stale: boolean;
  current_active_statement_vector_count: number;
  current_active_statement_vector_stale: number;
  successful_main_solution_count: number;
  successful_brute_solution_count: number;
  runnable_testcase_count: number;
  stale_solution_count: number;
  stale_testcase_count: number;
}

export interface ProblemEditRefreshReport {
  generated_at: string;
  decision: 'go' | 'no_go' | string;
  problem_id: string;
  operation_key: string;
  validation_report_sha256: string;
  preflight: ProblemEditRefreshPreflight;
  blocking_issues?: string[];
  release_status?: ProblemStatus;
  release_quarantine_reason?: string;
}

export interface PublicReleaseApprovalRequest {
  approved: true;
}

export interface PublicReleaseApprovalReport {
  problem_id: string;
  artifact_id: string;
  approval_id: string;
  approved_by: string;
  approved_at: string;
  release_status: ProblemStatus;
  release_quarantine_reason?: string;
}

// ---------------------------------------------------------------------------
// Solution
// ---------------------------------------------------------------------------

export interface Solution {
  id: string;
  problem_id: string;
  language: string;
  code: string;
  is_model_solution: boolean;
  execution_time_ms?: number;
  memory_usage_kb?: number;
  created_at: string;
}

// ---------------------------------------------------------------------------
// Test Case
// ---------------------------------------------------------------------------

export interface TestCase {
  id: string;
  problem_id: string;
  group_index: number;
  case_index: number;
  is_sample: boolean;
  input_preview?: string;
  output_preview?: string;
  input_size_bytes: number;
  output_size_bytes: number;
  description?: string;
  created_at: string;
}

// ---------------------------------------------------------------------------
// Tags
// ---------------------------------------------------------------------------

// Each TagCategory row from the backend is a single flat tag entry.
export interface TagCategory {
  id: number;
  level: ProblemLevel;
  tag_name: string;
  display_name: string;
  description?: string;
  sort_order: number;
  min_difficulty: number;
  max_difficulty: number;
}

// ---------------------------------------------------------------------------
// Workflow
// ---------------------------------------------------------------------------

// WorkflowStatus covers both Temporal execution statuses (capitalized, from
// the backend) and the friendly lowercase aliases used in UI filters.
export type WorkflowStatus =
  | 'pending'
  | 'running'
  | 'waiting_review'
  | 'rejected_quarantined'
  | 'approved'
  | 'rejected'
  | 'failed'
  | 'cancelled'
  | 'Running'
  | 'Completed'
  | 'Failed'
  | 'Canceled'
  | 'TimedOut'
  | 'Terminated'
  | string;

export interface LLMReviewSummary {
  approved: boolean;
  issues?: string[];
  suggestions?: string[];
  confidence: number;
  estimated_difficulty?: number;
}

export interface WorkflowReviewDecision {
  approved: boolean;
  feedback?: string;
}

export interface WorkflowReviewReference {
  token: string;
  run_id: string;
  review_attempt: number;
}

export interface WorkflowReviewRequest extends WorkflowReviewReference {
  review_result_hash: string;
  token_required: boolean;
  review_result: LLMReviewSummary;
  decision?: WorkflowReviewDecision;
}

export interface WorkflowReviewActionResponse {
  workflow_id: string;
  run_id: string;
  review_attempt: number;
  action: 'approved' | 'rejected';
}

export interface WorkflowRetryResponse {
  workflow_id: string;
  old_run_id: string;
  run_id: string;
  action: 'restarted';
}

// WorkflowState matches the backend's actual API response from Temporal.
export interface WorkflowState {
  workflow_id: string;
  run_id: string;
  status: WorkflowStatus;
  execution_status?: WorkflowStatus;
  review_request?: WorkflowReviewRequest;
  start_time?: string;
  close_time?: string;
  // Only present for completed workflows (detail endpoint)
  state?: {
    problem_id?: string;
    status?: string;
    current_step?: string;
    error?: string;
    [key: string]: unknown;
  };
  // Present when workflow has failed — extracted from Temporal history
  failure_reason?: string;
  // Used by workflow visualization components (WorkflowCanvas)
  steps?: WorkflowStep[];
}

// ---------------------------------------------------------------------------
// Workflow Steps (used by workflow visualization components)
// ---------------------------------------------------------------------------

export interface StepResult {
  success: boolean;
  message?: string;
  artifacts?: string[];
}

export interface WorkflowStep {
  name: string;
  description: string;
  status: 'pending' | 'running' | 'completed' | 'failed' | 'skipped';
  started_at?: string;
  completed_at?: string;
  result?: StepResult;
}

// ---------------------------------------------------------------------------
// Workflow SSE Events
// ---------------------------------------------------------------------------

export interface WorkflowEvent {
  type: 'step_started' | 'step_completed' | 'step_failed' | 'workflow_completed' | 'workflow_failed' | 'log';
  workflow_id: string;
  step_index?: number;
  step_name?: string;
  message?: string;
  data?: Record<string, unknown>;
  timestamp: string;
}

// ---------------------------------------------------------------------------
// Test Data Configuration
// ---------------------------------------------------------------------------

export interface ConstraintRange {
  min: number;
  max: number;
  variable: string;
  description?: string;
}

export interface BoundaryConfig {
  include_min: boolean;
  include_max: boolean;
  include_zero: boolean;
  include_negative: boolean;
  custom_boundaries: number[];
}

export interface CustomTestCase {
  input: string;
  expected_output?: string;
  description?: string;
}

export interface TestGroup {
  name: string;
  points: number;
  count: number;
  constraints: ConstraintRange[];
  boundary_config?: BoundaryConfig;
}

export interface TestDataConfig {
  groups: TestGroup[];
  custom_cases: CustomTestCase[];
  /** New generation requests always use the model-selected 10-20 range. */
  adaptive_count?: boolean;
  min_count?: number;
  max_count?: number;
  total_count: number;
  /** @deprecated Kept only for importing v1.3.1 saved configurations. */
  auto_case_count?: boolean;
  sample_count: number;
  time_limit: number;
  memory_limit: number;
  checker_type: 'exact' | 'float_tolerance' | 'special_judge';
  float_tolerance?: number;
}

// ---------------------------------------------------------------------------
// Problem Generation Parameters
// ---------------------------------------------------------------------------

export interface LLMRuntimeConfig {
  model?: string;
  api_key_ref?: string;
  api_key?: string;
  base_url?: string;
  provider?: string;
  protocol?: LLMProtocol;
  reasoning_effort?: LLMReasoningEffort;
}

export type LLMReasoningEffort =
  | ''
  | 'none'
  | 'minimal'
  | 'low'
  | 'medium'
  | 'high'
  | 'xhigh'
  | 'max';

export type LLMProtocol =
  | 'auto'
  | 'anthropic-messages'
  | 'gemini-native'
  | 'openai-responses'
  | 'openai-chat';

export interface ProblemProviderConfig {
  statement?: LLMRuntimeConfig;
  verification?: LLMRuntimeConfig;
  review?: LLMRuntimeConfig;
}

export type LLMProviderPurpose = 'statement' | 'verification' | 'review';
export type LLMOverridePurpose = Exclude<LLMProviderPurpose, 'statement'>;

export interface PersistentLLMProviderSetting {
  purpose: LLMProviderPurpose;
  model: string;
  base_url: string;
  provider: string;
  protocol: LLMProtocol;
  reasoning_effort: LLMReasoningEffort;
  api_key_source: 'environment' | 'stored';
  api_key_configured: boolean;
  source: 'deployment' | 'saved' | 'inherited';
  override_configured: boolean;
  inherited_from?: LLMProviderPurpose;
  updated_by?: string;
  updated_at?: string;
}

export interface PersistentLLMProviderSettings {
  statement: PersistentLLMProviderSetting;
  verification: PersistentLLMProviderSetting;
  review: PersistentLLMProviderSetting;
}

export interface PersistentLLMProviderSettingUpdate {
  model: string;
  base_url: string;
  provider?: string;
  protocol?: LLMProtocol;
  reasoning_effort?: LLMReasoningEffort;
  api_key?: string;
  use_environment_key?: boolean;
}

export interface ReviewSettings {
  auto_approve_public_release: boolean;
  scope: 'future_eligible_problems';
  updated_by: string;
  updated_at: string;
}

export interface ReviewSettingsUpdate {
  auto_approve_public_release: boolean;
}

export interface ProblemGenParams {
  level: ProblemLevel;
  difficulty: number;
  tags: string[];
  contest_style?: string;
  time_limit: number;
  memory_limit: number;
  test_data_config: {
    num_test_cases: number;
    min_test_cases?: number;
    max_test_cases?: number;
    num_samples: number;
    /** @deprecated Legacy alias accepted by the backend for v1.3.1 clients. */
    auto_case_count?: boolean;
    groups?: TestGroup[];
    custom_cases?: CustomTestCase[];
  };
  require_review: boolean;
  generate_editorial: boolean;
  custom_prompt?: string;
  languages: string[];
  similar_limit?: number;
  locale?: string;
  provider_config?: ProblemProviderConfig;
}

export interface ProblemGenerateResponse {
  workflow_id: string;
  run_id: string;
}

// ---------------------------------------------------------------------------
// GPLT (团体程序设计天梯赛) — one-click batch generation
// ---------------------------------------------------------------------------

export type GPLTTier = 'L1' | 'L2' | 'L3';

export interface GPLTBatchParams {
  batch_id?: string;
  time_limit: number;
  memory_limit: number;
  require_review: boolean;
  generate_editorial: boolean;
  languages: string[];
  locale?: string;
  similar_limit?: number;
  provider_config?: ProblemProviderConfig;
  custom_prompt?: string;
}

export interface GPLTBatchTriggerResponse {
  workflow_id: string;
  run_id: string;
  batch_id: string;
}

export interface GPLTProblemResult {
  tier: GPLTTier;
  level: ProblemLevel;
  difficulty: number;
  status: string;
  workflow_id?: string;
  error?: string;
}

export interface GPLTBatchState {
  batch_id: string;
  total: number;
  succeeded: number;
  failed: number;
  results: GPLTProblemResult[];
}

// ---------------------------------------------------------------------------
// Problem Filter
// ---------------------------------------------------------------------------

export interface ProblemFilter {
  level?: ProblemLevel;
  status?: ProblemStatus;
  difficulty_min?: number;
  difficulty_max?: number;
  tags?: string[];
  search?: string;
  sort_by?: 'created_at' | 'difficulty' | 'title' | 'serial_number';
  sort_order?: 'asc' | 'desc';
  page?: number;
  size?: number;
}

// ---------------------------------------------------------------------------
// Dashboard Stats
// ---------------------------------------------------------------------------

export interface DashboardStats {
  total_problems: number;
  published_problems: number;
  draft_problems: number;
  generating_problems: number;
  review_problems: number;
  rejected_problems: number;
  total_workflows: number;
  active_workflows: number;
  success_rate: number;
  problems_by_level: Partial<Record<ProblemLevel, number>>;
  problems_by_difficulty: Record<string, number>;
  recent_activity: ActivityItem[];
}

export interface ActivityItem {
  id: string;
  type: 'problem_created' | 'problem_published' | 'workflow_completed' | 'workflow_failed';
  description: string;
  timestamp: string;
  reference_id?: string;
}

// ---------------------------------------------------------------------------
// Embedding runtime status
// ---------------------------------------------------------------------------

export interface EmbeddingModelVersion {
  id: string;
  provider: string;
  model_id: string;
  revision: string;
  weights_hash: string;
  dimensions: number;
  normalization: string;
  quantization: string;
  instruction_template: string;
  index_params: Record<string, unknown>;
  status: 'shadow' | 'active' | 'retired' | string;
  created_at: string;
  updated_at: string;
}

export interface ActiveEmbeddingModelStatus {
  embedding_kind: 'statement' | 'solution' | string;
  expected_dimensions: number;
  updated_by: string;
  reason: string;
  updated_at: string;
  model_version: EmbeddingModelVersion;
}

export interface EmbeddingRuntimeSettings {
  base_url: string;
  provider_id: string;
  model: string;
  dimensions: number;
  statement_model_version_id?: string;
  timeout_sec: number;
  api_key_configured: boolean;
  source: 'deployment' | string;
  updated_by?: string;
  updated_at?: string;
}

export type EmbeddingKind = 'statement' | 'solution';

export interface LocalEmbeddingEndpointConfig {
  base_url: string;
  model: string;
  api_key?: string;
  dimensions?: number;
}

export interface LocalEmbeddingTestResult {
  ok: boolean;
  provider_id: string;
  model: string;
  base_url: string;
  dimensions: number;
  latency_ms: number;
  vector_norm: number;
  sample_sha256: string;
}

export interface EmbeddingBackfillCandidate {
  problem_id: string;
  content_hash: string;
  text: string;
  has_source_current: boolean;
  has_target_any: boolean;
  has_target_current: boolean;
}

export interface EmbeddingBackfillPlanReport {
  generated_at: string;
  from_model_version_id?: string;
  to_model_version_id: string;
  embedding_kind: EmbeddingKind | string;
  all_stale: boolean;
  limit: number;
  after_problem_id?: string;
  candidate_count: number;
  candidates: EmbeddingBackfillCandidate[];
}

export interface ActivePointerSwitchPreflight {
  eligible_problem_count: number;
  target_current_vector_count: number;
  target_missing_current_vector_count: number;
  target_duplicate_current_vector_count: number;
  shadow_read_count: number;
  shadow_error_count: number;
  shadow_error_rate: number;
}

export interface ActivePointerSwitchReport {
  generated_at: string;
  decision: 'go' | 'no_go' | string;
  operation: 'cutover' | 'rollback' | string;
  dry_run: boolean;
  committed: boolean;
  embedding_kind: EmbeddingKind | string;
  old_model_version_id: string;
  new_model_version_id: string;
  actor: string;
  reason: string;
  dataset_report_sha256: string;
  audit_event_id?: number;
  preflight: ActivePointerSwitchPreflight;
  blocking_issues?: string[];
}

export interface LocalEmbeddingDeployRequest {
  endpoint: LocalEmbeddingEndpointConfig;
  provider?: string;
  model_id?: string;
  revision?: string;
  weights_hash?: string;
  normalization?: string;
  quantization?: string;
  instruction_template?: string;
  index_params?: Record<string, unknown>;
  embedding_kinds?: EmbeddingKind[];
  actor?: string;
  reason?: string;
  dataset_report_sha256?: string;
  activate?: boolean;
  dry_run?: boolean;
  backfill_plan_limit?: number;
}

export interface LocalEmbeddingDeployResult {
  test: LocalEmbeddingTestResult;
  model_version: EmbeddingModelVersion;
  backfill_plans?: EmbeddingBackfillPlanReport[];
  activation_reports?: ActivePointerSwitchReport[];
  runtime_matches: boolean;
  requires_runtime_restart: boolean;
  activation_blocked_reason?: string;
  runtime_settings_saved: boolean;
  env: Record<string, string>;
}

export interface LocalEmbeddingBackfillRequest {
  endpoint: LocalEmbeddingEndpointConfig;
  model_version_id: string;
  embedding_kind?: EmbeddingKind;
  limit?: number;
  all_stale?: boolean;
  dry_run?: boolean;
}

export interface LocalEmbeddingBackfillFailure {
  problem_id: string;
  content_hash: string;
  error: string;
}

export interface LocalEmbeddingBackfillResult {
  plan: EmbeddingBackfillPlanReport;
  dry_run: boolean;
  embedded_count: number;
  failed_count: number;
  report_sha256: string;
  failures?: LocalEmbeddingBackfillFailure[];
}

export interface LocalEmbeddingActivateRequest {
  model_version_id: string;
  embedding_kinds?: EmbeddingKind[];
  actor?: string;
  reason?: string;
  dataset_report_sha256?: string;
  dry_run?: boolean;
  endpoint_base_url?: string;
  endpoint_model?: string;
  endpoint_dimensions?: number;
  allow_runtime_mismatch?: boolean;
}

export interface LocalEmbeddingActivateResult {
  reports: ActivePointerSwitchReport[];
  runtime_matches: boolean;
  requires_runtime_restart: boolean;
  activation_blocked_reason?: string;
}

// ---------------------------------------------------------------------------
// Notifications
// ---------------------------------------------------------------------------

export type NotificationType = 'success' | 'error' | 'warning' | 'info';

export interface Notification {
  id: string;
  type: NotificationType;
  title: string;
  message?: string;
  duration?: number;
  timestamp: number;
}

// ---------------------------------------------------------------------------
// Quiz
// ---------------------------------------------------------------------------

export type QuizType = 'programming' | 'choice' | 'fill_blank' | 'judge';
export type QuizDifficulty = 'easy' | 'medium' | 'hard';
export type QuizVisibility = 'public' | 'private';
export type QuizSubject = 'c_language' | 'data_structure_algorithm';

export interface QuizOption {
  label: string;
  content: string;
}

export interface QuizProblem {
  id: string;
  code: string;
  title: string;
  statement: string;
  type: QuizType;
  code_id?: number | null;
  code_hint?: string;
  options?: QuizOption[];
  answers: string[];
  difficulty: QuizDifficulty;
  visibility: QuizVisibility;
  is_vip: boolean;
  tags: string[];
  langs?: number[];
  explanation?: string;
  knowledge_point_ids?: string[];
  subject: QuizSubject | string;
  created_at: string;
  updated_at: string;
}

export interface QuizFilter {
  type?: QuizType;
  subject?: QuizSubject | string;
  difficulty?: QuizDifficulty;
  tag?: string;
  knowledge_point_id?: string;
  page?: number;
  size?: number;
}

export interface QuizGenerateParams {
  subject: QuizSubject | string;
  type: Exclude<QuizType, 'programming'>;
  difficulty: QuizDifficulty;
  knowledge_point_codes: string[];
  count: number;
  tags?: string[];
  visibility: QuizVisibility;
  is_vip: boolean;
  custom_prompt?: string;
}

export interface QuizGenerateResponse {
  workflow_id: string;
  count: number;
}

// ---------------------------------------------------------------------------
// Knowledge Point
// ---------------------------------------------------------------------------

export interface KnowledgePoint {
  id: string;
  subject: string;
  code: string;
  name: string;
  parent_id?: string | null;
  sort_order: number;
  created_at: string;
}

// ---------------------------------------------------------------------------
// Import / Export
// ---------------------------------------------------------------------------

export interface ImportRowError {
  row_index: number;
  code: string;
  reason: string;
}

export interface ImportReport {
  success_count: number;
  failed_count: number;
  inserted_ids: string[];
  failed_rows: ImportRowError[];
}

export type OnConflict = 'error' | 'skip' | 'update';

export interface HydroValidationIssue {
  path?: string;
  field?: string;
  message: string;
}

export interface HydroValidatedProblem {
  path: string;
  pid: string;
  title: string;
  statement_files: string[];
  testdata_files: number;
  case_count: number;
  config_mode: string;
  valid: boolean;
  errors?: HydroValidationIssue[];
  warnings?: HydroValidationIssue[];
  unsupported?: HydroValidationIssue[];
}

export interface HydroValidationReport {
  phase: string;
  valid: boolean;
  mode: string;
  problem_count: number;
  success_count: number;
  failed_count: number;
  problems: HydroValidatedProblem[];
  errors?: HydroValidationIssue[];
}

// ---------------------------------------------------------------------------
// Similarity / Dedup (workflow detail, all optional — 后端可能未实现)
// ---------------------------------------------------------------------------

export interface SimilarityNeighbor {
  id: string;
  title: string;
  one_line_hint?: string;
  tags?: string[];
  similarity: number;
}

export interface SimilarityResult {
  max_similarity?: number;
  hard_reject?: boolean;
  warning?: boolean;
  neighbors?: SimilarityNeighbor[];
  is_duplicate?: boolean;
  duplicate_of?: string;
  duplicate_reason?: string;
}

// ---------------------------------------------------------------------------
// Contest / problem sets
// ---------------------------------------------------------------------------

export type ProblemSetKind = 'contest' | 'homework' | 'curriculum' | 'mock_exam';
export type ProblemSetVisibility = 'public' | 'private';
export type ProblemSetStatus = 'draft' | 'ready' | 'exported';

export interface ProblemSetGenerationConfig {
  assembly?: ProblemSetAssemblyFilter;
  mode: 'programming' | 'mixed';
  requirements: string;
  distribution: { type: QuizType; count: number; score: number }[];
}

export interface ProblemSetAssemblyFilter {
  tags: string[];
  keyword: string;
  min_difficulty: number;
  max_difficulty: number;
  quiz_difficulty: '' | QuizDifficulty;
  exclude_recent_sets: number;
  seed: string;
}

export interface ProblemSetAssemblyRef {
  id: string;
  type: QuizType;
  updated_at: string;
}

export interface ProblemSetAssemblyCandidate extends ProblemSetAssemblyRef {
  code: string;
  title: string;
  difficulty?: number;
  quiz_difficulty?: QuizDifficulty;
  tags: string[];
  score: number;
}

export interface ProblemSetAssemblyRequest {
  config: ProblemSetGenerationConfig;
  filter: ProblemSetAssemblyFilter;
}

export interface ProblemSetAssembleRequest extends ProblemSetAssemblyRequest {
  title: string;
  code?: string;
  description?: string;
  kind: ProblemSetKind;
  subject?: string;
  items: ProblemSetAssemblyRef[];
}

export interface ProblemSetAssemblyPreview {
  filter: ProblemSetAssemblyFilter;
  items: ProblemSetAssemblyCandidate[];
  distribution: { type: QuizType; requested: number; available: number; selected: number; missing: number }[];
  total_score: number;
  missing_count: number;
}

export interface ProblemSetGenerationSlot {
  position: number;
  type: QuizType;
  score: number;
  title?: string;
  status: string;
  error?: string;
}

export interface ProblemSetGenerationState {
  id: string;
  status: string;
  slots: ProblemSetGenerationSlot[];
  error?: string;
  started_at: string;
  updated_at: string;
}

export interface ProblemSetQuality {
  valid: boolean;
  ready_for_export: boolean;
  recommended_item_count: number;
  item_count: number;
  knowledge_point_count: number;
  overlap_set_ids?: string[];
  reused_knowledge_points?: string[];
  reused_items?: string[];
  blocking_issues?: string[];
  warnings?: string[];
  generated_at: string;
}

export interface ProblemSetItem {
  id: string;
  set_id: string;
  problem_id?: string;
  quiz_id?: string;
  position: number;
  score: number;
  section: string;
  notes: string;
  problem?: Problem;
  quiz?: QuizProblem;
  knowledge_point_keys?: string[];
  fingerprint?: string;
  created_at: string;
  updated_at: string;
}

export interface ProblemSet {
  generation_config?: ProblemSetGenerationConfig;
  generation?: ProblemSetGenerationState;
  generation_error?: string;
  id: string;
  code: string;
  title: string;
  description: string;
  kind: ProblemSetKind;
  visibility: ProblemSetVisibility;
  subject: string;
  tags: string[];
  style_prompt: string;
  difficulty_prompt: string;
  generated_prompt?: string;
  generated_prompt_model?: string;
  generated_prompt_at?: string;
  desired_item_count: number;
  min_item_count: number;
  max_item_count: number;
  cooldown_sets: number;
  total_score: number;
  status: ProblemSetStatus;
  created_by: string;
  created_at: string;
  updated_at: string;
  items?: ProblemSetItem[];
  quality?: ProblemSetQuality;
}

export interface ProblemSetCreateRequest {
  generation_config?: ProblemSetGenerationConfig;
  start_generation?: boolean;
  code?: string;
  title: string;
  description?: string;
  kind?: ProblemSetKind;
  visibility?: ProblemSetVisibility;
  subject?: string;
  tags?: string[];
  style_prompt?: string;
  difficulty_prompt?: string;
  desired_item_count?: number;
  min_item_count?: number;
  max_item_count?: number;
  cooldown_sets?: number;
  generate_prompt?: boolean;
}

export interface ProblemSetUpdateRequest {
  generation_config?: ProblemSetGenerationConfig;
  code?: string;
  title?: string;
  description?: string;
  kind?: ProblemSetKind;
  visibility?: ProblemSetVisibility;
  subject?: string;
  tags?: string[];
  style_prompt?: string;
  difficulty_prompt?: string;
  desired_item_count?: number;
  min_item_count?: number;
  max_item_count?: number;
  cooldown_sets?: number;
  generate_prompt?: boolean;
}

export interface ProblemSetAddItemRequest {
  problem_id?: string;
  quiz_id?: string;
  position?: number;
  score?: number;
  section?: string;
  notes?: string;
}

export interface ProblemSetListFilter {
  kind?: ProblemSetKind;
  status?: ProblemSetStatus;
  search?: string;
  page?: number;
  size?: number;
}

// Unified search preserves the source library and its difficulty scale.
export interface QuestionSearchFilter {
  q?: string;
  type?: QuizType;
  tag?: string;
  knowledge_point?: string;
  min_difficulty?: number;
  max_difficulty?: number;
  quiz_difficulty?: QuizDifficulty;
  page?: number;
  size?: number;
}
export interface QuestionSearchItem {
  id: string;
  source: 'problem' | 'quiz';
  type: QuizType;
  code: string;
  title: string;
  tags: string[];
  knowledge_points: string[];
  difficulty?: number;
  quiz_difficulty?: QuizDifficulty;
  level?: string;
  status?: string;
  updated_at: string;
}
