package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/diversity"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversityapi"
	"github.com/Gingoo-TvT/Qraft/backend/internal/diversitymode"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/generationapi"
	authmw "github.com/Gingoo-TvT/Qraft/backend/internal/handler/middleware"
	"github.com/Gingoo-TvT/Qraft/backend/internal/qualitymode"
	algoworkflow "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	temporalmocks "go.temporal.io/sdk/mocks"
)

type fakeS5MicroBatchService struct {
	triggerCalls int
	trigger      func(context.Context, string, string, string, time.Duration, []domain.ProblemGenParams) error
	problems     map[string]*domain.Problem
	problemErr   error
}

func (fake *fakeS5MicroBatchService) TriggerS5MicroBatch(
	ctx context.Context,
	batchID string,
	payloadSHA256 string,
	principalSHA256 string,
	timeout time.Duration,
	params []domain.ProblemGenParams,
) (client.WorkflowRun, error) {
	fake.triggerCalls++
	if fake.trigger != nil {
		return nil, fake.trigger(ctx, batchID, payloadSHA256, principalSHA256, timeout, params)
	}
	return nil, nil
}

func (fake *fakeS5MicroBatchService) GetProblemByWorkflowID(_ context.Context, workflowID string) (*domain.Problem, error) {
	if fake.problemErr != nil {
		return nil, fake.problemErr
	}
	return fake.problems[workflowID], nil
}

func TestS5MicroBatchRoutesAreAbsentInLegacyOnly(t *testing.T) {
	quality, err := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	require.NoError(t, err)
	handler, err := NewS5MicroBatchHandler(&fakeS5MicroBatchService{}, temporalmocks.NewClient(t), nil, quality)
	require.NoError(t, err)

	legacy, err := diversitymode.ResolveMode(diversitymode.ModeLegacyOnly, true, nil)
	require.NoError(t, err)
	e := echo.New()
	require.NoError(t, RegisterS5MicroBatchRoutes(e.Group("/api/v1"), handler, legacy))
	for _, route := range e.Routes() {
		require.NotContains(t, route.Path, "/generation/micro-batches")
	}

	enabled, err := diversitymode.ResolveMode(diversitymode.ModeDiversityV1, true, nil)
	require.NoError(t, err)
	e = echo.New()
	require.NoError(t, RegisterS5MicroBatchRoutes(e.Group("/api/v1"), handler, enabled))
	got := map[string]bool{}
	for _, route := range e.Routes() {
		got[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"POST /api/v1/generation/micro-batches",
		"GET /api/v1/generation/micro-batches/:id",
		"GET /api/v1/generation/micro-batches/:id/result",
		"DELETE /api/v1/generation/micro-batches/:id",
	} {
		require.True(t, got[route], "missing route %s", route)
	}
}

func TestS5MicroBatchCreateIsStrictAndRequiresExactlyThreeSlots(t *testing.T) {
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	fake := &fakeS5MicroBatchService{}
	handler, err := NewS5MicroBatchHandler(fake, temporalmocks.NewClient(t), &ProblemHandler{}, quality)
	require.NoError(t, err)

	request := s5MicroBatchTestRequest(t, generationapi.EvidenceMinimal)
	request.Slots = request.Slots[:2]
	body, err := json.Marshal(request)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", string(body), "wrong-shape", nil, handler.HandleCreate)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.Zero(t, fake.triggerCalls)

	validBody := s5MicroBatchTestRequestBody(t, generationapi.EvidenceMinimal)
	recorder = invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", validBody+"\n{}", "trailing", nil, handler.HandleCreate)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, fake.triggerCalls)

	withUnknown := strings.Replace(validBody, `"schema_version":`, `"unknown":true,"schema_version":`, 1)
	recorder = invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", withUnknown, "unknown", nil, handler.HandleCreate)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, fake.triggerCalls)
}

func TestS5MicroBatchCreateRejectsRoleOnlyPrincipal(t *testing.T) {
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	fake := &fakeS5MicroBatchService{}
	handler, err := NewS5MicroBatchHandler(fake, temporalmocks.NewClient(t), &ProblemHandler{}, quality)
	require.NoError(t, err)
	claims := &authmw.JWTClaims{Role: "admin"}
	recorder := invokeS5MicroBatchHandler(
		t, http.MethodPost, "/api/v1/generation/micro-batches", "",
		s5MicroBatchTestRequestBody(t, generationapi.EvidenceMinimal), "role-only", claims, handler.HandleCreate,
	)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, fake.triggerCalls)
}

func TestS5MicroBatchLegacyQualityRejectsNonMinimalBeforeStart(t *testing.T) {
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeLegacyOnly, true, nil)
	fake := &fakeS5MicroBatchService{}
	handler, err := NewS5MicroBatchHandler(fake, temporalmocks.NewClient(t), &ProblemHandler{}, quality)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(
		t, http.MethodPost, "/api/v1/generation/micro-batches", "",
		s5MicroBatchTestRequestBody(t, generationapi.EvidenceStandard), "legacy-standard", nil, handler.HandleCreate,
	)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"unsupported_constraint"`)
	require.Zero(t, fake.triggerCalls)
}

func TestS5MicroBatchCreateBindsIdentityAndParentRuntimeTTL(t *testing.T) {
	request := s5MicroBatchTestRequest(t, generationapi.EvidenceMinimal)
	for index := range request.Slots {
		request.Slots[index].Quality.Budget.MaxWallTimeSeconds = generationapi.MaxWallTimeSeconds
	}
	bodyBytes, err := json.Marshal(request)
	require.NoError(t, err)
	body := string(bodyBytes)
	_, payloadSHA256, err := request.CanonicalPayload()
	require.NoError(t, err)
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "create-s5")
	require.NoError(t, err)

	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
		Return(nil, serviceerror.NewNotFound("absent")).Once()
	keyStore := &fakeRuntimeKeyStore{}
	providerHelper := NewProblemHandler(nil, nil, keyStore)
	providerHelper.SetPermanentProviderSettings(&fakePermanentProviderSettings{
		statement: &domain.LLMRuntimeConfig{
			Model: "statement", APIKey: "stored-statement-key", BaseURL: "https://llm.example.com",
			Provider: "openai", Protocol: "openai-responses",
		},
		verification: &domain.LLMRuntimeConfig{
			Model: "verification", APIKeyRef: "env:OPENAI_API_KEY", BaseURL: "https://review.example.com",
			Provider: "openai", Protocol: "openai-responses",
		},
	}, true)
	fake := &fakeS5MicroBatchService{}
	fake.trigger = func(
		_ context.Context,
		gotBatchID string,
		gotPayload string,
		gotPrincipal string,
		gotTimeout time.Duration,
		params []domain.ProblemGenParams,
	) error {
		require.Equal(t, batchID, gotBatchID)
		require.Equal(t, payloadSHA256, gotPayload)
		require.Equal(t, principalSHA256, gotPrincipal)
		require.Equal(t, 24*time.Hour, gotTimeout)
		require.Len(t, params, diversityapi.SlotCountV1)
		for _, slot := range params {
			require.Empty(t, slot.ProviderConfig.Statement.APIKey)
			require.Equal(t, "runtime:test-token", slot.ProviderConfig.Statement.APIKeyRef)
		}
		return nil
	}
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	handler, err := NewS5MicroBatchHandler(fake, temporalClient, providerHelper, quality)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", body, "create-s5", nil, handler.HandleCreate)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 24*time.Hour+s5MicroBatchRuntimeKeyTTLGraceV1, keyStore.lastTTL)
	require.Equal(t, diversityapi.SlotCountV1, keyStore.puts)
	require.Equal(t, 1, fake.triggerCalls)
	require.Contains(t, recorder.Body.String(), `"batch_id":"`+batchID+`"`)
	require.NotContains(t, recorder.Body.String(), "stored-statement-key")
}

func TestS5MicroBatchIdempotentReplayAndConflictUseParentMemo(t *testing.T) {
	request := s5MicroBatchTestRequest(t, generationapi.EvidenceMinimal)
	_, payloadSHA256, err := request.CanonicalPayload()
	require.NoError(t, err)
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "replay-s5")
	require.NoError(t, err)
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)

	t.Run("replay", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
			Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, payloadSHA256, principalSHA256), nil).Once()
		fake := &fakeS5MicroBatchService{}
		handler, err := NewS5MicroBatchHandler(fake, temporalClient, nil, quality)
		require.NoError(t, err)
		recorder := invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", s5MicroBatchTestRequestBody(t, generationapi.EvidenceMinimal), "replay-s5", nil, handler.HandleCreate)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"idempotent_replay":true`)
		require.Zero(t, fake.triggerCalls)
	})

	t.Run("conflict", func(t *testing.T) {
		temporalClient := temporalmocks.NewClient(t)
		temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
			Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("f", 64), principalSHA256), nil).Once()
		fake := &fakeS5MicroBatchService{}
		handler, err := NewS5MicroBatchHandler(fake, temporalClient, nil, quality)
		require.NoError(t, err)
		recorder := invokeS5MicroBatchHandler(t, http.MethodPost, "/api/v1/generation/micro-batches", "", s5MicroBatchTestRequestBody(t, generationapi.EvidenceMinimal), "replay-s5", nil, handler.HandleCreate)
		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Zero(t, fake.triggerCalls)
	})
}

func TestS5MicroBatchCompletedStatusAndResultProjectExactlyThreeProblems(t *testing.T) {
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "completed-s5")
	require.NoError(t, err)
	parentResult, problems := s5ValidParentResultV1(t, batchID)
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)

	for _, test := range []struct {
		name string
		path string
		call func(*S5MicroBatchHandler) echo.HandlerFunc
	}{
		{name: "status", path: "/api/v1/generation/micro-batches/" + batchID, call: func(h *S5MicroBatchHandler) echo.HandlerFunc { return h.HandleStatus }},
		{name: "result", path: "/api/v1/generation/micro-batches/" + batchID + "/result", call: func(h *S5MicroBatchHandler) echo.HandlerFunc { return h.HandleResult }},
	} {
		t.Run(test.name, func(t *testing.T) {
			temporalClient := temporalmocks.NewClient(t)
			temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
				Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, strings.Repeat("a", 64), principalSHA256), nil).Once()
			run := temporalmocks.NewWorkflowRun(t)
			run.On("Get", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					result := args.Get(1).(*algoworkflow.S5MicroBatchResultV1)
					*result = parentResult
				}).Return(nil).Once()
			temporalClient.On("GetWorkflow", mock.Anything, batchID, "s5-parent-run").Return(run).Once()
			handler, err := NewS5MicroBatchHandler(&fakeS5MicroBatchService{problems: problems}, temporalClient, nil, quality)
			require.NoError(t, err)
			recorder := invokeS5MicroBatchHandler(t, http.MethodGet, test.path, batchID, "", "", nil, test.call(handler))
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"status":"succeeded"`)
			require.Contains(t, recorder.Body.String(), `"corpus_revision":"`+parentResult.CorpusRevision+`"`)
			require.Contains(t, recorder.Body.String(), `"reservation_sha256":"`+parentResult.ReservationSHA256+`"`)
			require.Contains(t, recorder.Body.String(), `"manifest_sha256":"`+parentResult.ManifestSHA256+`"`)
			for _, problem := range problems {
				if test.name == "result" {
					require.Contains(t, recorder.Body.String(), problem.ID.String())
				}
			}
			if test.name == "result" {
				require.Equal(t, 12, strings.Count(recorder.Body.String(), `"content_hash":`))
				for _, slot := range parentResult.Reservation.Slots {
					require.Contains(t, recorder.Body.String(), `"concept_id":"`+slot.Winner.ConceptID+`"`)
					require.Contains(t, recorder.Body.String(), `"pool_sha256":"`+slot.PoolSHA256+`"`)
					require.Contains(t, recorder.Body.String(), `"structural_signature_sha256":"`+slot.Winner.StructuralSignatureSHA+`"`)
				}
			}
			require.NotContains(t, recorder.Body.String(), "run_id")
			require.NotContains(t, recorder.Body.String(), "workflow_id")
			require.NotContains(t, recorder.Body.String(), "workflow-artifacts/")
		})
	}
}

func TestS5MicroBatchResultRejectsSplicedDedupObservation(t *testing.T) {
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "spliced-s5")
	require.NoError(t, err)
	parentResult, problems := s5ValidParentResultV1(t, batchID)
	parentResult.DedupObservations[0].ContentHash = strings.Repeat("f", 64)
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
		Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, strings.Repeat("a", 64), principalSHA256), nil).Once()
	run := temporalmocks.NewWorkflowRun(t)
	run.On("Get", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		result := args.Get(1).(*algoworkflow.S5MicroBatchResultV1)
		*result = parentResult
	}).Return(nil).Once()
	temporalClient.On("GetWorkflow", mock.Anything, batchID, "s5-parent-run").Return(run).Once()
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	handler, err := NewS5MicroBatchHandler(&fakeS5MicroBatchService{problems: problems}, temporalClient, nil, quality)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(t, http.MethodGet, "/api/v1/generation/micro-batches/"+batchID+"/result", batchID, "", "", nil, handler.HandleResult)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"internal_error"`)
	require.NotContains(t, recorder.Body.String(), strings.Repeat("f", 64))
}

func TestS5MicroBatchRunningStatusUsesValidatedParentQuery(t *testing.T) {
	batchID, principalSHA256, err := diversityapi.BatchIDForIdempotencyKey("local-dev", "running-s5")
	require.NoError(t, err)
	state := algoworkflow.S5MicroBatchStateV1{
		PayloadVersion: algoworkflow.S5MicroBatchPayloadVersionV1,
		BatchID:        batchID, Phase: algoworkflow.S5MicroBatchPhaseDedup,
		Status: domain.WorkflowStatusRunning, Progress: 50,
		ChildJobIDs: make([]string, diversityapi.SlotCountV1),
		Children:    make([]algoworkflow.S5MicroBatchChildStateV1, diversityapi.SlotCountV1),
	}
	for index := 0; index < diversityapi.SlotCountV1; index++ {
		childID, childErr := diversityapi.ChildJobID(batchID, index)
		require.NoError(t, childErr)
		state.ChildJobIDs[index] = childID
		state.Children[index] = algoworkflow.S5MicroBatchChildStateV1{
			SlotIndex: index, JobID: childID, Status: domain.WorkflowStatusPending,
		}
	}
	payloads, err := converter.GetDefaultDataConverter().ToPayloads(state)
	require.NoError(t, err)
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
		Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("a", 64), principalSHA256), nil).Once()
	temporalClient.On("QueryWorkflow", mock.Anything, batchID, "s5-parent-run", algoworkflow.S5MicroBatchStateQueryName).
		Return(client.NewValue(payloads), nil).Once()
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	handler, err := NewS5MicroBatchHandler(&fakeS5MicroBatchService{}, temporalClient, nil, quality)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(t, http.MethodGet, "/api/v1/generation/micro-batches/"+batchID, batchID, "", "", nil, handler.HandleStatus)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"phase":"validating"`)
	require.Contains(t, recorder.Body.String(), `"progress":50`)
}

func TestS5MicroBatchWrongPrincipalIsHiddenAsNotFound(t *testing.T) {
	batchID, ownerSHA256, err := diversityapi.BatchIDForIdempotencyKey("user:owner", "owned-s5")
	require.NoError(t, err)
	temporalClient := temporalmocks.NewClient(t)
	temporalClient.On("DescribeWorkflowExecution", mock.Anything, batchID, "").
		Return(s5MicroBatchTestDescription(t, batchID, enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, strings.Repeat("a", 64), ownerSHA256), nil).Once()
	quality, _ := qualitymode.ResolveMode(qualitymode.ModeQualityV1, true, nil)
	handler, err := NewS5MicroBatchHandler(&fakeS5MicroBatchService{}, temporalClient, nil, quality)
	require.NoError(t, err)
	recorder := invokeS5MicroBatchHandler(
		t, http.MethodGet, "/api/v1/generation/micro-batches/"+batchID, batchID, "", "",
		&authmw.JWTClaims{UserID: "other"}, handler.HandleStatus,
	)
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "owner")
}

func s5ValidParentResultV1(
	t *testing.T,
	batchID string,
) (algoworkflow.S5MicroBatchResultV1, map[string]*domain.Problem) {
	t.Helper()
	corpusRevision := strings.Repeat("b", 64)
	pools := make([]diversity.ConceptPoolV1, diversityapi.SlotCountV1)
	for slotIndex := range pools {
		pool := diversity.ConceptPoolV1{
			SchemaVersion: diversity.ConceptPoolSchemaV1, BatchID: batchID,
			SlotIndex: slotIndex, SlotID: fmt.Sprintf("s5-slot-%d", slotIndex),
			BriefSHA256: strings.Repeat(string(rune('a'+slotIndex)), 64), CorpusRevision: corpusRevision,
			Budget: diversity.ConceptBudgetV1{
				MaxCreativeAttempts: 2, MaxConcepts: 4, MaxModelCalls: 3,
				MaxNetworkRetries: 0, MaxTokens: 1000, MaxWallMilliseconds: 1000,
			},
			Attempts: make([]diversity.ConceptAttemptV1, 2),
			Normalization: diversity.ConceptNormalizationV1{
				NormalizerVersion: "algoforge.s5-concept-normalizer.v1",
				Receipt:           diversity.ArtifactRefV1{SHA256: strings.Repeat("e", 64), URI: fmt.Sprintf("cas://slot/%d/normalize", slotIndex)},
				Usage:             diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 10, WallMilliseconds: 1},
			},
		}
		for attemptIndex := 0; attemptIndex < 2; attemptIndex++ {
			attempt := diversity.ConceptAttemptV1{
				AttemptIndex: attemptIndex, LogicalAttemptID: fmt.Sprintf("s5-slot-%d-attempt-%d", slotIndex, attemptIndex),
				Receipt:  diversity.ArtifactRefV1{SHA256: strings.Repeat("d", 64), URI: fmt.Sprintf("cas://slot/%d/attempt/%d", slotIndex, attemptIndex)},
				Usage:    diversity.ConceptAttemptUsageV1{ModelCalls: 1, Tokens: 10, WallMilliseconds: 1},
				Concepts: make([]diversity.ConceptCardV1, 2),
			}
			for conceptIndex := 0; conceptIndex < 2; conceptIndex++ {
				ordinal := slotIndex*4 + attemptIndex*2 + conceptIndex
				conceptID := fmt.Sprintf("s5-slot-%d-concept-%d", slotIndex, ordinal)
				attempt.Concepts[conceptIndex] = diversity.ConceptCardV1{
					ConceptIndex: conceptIndex, ConceptID: conceptID,
					OneParagraphPitch:   fmt.Sprintf("deterministic S5 pitch %d", ordinal),
					UnresolvedQuestions: []string{"boundary", "ties"},
					Spec: diversity.ConceptSpecV1{
						SchemaVersion:        diversity.ConceptSpecSchemaV1,
						CanonicalizerVersion: diversity.ConceptCanonicalizerVersionV1,
						ExtractionConfidence: 0.9, QualityTier: diversity.QualityTierViable,
						ProblemMode: "offline", InputObject: fmt.Sprintf("sequence-%d", ordinal),
						Topology: fmt.Sprintf("line-%d", slotIndex), OperationModel: "static",
						Objective:             fmt.Sprintf("objective-%d", ordinal),
						StateDimensions:       []string{"position", "prefix"},
						TransitionOrInvariant: fmt.Sprintf("invariant-%d", ordinal),
						SolutionOperatorSeq:   []string{"scan", fmt.Sprintf("operator-%d", ordinal)},
						OutputForm:            "integer", ComplexityClass: "linear", ConstraintRegime: "large",
						WrongSolutionFamilies: []string{"greedy", "overflow"},
					},
				}
			}
			pool.Attempts[attemptIndex] = attempt
		}
		pools[slotIndex] = pool
	}
	reservation, _, reservationSHA256, err := diversity.SelectMicroBatchV1(pools)
	require.NoError(t, err)

	observations := make([]diversity.DedupObservationV1, 0, 12)
	dedupBindings := make([]activities.S5ConceptDedupBindingV1, 0, 12)
	for slotIndex, pool := range pools {
		for attemptIndex, attempt := range pool.Attempts {
			for conceptIndex, card := range attempt.Concepts {
				contentHash, hashErr := activities.S5ConceptContentHashV1(card.Spec)
				require.NoError(t, hashErr)
				count := 0
				observation := diversity.DedupObservationV1{
					SchemaVersion: diversity.DedupObservationSchemaV1,
					Stage:         diversity.DedupStageConceptSelection, ModelVersion: "structure-model-v1",
					Kind: diversity.DedupKindStructure, ContentHash: contentHash, CorpusRevision: corpusRevision,
					RequestedTopK: 8, Threshold: 0.9, Decision: diversity.DedupDecisionPass,
					NeighborCount: &count,
				}
				observations = append(observations, observation)
				dedupBindings = append(dedupBindings, activities.S5ConceptDedupBindingV1{
					SlotIndex: slotIndex, AttemptIndex: attemptIndex, ConceptIndex: conceptIndex,
					ConceptID: card.ConceptID, Observation: observation,
				})
			}
		}
	}

	result := algoworkflow.S5MicroBatchResultV1{
		PayloadVersion: algoworkflow.S5MicroBatchPayloadVersionV1,
		BatchID:        batchID, CorpusRevision: corpusRevision, Pools: pools,
		DedupObservations: observations, Reservation: reservation, ReservationSHA256: reservationSHA256,
		ChildJobIDs: make([]string, diversityapi.SlotCountV1),
		Children:    make([]algoworkflow.S5MicroBatchChildResultV1, diversityapi.SlotCountV1),
	}
	problems := map[string]*domain.Problem{}
	for slotIndex := 0; slotIndex < diversityapi.SlotCountV1; slotIndex++ {
		childID, childErr := diversityapi.ChildJobID(batchID, slotIndex)
		require.NoError(t, childErr)
		problem := &domain.Problem{ID: uuid.New(), Status: domain.ProblemStatusDraft}
		problems[childID] = problem
		result.ChildJobIDs[slotIndex] = childID
		result.Children[slotIndex] = algoworkflow.S5MicroBatchChildResultV1{
			SlotIndex: slotIndex, SlotID: reservation.Slots[slotIndex].SlotID, JobID: childID,
			ConceptID: reservation.Slots[slotIndex].Winner.ConceptID, Decision: "pass",
			StoredProblemID: problem.ID.String(), StoredProblemStatus: domain.ProblemStatusDraft,
		}
	}
	manifest := activities.S5ProvisionalReservationManifestV1{
		SchemaVersion: activities.S5ProvisionalReservationManifestSchemaV1,
		BatchID:       batchID, CorpusRevision: corpusRevision, ReservationSHA256: reservationSHA256,
		Reservation: reservation, Pools: pools, DedupBindings: dedupBindings,
		ChildJobIDs: append([]string(nil), result.ChildJobIDs...),
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	result.ManifestSHA256 = diversity.SHA256Hex(manifestBytes)
	result.ManifestArtifact = &activities.ArtifactRef{
		SchemaVersion:  activities.ArtifactRefSchemaVersion,
		PayloadVersion: activities.ActivityPayloadVersion,
		Bucket:         "s5-test", Key: "workflow-artifacts/v1/sha256/" + result.ManifestSHA256[:2] + "/" + result.ManifestSHA256,
		SHA256: result.ManifestSHA256, SizeBytes: int64(len(manifestBytes)), ContentType: "application/json",
		Producer: "StoreS5ProvisionalReservationActivityV1", Provider: "algoforge",
		Model: "not_applicable", ModelRevision: "not_applicable", WorkflowID: batchID,
	}
	return result, problems
}

func s5MicroBatchTestRequest(t *testing.T, evidenceLevel string) diversityapi.RequestV1 {
	t.Helper()
	var slot generationapi.Request
	require.NoError(t, json.Unmarshal([]byte(generationJobTestRequestBody(t)), &slot))
	slot.Output.EvidenceLevel = evidenceLevel
	slots := make([]generationapi.Request, diversityapi.SlotCountV1)
	for index := range slots {
		slots[index] = slot
		slots[index].CustomRequirements = "S5 slot " + string(rune('A'+index))
	}
	return diversityapi.RequestV1{SchemaVersion: diversityapi.ContractVersion, Slots: slots}
}

func s5MicroBatchTestRequestBody(t *testing.T, evidenceLevel string) string {
	t.Helper()
	payload, err := json.Marshal(s5MicroBatchTestRequest(t, evidenceLevel))
	require.NoError(t, err)
	return string(payload)
}

func s5MicroBatchTestDescription(
	t *testing.T,
	batchID string,
	status enumspb.WorkflowExecutionStatus,
	payloadSHA256 string,
	principalSHA256 string,
) *workflowservice.DescribeWorkflowExecutionResponse {
	t.Helper()
	fields := make(map[string]*commonpb.Payload)
	for key, value := range map[string]string{
		diversityapi.MemoContractVersionKey: diversityapi.ContractVersion,
		diversityapi.MemoPayloadSHA256Key:   payloadSHA256,
		diversityapi.MemoPrincipalScopeKey:  principalSHA256,
	} {
		payload, err := converter.GetDefaultDataConverter().ToPayload(value)
		require.NoError(t, err)
		fields[key] = payload
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: batchID, RunId: "s5-parent-run"},
			Type:      &commonpb.WorkflowType{Name: diversityapi.WorkflowTypeV1},
			Status:    status,
			Memo:      &commonpb.Memo{Fields: fields},
		},
	}
}

func invokeS5MicroBatchHandler(
	t *testing.T,
	method string,
	path string,
	batchID string,
	body string,
	idempotencyKey string,
	claims *authmw.JWTClaims,
	handler echo.HandlerFunc,
) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	ctx := e.NewContext(request, recorder)
	if batchID != "" {
		ctx.SetParamNames("id")
		ctx.SetParamValues(batchID)
	}
	if claims != nil {
		ctx.Set("user", claims)
	}
	require.NoError(t, handler(ctx))
	return recorder
}
