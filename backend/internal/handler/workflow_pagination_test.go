package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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

type cursorTemporal struct {
	client.Client
	ids []string
}

func (m *cursorTemporal) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	offset := 0
	if len(req.NextPageToken) > 0 {
		fmt.Sscanf(string(req.NextPageToken), "%d", &offset)
	}
	end := offset + int(req.PageSize)
	if end > len(m.ids) {
		end = len(m.ids)
	}
	out := &workflowservice.ListWorkflowExecutionsResponse{}
	for _, id := range m.ids[offset:end] {
		out.Executions = append(out.Executions, &workflowpb.WorkflowExecutionInfo{Execution: &commonpb.WorkflowExecution{WorkflowId: id}})
	}
	if end < len(m.ids) {
		out.NextPageToken = []byte(fmt.Sprint(end))
	}
	return out, nil
}
func TestWorkflowCursorPaginationDoesNotLoseAuthorizedRows(t *testing.T) {
	tc := &cursorTemporal{ids: []string{"a", "private", "b", "c", "private2", "d", "e"}}
	h := NewWorkflowHandler(tc, "test")
	h.SetWorkflowAccess(service.NewWorkflowAccess(nil, handlerWorkflowOwners{"a": "me", "b": "me", "c": "me", "d": "me", "e": "me", "private": "other", "private2": "other"}))
	cursor := ""
	var all []string
	for page := 0; page < 3; page++ {
		req := httptest.NewRequest(http.MethodGet, "/workflows?size=2&cursor="+cursor, nil)
		req = req.WithContext(access.WithPrincipal(req.Context(), access.Principal{UserID: "me", Role: "member"}))
		rec := httptest.NewRecorder()
		require.NoError(t, h.HandleList(echo.New().NewContext(req, rec)))
		require.Equal(t, 200, rec.Code)
		var result struct {
			Data []workflowSummary
			Meta Meta
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
		for _, s := range result.Data {
			all = append(all, s.WorkflowID)
		}
		cursor = result.Meta.NextPageToken
		if page < 2 {
			require.NotEmpty(t, cursor)
			_, err := base64.RawURLEncoding.DecodeString(cursor)
			require.NoError(t, err)
		}
	}
	require.Empty(t, cursor)
	require.Equal(t, []string{"a", "b", "c", "d", "e"}, all)
}
func TestWorkflowRejectsMalformedCursorBeforeTemporal(t *testing.T) {
	h := NewWorkflowHandler(nil, "test")
	rec := httptest.NewRecorder()
	require.NoError(t, h.HandleList(echo.New().NewContext(httptest.NewRequest("GET", "/workflows?cursor=!!", nil), rec)))
	require.Equal(t, 400, rec.Code)
}
