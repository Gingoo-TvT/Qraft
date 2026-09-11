//go:build integration

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	workflowpkg "github.com/Gingoo-TvT/Qraft/backend/internal/workflow"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
)

func TestReviewDecisionConcurrentHTTPTemporalIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_TEMPORAL_INTEGRATION") != "true" {
		t.Skip("set ALGOFORGE_TEMPORAL_INTEGRATION=true to run against a disposable Temporal service")
	}

	worker.SetStickyWorkflowCacheSize(0)
	tests := []struct {
		name          string
		firstApproved bool
		wantStatus    domain.WorkflowStatus
		wantStore     int32
	}{
		{
			name:          "approve signal wins",
			firstApproved: true,
			wantStatus:    domain.WorkflowStatusCompleted,
			wantStore:     1,
		},
		{
			name:          "reject signal wins",
			firstApproved: false,
			wantStatus:    domain.WorkflowStatusFailed,
			wantStore:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runReviewDecisionConflictTemporalTest(t, tt.firstApproved, tt.wantStatus, tt.wantStore)
		})
	}
}

func TestReviewGateV2RejectedQuarantineTemporalIntegration(t *testing.T) {
	if os.Getenv("ALGOFORGE_TEMPORAL_INTEGRATION") != "true" {
		t.Skip("set ALGOFORGE_TEMPORAL_INTEGRATION=true to run against a disposable Temporal service")
	}
	worker.SetStickyWorkflowCacheSize(0)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	temporalClient, err := client.Dial(client.Options{
		HostPort:  envOrDefault("TEMPORAL_ADDRESS", "127.0.0.1:7233"),
		Namespace: envOrDefault("TEMPORAL_NAMESPACE", "default"),
	})
	require.NoError(t, err)
	defer temporalClient.Close()

	taskQueue := "s1-review-v2-" + uuid.NewString()
	workflowID := "s1-review-v2-" + uuid.NewString()
	fixture := &reviewTemporalActivities{}
	activeWorker := startReviewTemporalWorker(t, temporalClient, taskQueue, fixture)
	defer activeWorker.Stop()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskQueue,
		WorkflowExecutionTimeout: 2 * time.Minute,
		WorkflowRunTimeout:       2 * time.Minute,
	}, workflowpkg.ProblemGenerationWorkflow, reviewTemporalProductParams())
	require.NoError(t, err)

	var state domain.WorkflowState
	require.NoError(t, run.Get(ctx, &state))
	require.Equal(t, domain.WorkflowStatusRejectedQuarantined, state.Status)
	require.Equal(t, int32(1), fixture.storeCalls.Load())
	storeInput := fixture.lastStoreInput()
	require.NotNil(t, storeInput)
	require.NotNil(t, storeInput.ReviewQuarantine)
	require.Equal(t, "problem-generation-review-gate-v2", storeInput.ReviewQuarantine.ReviewGateChangeID)
	require.Equal(t, 2, storeInput.ReviewQuarantine.ReviewGateVersion)
	require.NotEmpty(t, storeInput.ReviewQuarantine.ReviewResult.FullText)
	require.NotEmpty(t, storeInput.ReviewQuarantine.SourceAncestry)

	history := temporalClient.GetWorkflowHistory(ctx, workflowID, run.GetRunID(), false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	var timers, stores, v2Markers int
	for history.HasNext() {
		event, err := history.Next()
		require.NoError(t, err)
		switch event.GetEventType() {
		case enums.EVENT_TYPE_TIMER_STARTED:
			timers++
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			if event.GetActivityTaskScheduledEventAttributes().GetActivityType().GetName() == "StoreProblemActivity" {
				stores++
			}
		case enums.EVENT_TYPE_MARKER_RECORDED:
			attrs := event.GetMarkerRecordedEventAttributes()
			if attrs == nil || attrs.GetMarkerName() != "Version" {
				continue
			}
			var changeID string
			if payloads := attrs.GetDetails()["change-id"]; payloads != nil {
				require.NoError(t, converter.GetDefaultDataConverter().FromPayloads(payloads, &changeID))
			}
			if changeID == "problem-generation-review-gate-v2" {
				v2Markers++
			}
		}
	}
	require.Zero(t, timers, "v2 product rejection must not schedule the legacy 72h timer")
	require.Equal(t, 1, stores)
	require.Equal(t, 1, v2Markers)
}

func runReviewDecisionConflictTemporalTest(
	t *testing.T,
	firstApproved bool,
	wantStatus domain.WorkflowStatus,
	wantStore int32,
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	temporalClient, err := client.Dial(client.Options{
		HostPort:  envOrDefault("TEMPORAL_ADDRESS", "127.0.0.1:7233"),
		Namespace: envOrDefault("TEMPORAL_NAMESPACE", "default"),
	})
	require.NoError(t, err)
	defer temporalClient.Close()

	taskQueue := "s0-review-ack-" + uuid.NewString()
	workflowID := "s0-review-ack-" + uuid.NewString()
	fixture := &reviewTemporalActivities{}
	activeWorker := startReviewTemporalWorker(t, temporalClient, taskQueue, fixture)
	defer func() {
		if activeWorker != nil {
			activeWorker.Stop()
		}
	}()

	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                taskQueue,
		WorkflowExecutionTimeout: 2 * time.Minute,
		WorkflowRunTimeout:       2 * time.Minute,
	}, workflowpkg.ProblemGenerationWorkflow, reviewTemporalParams())
	require.NoError(t, err)
	runID := run.GetRunID()
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		_ = temporalClient.CancelWorkflow(cleanupCtx, workflowID, runID)
	}()

	reviewRequest := waitForTemporalReviewRequest(t, ctx, temporalClient, workflowID, runID)
	barrier := newReviewQueryBarrier()
	gatedClient := newReviewConflictClient(
		temporalClient,
		workflowID,
		runID,
		barrier,
		firstApproved,
	)
	h := NewWorkflowHandler(gatedClient, envOrDefault("TEMPORAL_NAMESPACE", "default"))

	responses := make(chan reviewHTTPResult, 2)
	go invokeTemporalReviewHandler(
		ctx,
		workflowID,
		h.HandleApprove,
		reviewDecisionRequest{
			Token:         reviewRequest.Token,
			RunID:         runID,
			ReviewAttempt: reviewRequest.ReviewAttempt,
			Feedback:      "approved in concurrent integration test",
		},
		responses,
	)
	go invokeTemporalReviewHandler(
		ctx,
		workflowID,
		h.HandleReject,
		reviewDecisionRequest{
			Token:         reviewRequest.Token,
			RunID:         runID,
			ReviewAttempt: reviewRequest.ReviewAttempt,
			Feedback:      "rejected in concurrent integration test",
		},
		responses,
	)

	waitForTestSignal(t, ctx, barrier.ready, "both HTTP preflight queries")
	activeWorker.Stop()
	activeWorker = nil
	close(barrier.release)
	waitForTestSignal(t, ctx, gatedClient.signalsFinished, "both persisted review signals")

	activeWorker = startReviewTemporalWorker(t, temporalClient, taskQueue, fixture)
	results := []reviewHTTPResult{
		waitForReviewHTTPResult(t, ctx, responses),
		waitForReviewHTTPResult(t, ctx, responses),
	}
	for _, result := range results {
		require.NoError(t, result.err, result.body)
	}
	codes := []int{results[0].code, results[1].code}
	sort.Ints(codes)
	require.Equal(t, []int{http.StatusOK, http.StatusConflict}, codes)

	var workflowState domain.WorkflowState
	workflowErr := run.Get(ctx, &workflowState)
	if firstApproved {
		require.NoError(t, workflowErr)
	} else {
		require.Error(t, workflowErr)
	}
	require.Equal(t, wantStore, fixture.storeCalls.Load())

	closedState := queryTemporalReviewStateAllowClosed(t, ctx, temporalClient, workflowID, runID)
	require.Equal(t, wantStatus, closedState.State.Status)
	require.NotNil(t, closedState.ReviewRequest)
	require.NotNil(t, closedState.ReviewRequest.Decision)
	require.Equal(t, firstApproved, closedState.ReviewRequest.Decision.Approved)
	for _, result := range results {
		if result.approved == firstApproved {
			require.Equal(t, http.StatusOK, result.code, result.body)
		} else {
			require.Equal(t, http.StatusConflict, result.code, result.body)
		}
	}
}

type reviewTemporalActivities struct {
	storeCalls atomic.Int32
	mu         sync.Mutex
	storeInput *activities.StoreInput
}

func (a *reviewTemporalActivities) GenerateStatementActivity(
	context.Context,
	activities.GenerateStatementInput,
) (*activities.StatementResult, error) {
	return &activities.StatementResult{
		Title:       "Review acknowledgement fixture",
		Statement:   "Given one integer, print it.",
		OneLineHint: "Identity",
	}, nil
}

func (a *reviewTemporalActivities) CleanStatementActivity(
	_ context.Context,
	statement activities.StatementResult,
) (*activities.StatementResult, error) {
	return &statement, nil
}

func (a *reviewTemporalActivities) PostStatementSimilarityActivity(
	context.Context,
	activities.StatementResult,
) (*activities.PostStatementSimilarityResult, error) {
	return &activities.PostStatementSimilarityResult{}, nil
}

func (a *reviewTemporalActivities) GenerateTestDataActivity(
	context.Context,
	string,
	domain.TestDataConfig,
) (*activities.TestDataResult, error) {
	return &activities.TestDataResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		TestCases: []activities.TestCaseData{{
			Input:       "1\n",
			GroupID:     1,
			IsSample:    true,
			Description: "fixture",
		}},
	}, nil
}

func (a *reviewTemporalActivities) GenerateSolutionActivity(
	context.Context,
	string,
	domain.ProblemGenParams,
) (*activities.SolutionResult, error) {
	return &activities.SolutionResult{
		MainSolution: domain.Solution{
			SolutionType: domain.SolutionTypeMain,
			Language:     "cpp",
			SourceCode:   "int main() {}",
		},
		BruteSolution: domain.Solution{
			SolutionType: domain.SolutionTypeBrute,
			Language:     "cpp",
			SourceCode:   "int main() {}",
		},
	}, nil
}

func (a *reviewTemporalActivities) CompileCheckActivity(
	context.Context,
	[]domain.Solution,
) (*activities.CompileCheckResult, error) {
	return &activities.CompileCheckResult{AllCompiled: true}, nil
}

func (a *reviewTemporalActivities) RunSandboxActivity(
	context.Context,
	domain.Solution,
	[]activities.TestCaseData,
	activities.ExecutionLimits,
) (*activities.SandboxResult, error) {
	return &activities.SandboxResult{
		PayloadVersion: activities.ActivityPayloadVersion,
		Outputs:        []string{"1\n"},
	}, nil
}

func (a *reviewTemporalActivities) ValidateActivity(
	context.Context,
	activities.SandboxResult,
	activities.SandboxResult,
) (*activities.ValidationResult, error) {
	return &activities.ValidationResult{AllPassed: true}, nil
}

func (a *reviewTemporalActivities) LLMReviewActivity(
	context.Context,
	activities.LLMReviewInput,
) (*activities.ReviewResult, error) {
	return &activities.ReviewResult{
		SourceArtifacts: []*activities.ArtifactRef{{
			ModelRevision: "integration-review-r1",
			SHA256:        strings.Repeat("a", 64),
		}},
		Approved:   false,
		Confidence: 0.9,
		Issues:     []string{"integration review issue"},
		FullText:   `{"approved":false,"issues":["integration review issue"],"review_details":{"correctness":{"score":4,"notes":"fixture rejection"}}}`,
	}, nil
}

func (a *reviewTemporalActivities) StoreProblemActivity(
	_ context.Context,
	input activities.StoreInput,
) (*activities.StoreResult, error) {
	a.storeCalls.Add(1)
	a.mu.Lock()
	inputCopy := input
	a.storeInput = &inputCopy
	a.mu.Unlock()
	status := domain.ProblemStatusDraft
	quarantineReason := ""
	if input.ReviewQuarantine != nil {
		status = domain.ProblemStatusQuarantined
		quarantineReason = input.ReviewQuarantine.Reason
	}
	return &activities.StoreResult{
		ProblemID:        uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SerialNumber:     "AF-REVIEW-INTEGRATION",
		Status:           status,
		QuarantineReason: quarantineReason,
	}, nil
}

func (a *reviewTemporalActivities) lastStoreInput() *activities.StoreInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.storeInput == nil {
		return nil
	}
	copy := *a.storeInput
	return &copy
}

func reviewTemporalParams() domain.ProblemGenParams {
	params := domain.DefaultProblemGenParams()
	params.SimilarLimit = 0
	params.RequireReview = true // exercise the deprecated operational break-glass path
	params.GenerateEditorial = false
	params.TestDataConfig = domain.TestDataConfig{
		NumTestCases: 1,
		NumSamples:   1,
		Groups: []domain.TestGroup{{
			GroupID:    1,
			NumCases:   1,
			Score:      100,
			BruteCheck: true,
		}},
	}
	return params
}

func reviewTemporalProductParams() domain.ProblemGenParams {
	params := reviewTemporalParams()
	params.RequireReview = false
	return params
}

func startReviewTemporalWorker(
	t *testing.T,
	temporalClient client.Client,
	taskQueue string,
	fixture *reviewTemporalActivities,
) worker.Worker {
	t.Helper()
	w := worker.New(temporalClient, taskQueue, worker.Options{
		StickyScheduleToStartTimeout: 100 * time.Millisecond,
	})
	w.RegisterWorkflow(workflowpkg.ProblemGenerationWorkflow)
	w.RegisterActivity(fixture)
	require.NoError(t, w.Start())
	return w
}

func waitForTemporalReviewRequest(
	t *testing.T,
	ctx context.Context,
	temporalClient client.Client,
	workflowID string,
	runID string,
) *domain.ReviewRequest {
	t.Helper()

	var review *domain.ReviewRequest
	require.Eventually(t, func() bool {
		value, err := temporalClient.QueryWorkflow(ctx, workflowID, runID, domain.WorkflowStateQueryName)
		if err != nil {
			return false
		}
		var state domain.WorkflowStateQuery
		if err := value.Get(&state); err != nil ||
			state.State.Status != domain.WorkflowStatusWaitingReview ||
			state.ReviewRequest == nil ||
			state.ReviewRequest.Decision != nil {
			return false
		}
		copy := *state.ReviewRequest
		review = &copy
		return true
	}, 20*time.Second, 50*time.Millisecond)
	require.NotNil(t, review)
	require.Equal(t, runID, review.RunID)
	require.True(t, review.TokenRequired)
	return review
}

type reviewQueryBarrier struct {
	mu        sync.Mutex
	arrivals  int
	ready     chan struct{}
	release   chan struct{}
	readyOnce sync.Once
}

func newReviewQueryBarrier() *reviewQueryBarrier {
	return &reviewQueryBarrier{
		ready:   make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *reviewQueryBarrier) arrive(ctx context.Context) error {
	b.mu.Lock()
	b.arrivals++
	if b.arrivals == 2 {
		b.readyOnce.Do(func() { close(b.ready) })
	}
	b.mu.Unlock()

	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type reviewConflictClient struct {
	client.Client
	workflowID      string
	runID           string
	queryBarrier    *reviewQueryBarrier
	firstApproved   bool
	firstPersisted  chan struct{}
	firstOnce       sync.Once
	signalMu        sync.Mutex
	signalCount     int
	signalsFinished chan struct{}
	finishedOnce    sync.Once
}

func newReviewConflictClient(
	temporalClient client.Client,
	workflowID string,
	runID string,
	queryBarrier *reviewQueryBarrier,
	firstApproved bool,
) *reviewConflictClient {
	return &reviewConflictClient{
		Client:          temporalClient,
		workflowID:      workflowID,
		runID:           runID,
		queryBarrier:    queryBarrier,
		firstApproved:   firstApproved,
		firstPersisted:  make(chan struct{}),
		signalsFinished: make(chan struct{}),
	}
}

func (c *reviewConflictClient) QueryWorkflow(
	ctx context.Context,
	workflowID string,
	runID string,
	queryType string,
	args ...interface{},
) (converter.EncodedValue, error) {
	value, err := c.Client.QueryWorkflow(ctx, workflowID, runID, queryType, args...)
	if err != nil {
		return nil, err
	}
	if workflowID == c.workflowID && runID == c.runID && queryType == domain.WorkflowStateQueryName {
		if err := c.queryBarrier.arrive(ctx); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func (c *reviewConflictClient) SignalWorkflow(
	ctx context.Context,
	workflowID string,
	runID string,
	signalName string,
	arg interface{},
) error {
	signal, ok := arg.(domain.ReviewSignal)
	if !ok {
		return fmt.Errorf("unexpected review signal payload %T", arg)
	}
	if signal.Decision.Approved != c.firstApproved {
		select {
		case <-c.firstPersisted:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	err := c.Client.SignalWorkflow(ctx, workflowID, runID, signalName, arg)
	if signal.Decision.Approved == c.firstApproved {
		c.firstOnce.Do(func() { close(c.firstPersisted) })
	}
	c.signalMu.Lock()
	c.signalCount++
	if c.signalCount == 2 {
		c.finishedOnce.Do(func() { close(c.signalsFinished) })
	}
	c.signalMu.Unlock()
	return err
}

type reviewHTTPResult struct {
	approved bool
	code     int
	body     string
	err      error
}

func invokeTemporalReviewHandler(
	ctx context.Context,
	workflowID string,
	handler echo.HandlerFunc,
	body reviewDecisionRequest,
	responses chan<- reviewHTTPResult,
) {
	encoded, err := json.Marshal(body)
	if err != nil {
		responses <- reviewHTTPResult{err: err}
		return
	}
	approved := body.Feedback == "approved in concurrent integration test"
	e := echo.New()
	request := httptest.NewRequest(http.MethodPost, "/workflows/"+workflowID, bytes.NewReader(encoded)).WithContext(ctx)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()
	echoCtx := e.NewContext(request, recorder)
	echoCtx.SetPath("/workflows/:id")
	echoCtx.SetParamNames("id")
	echoCtx.SetParamValues(workflowID)
	err = handler(echoCtx)
	responses <- reviewHTTPResult{
		approved: approved,
		code:     recorder.Code,
		body:     recorder.Body.String(),
		err:      err,
	}
}

func waitForReviewHTTPResult(
	t *testing.T,
	ctx context.Context,
	responses <-chan reviewHTTPResult,
) reviewHTTPResult {
	t.Helper()
	select {
	case result := <-responses:
		return result
	case <-ctx.Done():
		t.Fatalf("waiting for HTTP review result: %v", ctx.Err())
		return reviewHTTPResult{}
	}
}

func queryTemporalReviewStateAllowClosed(
	t *testing.T,
	ctx context.Context,
	temporalClient client.Client,
	workflowID string,
	runID string,
) domain.WorkflowStateQuery {
	t.Helper()
	response, err := temporalClient.QueryWorkflowWithOptions(ctx, &client.QueryWorkflowWithOptionsRequest{
		WorkflowID:           workflowID,
		RunID:                runID,
		QueryType:            domain.WorkflowStateQueryName,
		QueryRejectCondition: enums.QUERY_REJECT_CONDITION_NONE,
	})
	require.NoError(t, err)
	require.Nil(t, response.QueryRejected)
	require.NotNil(t, response.QueryResult)
	var state domain.WorkflowStateQuery
	require.NoError(t, response.QueryResult.Get(&state))
	return state
}

func waitForTestSignal(t *testing.T, ctx context.Context, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", description, ctx.Err())
	}
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
