package workflow

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"go.temporal.io/sdk/temporal"
)

func TestTestDataFailureCanReuseStatementV1OnlyForCandidateDefects(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "invalid artifact",
			err: temporal.NewNonRetryableApplicationError(
				"generator batch 7 compilation failed", "InvalidGeneratedTestData", nil,
			),
			want: true,
		},
		{
			name: "quality gate",
			err: temporal.NewNonRetryableApplicationError(
				"generated 0 sample tests", "QualityNotMet", nil,
			),
			want: true,
		},
		{
			name: "provider lease",
			err:  temporal.NewApplicationError("lease is active", "ProviderEffectBusy"),
			want: false,
		},
		{
			name: "transport timeout",
			err:  errors.New("calling provider: context deadline exceeded"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := testDataFailureCanReuseStatementV1(tt.err); got != tt.want {
				t.Fatalf("testDataFailureCanReuseStatementV1(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestProblemGenerationTestDataParamsWithRetryFeedbackV1CarriesExactDiagnostic(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.CustomPrompt = "Keep the requested difficulty."
	feedback := newStatementRetryFeedbackV1(
		2,
		"broken candidate",
		"generate_testdata",
		"generator batch 1/8 compilation failed: stray '\\\\n' in C++ source",
		"",
		nil,
	)

	got := problemGenerationTestDataParamsWithRetryFeedbackV1(params, feedback)
	if !reflect.DeepEqual(got.TestDataConfig, params.TestDataConfig) || got.TimeLimit != params.TimeLimit || got.MemoryLimit != params.MemoryLimit {
		t.Fatalf("retry feedback changed unrelated generation parameters: got=%+v want=%+v", got, params)
	}
	if !strings.Contains(got.CustomPrompt, "Keep the requested difficulty.") ||
		!strings.Contains(got.CustomPrompt, testDataRetryFeedbackMarkerV1) ||
		!strings.Contains(got.CustomPrompt, "stray") ||
		!strings.Contains(got.CustomPrompt, "broken candidate") {
		t.Fatalf("generator diagnostic context was not carried: %q", got.CustomPrompt)
	}
	if strings.Contains(got.CustomPrompt, "all solution") {
		t.Fatalf("unrelated solution context leaked into generator prompt: %q", got.CustomPrompt)
	}
}

func TestProblemGenerationTestDataParamsWithRetryFeedbackV1IgnoresUnrelatedFailure(t *testing.T) {
	params := domain.DefaultProblemGenParams()
	params.CustomPrompt = "base"
	feedback := &activities.StatementRetryFeedbackV1{
		SchemaVersion:    activities.GenerationRetryFeedbackSchemaVersionV1,
		StatementAttempt: 1,
		FailureStage:     "solution_pipeline",
		Diagnostic:       "main solution mismatch",
	}
	got := problemGenerationTestDataParamsWithRetryFeedbackV1(params, feedback)
	if got.CustomPrompt != params.CustomPrompt {
		t.Fatalf("unrelated failure was appended to generator prompt: %q", got.CustomPrompt)
	}
}
