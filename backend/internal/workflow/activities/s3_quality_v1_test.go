package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	qualitygate "github.com/Gingoo-TvT/Qraft/backend/internal/qualitygate/v1"
	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"go.temporal.io/sdk/testsuite"
)

func TestS3SourceInputsCannotInjectDerivedPassEvidence(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		forbidden []string
	}{
		{"oracle", S3OracleGateInputV1{}, []string{"DifferentialCases", "Gate", "Receipt", "CandidateCompiled", "Mismatches"}},
		{"samples", RunVerifiedAuthoringSamplesInputV1{}, []string{"SandboxResult", "FirstRun", "SecondRun", "Gate"}},
		{"sanitizer", S3SanitizerGateInputV1{}, []string{"Cases", "Execution", "Passed", "Gate", "Receipt"}},
		{"manifest", S3ManifestGateInputV1{}, []string{"Cases", "TestManifest", "BoundaryCoverage", "Gate"}},
		{"testdata", GenerateS3TestDataInputV1{}, []string{"FinalStatementBundleArtifact", "FinalBundle", "Cases"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			typeOf := reflect.TypeOf(test.value)
			for _, name := range test.forbidden {
				if _, exists := typeOf.FieldByName(name); exists {
					t.Fatalf("%T exposes caller-supplied derived field %q", test.value, name)
				}
			}
		})
	}
}

func TestExecutionLimitsLegacyJSONIsUnchangedWhenS3FieldsAreEmpty(t *testing.T) {
	encoded, err := json.Marshal(ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(encoded), `{"time_limit_ms":1000,"memory_limit_mb":256}`; got != want {
		t.Fatalf("legacy execution-limit JSON changed: got %s want %s", got, want)
	}
}

func TestRunSandboxPreserveOutputBytesIsAdditive(t *testing.T) {
	executor := &s3TestRemoteExecutor{outputForSource: func(string) string { return "7 \n" }}
	acts := New(&Dependencies{RemoteSandboxFactory: func(time.Duration) (remotesandbox.RemoteExecutor, error) { return executor, nil }})
	caseData := []TestCaseData{{Input: "1\n", Origin: TestCaseOriginCustom}}
	legacyLimits := ExecutionLimits{TimeLimitMs: 1000, MemoryLimitMB: 64}
	legacy, err := acts.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp", SourceCode: "main"}, caseData, legacyLimits)
	if err != nil {
		t.Fatal(err)
	}
	if got := legacy.Outputs[0]; got != "7" {
		t.Fatalf("legacy output=%q want historical normalization", got)
	}
	s3Limits := legacyLimits
	s3Limits.OutputLimitBytes = S3StableOutputLimitBytesV1
	s3Limits.PreserveOutputBytes = true
	exact, err := acts.RunSandboxActivity(context.Background(), domain.Solution{Language: "cpp", SourceCode: "main"}, caseData, s3Limits)
	if err != nil {
		t.Fatal(err)
	}
	if got := exact.Outputs[0]; got != "7 \n" {
		t.Fatalf("exact S3 output=%q; trailing bytes were lost", got)
	}
	if len(executor.limits) != 2 || executor.limits[0].OutputLimitBytes != 2<<20 || executor.limits[1].OutputLimitBytes != S3StableOutputLimitBytesV1 {
		t.Fatalf("remote limits=%+v", executor.limits)
	}
}

func TestMaterializeS3CasePlanDerivesLabelsFromParsedInputs(t *testing.T) {
	rawInputs := []string{"1\n0\n", "1\n1\n", "1\n2\n"}
	fixture := newStatementDraftFixture(t, rawInputs)
	// This focused materialization fixture represents an explicit three-case
	// legacy bundle; adaptive generation is exercised by the workflow tests.
	fixture.bundle.RequestedTestCaseCount = len(rawInputs)
	fixture.bundle.MinTestCaseCount = 0
	fixture.bundle.MaxTestCaseCount = 0
	fixture.bundle.AdaptiveTestCaseCount = false
	fixture.replaceBundle(t)
	testData := TestDataResult{PayloadVersion: ActivityPayloadVersion, TestCases: make([]TestCaseData, len(rawInputs))}
	for index, raw := range rawInputs {
		testData.TestCases[index] = TestCaseData{Input: raw, Origin: TestCaseOriginLLMInline, IsSample: index == 0, Description: "caller-claimed-random-label"}
	}
	acts := New(&Dependencies{ArtifactStore: fixture.store, ProvenanceRecorder: &captureProvenanceRecorder{}})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(acts.MaterializeS3CasePlanActivityV1)
	value, err := env.ExecuteActivity(acts.MaterializeS3CasePlanActivityV1, MaterializeS3CasePlanInputV1{PayloadVersion: S3QualityPayloadVersionV1, AuthoringBundleArtifact: fixture.input.BundleArtifact, ExpectedAuthoringBundleSHA: fixture.input.ExpectedBundleSHA256, TestData: testData})
	if err != nil {
		t.Fatal(err)
	}
	var result MaterializeS3CasePlanResultV1
	if err := value.Get(&result); err != nil {
		t.Fatal(err)
	}
	if result.CasePlanArtifact == nil || result.CasePlanArtifact.Producer != "MaterializeS3CasePlanActivityV1" || result.TestCount != 3 {
		t.Fatalf("materialized result=%+v", result)
	}
	materialized, _, err := acts.loadS3CasePlanV1(context.Background(), *result.CasePlanArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Cases[0].Purpose != TestManifestPurposeSample || materialized.Cases[0].ConstraintRegion != "n=1" || materialized.Cases[2].ConstraintRegion != "n=1" {
		t.Fatalf("case labels were not derived from parsed bindings: %+v", materialized.Cases)
	}
	for _, item := range materialized.Cases {
		if item.InputArtifact.SHA256 == "" || item.InputArtifact.ContentType != "text/plain" {
			t.Fatalf("case %q input was not materialized to CAS", item.TestID)
		}
	}
}

func TestLoadS3CasePlanRejectsSmallSuiteIdentityTamper(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	inputRef := s3SeedArtifactForTest(store, []byte("1\n0\n"), "text/plain", "MaterializeS3CasePlanActivityV1")
	plan := S3CasePlanBundleV1{
		SchemaVersion: S3CasePlanSchemaV1, SemanticSpecSHA256: strings.Repeat("a", 64), AuthoringBundleSHA256: strings.Repeat("b", 64), TestDataIdentitySHA256: strings.Repeat("c", 64),
		Cases:            []S3ManifestCasePlanV1{{TestID: "case-000000", Purpose: TestManifestPurposeRandom, ConstraintRegion: "unassigned", BoundaryRefs: []string{}, InputArtifact: inputRef, KilledWrongIDs: []string{}, KilledWrongIDsRetentionReason: "fixture"}},
		SanitizerCaseIDs: []string{"case-000000"}, SanitizerSuiteSHA256: strings.Repeat("d", 64),
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	ref := s3SeedArtifactForTest(store, data, "application/json", "MaterializeS3CasePlanActivityV1")
	acts := New(&Dependencies{ArtifactStore: store})
	if _, _, err := acts.loadS3CasePlanV1(context.Background(), ref); err == nil {
		t.Fatal("tampered sanitizer suite identity was accepted")
	}
}

func TestBuildS3ManifestPreservesPresentEmptyKilledWrongIDs(t *testing.T) {
	got := copyPresentS3StringsV1([]string{})
	if got == nil || len(got) != 0 {
		t.Fatalf("present-empty killed_wrong_ids became %#v", got)
	}
	if copyPresentS3StringsV1(nil) != nil {
		t.Fatal("absent killed_wrong_ids was silently made present")
	}
}

func TestHiddenOpaqueContractRejectsSecretBearingTokensAndFreeformFailureCodes(t *testing.T) {
	for _, token := range []string{"suite input=secret", "../suite", strings.Repeat("a", 129)} {
		if safeOpaqueS3TokenV1(token, 128) {
			t.Fatalf("unsafe opaque token %q accepted", token)
		}
	}
	for _, code := range []string{"expected-output:42", "hidden.failure.secret", strings.Repeat("a", 200)} {
		if validS3HiddenFailureCodeV1(code) {
			t.Fatalf("free-form hidden failure code %q accepted", code)
		}
	}
	if !safeOpaqueS3TokenV1("suite-prod:v1", 128) || !validS3HiddenFailureCodeV1("hidden.assertion_failed") {
		t.Fatal("canonical opaque hidden identifiers were rejected")
	}
}

func TestHiddenSuiteResolutionMissingDependencyBecomesCheckFailed(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	acts := New(&Dependencies{ArtifactStore: store})
	resolution, err := acts.ResolveS3HiddenSuiteActivityV1(context.Background(), ResolveS3HiddenSuiteInputV1{PayloadVersion: S3QualityPayloadVersionV1, SubjectID: "subject-1", SemanticSpecSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Available || resolution.Suite != nil || resolution.FailureCode != "hidden.suite_unavailable" {
		t.Fatalf("missing resolver result = %+v", resolution)
	}
	candidate := s3SeedArtifactForTest(store, []byte("candidate"), "text/plain", "GenerateMainSolutionActivityV1")
	gate, err := acts.S3HiddenRegressionActivityV1(context.Background(), S3HiddenGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, Resolution: *resolution, CandidateArtifact: candidate})
	if err != nil {
		t.Fatal(err)
	}
	if gate.Gate.Status != "check_failed" || gate.Gate.ExposureDetected || gate.ReceiptArtifact == nil {
		t.Fatalf("missing hidden dependency gate = %+v", gate)
	}
}

func TestHiddenExposureIsBlockedWithMinimalEvidence(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	deps := &Dependencies{ArtifactStore: store, HiddenSuiteResolver: s3TestHiddenResolver{}, HiddenRegressionExecutor: s3TestHiddenExecutor{response: HiddenRegressionExecutionResponseV1{SchemaVersion: S3HiddenExecutorResponseSchemaV1, ExposureDetected: true, ExecutedCount: 1, FailureCode: "hidden.exposure_detected"}}}
	acts := New(deps)
	resolution, err := acts.ResolveS3HiddenSuiteActivityV1(context.Background(), ResolveS3HiddenSuiteInputV1{PayloadVersion: S3QualityPayloadVersionV1, SubjectID: "subject-1", SemanticSpecSHA256: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	candidate := s3SeedArtifactForTest(store, []byte("candidate"), "text/plain", "GenerateMainSolutionActivityV1")
	gate, err := acts.S3HiddenRegressionActivityV1(context.Background(), S3HiddenGateInputV1{PayloadVersion: S3QualityPayloadVersionV1, Resolution: *resolution, CandidateArtifact: candidate})
	if err != nil {
		t.Fatal(err)
	}
	if gate.Gate.Status != "blocked" || !gate.Gate.ExposureDetected || len(gate.Gate.Blockers) != 1 || gate.Gate.Blockers[0].Code != "hidden.exposure_detected" {
		t.Fatalf("hidden exposure gate = %+v", gate)
	}
}

func TestS3SandboxIdentityPolicyRejectsRunBoundButUnapprovedDigest(t *testing.T) {
	want := &S3SandboxIdentityPolicyV1{ImageDigest: s3TestDigest("a"), ToolchainManifestDigest: s3TestDigest("b"), SeccompPolicyDigest: s3TestDigest("c")}
	acts := New(&Dependencies{S3SandboxIdentityPolicy: want})
	matching := SandboxAuditMetadata{ImageDigest: want.ImageDigest, ToolchainManifestDigest: want.ToolchainManifestDigest, SeccompPolicyDigest: want.SeccompPolicyDigest, LimitProfile: "execute-v1"}
	if err := acts.validateS3SandboxIdentityPolicyV1(matching); err != nil {
		t.Fatal(err)
	}
	matching.ImageDigest = s3TestDigest("d")
	if err := acts.validateS3SandboxIdentityPolicyV1(matching); err == nil {
		t.Fatal("run-bound but deployment-unapproved image digest was accepted")
	}
}

func TestS3RepairPolicyProductionDefaultIsDisabled(t *testing.T) {
	result, err := New(&Dependencies{}).ResolveS3RepairPolicyActivityV1(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Enabled || result.MaxRounds != qualitygate.MaxRepairRoundsV1 || result.ReasonCode != "t34_calibration_not_frozen" {
		t.Fatalf("production repair policy = %+v", result)
	}
}

func TestS3RepairRevisionBindsCanonicalBriefAndRejectsSameRevision(t *testing.T) {
	store := newOracleMemoryArtifactStore()
	originalInput := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  "Count the values that satisfy a fixed integer predicate.",
		RequiredFacts:  []CanonicalAuthoringBriefFactV1{{Key: "output", Value: "Print the exact count."}},
	}
	baseActs := New(&Dependencies{ArtifactStore: store})
	original, err := baseActs.BuildCanonicalAuthoringBriefActivityV1(context.Background(), originalInput)
	if err != nil {
		t.Fatal(err)
	}
	semanticRef := s3SeedArtifactForTest(store, []byte(`{"schema_version":"algoforge.semantic-spec.v1"}`), "application/json", "ExtractSemanticSpecArtifactActivityV1")
	blocker := qualitygate.ReviewBlockerV1{
		Code:             "review.semantic_spec_incomplete",
		ResponsibleAsset: s3ArtifactRefURI(&semanticRef),
		Witness: qualitygate.ExecutableWitnessV1{
			Runner: "algoforge.strict-reviewer.v1", FixtureRef: "visible:semantic_spec", Assertion: "required rule must be explicit",
		},
	}
	input := S3RepairRevisionInputV1{
		PayloadVersion: S3QualityPayloadVersionV1, ParentRevisionSHA256: original.CanonicalBriefSHA256, Round: 1,
		BlockerCodes: []string{blocker.Code}, Blockers: []qualitygate.ReviewBlockerV1{blocker},
		ResponsibleAssets: []RepairResponsibleAssetV1{{Role: "semantic_spec", Artifact: semanticRef}},
	}
	same := &s3TestRepairGenerator{response: RepairRevisionProviderResponseV1{PayloadVersion: originalInput.PayloadVersion, FrozenConcept: originalInput.FrozenConcept, RequiredFacts: originalInput.RequiredFacts}}
	if _, err := New(&Dependencies{ArtifactStore: store, RepairRevisionGenerator: same}).S3RepairRevisionActivityV1(context.Background(), input); err == nil {
		t.Fatal("same canonical brief was accepted as a new immutable revision")
	}
	if same.request.ParentRevisionSHA256 != original.CanonicalBriefSHA256 || len(same.request.ResponsibleAssets) != 1 || same.request.ResponsibleAssets[0].Role != "semantic_spec" {
		t.Fatalf("repair provider received unbound request: %+v", same.request)
	}

	revisedInput := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  "Count the values that satisfy the explicitly stated fixed integer predicate.",
		RequiredFacts:  originalInput.RequiredFacts,
	}
	revised := &s3TestRepairGenerator{response: RepairRevisionProviderResponseV1{PayloadVersion: revisedInput.PayloadVersion, FrozenConcept: revisedInput.FrozenConcept, RequiredFacts: revisedInput.RequiredFacts}}
	result, err := New(&Dependencies{ArtifactStore: store, RepairRevisionGenerator: revised}).S3RepairRevisionActivityV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := baseActs.BuildCanonicalAuthoringBriefActivityV1(context.Background(), result.NewAuthoringInput)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewRevisionSHA256 == original.CanonicalBriefSHA256 || result.NewRevisionSHA256 != rebuilt.CanonicalBriefSHA256 || result.NewCanonicalBrief != rebuilt.CanonicalBrief || result.NewRevisionArtifact == nil {
		t.Fatalf("repair result is not a rebuilt immutable canonical brief: %+v", result)
	}
}

type s3TestRemoteExecutor struct {
	outputForSource func(string) string
	limits          []remotesandbox.RemoteLimits
}

type s3TestHiddenResolver struct{}

func (s3TestHiddenResolver) ResolveHiddenSuiteV1(context.Context, HiddenSuiteResolutionRequestV1) (HiddenSuiteRefV1, error) {
	return HiddenSuiteRefV1{SchemaVersion: S3HiddenSuiteSchemaV1, SuiteID: "suite-prod:v1", RevisionSHA256: strings.Repeat("f", 64)}, nil
}

type s3TestHiddenExecutor struct {
	response HiddenRegressionExecutionResponseV1
}

func (f s3TestHiddenExecutor) ExecuteHiddenRegressionV1(context.Context, HiddenRegressionExecutionRequestV1) (HiddenRegressionExecutionResponseV1, error) {
	return f.response, nil
}

type s3TestRepairGenerator struct {
	response RepairRevisionProviderResponseV1
	request  RepairRevisionProviderRequestV1
}

func (f *s3TestRepairGenerator) GenerateRepairRevisionV1(_ context.Context, request RepairRevisionProviderRequestV1) (RepairRevisionProviderResponseV1, error) {
	f.request = request
	return f.response, nil
}

func (f *s3TestRemoteExecutor) Compile(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
	return nil, errors.New("compile is not used")
}

func (f *s3TestRemoteExecutor) Execute(_ context.Context, language, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
	f.limits = append(f.limits, limits)
	audit := remotesandbox.RemoteAuditMetadata{RunID: "run_" + strings.Repeat("1", 32), ManifestDigest: s3TestDigest("1"), LimitProfile: fmt.Sprintf("execute-v1:time_ms=%d,memory_mb=%d,output_bytes=%d,pids=%d", limits.TimeLimitMS, limits.MemoryLimitMB, limits.OutputLimitBytes, limits.MaxProcesses), Profile: limits.Profile, ImageDigest: s3TestDigest("a"), ToolchainManifestDigest: s3TestDigest("b"), SeccompPolicyDigest: s3TestDigest("c")}
	if limits.Profile != "" {
		audit.LimitProfile += ",profile=" + limits.Profile
	}
	compile := remotesandbox.RemoteCompileResult{Version: remotesandbox.ProtocolVersion, Language: language, Success: true, Toolchain: "fixture", SandboxRevision: "fixture", Audit: audit}
	results := make([]remotesandbox.RemoteCaseResult, len(inputs))
	for index := range inputs {
		results[index] = remotesandbox.RemoteCaseResult{Index: index, Verdict: remotesandbox.VerdictOK, Stdout: f.outputForSource(source), TimeMS: 1, MemoryBytes: 1}
	}
	return &remotesandbox.RemoteExecuteResult{Version: remotesandbox.ProtocolVersion, Compile: compile, Results: results, Audit: audit}, nil
}

func s3SeedArtifactForTest(store *oracleMemoryArtifactStore, data []byte, contentType, producer string) ArtifactRef {
	digest := sha256Hex(data)
	store.objects[digest] = append([]byte(nil), data...)
	return ArtifactRef{SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion, Bucket: "fixture", Key: artifactKey(digest), SHA256: digest, SizeBytes: int64(len(data)), ContentType: contentType, Producer: producer, Provider: "fixture-provider", Model: "fixture-model", ModelRevision: "fixture-revision", WorkflowID: "fixture-workflow"}
}

func s3TestDigest(ch string) string { return "sha256:" + strings.Repeat(ch, 64) }
