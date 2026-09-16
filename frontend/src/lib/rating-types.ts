// API contract: backend/internal/rating/types.go
export interface TestArtifact {
 id: string;
 input_path: string;
 output_path: string;
 input_sha256: string;
 output_sha256: string;
 is_sample: boolean;
 input?: string;
 output?: string;
}
export interface Subject {
 problem_id: string;
 hash: string;
 title: string;
 statement: string;
 time_limit: number;
 memory_limit: number;
 target_difficulty: number;
 expected_tags: string[];
 official_solution?: string;
 metadata?: unknown;
 tests: TestArtifact[];
 captured_at: string;
}
export interface KC {
 id: string;
 name: string;
 definition: string;
 conditions: string;
 related_tags: string[];
 status: string;
}
export interface Evidence {
 id: string;
 kind: string;
 status: string;
 summary: string;
 artifact_ref?: string;
 details?: unknown;
}
export interface Path {
 id: string;
 name: string;
 kind: string;
 summary: string;
 proof: string;
 complexity: string;
 language: string;
 code?: string;
 kc_ids: string[];
 bypasses: string[];
 evidence: Evidence[];
 validation: string;
 human_observations: number;
}
export interface Anchor {
 id: string;
 title: string;
 source_url: string;
 rating: number;
 rating_source: string;
 retrieved_at: string;
 statement_summary: string;
 solution_summary: string;
 kc_ids: string[];
 population: string;
 family: string;
 reviewed_by: string;
 reviewed_at: string;
 source_confirmed: boolean;
}
export interface AnchorComparison {
 anchor_id: string;
 anchor_rating: number;
 relation: string;
 reason: string;
}
export interface ReferenceEstimate {
 status: string;
 lower?: number;
 upper?: number;
 representative?: number;
 notes: string[];
}
export interface ModelRun {
 role: string;
 model: string;
 provider: string;
 evidence_id?: string;
 summary: string;
}
export interface Report {
 rule_version: string;
 summary: string;
 validity: string;
 kcs: KC[];
 paths: Path[];
 evidence: Evidence[];
 anchors: Anchor[];
 comparisons: AnchorComparison[];
 estimate: ReferenceEstimate;
 models: ModelRun[];
 disagreements: string[];
 limitations: string[];
}
export interface Assessment {
 id: string;
 problem_id: string;
 subject: Subject;
 status: string;
 phase: string;
 workflow_id: string;
 rule_version: string;
 report?: Report;
 error?: string;
 created_by: string;
 created_at: string;
 updated_at: string;
 stale: boolean;
}
export interface InvitationRequest {
 reviewer_key: string;
 window_minutes: number;
 context: string;
 expires_in_days: number;
}
export interface Invitation {
 id: string;
 problem_id: string;
 subject_hash: string;
 reviewer_id: string;
 reviewer_key: string;
 window_minutes: number;
 context: string;
 expires_at: string;
 revoked_at?: string;
 created_at: string;
}
export interface IssuedInvitation {
 invitation: Invitation;
 token: string;
}
export interface FeedbackInput {
 outcome: string;
 seen_before: boolean;
 assistance: string[];
 assistance_after_minutes?: number;
 independent_minutes: number;
 elapsed_minutes: number;
 observed_full_window: boolean;
 first_route: string;
 final_route: string;
 blockers: string;
 subjective_rating?: number;
 cf_rating?: number;
 cf_rating_at?: string;
 code?: string;
 result_source: string;
 result_url?: string;
 notes: string;
}
export interface Feedback extends FeedbackInput {
 id: string;
 problem_id: string;
 subject_hash: string;
 reviewer_id: string;
 revision: number;
 window_minutes: number;
 context: string;
 updated_at: string;
}
export interface ReviewTask {
 title: string;
 statement: string;
 time_limit: number;
 memory_limit: number;
 samples: ReviewSample[];
 subject_hash: string;
 window_minutes: number;
 context: string;
 expires_at: string;
 feedback?: FeedbackInput;
}
export interface HumanSummary {
 total_reviewers: number;
 effective_reviewers: number;
 independent_solved: number;
 window_failures: number;
 censored: number;
 assisted: number;
 seen_before: number;
 review_threshold: number;
 review_triggered: boolean;
 groups: FeedbackGroup[];
 limitations: string[];
}
export interface Calibration {
 id: string;
 problem_id: string;
 subject_hash: string;
 feedback_hash: string;
 rule_version: string;
 summary: HumanSummary;
 suggested_rating?: number;
 status: string;
 reasons: string[];
 created_at: string;
}
export interface DecisionInput {
 expected_decision_id?: string;
 subject_hash: string;
 feedback_hash: string;
 assessment_id?: string;
 calibration_id?: string;
 action: string;
 rating?: number;
 reason: string;
}
export interface Decision extends DecisionInput {
 id: string;
 problem_id: string;
 previous_rating?: number;
 actor: string;
 created_at: string;
}
export interface OfficialRating {
 rating: number;
 subject_hash: string;
 decision_id: string;
 stale: boolean;
 updated_at: string;
}
export interface Workspace {
 subject: Subject;
 official?: OfficialRating;
 assessments: Assessment[];
 feedback: Feedback[];
 feedback_hash: string;
 human: HumanSummary;
 calibrations: Calibration[];
 decisions: Decision[];
}
export interface ReviewSample { input: string; output: string }
export interface FeedbackGroup { key: string; count: number; independent_solved: number; window_failures: number; censored: number }
