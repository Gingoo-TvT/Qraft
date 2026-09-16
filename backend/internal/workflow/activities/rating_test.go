package activities

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/google/uuid"
)

type ratingSandboxStub struct {
	calls        int
	verdict      sandbox.Verdict
	output       string
	missingAudit bool
	runError     bool
}

func (s *ratingSandboxStub) Compile(context.Context, string, string) (*sandbox.RemoteCompileResult, error) {
	return &sandbox.RemoteCompileResult{Success: true}, nil
}
func (s *ratingSandboxStub) Execute(_ context.Context, language, code string, inputs []string, limits sandbox.RemoteLimits) (*sandbox.RemoteExecuteResult, error) {
	s.calls++
	if s.runError {
		return nil, errors.New("synthetic unreachable")
	}
	out := &sandbox.RemoteExecuteResult{Compile: sandbox.RemoteCompileResult{Success: true}, Audit: sandbox.RemoteAuditMetadata{RunID: "synthetic-run", ManifestDigest: "synthetic-manifest", ImageDigest: "synthetic-image", ToolchainManifestDigest: "synthetic-toolchain", SeccompPolicyDigest: "synthetic-seccomp"}}
	if s.missingAudit {
		out.Audit = sandbox.RemoteAuditMetadata{}
	}
	for i := range inputs {
		out.Results = append(out.Results, sandbox.RemoteCaseResult{Index: i, Verdict: s.verdict, Stdout: s.output})
	}
	return out, nil
}
func ratingStoreFixture(t *testing.T, store *oracleMemoryArtifactStore, value any) ArtifactRef {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), data, "application/json", ArtifactMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}
func TestRatingVerificationRequiresActualKillAndRetainsSemanticReview(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		output                 string
		runError, missingAudit bool
		want                   string
		status                 string
	}{
		{"actual-mismatch", "wrong", false, false, "refuted", "killed"},
		{"pass-is-not-KC-proof", "expected", false, false, "tested", "passed_tests"},
		{"no-execution", "", true, false, "needs_review", "not_run"},
		{"missing-receipt", "wrong", false, true, "needs_review", "needs_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newOracleMemoryArtifactStore()
			stub := &ratingSandboxStub{verdict: sandbox.VerdictOK, output: tc.output, runError: tc.runError, missingAudit: tc.missingAudit}
			a := &Activities{deps: &Dependencies{RemoteSandboxFactory: func(time.Duration) (sandbox.RemoteExecutor, error) { return stub, nil }}, artifacts: store}
			subject := rating.Subject{Hash: "frozen", JudgeMode: "exact_normalized", TimeLimit: 1000, MemoryLimit: 256, Tests: []rating.TestArtifact{{ID: "frozen-1", Input: "2", Output: "expected", InputSHA256: rating.Digest([]byte("2")), OutputSHA256: rating.Digest([]byte("expected"))}}}
			report := rating.Report{SnapshotHash: "frozen", Paths: []rating.Path{{ID: "candidate", Name: "fake-shortcut", Kind: "alternative", Language: "cpp", Code: "int main(){}", ConstraintScope: "full", SemanticReview: "equivalent_dependency", Validation: "candidate", Counterexamples: []rating.Counterexample{{Input: "unverified"}}}}}
			result, err := a.RatingVerifyActivity(context.Background(), RatingVerifyInput{Snapshot: ratingStoreFixture(t, store, RatingSnapshot{Subject: subject}), Report: ratingStoreFixture(t, store, report)})
			if err != nil {
				t.Fatal(err)
			}
			if stub.calls != 1 {
				t.Fatal("did not invoke executor exactly once")
			}
			var got rating.Report
			if err := a.ratingRead(context.Background(), result.Report, &got); err != nil {
				t.Fatal(err)
			}
			p := got.Paths[0]
			if p.Validation != tc.want || p.SemanticReview != "equivalent_dependency" {
				t.Fatalf("%+v", p)
			}
			found := false
			for _, e := range p.Evidence {
				if e.Kind == "sandbox_test" && e.Status == tc.status {
					found = true
				}
				if e.Kind == "proposed_counterexample" && e.Status != "not_run" {
					t.Fatal("candidate promoted without legality evidence")
				}
			}
			if !found {
				t.Fatalf("missing evidence %+v", p.Evidence)
			}
		})
	}
}
func TestRatingVerificationRejectsChangedFrozenInput(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	stub := &ratingSandboxStub{}
	a := &Activities{deps: &Dependencies{RemoteSandboxFactory: func(time.Duration) (sandbox.RemoteExecutor, error) { return stub, nil }}, artifacts: store}
	subject := rating.Subject{Hash: "frozen", JudgeMode: "exact_normalized", Tests: []rating.TestArtifact{{ID: "case", Input: "modified", Output: "", InputSHA256: rating.Digest([]byte("original")), OutputSHA256: rating.Digest(nil)}}}
	report := rating.Report{SnapshotHash: "frozen", Paths: []rating.Path{{ID: "candidate", Language: "cpp", Code: "int main(){}"}}}
	_, err := a.RatingVerifyActivity(context.Background(), RatingVerifyInput{Snapshot: ratingStoreFixture(t, store, RatingSnapshot{Subject: subject}), Report: ratingStoreFixture(t, store, report)})
	if err == nil || stub.calls != 0 {
		t.Fatalf("changed test executed: %v calls %d", err, stub.calls)
	}
}
func TestRatingSpecialCheckerDoesNotUseStringEquality(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	stub := &ratingSandboxStub{}
	a := &Activities{deps: &Dependencies{RemoteSandboxFactory: func(time.Duration) (sandbox.RemoteExecutor, error) { return stub, nil }}, artifacts: store}
	subject := rating.Subject{Hash: "frozen", JudgeMode: "unsupported_checker", Tests: []rating.TestArtifact{{ID: "case"}}}
	report := rating.Report{SnapshotHash: "frozen", Paths: []rating.Path{{ID: "candidate", Language: "cpp", Code: "int main(){}"}}}
	result, err := a.RatingVerifyActivity(context.Background(), RatingVerifyInput{Snapshot: ratingStoreFixture(t, store, RatingSnapshot{Subject: subject}), Report: ratingStoreFixture(t, store, report)})
	if err != nil {
		t.Fatal(err)
	}
	if stub.calls != 0 {
		t.Fatal("unsupported checker silently replaced")
	}
	var got rating.Report
	a.ratingRead(context.Background(), result.Report, &got)
	if got.Paths[0].Validation != "needs_review" {
		t.Fatalf("%+v", got)
	}
}
func TestRatingBlindActivityProvenanceAndNoAuthorLeak(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	response := rating.BlindSolution{Name: "sum", Summary: "add", Proof: "arithmetic", Complexity: "O(1)", ConstraintScope: "full", Language: "cpp", Code: "int main(){}"}
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(string(rating.StableJSON(response)))}
	a := &Activities{deps: &Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
	subject := rating.Subject{Title: "Sum", Statement: "Add two integers", TargetDifficulty: 3500, ExpectedTags: []string{"SECRET_TAG"}, OfficialSolution: "SECRET_SOLUTION", Metadata: json.RawMessage(`{"secret":"SECRET_METADATA"}`)}
	result, err := a.RatingBlindSolveActivity(context.Background(), RatingBlindInput{Snapshot: ratingStoreFixture(t, store, RatingSnapshot{Subject: subject}), Role: "blind_a"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(rating.StableJSON(provider.request))
	for _, secret := range []string{"SECRET_TAG", "SECRET_SOLUTION", "SECRET_METADATA", "3500"} {
		if strings.Contains(prompt, secret) {
			t.Fatal("provider request leaks " + secret)
		}
	}
	var got RatingBlindResult
	if err := a.ratingRead(context.Background(), *result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Evidence.ID == "" || got.Model.EvidenceID != got.Evidence.ID {
		t.Fatal("missing auditable call identity")
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) > 4096 {
		t.Fatal("provider output entered history rather than reference")
	}
}
func TestRatingCaseSelectionBoundedAndSpansSuite(t *testing.T) {
	indexes := ratingCaseIndexes(1000)
	if len(indexes) != 32 || indexes[0] != 0 || indexes[31] != 999 {
		t.Fatal(indexes)
	}
	for i := 1; i < len(indexes); i++ {
		if indexes[i] <= indexes[i-1] {
			t.Fatal("duplicate or unordered cases")
		}
	}
}

func TestRatingIndependentReviewCanRejectBlindFullScope(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	analysis := rating.Analysis{Summary: "only a small-case shortcut", Paths: []rating.Path{
		{ID: "blind_a", Name: "shortcut", Kind: "alternative", ConstraintScope: "restricted", Proof: "Only correct for small n", Complexity: "O(n^3)", Language: "cpp", Code: "ignored"},
		{ID: "blind_b", Name: "other", Kind: "alternative", ConstraintScope: "uncertain", Proof: "Incomplete proof", Complexity: "unknown", Language: "cpp", Code: "ignored"},
	}}
	anchors := []rating.Anchor{}
	for i, n := range []int{1200, 1600, 2000} {
		anchor := rating.Anchor{ID: uuid.New(), Rating: n, SourceURL: "https://example.invalid/" + string(rune('a'+i)), SourceConfirmed: true, ReviewedBy: "synthetic", ReviewedAt: time.Unix(1, 0)}
		anchors = append(anchors, anchor)
		relation := []string{"harder", "similar", "easier"}[i]
		analysis.Comparisons = append(analysis.Comparisons, rating.AnchorComparison{AnchorID: anchor.ID, Relation: relation, Reason: "synthetic structural comparison"})
	}
	provider := &capturingAuthoringLLM{response: authoringLLMResponse(string(rating.StableJSON(analysis)))}
	stub := &ratingSandboxStub{verdict: sandbox.VerdictOK, output: "expected"}
	a := &Activities{deps: &Dependencies{LLM: provider, LLMProvider: "fixture-provider", LLMModel: "model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}, RemoteSandboxFactory: func(time.Duration) (sandbox.RemoteExecutor, error) { return stub, nil }}, artifacts: store}
	subject := rating.Subject{Hash: "frozen", JudgeMode: "exact_normalized", TimeLimit: 1000, MemoryLimit: 256, Tests: []rating.TestArtifact{{ID: "case", Input: "2", Output: "expected", InputSHA256: rating.Digest([]byte("2")), OutputSHA256: rating.Digest([]byte("expected"))}}}
	snapshot := ratingStoreFixture(t, store, RatingSnapshot{Subject: subject, Anchors: anchors})
	blinds := []ArtifactRef{}
	for _, role := range []string{"blind_a", "blind_b"} {
		blinds = append(blinds, ratingStoreFixture(t, store, RatingBlindResult{Solution: rating.BlindSolution{Name: role, Summary: "claim", Proof: "claims proof", Complexity: "O(1)", ConstraintScope: "full", Language: "cpp", Code: "int main(){}"}, Model: rating.ModelRun{Role: role}}))
	}
	reportRef, err := a.RatingAnalyzeActivity(context.Background(), RatingAnalyzeInput{Snapshot: snapshot, Blinds: blinds})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.RatingVerifyActivity(context.Background(), RatingVerifyInput{Snapshot: snapshot, Report: *reportRef})
	if err != nil {
		t.Fatal(err)
	}
	var report rating.Report
	if err := a.ratingRead(context.Background(), result.Report, &report); err != nil {
		t.Fatal(err)
	}
	if report.Paths[0].ConstraintScope != "restricted" || report.Paths[1].ConstraintScope != "uncertain" {
		t.Fatalf("blind overwrote independent scope: %+v", report.Paths)
	}
	if report.Estimate.Lower != nil || report.Estimate.Representative != nil || report.Validity != "needs_review" {
		t.Fatalf("small tests promoted bogus shortcut: %+v", report)
	}
}

type ratingErrorLLM struct{}

func (ratingErrorLLM) CompleteWithRetry(context.Context, *llm.Request, int) (*llm.Response, error) {
	return nil, errors.New("upstream echoed secret-sk-" + "synthetic-do-not-expose")
}
func TestRatingProviderErrorIsNotExposedAsActivityCause(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	a := &Activities{deps: &Dependencies{LLM: ratingErrorLLM{}, LLMProvider: "fixture-provider", LLMModel: "model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
	_, err := a.RatingBlindSolveActivity(context.Background(), RatingBlindInput{Snapshot: ratingStoreFixture(t, store, RatingSnapshot{Subject: rating.Subject{Title: "synthetic", Statement: "sum"}}), Role: "blind_a"})
	if err == nil || strings.Contains(err.Error(), "secret-sk") || errors.Unwrap(err) != nil {
		t.Fatalf("unsafe error: %v", err)
	}
}

type ratingFeedbackStoreStub struct {
	rating.Store
	assessment rating.Assessment
	feedback   []rating.Feedback
}

func (s *ratingFeedbackStoreStub) GetAssessment(context.Context, uuid.UUID) (rating.Assessment, error) {
	return s.assessment, nil
}
func (s *ratingFeedbackStoreStub) ListAnchors(context.Context) ([]rating.Anchor, error) {
	return nil, nil
}
func (s *ratingFeedbackStoreStub) ListFeedback(_ context.Context, id uuid.UUID, hash string) ([]rating.Feedback, error) {
	if id != s.assessment.ProblemID || hash != s.assessment.Subject.Hash {
		return nil, errors.New("feedback version mismatch")
	}
	return s.feedback, nil
}
func (s *ratingFeedbackStoreStub) UpdateAssessment(context.Context, uuid.UUID, string, string, *rating.Report, string) error {
	return nil
}
func TestRatingHumanFeedbackReachesAnalysisButNeverBlindRequest(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	problemID, assessmentID, reviewerID := uuid.New(), uuid.New(), uuid.New()
	subjective, ability := 3300, 3400
	record := rating.Feedback{ProblemID: problemID, SubjectHash: "frozen", ReviewerID: reviewerID, WindowMinutes: 60, FeedbackInput: rating.FeedbackInput{Outcome: "solved", ElapsedMinutes: 35, IndependentMinutes: 35, FirstRoute: "unique initial greedy observation", FinalRoute: "unique alternate invariant", Blockers: "unique difficulty proving exchange", SubjectiveRating: &subjective, CFRating: &ability, Notes: "private irrelevant notes"}}
	repo := &ratingFeedbackStoreStub{assessment: rating.Assessment{ID: assessmentID, ProblemID: problemID, RuleVersion: rating.RuleVersion, Subject: rating.Subject{Hash: "frozen", Title: "Synthetic sum", Statement: "add integers", TargetDifficulty: 3500}}, feedback: []rating.Feedback{record}}
	provider := &capturingAuthoringLLM{}
	a := &Activities{deps: &Dependencies{RatingStore: repo, LLM: provider, LLMProvider: "fixture-provider", LLMModel: "model", LLMBaseURL: "https://fixture.invalid/v1", ProvenanceRecorder: &captureProvenanceRecorder{}}, artifacts: store}
	snapshot, err := a.RatingLoadActivity(context.Background(), rating.WorkflowInput{AssessmentID: assessmentID, ProblemID: problemID, SnapshotHash: "frozen"})
	if err != nil {
		t.Fatal(err)
	}
	provider.response = authoringLLMResponse(string(rating.StableJSON(rating.BlindSolution{Name: "sum", Summary: "add", Proof: "arithmetic", Complexity: "O(1)", ConstraintScope: "full", Language: "cpp", Code: "int main(){}"})))
	blinds := []ArtifactRef{}
	for _, role := range []string{"blind_a", "blind_b"} {
		ref, err := a.RatingBlindSolveActivity(context.Background(), RatingBlindInput{Snapshot: *snapshot, Role: role})
		if err != nil {
			t.Fatal(err)
		}
		blinds = append(blinds, *ref)
		text := string(rating.StableJSON(provider.request))
		for _, forbidden := range []string{"unique initial", "unique alternate", "unique difficulty", "human_feedback", reviewerID.String(), "private irrelevant", "3300", "3400", "3500"} {
			if strings.Contains(text, forbidden) {
				t.Fatal("blind request leaked " + forbidden)
			}
		}
	}
	provider.response = authoringLLMResponse(string(rating.StableJSON(rating.Analysis{Summary: "check the self-reported invariant", Paths: []rating.Path{{ID: "blind_a", Name: "sum", Kind: "alternative", Proof: "sum", Complexity: "O(1)", ConstraintScope: "full"}, {ID: "blind_b", Name: "other", Kind: "alternative", Proof: "sum", Complexity: "O(1)", ConstraintScope: "full"}}})))
	ref, err := a.RatingAnalyzeActivity(context.Background(), RatingAnalyzeInput{Snapshot: *snapshot, Blinds: blinds})
	if err != nil {
		t.Fatal(err)
	}
	text := string(rating.StableJSON(provider.request))
	for _, required := range []string{"unique initial", "unique alternate", "unique difficulty", "human_self_report", "unverified"} {
		if !strings.Contains(text, required) {
			t.Fatal("analysis missing " + required)
		}
	}
	for _, forbidden := range []string{reviewerID.String(), "private irrelevant", "3300", "3400", "3500", "subjective_rating", "cf_rating", "reviewer_id"} {
		if strings.Contains(text, forbidden) {
			t.Fatal("analysis leaks " + forbidden)
		}
	}
	var report rating.Report
	if err := a.ratingRead(context.Background(), *ref, &report); err != nil {
		t.Fatal(err)
	}
	for _, path := range report.Paths {
		if path.HumanObservations != 0 {
			t.Fatal("model evidence counted as verified observations")
		}
	}
	found := false
	for _, e := range report.Evidence {
		if e.Kind == "human_feedback_signals" && e.Status == "self_report_unverified" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing feedback evidence level")
	}
}
