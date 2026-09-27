package service

import (
	"context"
	"errors"
	"github.com/Gingoo-TvT/Qraft/backend/internal/access"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"testing"
)

type memoryWorkflowOwners struct {
	owners  map[string]string
	failure error
}

func (m *memoryWorkflowOwners) Owner(_ context.Context, id string) (string, error) {
	return m.owners[id], m.failure
}
func (m *memoryWorkflowOwners) Reserve(_ context.Context, id, owner string) error {
	if m.failure != nil {
		return m.failure
	}
	if old := m.owners[id]; old != "" && old != owner {
		return errors.New("immutable owner")
	}
	m.owners[id] = owner
	return nil
}

type ownershipTemporal struct {
	client.Client
	parents   map[string]string
	existing  map[string]bool
	starts    int
	onStart   func()
	startErr  error
	describes int
}

func (m *ownershipTemporal) DescribeWorkflowExecution(_ context.Context, id, run string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	m.describes++
	parent, child := m.parents[id]
	if !child && !m.existing[id] {
		return nil, serviceerror.NewNotFound("absent")
	}
	info := &workflowpb.WorkflowExecutionInfo{Execution: &commonpb.WorkflowExecution{WorkflowId: id}}
	if child {
		info.ParentExecution = &commonpb.WorkflowExecution{WorkflowId: parent}
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: info}, nil
}
func (m *ownershipTemporal) ExecuteWorkflow(_ context.Context, _ client.StartWorkflowOptions, _ interface{}, _ ...interface{}) (client.WorkflowRun, error) {
	m.starts++
	if m.onStart != nil {
		m.onStart()
	}
	return nil, m.startErr
}
func ownerContext(id, role string) context.Context {
	return access.WithPrincipal(context.Background(), access.Principal{UserID: id, Role: role})
}

func TestWorkflowAccessSeparatesMembersAndInheritsOnlyRealParents(t *testing.T) {
	store := &memoryWorkflowOwners{owners: map[string]string{"root-a": "a", "root-b": "b"}}
	temporal := &ownershipTemporal{parents: map[string]string{"child": "root-a", "grandchild": "child", "cycle-a": "cycle-b", "cycle-b": "cycle-a"}, existing: map[string]bool{"historical": true, "root-a-forged-child": true}}
	acl := NewWorkflowAccess(temporal, store)
	for _, tc := range []struct {
		id, user, role string
		want           bool
	}{
		{"root-a", "a", "member", true}, {"root-a", "b", "member", false},
		{"root-b", "a", "member", false}, {"child", "a", "member", true},
		{"grandchild", "a", "member", true}, {"grandchild", "b", "member", false},
		{"historical", "a", "member", false}, {"historical", "admin", "admin", true},
		{"root-a-forged-child", "a", "member", false}, {"cycle-a", "a", "member", false},
	} {
		t.Run(tc.id+tc.user, func(t *testing.T) {
			got, err := acl.CanAccess(ownerContext(tc.user, tc.role), tc.id)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	require.Equal(t, "a", store.owners["grandchild"])
	temporal.parents = nil
	allowed, err := acl.CanAccess(ownerContext("a", "member"), "grandchild")
	require.NoError(t, err)
	require.True(t, allowed, "saved child owner survives Temporal retention")
	allowed, err = acl.CanAccess(context.Background(), "root-a")
	require.NoError(t, err)
	require.False(t, allowed)
	store.failure = errors.New("database unavailable")
	allowed, err = acl.CanAccess(ownerContext("a", "member"), "root-a")
	require.Error(t, err)
	require.False(t, allowed)
}

func TestOwnedWorkflowStartReservesBeforeTemporalAndKeepsOwnerOnUncertainty(t *testing.T) {
	store := &memoryWorkflowOwners{owners: map[string]string{}}
	temporal := &ownershipTemporal{startErr: errors.New("uncertain timeout")}
	temporal.onStart = func() { require.Equal(t, "a", store.owners["new-task"]) }
	secured := NewOwnedWorkflowClient(temporal, NewWorkflowAccess(temporal, store))
	opts := client.StartWorkflowOptions{ID: "new-task"}
	_, err := secured.ExecuteWorkflow(ownerContext("a", "member"), opts, "test")
	require.Error(t, err)
	require.Equal(t, 1, temporal.starts)
	_, err = secured.ExecuteWorkflow(ownerContext("b", "member"), opts, "test")
	require.ErrorIs(t, err, ErrWorkflowAccessDenied)
	require.Equal(t, 1, temporal.starts)
	_, err = secured.ExecuteWorkflow(ownerContext("admin", "admin"), opts, "test")
	require.Error(t, err)
	require.Equal(t, 2, temporal.starts)
	require.Equal(t, "a", store.owners["new-task"])
	_, err = secured.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{ID: "no-identity"}, "test")
	require.ErrorIs(t, err, ErrWorkflowAccessDenied)
	require.NotContains(t, store.owners, "no-identity")
}

func TestOwnedWorkflowStartCannotClaimHistoricalTask(t *testing.T) {
	store := &memoryWorkflowOwners{owners: map[string]string{}}
	temporal := &ownershipTemporal{existing: map[string]bool{"historical": true}}
	secured := NewOwnedWorkflowClient(temporal, NewWorkflowAccess(temporal, store))
	_, err := secured.ExecuteWorkflow(ownerContext("a", "member"), client.StartWorkflowOptions{ID: "historical"}, "test")
	require.ErrorIs(t, err, ErrWorkflowAccessDenied)
	require.Empty(t, store.owners)
	require.Zero(t, temporal.starts)
	_, err = secured.ExecuteWorkflow(ownerContext("admin", "admin"), client.StartWorkflowOptions{ID: "historical"}, "test")
	require.NoError(t, err)
	require.Empty(t, store.owners)
	require.Equal(t, 1, temporal.starts)
}
func TestPrivateProblemReadRequiresOwningTask(t *testing.T) {
	store := &memoryWorkflowOwners{owners: map[string]string{"a-task": "a"}}
	svc := &ProblemService{permissions: NewWorkflowAccess(nil, store)}
	id := "a-task"
	problem := &domain.Problem{WorkflowID: &id, Status: domain.ProblemStatusDraft}
	require.NoError(t, svc.authorizeProblem(ownerContext("a", "member"), problem))
	require.ErrorIs(t, svc.authorizeProblem(ownerContext("b", "member"), problem), ErrNotFound)
	require.NoError(t, svc.authorizeProblem(ownerContext("admin", "admin"), problem))
	require.ErrorIs(t, svc.authorizeProblem(context.Background(), problem), ErrNotFound)
	problem.WorkflowID = nil
	require.ErrorIs(t, svc.authorizeProblem(ownerContext("a", "member"), problem), ErrNotFound)
	problem.Status = domain.ProblemStatusPublished
	require.NoError(t, svc.authorizeProblem(ownerContext("b", "member"), problem))
}
func TestSetOwnershipSeparatesPrivateEditingAndSharedReading(t *testing.T) {
	svc := &ProblemSetService{permissions: NewWorkflowAccess(nil, &memoryWorkflowOwners{owners: map[string]string{}})}
	set := &domain.ProblemSet{OwnerUserID: "a", Visibility: domain.ProblemSetVisibilityPrivate}
	for _, write := range []bool{false, true} {
		require.NoError(t, svc.authorizeSet(ownerContext("a", "member"), set, write))
		require.ErrorIs(t, svc.authorizeSet(ownerContext("b", "member"), set, write), ErrNotFound)
		require.NoError(t, svc.authorizeSet(ownerContext("admin", "admin"), set, write))
		require.ErrorIs(t, svc.authorizeSet(context.Background(), set, write), ErrNotFound)
	}
	set.Visibility = domain.ProblemSetVisibilityPublic
	require.NoError(t, svc.authorizeSet(ownerContext("b", "member"), set, false))
	require.ErrorIs(t, svc.authorizeSet(ownerContext("a", "member"), set, true), ErrNotFound)
	set.Generation = &domain.ProblemSetGenerationState{ID: "private-task"}
	svc.redactSetTask(ownerContext("b", "member"), set)
	require.Nil(t, set.Generation)
}
