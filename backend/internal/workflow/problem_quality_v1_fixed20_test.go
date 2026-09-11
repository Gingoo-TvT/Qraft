package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/llm"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
)

const (
	fixed20Bucket  = "s3-fixed20-fixture"
	fixed20Image   = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixed20Tools   = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fixed20Seccomp = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type fixed20ArtifactRecord struct {
	Ref      activities.ArtifactRef
	Metadata activities.ArtifactMetadata
}

type fixed20CAS struct {
	mu      sync.Mutex
	objects map[string][]byte
	records []fixed20ArtifactRecord
}

func newFixed20CAS() *fixed20CAS {
	return &fixed20CAS{objects: make(map[string][]byte)}
}

func (s *fixed20CAS) Put(_ context.Context, data []byte, contentType string, metadata activities.ArtifactMetadata) (activities.ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	ref := activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         fixed20Bucket,
		Key:            "workflow-artifacts/v1/sha256/" + digestHex[:2] + "/" + digestHex,
		SHA256:         digestHex,
		SizeBytes:      int64(len(data)),
		ContentType:    contentType,
		Producer:       metadata.Producer,
		Provider:       metadata.Provider,
		Model:          metadata.Model,
		ModelRevision:  metadata.ModelRevision,
		WorkflowID:     metadata.WorkflowID,
	}
	if err := ref.Validate(fixed20Bucket); err != nil {
		return activities.ArtifactRef{}, err
	}
	s.objects[digestHex] = append([]byte(nil), data...)
	s.records = append(s.records, fixed20ArtifactRecord{Ref: ref, Metadata: metadata})
	return ref, nil
}

func (s *fixed20CAS) Get(_ context.Context, ref activities.ArtifactRef) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ref.Validate(fixed20Bucket); err != nil {
		return nil, err
	}
	data, ok := s.objects[ref.SHA256]
	if !ok {
		return nil, fmt.Errorf("fixed20 CAS object %s not found", ref.SHA256)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref.SHA256 || int64(len(data)) != ref.SizeBytes {
		return nil, fmt.Errorf("fixed20 CAS object %s failed identity validation", ref.SHA256)
	}
	return append([]byte(nil), data...), nil
}

func (s *fixed20CAS) refsForArtifactType(artifactType string) []activities.ArtifactRef {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := make([]activities.ArtifactRef, 0)
	for _, record := range s.records {
		if record.Metadata.ArtifactType == artifactType {
			refs = append(refs, record.Ref)
		}
	}
	return refs
}

type fixed20ProvenanceRecorder struct{}

func (*fixed20ProvenanceRecorder) RecordWorkflowArtifact(context.Context, repository.WorkflowArtifactProvenance) (uuid.UUID, error) {
	return uuid.MustParse("20000000-0000-0000-0000-000000000001"), nil
}

type fixed20ProviderEffects struct{}

func (*fixed20ProviderEffects) Acquire(context.Context, string, string, string, time.Duration) (repository.ProviderEffectClaim, error) {
	return repository.ProviderEffectClaim{
		State:      repository.ProviderEffectAcquired,
		LeaseToken: uuid.MustParse("20000000-0000-0000-0000-000000000002"),
	}, nil
}

func (*fixed20ProviderEffects) Complete(context.Context, string, string, string, uuid.UUID, json.RawMessage) error {
	return nil
}

func (*fixed20ProviderEffects) Fail(context.Context, string, string, string, uuid.UUID, string) error {
	return nil
}

type fixed20Embedder struct{ index int }

func (e *fixed20Embedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{float32(e.index), 1, -1}, nil
}

func (e *fixed20Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		value, err := e.Embed(ctx, texts[i])
		if err != nil {
			return nil, err
		}
		result[i] = value
	}
	return result, nil
}

type fixed20VectorStore struct{}

func (*fixed20VectorStore) FindSimilarForVersion(context.Context, []float32, uuid.UUID, string, int, float64) ([]*domain.Problem, []float64, error) {
	return []*domain.Problem{}, []float64{}, nil
}

func (*fixed20VectorStore) UpdateEmbeddingForVersion(context.Context, repository.EmbeddingWrite) error {
	return nil
}

type fixed20TitleLookup struct{}

func (*fixed20TitleLookup) FindByTitle(context.Context, string, int) ([]*domain.Problem, error) {
	return []*domain.Problem{}, nil
}

type fixed20HiddenResolver struct {
	mu       sync.Mutex
	index    int
	requests []activities.HiddenSuiteResolutionRequestV1
}

func (r *fixed20HiddenResolver) ResolveHiddenSuiteV1(_ context.Context, request activities.HiddenSuiteResolutionRequestV1) (activities.HiddenSuiteRefV1, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	digest := sha256.Sum256([]byte(fmt.Sprintf("fixed20-hidden-%02d", r.index)))
	return activities.HiddenSuiteRefV1{
		SchemaVersion:  activities.S3HiddenSuiteSchemaV1,
		SuiteID:        fmt.Sprintf("fixed20-suite-%02d", r.index),
		RevisionSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func (r *fixed20HiddenResolver) requestCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

type fixed20HiddenExecutor struct {
	mu       sync.Mutex
	requests []activities.HiddenRegressionExecutionRequestV1
}

func (e *fixed20HiddenExecutor) ExecuteHiddenRegressionV1(_ context.Context, request activities.HiddenRegressionExecutionRequestV1) (activities.HiddenRegressionExecutionResponseV1, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.requests = append(e.requests, request)
	return activities.HiddenRegressionExecutionResponseV1{
		SchemaVersion: activities.S3HiddenExecutorResponseSchemaV1,
		Passed:        true,
		ExecutedCount: 3,
	}, nil
}

func (e *fixed20HiddenExecutor) requestCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.requests)
}

type fixed20LLM struct {
	mu                  sync.Mutex
	index               int
	falseReviewer       bool
	wrongMain           bool
	requests            []*llm.Request
	verificationPrompts []string
	reviewerPrompts     []string
}

func (f *fixed20LLM) CompleteWithRetry(_ context.Context, request *llm.Request, _ int) (*llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var captured llm.Request
	if err := json.Unmarshal(encodedRequest, &captured); err != nil {
		return nil, err
	}
	f.requests = append(f.requests, &captured)

	var text string
	switch {
	case strings.Contains(request.System, "G formalization stage"):
		text, err = fixed20AuthoringResponse(f.index)
	case strings.Contains(request.System, "typed competitive-programming specification"):
		text = `{"title":"Lantern Gathering","narrative":"Friends gather beneath warm lanterns."}`
	case strings.Contains(request.System, "G implementation stage"):
		marker := "AF-MAIN"
		if f.wrongMain {
			marker = "AF-WRONG"
		}
		text, err = fixed20ProgramResponse(marker, f.index)
	case strings.Contains(request.System, "independent V oracle stage"):
		if len(request.Messages) != 1 {
			return nil, fmt.Errorf("verification request messages=%d, want 1", len(request.Messages))
		}
		f.verificationPrompts = append(f.verificationPrompts, request.Messages[0].Content)
		text, err = fixed20ProgramResponse("AF-ORACLE", f.index)
	case strings.Contains(request.System, "test data generator"):
		text, err = fixed20TestDataResponse(f.index)
	case strings.Contains(request.System, "isolated R reviewer"):
		if len(request.Messages) != 1 {
			return nil, fmt.Errorf("reviewer request messages=%d, want 1", len(request.Messages))
		}
		f.reviewerPrompts = append(f.reviewerPrompts, request.Messages[0].Content)
		text, err = fixed20ReviewResponse(!f.falseReviewer)
	default:
		return nil, fmt.Errorf("unexpected fixed20 LLM system prompt: %.120s", request.System)
	}
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(request.Model)
	if model == "" {
		model = fmt.Sprintf("fixed20-model-%02d", f.index)
	}
	return &llm.Response{
		Model:         model,
		ModelObserved: true,
		Content:       []llm.ContentBlock{{Type: "text", Text: text}},
		StopReason:    "end_turn",
	}, nil
}

func (f *fixed20LLM) assertVerifierIsolation(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.verificationPrompts) != 1 {
		t.Fatalf("verification provider calls=%d, want 1", len(f.verificationPrompts))
	}
	lower := strings.ToLower(f.verificationPrompts[0])
	for _, forbidden := range []string{"authoring_plan", "statement_markdown", "one_line_hint", "af-main", "af-wrong", "hidden"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("verification request leaked %q: %s", forbidden, lower)
		}
	}
}

func (f *fixed20LLM) assertReviewerPromptContainsFullManifest(t *testing.T, want activities.TestManifestV2) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reviewerPrompts) != 1 {
		t.Fatalf("reviewer provider calls=%d, want 1", len(f.reviewerPrompts))
	}
	var prompt struct {
		TestManifest  activities.TestManifestV2 `json:"test_manifest"`
		VisibleAssets []struct {
			Role          string `json:"role"`
			ArtifactRef   string `json:"artifact_ref"`
			SHA256        string `json:"sha256"`
			ContentBase64 string `json:"content_base64"`
		} `json:"visible_assets"`
	}
	if err := json.Unmarshal([]byte(f.reviewerPrompts[0]), &prompt); err != nil {
		t.Fatalf("decode reviewer prompt: %v", err)
	}
	if want.TestCount != 3 || len(want.Cases) != 3 {
		t.Fatalf("fixture manifest cases=%d/%d, want 3", want.TestCount, len(want.Cases))
	}
	if prompt.TestManifest.SchemaVersion != want.SchemaVersion || prompt.TestManifest.TestCount != want.TestCount || len(prompt.TestManifest.Cases) != len(want.Cases) {
		t.Fatalf("reviewer manifest summary=%+v, want=%+v", prompt.TestManifest, want)
	}
	for index := range want.Cases {
		got, expected := prompt.TestManifest.Cases[index], want.Cases[index]
		if got.TestID != expected.TestID || got.Purpose != expected.Purpose || got.ConstraintRegion != expected.ConstraintRegion || got.InputSHA256 != expected.InputSHA256 || got.OutputSHA256 != expected.OutputSHA256 || got.KilledWrongIDs == nil {
			t.Fatalf("reviewer manifest case %d=%+v, want=%+v", index, got, expected)
		}
	}
	required := map[string]bool{
		"semantic_spec":     false,
		"final_statement":   false,
		"oracle_promotion":  false,
		"sanitizer":         false,
		"boundary_coverage": false,
	}
	if len(prompt.VisibleAssets) != len(required) {
		t.Fatalf("reviewer visible assets=%d, want=%d", len(prompt.VisibleAssets), len(required))
	}
	for _, asset := range prompt.VisibleAssets {
		seen, ok := required[asset.Role]
		if !ok || seen {
			t.Fatalf("reviewer visible asset role=%q is unknown or duplicated", asset.Role)
		}
		content, err := base64.StdEncoding.DecodeString(asset.ContentBase64)
		if err != nil || len(content) == 0 {
			t.Fatalf("reviewer visible asset %q content is missing or invalid: %v", asset.Role, err)
		}
		digest := sha256.Sum256(content)
		if hex.EncodeToString(digest[:]) != asset.SHA256 || !strings.HasPrefix(asset.ArtifactRef, "cas://"+fixed20Bucket+"/") || !strings.HasSuffix(asset.ArtifactRef, "/"+asset.SHA256) {
			t.Fatalf("reviewer visible asset %q is not content/ref bound: %+v", asset.Role, asset)
		}
		required[asset.Role] = true
	}
	for role, seen := range required {
		if !seen {
			t.Fatalf("reviewer prompt omitted visible asset role %q", role)
		}
	}
}

type fixed20Sandbox struct {
	mu                 sync.Mutex
	index              int
	compileFailure     bool
	runtimeFailure     bool
	whitespaceMismatch bool
	mutateReparse      bool
	mainRuns           int
	limits             []remotesandbox.RemoteLimits
}

func (s *fixed20Sandbox) Compile(_ context.Context, language, _ string) (*remotesandbox.RemoteCompileResult, error) {
	audit := s.audit(language, "compile", remotesandbox.RemoteLimits{})
	return &remotesandbox.RemoteCompileResult{
		Version: remotesandbox.ProtocolVersion, Language: language,
		Success: !s.compileFailure, Toolchain: "fixed20-toolchain", SandboxRevision: "fixed20-r1", Audit: audit,
	}, nil
}

func (s *fixed20Sandbox) Execute(_ context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = append(s.limits, limits)
	isMain := strings.Contains(source, "AF-MAIN") || strings.Contains(source, "AF-WRONG")
	if isMain {
		s.mainRuns++
	}
	audit := s.audit(language, source+strings.Join(inputs, "\x00"), limits)
	compile := remotesandbox.RemoteCompileResult{
		Version: remotesandbox.ProtocolVersion, Language: language,
		Success: !s.compileFailure, Toolchain: "fixed20-toolchain", SandboxRevision: "fixed20-r1", Audit: audit,
	}
	results := make([]remotesandbox.RemoteCaseResult, len(inputs))
	for i, input := range inputs {
		output, err := fixed20ExpectedOutput(input)
		if err != nil {
			return nil, err
		}
		if (s.whitespaceMismatch && isMain) || strings.Contains(source, "AF-WRONG") {
			output = strings.TrimSuffix(output, " \n") + "\n"
		}
		if s.mutateReparse && isMain && s.mainRuns == 3 {
			output += "mutated\n"
		}
		verdict := remotesandbox.VerdictOK
		stderr := ""
		exitCode := 0
		if s.runtimeFailure && isMain {
			verdict = remotesandbox.VerdictRE
			stderr = "fixture runtime failure"
			exitCode = 1
		}
		results[i] = remotesandbox.RemoteCaseResult{
			Index: i, Verdict: verdict, Stdout: output, Stderr: stderr,
			ExitCode: exitCode, TimeMS: 1, MemoryBytes: 1024,
		}
	}
	return &remotesandbox.RemoteExecuteResult{
		Version: remotesandbox.ProtocolVersion,
		Compile: compile,
		Results: results,
		Audit:   audit,
	}, nil
}

func (s *fixed20Sandbox) audit(language, identity string, limits remotesandbox.RemoteLimits) remotesandbox.RemoteAuditMetadata {
	digest := sha256.Sum256([]byte(fmt.Sprintf("fixed20:%d:%s:%s:%+v", s.index, language, identity, limits)))
	limitProfile := fmt.Sprintf("execute-v1:time_ms=%d,memory_mb=%d,output_bytes=%d,pids=%d", limits.TimeLimitMS, limits.MemoryLimitMB, limits.OutputLimitBytes, limits.MaxProcesses)
	if limits.Profile != "" {
		limitProfile += ",profile=" + limits.Profile
	}
	return remotesandbox.RemoteAuditMetadata{
		RunID:                   fmt.Sprintf("fixed20-%02d-%s", s.index, hex.EncodeToString(digest[:8])),
		ManifestDigest:          "sha256:" + hex.EncodeToString(digest[:]),
		Seed:                    int64(s.index),
		LimitProfile:            limitProfile,
		Profile:                 limits.Profile,
		ImageDigest:             fixed20Image,
		ToolchainManifestDigest: fixed20Tools,
		SeccompPolicyDigest:     fixed20Seccomp,
	}
}

func fixed20ExpectedOutput(input string) (string, error) {
	reader := strings.NewReader(input)
	var n int
	if _, err := fmt.Fscan(reader, &n); err != nil {
		return "", err
	}
	var sum int64
	for i := 0; i < n; i++ {
		var value int64
		if _, err := fmt.Fscan(reader, &value); err != nil {
			return "", err
		}
		sum += value
	}
	return fmt.Sprintf("%d \n", sum), nil
}

func fixed20Inputs(index int) []string {
	minimum := -int64(index + 3)
	maximum := int64(index + 5)
	return []string{
		fmt.Sprintf("1\n%d\n", minimum),
		"1\n0\n",
		fmt.Sprintf("2\n%d %d\n", minimum, maximum),
	}
}

func fixed20AuthoringResponse(index int) (string, error) {
	minimum := -int64(index + 3)
	maximum := int64(index + 5)
	one, two := int64(1), int64(2)
	count := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}
	literalOne := domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one}
	spec := domain.SemanticSpecV1{
		ProblemDefinition: fmt.Sprintf("Fixture %02d: given a short integer sequence, output its sum.", index),
		Sections: domain.SemanticSpecSectionsV1{
			Input:       &domain.SemanticSectionV1{Summary: "Read n and then n integers.", SymbolRefs: []string{"n", "a"}},
			Output:      &domain.SemanticSectionV1{Summary: "Print the unique integer sum.", SymbolRefs: []string{"answer"}},
			Constraints: &domain.SemanticSectionV1{Summary: "Length and element values are bounded.", SymbolRefs: []string{"n", "a"}},
		},
		InputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
			{ID: "size", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "read the sequence length"},
			{ID: "values", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "a", Mode: domain.SemanticGrammarFieldSequence, Count: &count}}, Meaning: "read exactly n values"},
		}},
		OutputGrammar: domain.SemanticGrammarV1{Profile: domain.SemanticGrammarTokenLinesV1, Lines: []domain.SemanticGrammarLineV1{
			{ID: "answer", Repeat: literalOne, Fields: []domain.SemanticGrammarFieldV1{{Symbol: "answer", Mode: domain.SemanticGrammarFieldScalar}}, Meaning: "print the sum"},
		}},
		Objective: domain.SemanticObjectiveV1{Kind: domain.SemanticObjectiveCompute, Summary: "Compute the sequence sum.", SymbolRefs: []string{"a", "answer"}},
		Symbols: []domain.SemanticSymbolV1{
			{Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput, Role: "sequence length", Definition: "number of input values", BoundaryPolicy: domain.SemanticBoundaryPolicyZeroOneRequired},
			{Name: "a", Type: domain.SemanticSymbolIntegerSequence, Scope: domain.SemanticSymbolScopeInput, Role: "input values", Definition: "the sequence", BoundaryPolicy: domain.SemanticBoundaryPolicyExplicit},
			{Name: "answer", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeOutput, Role: "result", Definition: "sum of all values", BoundaryPolicy: domain.SemanticBoundaryPolicyNotApplicable},
		},
		Constraints: []domain.SemanticConstraintV1{
			{Subject: "n", Kind: domain.SemanticConstraintIntegerRange, Min: &one, Max: &two, Meaning: "short sequence length"},
			{Subject: "a", Kind: domain.SemanticConstraintLengthRange, Min: &one, Max: &two, Meaning: "length equals n"},
			{Subject: "a", Kind: domain.SemanticConstraintElementRange, Min: &minimum, Max: &maximum, Meaning: "bounded element values"},
		},
		Relations: []domain.SemanticRelationV1{{ID: "a-length-equals-n", Left: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionLength, Symbol: "a"}, Operator: domain.SemanticRelationEqual, Right: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionSymbol, Symbol: "n"}, Meaning: "the sequence contains exactly n values"}},
		Topology:  domain.SemanticTopologyV1{Kind: domain.SemanticTopologyLinear, Subject: "a", Direction: domain.SemanticTopologyNotApplicable, CyclePolicy: domain.SemanticTopologyAcyclic, Connectivity: domain.SemanticTopologyNotApplicable, Dynamics: domain.SemanticTopologyStatic, SelfLoops: domain.SemanticTopologyNotApplicable, MultiEdges: domain.SemanticTopologyNotApplicable},
		Boundaries: []domain.SemanticBoundaryV1{
			{ID: "a-element-max", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: maximum, Allowed: true, Meaning: "maximum element"},
			{ID: "a-element-min", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: minimum, Allowed: true, Meaning: "minimum element"},
			{ID: "a-element-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureElementValue, Value: 0, Allowed: true, Meaning: "zero element"},
			{ID: "a-length-max", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: two, Allowed: true, Meaning: "maximum length"},
			{ID: "a-one", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: one, Allowed: true, Meaning: "single element"},
			{ID: "a-zero", Symbol: "a", Measure: domain.SemanticBoundaryMeasureLength, Value: 0, Allowed: false, Meaning: "empty sequence forbidden"},
			{ID: "n-max", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: two, Allowed: true, Meaning: "maximum n"},
			{ID: "n-one", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: one, Allowed: true, Meaning: "unit n"},
			{ID: "n-zero", Symbol: "n", Measure: domain.SemanticBoundaryMeasureValue, Value: 0, Allowed: false, Meaning: "zero n forbidden"},
		},
		AnswerSemantics: domain.SemanticAnswerSemanticsV1{NoSolutionPolicy: domain.SemanticNoSolutionImpossible, MultipleSolutionPolicy: domain.SemanticMultipleSolutionUnique},
		Judge:           domain.SemanticJudgeV1{Mode: domain.SemanticJudgeExactNormalized, ComparisonProfile: domain.SemanticComparisonTrimTrailingSpaceLFV1},
		SampleInputs:    []string{fixed20Inputs(index)[0]},
	}
	plan := domain.AuthoringPlanV1{
		CoreIdea:                "Accumulate each value exactly once.",
		ConceptRoles:            []domain.AuthoringConceptRoleV1{{Slug: "prefix-sum", Role: "main algorithm", Necessity: "linear aggregation is required"}},
		IntendedSolution:        domain.AuthoringAlgorithmPlanV1{Summary: "single-pass sum", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		BruteForceBaseline:      domain.AuthoringAlgorithmPlanV1{Summary: "direct sum on tiny inputs", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		OracleCandidateStrategy: domain.AuthoringAlgorithmPlanV1{Summary: "independent checked accumulation", TimeComplexity: "O(n)", SpaceComplexity: "O(1)"},
		FailureModes:            []domain.AuthoringFailureModeV1{{ID: "drops-last-value", Description: "forgets the final sequence element", WitnessIntent: "two-element maximum fixture"}},
		TestIntents: []domain.AuthoringTestIntentV1{
			{Purpose: "minimum", ConstraintRegion: fmt.Sprintf("fixed-%02d-min", index), BoundaryRefs: []string{"a-element-min", "a-one", "n-one"}},
			{Purpose: "zero", ConstraintRegion: fmt.Sprintf("fixed-%02d-zero", index), BoundaryRefs: []string{"a-element-zero"}},
			{Purpose: "maximum", ConstraintRegion: fmt.Sprintf("fixed-%02d-max", index), BoundaryRefs: []string{"a-element-max", "a-length-max", "n-max"}},
		},
		TargetDifficulty:   1500,
		TeachingObjectives: []string{"linear aggregation"},
		CreativeIntent:     fmt.Sprintf("Use a distinct lantern presentation number %02d.", index),
	}
	encoded, err := json.Marshal(map[string]interface{}{"decision": "accepted", "semantic_spec": spec, "authoring_plan": plan})
	if err != nil {
		return "", err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		return "", err
	}
	for _, name := range []string{"semantic_spec", "authoring_plan"} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(top[name], &object); err != nil {
			return "", err
		}
		delete(object, "schema_version")
		delete(object, "brief_sha256")
		delete(object, "semantic_spec_sha256")
		top[name], err = json.Marshal(object)
		if err != nil {
			return "", err
		}
	}
	encoded, err = json.Marshal(top)
	if err != nil {
		return "", err
	}
	return string(encoded[1:]), nil
}

func fixed20ProgramResponse(marker string, index int) (string, error) {
	source := fmt.Sprintf("// %s-%02d\n#include <iostream>\nint main(){long long n,x,s=0;if(!(std::cin>>n))return 0;while(n--&&std::cin>>x)s+=x;std::cout<<s<<' '<<'\\n';}\n", marker, index)
	encoded, err := json.Marshal(map[string]string{"source_code": source, "language": "cpp"})
	return string(encoded), err
}

func fixed20TestDataResponse(index int) (string, error) {
	inputs := fixed20Inputs(index)
	value := map[string]interface{}{
		"test_cases": []map[string]interface{}{
			{"input": inputs[0], "group_id": 0, "is_sample": true, "description": "minimum boundary"},
			{"input": inputs[1], "group_id": 0, "is_sample": false, "description": "zero element"},
			{"input": inputs[2], "group_id": 0, "is_sample": false, "description": "maximum boundary"},
		},
		"generator_code": "",
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded[1:]), nil
}

func fixed20ReviewResponse(approved bool) (string, error) {
	assessment := qualitygate.ReviewAssessmentV1{
		SchemaVersion: qualitygate.ReviewAssessmentSchemaVersionV1,
		Approved:      qualitygate.BoolV1(approved),
		Dimensions: &qualitygate.ReviewDimensionsV1{
			Clarity:               &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(9), Notes: "clear"},
			Correctness:           &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(9), Notes: "correct"},
			TestCoverage:          &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(9), Notes: "complete"},
			DifficultyCalibration: &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(8), Notes: "calibrated"},
			TagAccuracy:           &qualitygate.ReviewDimensionV1{Score: qualitygate.ScoreV1(9), Notes: "accurate"},
		},
		Blockers: []qualitygate.ReviewBlockerV1{},
	}
	encoded, err := json.Marshal(assessment)
	return string(encoded), err
}

type fixed20Scenario struct {
	falseReviewer      bool
	wrongMain          bool
	compileFailure     bool
	runtimeFailure     bool
	whitespaceMismatch bool
	mutateReparse      bool
}

type fixed20ActivityTrace struct {
	mu     sync.Mutex
	counts map[string]int
}

func newFixed20ActivityTrace() *fixed20ActivityTrace {
	return &fixed20ActivityTrace{counts: make(map[string]int)}
}

func (t *fixed20ActivityTrace) started(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[info.ActivityType.Name]++
}

func (t *fixed20ActivityTrace) count(name string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

type fixed20Harness struct {
	env      *testsuite.TestWorkflowEnvironment
	acts     *activities.Activities
	input    ProblemGenerationQualityInputV1
	store    *fixed20CAS
	llm      *fixed20LLM
	sandbox  *fixed20Sandbox
	resolver *fixed20HiddenResolver
	hidden   *fixed20HiddenExecutor
	trace    *fixed20ActivityTrace
}

func newFixed20Harness(t *testing.T, index int, scenario fixed20Scenario) *fixed20Harness {
	return newFixed20HarnessWithOptions(t, index, scenario, fixed20HarnessOptions{})
}

type fixed20HarnessOptions struct {
	remoteSandboxFactory activities.RemoteSandboxExecutorFactoryV1
	sandboxIdentity      *activities.S3SandboxIdentityPolicyV1
}

func newFixed20HarnessWithOptions(t *testing.T, index int, scenario fixed20Scenario, options fixed20HarnessOptions) *fixed20Harness {
	t.Helper()
	store := newFixed20CAS()
	provider := &fixed20LLM{index: index, falseReviewer: scenario.falseReviewer, wrongMain: scenario.wrongMain}
	sandbox := &fixed20Sandbox{
		index:              index,
		compileFailure:     scenario.compileFailure,
		runtimeFailure:     scenario.runtimeFailure,
		whitespaceMismatch: scenario.whitespaceMismatch,
		mutateReparse:      scenario.mutateReparse,
	}
	hidden := &fixed20HiddenExecutor{}
	resolver := &fixed20HiddenResolver{index: index}
	remoteFactory := activities.RemoteSandboxExecutorFactoryV1(func(time.Duration) (remotesandbox.RemoteExecutor, error) { return sandbox, nil })
	identityPolicy := &activities.S3SandboxIdentityPolicyV1{ImageDigest: fixed20Image, ToolchainManifestDigest: fixed20Tools, SeccompPolicyDigest: fixed20Seccomp}
	if options.remoteSandboxFactory != nil {
		remoteFactory = options.remoteSandboxFactory
	}
	if options.sandboxIdentity != nil {
		identityPolicy = options.sandboxIdentity
	}
	deps := &activities.Dependencies{
		LLM:                      provider,
		LLMProvider:              "fixed20-default-provider",
		LLMModel:                 "fixed20-default-model",
		LLMBaseURL:               "https://default.fixed20.invalid/v1",
		Embedding:                &fixed20Embedder{index: index},
		EmbeddingEnabled:         true,
		EmbeddingProvider:        "fixed20-embedding-provider",
		EmbeddingModel:           "fixed20-embedding-model",
		EmbeddingModelVersionID:  uuid.MustParse("20000000-0000-0000-0000-000000000003"),
		ProblemTitleLookup:       &fixed20TitleLookup{},
		VectorRepo:               &fixed20VectorStore{},
		ProviderEffects:          &fixed20ProviderEffects{},
		ProviderEffectLease:      time.Minute,
		ProvenanceRecorder:       &fixed20ProvenanceRecorder{},
		ArtifactStore:            store,
		RemoteSandboxFactory:     remoteFactory,
		S3SandboxIdentityPolicy:  identityPolicy,
		HiddenSuiteResolver:      resolver,
		HiddenRegressionExecutor: hidden,
	}
	acts := activities.New(deps)
	params := domain.DefaultProblemGenParams()
	params.Tags = []string{"prefix-sum"}
	params.GenerateEditorial = false
	params.TestDataConfig = domain.TestDataConfig{
		NumTestCases:   3,
		NumSamples:     1,
		BoundaryConfig: domain.BoundaryConfig{IncludeMinCase: true, IncludeMaxCase: true, IncludeZero: true},
	}
	params.ProviderConfig = &domain.ProviderRuntimeConfig{
		Statement:    &domain.LLMRuntimeConfig{Model: "fixed20-g-model", APIKeyRef: "env:ALGOFORGE_FIXED20_G_KEY", BaseURL: "https://g.fixed20.invalid/v1", Provider: "fixed20-g", Protocol: "openai-chat"},
		Verification: &domain.LLMRuntimeConfig{Model: "fixed20-v-model", APIKeyRef: "env:ALGOFORGE_FIXED20_V_KEY", BaseURL: "https://v.fixed20.invalid/v1", Provider: "fixed20-v", Protocol: "openai-chat"},
		Review:       &domain.LLMRuntimeConfig{Model: "fixed20-r-model", APIKeyRef: "env:ALGOFORGE_FIXED20_R_KEY", BaseURL: "https://r.fixed20.invalid/v1", Provider: "fixed20-r", Protocol: "openai-chat"},
	}
	input := ProblemGenerationQualityInputV1{
		PayloadVersion: ProblemGenerationQualityPayloadVersionV1,
		SubjectID:      fmt.Sprintf("fixed20-%02d", index),
		Language:       "cpp",
		FrozenConcept:  fmt.Sprintf("Fixture %02d sums a short integer sequence.", index),
		RequiredFacts: []activities.CanonicalAuthoringBriefFactV1{
			{Key: "fixture_id", Value: fmt.Sprintf("fixed20-%02d", index)},
			{Key: "objective", Value: "sum every input element exactly once"},
		},
		Params: params,
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	trace := newFixed20ActivityTrace()
	env.SetOnActivityStartedListener(trace.started)
	env.RegisterWorkflow(ProblemGenerationQualityWorkflowV1)
	registerFixed20Activities(env, acts)
	return &fixed20Harness{env: env, acts: acts, input: input, store: store, llm: provider, sandbox: sandbox, resolver: resolver, hidden: hidden, trace: trace}
}

func registerFixed20Activities(env *testsuite.TestWorkflowEnvironment, acts *activities.Activities) {
	env.RegisterActivity(acts.ResolveS3RepairPolicyActivityV1)
	env.RegisterActivity(acts.BuildCanonicalAuthoringBriefActivityV1)
	env.RegisterActivity(acts.GenerateAuthoringPlanActivity)
	env.RegisterActivity(acts.RenderStatementFromAuthoringBundleActivityV1)
	env.RegisterActivity(acts.ValidateAuthoringSampleCandidatesActivityV1)
	env.RegisterActivity(acts.ExtractSemanticSpecArtifactActivityV1)
	env.RegisterActivity(acts.BuildS3SpecLintGateActivityV1)
	env.RegisterActivity(acts.GenerateS3TestDataActivityV1)
	env.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
	env.RegisterActivity(acts.GenerateMainSolutionActivityV1)
	env.RegisterActivity(acts.GenerateOracleCandidateActivityV1)
	env.RegisterActivity(acts.VerifyProgramAgainstIndependentOracleActivityV1)
	env.RegisterActivity(acts.ResolveS3HiddenSuiteActivityV1)
	env.RegisterActivity(acts.RunVerifiedAuthoringSamplesActivityV1)
	env.RegisterActivity(acts.StageAuthoringStatementSamplesActivityV1)
	env.RegisterActivity(acts.FinalizeAuthoringStatementSamplesActivityV1)
	env.RegisterActivity(acts.BuildS3SampleOutputGateActivityV1)
	env.RegisterActivity(acts.RunSanitizerGateActivityV1)
	env.RegisterActivity(acts.BuildS3TestManifestActivityV1)
	env.RegisterActivity(acts.S3DedupGateActivityV1)
	env.RegisterActivity(acts.S3StrictReviewerActivityV1)
	env.RegisterActivity(acts.S3HiddenRegressionActivityV1)
	env.RegisterActivity(acts.RecomputeS3VerdictActivityV1)
}

func (h *fixed20Harness) execute(t *testing.T) (*ProblemGenerationQualityResultV1, error) {
	t.Helper()
	h.env.ExecuteWorkflow(ProblemGenerationQualityWorkflowV1, h.input)
	if err := h.env.GetWorkflowError(); err != nil {
		return nil, err
	}
	var result ProblemGenerationQualityResultV1
	if err := h.env.GetWorkflowResult(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func TestProblemGenerationQualityWorkflowV1Fixed20UsesSourceActivities(t *testing.T) {
	seenSemantic := map[string]bool{}
	seenInput := map[string]bool{}
	seenProgram := map[string]bool{}
	seenManifest := map[string]bool{}
	seenHidden := map[string]bool{}
	for index := 1; index <= 20; index++ {
		t.Run(fmt.Sprintf("fixture-%02d", index), func(t *testing.T) {
			h := newFixed20Harness(t, index, fixed20Scenario{})
			assertFixed20StartInputHasNoDerivedEvidence(t, h.input)
			result, err := h.execute(t)
			if err != nil {
				t.Fatalf("execute fixed20 quality workflow: %v", err)
			}
			if result.Decision != qualitygate.DecisionPass || result.StoppedGate != "" || !result.ReviewerInvoked || !result.HiddenInvoked || result.Audit == nil || result.AuditArtifact == nil || result.TestManifestArtifact == nil || result.MainProgramArtifact == nil {
				t.Fatalf("unexpected fixed20 terminal result: %+v", result)
			}
			if got, want := len(result.Audit.GateResults), 9; got != want {
				t.Fatalf("gate results=%d want=%d: %+v", got, want, result.Audit.GateResults)
			}
			for _, gate := range result.Audit.GateResults {
				if gate.Status != qualitygate.GateStatusPass {
					t.Fatalf("gate %s status=%s, want pass", gate.Gate, gate.Status)
				}
			}
			h.llm.assertVerifierIsolation(t)
			if h.hidden.requestCount() != 1 {
				t.Fatalf("hidden executor calls=%d want=1", h.hidden.requestCount())
			}
			if h.resolver.requestCount() != 1 {
				t.Fatalf("hidden resolver calls=%d want=1", h.resolver.requestCount())
			}
			manifestBytes, err := h.store.Get(context.Background(), *result.TestManifestArtifact)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := activities.ParseTestManifestV2JSON(manifestBytes)
			if err != nil || manifest.TestCount != 3 || len(manifest.Cases) != 3 {
				t.Fatalf("manifest=%+v err=%v", manifest, err)
			}
			h.llm.assertReviewerPromptContainsFullManifest(t, *manifest)
			semanticRefs := h.store.refsForArtifactType("qg04_semantic_spec_only")
			inputRefs := h.store.refsForArtifactType("qg06_test_input")
			hiddenRefs := h.store.refsForArtifactType("qg09_hidden_regression_receipt")
			if len(semanticRefs) != 1 || len(inputRefs) != 3 || len(hiddenRefs) != 1 {
				t.Fatalf("source refs semantic=%d inputs=%d hidden=%d", len(semanticRefs), len(inputRefs), len(hiddenRefs))
			}
			assertFixed20FreshSHA(t, seenSemantic, "SemanticSpec", semanticRefs[0].SHA256)
			assertFixed20FreshSHA(t, seenInput, "input", inputRefs[0].SHA256)
			assertFixed20FreshSHA(t, seenProgram, "program", result.MainProgramArtifact.SHA256)
			assertFixed20FreshSHA(t, seenManifest, "manifest", result.TestManifestArtifact.SHA256)
			assertFixed20FreshSHA(t, seenHidden, "hidden receipt", hiddenRefs[0].SHA256)
			assertFixed20PassActivityCounts(t, h.trace)
		})
	}
}

func TestProblemGenerationQualityWorkflowV1DefectReplayStopsBeforeReviewer(t *testing.T) {
	var firstReceiptSHA string
	for replay := 0; replay < 2; replay++ {
		h := newFixed20Harness(t, 31, fixed20Scenario{whitespaceMismatch: true})
		result, err := h.execute(t)
		if err != nil {
			t.Fatalf("defect replay %d: %v", replay, err)
		}
		if result.Decision != qualitygate.DecisionQuarantine || result.StoppedGate != qualitygate.GateOracleDifferential || result.ReviewerInvoked || result.HiddenInvoked {
			t.Fatalf("defect replay %d terminal=%+v", replay, result)
		}
		receipts := h.store.refsForArtifactType("qg04_oracle_gate_receipt")
		verified := h.store.refsForArtifactType("qg03_verified_program_receipt")
		if len(receipts) != 1 || len(verified) != 0 {
			t.Fatalf("defect replay %d oracle receipts=%d verified=%d", replay, len(receipts), len(verified))
		}
		if replay == 0 {
			firstReceiptSHA = receipts[0].SHA256
		} else if receipts[0].SHA256 != firstReceiptSHA {
			t.Fatalf("defect receipt drifted: first=%s replay=%s", firstReceiptSHA, receipts[0].SHA256)
		}
		assertFixed20ActivityCount(t, h.trace, "S3StrictReviewerActivityV1", 0)
		assertFixed20ActivityCount(t, h.trace, "S3HiddenRegressionActivityV1", 0)
		assertFixed20ActivityCount(t, h.trace, "RunSanitizerGateActivityV1", 0)
	}
}

func TestProblemGenerationQualityWorkflowV1FalseReviewerRejectIsServerRecomputedPass(t *testing.T) {
	h := newFixed20Harness(t, 32, fixed20Scenario{falseReviewer: true})
	result, err := h.execute(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != qualitygate.DecisionPass || result.Audit == nil || result.Audit.ReviewerDeclaredApproved || !result.Audit.ReviewerAdvisoryApproved || len(result.Audit.BlockingIssues) != 0 {
		t.Fatalf("false reviewer rejection was not recomputed from five dimensions: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"approved"`, `"dimensions"`, `"source_code"`, `"test_cases"`, `"expected_output"`} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("terminal result inlined %s: %s", forbidden, encoded)
		}
	}
	if result.TestManifestArtifact == nil || result.AuditArtifact == nil {
		t.Fatalf("false-reject result omitted CAS references: %+v", result)
	}
}

func TestProblemGenerationQualityWorkflowV1SandboxFailuresDoNotMintOracleReceipts(t *testing.T) {
	for name, scenario := range map[string]fixed20Scenario{
		"compile": {compileFailure: true},
		"runtime": {runtimeFailure: true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newFixed20Harness(t, 40, scenario)
			if result, err := h.execute(t); err == nil || result != nil {
				t.Fatalf("%s failure completed: result=%+v err=%v", name, result, err)
			}
			if got := len(h.store.refsForArtifactType("qg04_oracle_gate_receipt")); got != 0 {
				t.Fatalf("%s failure minted %d oracle receipts", name, got)
			}
			if got := len(h.store.refsForArtifactType("qg03_verified_program_receipt")); got != 0 {
				t.Fatalf("%s failure minted %d verified-program receipts", name, got)
			}
			assertFixed20ActivityCount(t, h.trace, "S3StrictReviewerActivityV1", 0)
		})
	}
}

func TestProblemGenerationQualityWorkflowV1RejectsSecondSampleRunMutation(t *testing.T) {
	h := newFixed20Harness(t, 41, fixed20Scenario{mutateReparse: true})
	result, err := h.execute(t)
	if err == nil || result != nil {
		t.Fatalf("mutated second sample run completed: result=%+v err=%v", result, err)
	}
	if !strings.Contains(err.Error(), "output differs between first and parse-back sandbox runs") {
		t.Fatalf("mutated second sample run failed for the wrong reason: %v", err)
	}
	if h.sandbox.mainRuns != 3 {
		t.Fatalf("main sandbox runs=%d, want oracle verification plus two sample runs", h.sandbox.mainRuns)
	}
	assertFixed20ActivityCount(t, h.trace, "BuildS3SampleOutputGateActivityV1", 0)
	assertFixed20ActivityCount(t, h.trace, "S3StrictReviewerActivityV1", 0)
}

func TestProblemGenerationQualityWorkflowV1RealRemoteSandbox(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("ALGOFORGE_S3_REAL_SANDBOX_URL"))
	imageDigest := strings.TrimSpace(os.Getenv("ALGOFORGE_S3_REAL_SANDBOX_IMAGE_DIGEST"))
	toolchainDigest := strings.TrimSpace(os.Getenv("ALGOFORGE_S3_REAL_SANDBOX_TOOLCHAIN_MANIFEST_DIGEST"))
	seccompDigest := strings.TrimSpace(os.Getenv("ALGOFORGE_S3_REAL_SANDBOX_SECCOMP_POLICY_DIGEST"))
	if url == "" || imageDigest == "" || toolchainDigest == "" || seccompDigest == "" {
		t.Skip("set ALGOFORGE_S3_REAL_SANDBOX_URL and the three explicit *_DIGEST variables to run the real remote-sandbox acceptance")
	}

	remoteFactory := activities.RemoteSandboxExecutorFactoryV1(func(timeout time.Duration) (remotesandbox.RemoteExecutor, error) {
		return remotesandbox.NewHTTPClient(url, timeout)
	})
	policy := &activities.S3SandboxIdentityPolicyV1{
		ImageDigest:             imageDigest,
		ToolchainManifestDigest: toolchainDigest,
		SeccompPolicyDigest:     seccompDigest,
	}
	h := newFixed20HarnessWithOptions(t, 51, fixed20Scenario{}, fixed20HarnessOptions{
		remoteSandboxFactory: remoteFactory,
		sandboxIdentity:      policy,
	})
	result, err := h.execute(t)
	if err != nil {
		t.Fatalf("execute real remote-sandbox composite: %v", err)
	}
	if result.Decision != qualitygate.DecisionPass || result.Audit == nil || len(result.Audit.GateResults) != 9 {
		t.Fatalf("real remote-sandbox terminal result=%+v", result)
	}
	for _, gate := range result.Audit.GateResults {
		if gate.Status != qualitygate.GateStatusPass {
			t.Fatalf("real remote-sandbox gate %s status=%s", gate.Gate, gate.Status)
		}
	}
	assertFixed20PassActivityCounts(t, h.trace)

	oracleRefs := h.store.refsForArtifactType("qg04_oracle_gate_receipt")
	if len(oracleRefs) != 1 {
		t.Fatalf("oracle receipts=%d want=1", len(oracleRefs))
	}
	oracleBytes, err := h.store.Get(context.Background(), oracleRefs[0])
	if err != nil {
		t.Fatal(err)
	}
	var oracle activities.S3OracleGateReceiptV1
	if err := json.Unmarshal(oracleBytes, &oracle); err != nil || oracle.Status != qualitygate.GateStatusPass || oracle.MainRunSHA256 == "" || oracle.OracleRunSHA256 == "" || oracle.MainRunSHA256 == oracle.OracleRunSHA256 {
		t.Fatalf("real oracle receipt=%+v err=%v", oracle, err)
	}

	finalRefs := h.store.refsForArtifactType("qg03_final_statement_samples")
	if len(finalRefs) != 1 {
		t.Fatalf("final sample bundles=%d want=1", len(finalRefs))
	}
	finalBytes, err := h.store.Get(context.Background(), finalRefs[0])
	if err != nil {
		t.Fatal(err)
	}
	var finalSamples activities.FinalAuthoringStatementSamplesBundleV1
	if err := json.Unmarshal(finalBytes, &finalSamples); err != nil || finalSamples.FirstRunSHA256 == "" || finalSamples.SecondRunSHA256 == "" || finalSamples.FirstRunSHA256 == finalSamples.SecondRunSHA256 {
		t.Fatalf("real two-run sample bundle=%+v err=%v", finalSamples, err)
	}

	sanitizerRefs := h.store.refsForArtifactType("qg05_sanitizer_receipt")
	if len(sanitizerRefs) != 1 {
		t.Fatalf("sanitizer receipts=%d want=1", len(sanitizerRefs))
	}
	sanitizerBytes, err := h.store.Get(context.Background(), sanitizerRefs[0])
	if err != nil {
		t.Fatal(err)
	}
	var sanitizer activities.S3SanitizerReceiptV1
	if err := json.Unmarshal(sanitizerBytes, &sanitizer); err != nil {
		t.Fatal(err)
	}
	if !sanitizer.Execution.Passed || sanitizer.Execution.Profile != remotesandbox.SanitizerCAndCPPProfileV1 || sanitizer.Execution.Audit.Profile != remotesandbox.SanitizerCAndCPPProfileV1 || !strings.HasSuffix(sanitizer.Execution.Audit.LimitProfile, ",profile="+remotesandbox.SanitizerCAndCPPProfileV1) || sanitizer.Execution.Audit.ImageDigest != imageDigest || sanitizer.Execution.Audit.ToolchainManifestDigest != toolchainDigest || sanitizer.Execution.Audit.SeccompPolicyDigest != seccompDigest || len(sanitizer.Execution.FindingCodes) != 0 {
		t.Fatalf("real sanitizer receipt did not bind the explicit profile/policy: %+v", sanitizer)
	}
}

func assertFixed20StartInputHasNoDerivedEvidence(t *testing.T, input ProblemGenerationQualityInputV1) {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var value interface{}
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"brief_sha256":     true,
		"authoring_bundle": true,
		"cases":            true,
		"case_plan":        true,
		"main_run":         true,
		"sandbox_result":   true,
		"receipt":          true,
		"test_manifest":    true,
		"gate_status":      true,
		"hidden_input":     true,
		"hidden_suite":     true,
		"expected_output":  true,
		"mutant":           true,
	}
	var walk func(interface{})
	walk = func(current interface{}) {
		switch typed := current.(type) {
		case map[string]interface{}:
			for key, child := range typed {
				if forbidden[key] {
					t.Fatalf("workflow start input exposes derived or secret field %q: %s", key, encoded)
				}
				walk(child)
			}
		case []interface{}:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

func assertFixed20FreshSHA(t *testing.T, seen map[string]bool, role, sha string) {
	t.Helper()
	if seen[sha] {
		t.Fatalf("%s SHA was reused across fixed20 fixtures: %s", role, sha)
	}
	seen[sha] = true
}

func assertFixed20ActivityCount(t *testing.T, trace *fixed20ActivityTrace, name string, want int) {
	t.Helper()
	if got := trace.count(name); got != want {
		t.Fatalf("activity %s starts=%d want=%d", name, got, want)
	}
}

func assertFixed20PassActivityCounts(t *testing.T, trace *fixed20ActivityTrace) {
	t.Helper()
	for _, name := range []string{
		"ResolveS3RepairPolicyActivityV1",
		"BuildCanonicalAuthoringBriefActivityV1",
		"GenerateAuthoringPlanActivity",
		"RenderStatementFromAuthoringBundleActivityV1",
		"ValidateAuthoringSampleCandidatesActivityV1",
		"ExtractSemanticSpecArtifactActivityV1",
		"BuildS3SpecLintGateActivityV1",
		"GenerateS3TestDataActivityV1",
		"MaterializeS3CasePlanActivityV1",
		"GenerateMainSolutionActivityV1",
		"GenerateOracleCandidateActivityV1",
		"VerifyProgramAgainstIndependentOracleActivityV1",
		"ResolveS3HiddenSuiteActivityV1",
		"StageAuthoringStatementSamplesActivityV1",
		"FinalizeAuthoringStatementSamplesActivityV1",
		"BuildS3SampleOutputGateActivityV1",
		"RunSanitizerGateActivityV1",
		"BuildS3TestManifestActivityV1",
		"S3DedupGateActivityV1",
		"S3StrictReviewerActivityV1",
		"S3HiddenRegressionActivityV1",
		"RecomputeS3VerdictActivityV1",
	} {
		assertFixed20ActivityCount(t, trace, name, 1)
	}
	assertFixed20ActivityCount(t, trace, "RunVerifiedAuthoringSamplesActivityV1", 2)
	for _, legacy := range []string{"GenerateStatementActivity", "GenerateSolutionActivity", "StoreProblemActivity"} {
		assertFixed20ActivityCount(t, trace, legacy, 0)
	}
}
