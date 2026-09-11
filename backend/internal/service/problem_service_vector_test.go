package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

func TestProblemSimilarityDoesNotFallBackToActivePointerWithoutConfiguration(t *testing.T) {
	runtimeVectorRepo := repository.NewVectorRepository(nil).RequireStatementModelVersion()
	service := NewProblemService(nil, nil, nil, runtimeVectorRepo, nil, "")

	_, _, err := service.FindSimilarProblems(context.Background(), []float32{1}, 10, 0.7)
	if !errors.Is(err, repository.ErrConfiguredStatementModelVersionRequired) {
		t.Fatalf("unconfigured similarity error = %v", err)
	}
}
