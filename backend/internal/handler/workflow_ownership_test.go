package handler

import (
	"context"
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"net/http"
	"net/http/httptest"
	"testing"
)

type handlerWorkflowOwners map[string]string

func (m handlerWorkflowOwners) Owner(_ context.Context, id string) (string, error) { return m[id], nil }
func (m handlerWorkflowOwners) Reserve(_ context.Context, id, owner string) error {
	m[id] = owner
	return nil
}
func TestWorkflowEveryActionRejectsAnotherMembersTaskBeforeTemporal(t *testing.T) {
	h := NewWorkflowHandler(nil, "test")
	h.SetWorkflowAccess(service.NewWorkflowAccess(nil, handlerWorkflowOwners{"private": "member-a"}))
	for name, action := range map[string]echo.HandlerFunc{"get": h.HandleGet, "events": h.HandleEvents, "approve": h.HandleApprove, "reject": h.HandleReject, "retry": h.HandleRetry, "cancel": h.HandleCancel} {
		t.Run(name, func(t *testing.T) {
			e := echo.New()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/workflows/private/"+name, nil)
			req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "member-b", Role: "member"}))
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("private")
			err := action(c)
			require.Error(t, err)
			e.HTTPErrorHandler(err, c)
			require.Equal(t, http.StatusNotFound, rec.Code)
			require.NotContains(t, rec.Header().Get("Content-Type"), "event-stream")
			require.NotContains(t, rec.Body.String(), "member-a")
		})
	}
}

type pagedOwnershipTemporal struct {
	client.Client
	pages int
}

func (m *pagedOwnershipTemporal) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	m.pages++
	id := "task-b"
	var next []byte
	if len(req.NextPageToken) == 0 {
		next = []byte("next")
	} else {
		id = "task-a"
	}
	return &workflowservice.ListWorkflowExecutionsResponse{Executions: []*workflowpb.WorkflowExecutionInfo{{Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: "synthetic"}}}, NextPageToken: next}, nil
}
func TestWorkflowListDoesNotLeakOtherMembersAndWalksPages(t *testing.T) {
	temporal := &pagedOwnershipTemporal{}
	h := NewWorkflowHandler(temporal, "test")
	h.SetWorkflowAccess(service.NewWorkflowAccess(nil, handlerWorkflowOwners{"task-a": "a", "task-b": "b"}))
	e := echo.New()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/workflows?size=1", nil)
	req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "a", Role: "member"}))
	require.NoError(t, h.HandleList(e.NewContext(req, rec)))
	require.Equal(t, 2, temporal.pages)
	require.NotContains(t, rec.Body.String(), "task-b")
	var result struct{ Data []workflowSummary }
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(t, result.Data, 1)
	require.Equal(t, "task-a", result.Data[0].WorkflowID)
}
func TestGenerationScopeRequiresExplicitAuthentication(t *testing.T) {
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder())
	_, err := generationJobPrincipalScope(c)
	require.ErrorIs(t, err, errGenerationJobPrincipalRequired)
}
