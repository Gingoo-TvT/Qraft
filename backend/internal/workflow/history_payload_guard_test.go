package workflow

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/interceptor"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

type staticActivityInbound struct {
	interceptor.ActivityInboundInterceptorBase
	result interface{}
}

func (s *staticActivityInbound) ExecuteActivity(context.Context, *interceptor.ExecuteActivityInput) (interface{}, error) {
	return s.result, nil
}

func TestHistoryPayloadGuardRejectsOversizedActivityResult(t *testing.T) {
	next := &staticActivityInbound{result: strings.Repeat("x", 2048)}
	guard := NewHistoryPayloadGuard(1024)
	inbound := guard.InterceptActivity(context.Background(), next)
	if _, err := inbound.ExecuteActivity(context.Background(), &interceptor.ExecuteActivityInput{}); err == nil || !strings.Contains(err.Error(), "HistoryPayloadTooLarge") {
		t.Fatalf("oversized result error = %v", err)
	}

	next.result = "small"
	result, err := inbound.ExecuteActivity(context.Background(), &interceptor.ExecuteActivityInput{})
	if err != nil || result != "small" {
		t.Fatalf("small result = %v, err=%v", result, err)
	}
}

func TestStepOutputCompactionIsVersionGated(t *testing.T) {
	start := time.Unix(1, 0)
	large := strings.Repeat("x", maxStepOutputBytes+1)
	legacy := completedStepVersioned(sdkworkflow.DefaultVersion, domain.StepGenerateStatement, start, start.Add(time.Second), large)
	if legacy.Output != large {
		t.Fatal("legacy workflow output changed during replay")
	}
	versioned := completedStepVersioned(1, domain.StepGenerateStatement, start, start.Add(time.Second), large)
	compacted, ok := versioned.Output.(compactedStepOutput)
	if !ok || !compacted.Compacted || compacted.OriginalBytes <= maxStepOutputBytes || len(compacted.SHA256) != 64 {
		t.Fatalf("unexpected compacted output: %#v", versioned.Output)
	}
}
