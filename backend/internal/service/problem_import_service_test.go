package service

import (
	"context"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type importKeysFixture struct {
	ttl time.Duration
	key string
}

func (k *importKeysFixture) PutWithTTL(_ context.Context, key string, ttl time.Duration) (string, error) {
	k.ttl = ttl
	k.key = key
	return "runtime:synthetic", nil
}
func TestProblemImportKeyLifetimeCoversBoundedBatch(t *testing.T) {
	keys := &importKeysFixture{}
	ref, e := (importKeyLifetime{keys}).Put(context.Background(), "synthetic-key")
	require.NoError(t, e)
	require.Equal(t, "runtime:synthetic", ref)
	require.Equal(t, 24*time.Hour+5*time.Minute, keys.ttl)
}
func TestProblemImportStatusDeniesOtherOwnersBeforeQuery(t *testing.T) {
	raw := &ownershipTemporal{}
	owners := &memoryWorkflowOwners{owners: map[string]string{"problem-import-fixture": "owner"}}
	acl := NewWorkflowAccess(raw, owners)
	service := NewProblemImportService(raw, "fixture", nil, acl)
	_, e := service.Status(ownerContext("someone-else", "member"), "problem-import-fixture")
	require.ErrorIs(t, e, ErrNotFound)
	require.Zero(t, raw.describes)
	_, e = service.Status(context.Background(), "problem-import-fixture")
	require.ErrorIs(t, e, ErrNotFound)
	require.Zero(t, raw.describes)
}
func TestProblemImportStartRequiresIdentityAndReservesAuthenticatedOwner(t *testing.T) {
	raw := &ownershipTemporal{}
	owners := &memoryWorkflowOwners{owners: map[string]string{}}
	acl := NewWorkflowAccess(raw, owners)
	service := NewProblemImportService(NewOwnedWorkflowClient(raw, acl), "fixture", func(context.Context) (*domain.ProviderRuntimeConfig, error) {
		return &domain.ProviderRuntimeConfig{}, nil
	}, acl)
	req := domain.ProblemImportRequest{Mode: "preserve_statement", Items: []domain.SourceProblem{{ItemID: "one", Title: "T", Statement: "synthetic"}}}
	_, e := service.Start(context.Background(), req)
	require.ErrorIs(t, e, ErrWorkflowAccessDenied)
	require.Zero(t, raw.starts)
	id, e := service.Start(ownerContext("authenticated-user", "member"), req)
	require.NoError(t, e)
	require.Equal(t, "authenticated-user", owners.owners[id])
	require.Equal(t, 1, raw.starts)
}
