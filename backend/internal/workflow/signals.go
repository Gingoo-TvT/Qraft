package workflow

import (
	"fmt"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/workflow"
)

const (
	// ReviewSignalChannelName is the Temporal signal channel name used for
	// human review decisions. External clients send a ReviewSignal to this
	// channel to approve or reject a problem that is waiting for review.
	ReviewSignalChannelName = domain.ReviewSignalChannelName
)

// ReviewSignal remains an alias for callers that imported the workflow
// package before the signal contract moved to the shared domain package.
type ReviewSignal = domain.ReviewSignal

// waitForLegacyReviewSignal preserves the exact pre-token command order for
// DefaultVersion executions, including timer cancellation before validation.
func waitForLegacyReviewSignal(ctx workflow.Context, timeout time.Duration) (*domain.ReviewDecision, error) {
	logger := workflow.GetLogger(ctx)
	signalCh := workflow.GetSignalChannel(ctx, ReviewSignalChannelName)
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	timerFuture := workflow.NewTimer(timerCtx, timeout)
	selector := workflow.NewSelector(ctx)

	var decision *domain.ReviewDecision
	var signalReceived bool
	selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
		var signal ReviewSignal
		ch.Receive(ctx, &signal)
		decision = &signal.Decision
		signalReceived = true
		cancelTimer()
		logger.Info("received human review signal",
			"approved", signal.Decision.Approved,
			"feedback", signal.Decision.Feedback,
		)
	})
	selector.AddFuture(timerFuture, func(f workflow.Future) {
		logger.Warn("human review timed out", "timeout", timeout)
	})
	selector.Select(ctx)

	if !signalReceived {
		return nil, fmt.Errorf("human review timed out after %v", timeout)
	}
	if err := decision.Validate(); err != nil {
		return nil, fmt.Errorf("invalid review decision: %w", err)
	}
	return decision, nil
}

// waitForReviewSignal is the token-aware v1 wait path. Stale signals are
// ignored without resetting the original timeout.
func waitForReviewSignal(ctx workflow.Context, timeout time.Duration, expectedToken string) (*domain.ReviewDecision, error) {
	logger := workflow.GetLogger(ctx)

	signalCh := workflow.GetSignalChannel(ctx, ReviewSignalChannelName)

	// Create a timer for the review timeout.
	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	timerFuture := workflow.NewTimer(timerCtx, timeout)

	var decision *domain.ReviewDecision
	var validationErr error
	timedOut := false
	for decision == nil && !timedOut {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
			var signal ReviewSignal
			ch.Receive(ctx, &signal)
			if expectedToken != "" && signal.Token != expectedToken {
				logger.Warn("ignoring human review signal with stale token")
				return
			}
			if err := signal.Decision.Validate(); err != nil {
				validationErr = fmt.Errorf("invalid review decision: %w", err)
				return
			}
			accepted := signal.Decision
			decision = &accepted
			cancelTimer()
			logger.Info("received human review signal",
				"approved", signal.Decision.Approved,
				"feedback", signal.Decision.Feedback,
			)
		})
		selector.AddFuture(timerFuture, func(f workflow.Future) {
			timedOut = true
			logger.Warn("human review timed out", "timeout", timeout)
		})
		selector.Select(ctx)
		if validationErr != nil {
			return nil, validationErr
		}
	}

	if decision == nil {
		return nil, fmt.Errorf("human review timed out after %v", timeout)
	}

	return decision, nil
}
