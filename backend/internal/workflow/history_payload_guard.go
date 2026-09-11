package workflow

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/protobuf/proto"
)

const DefaultMaxActivityResultBytes = 1 << 20

type historyPayloadGuard struct {
	interceptor.WorkerInterceptorBase
	maxBytes int
}

func NewHistoryPayloadGuard(maxBytes int) interceptor.WorkerInterceptor {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxActivityResultBytes
	}
	return &historyPayloadGuard{maxBytes: maxBytes}
}

func (g *historyPayloadGuard) InterceptActivity(
	_ context.Context,
	next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	return &historyPayloadActivityInterceptor{
		ActivityInboundInterceptorBase: interceptor.ActivityInboundInterceptorBase{Next: next},
		maxBytes:                       g.maxBytes,
	}
}

type historyPayloadActivityInterceptor struct {
	interceptor.ActivityInboundInterceptorBase
	maxBytes int
}

func (g *historyPayloadActivityInterceptor) ExecuteActivity(
	ctx context.Context,
	input *interceptor.ExecuteActivityInput,
) (interface{}, error) {
	result, err := g.Next.ExecuteActivity(ctx, input)
	if err != nil || result == nil {
		return result, err
	}
	size, err := temporalPayloadSize(result)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("activity result cannot be encoded for history: %v", err),
			"HistoryPayloadEncodingError",
			err,
		)
	}
	if size > g.maxBytes {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("activity result payload %d bytes exceeds history guard %d bytes; externalize it to CAS", size, g.maxBytes),
			"HistoryPayloadTooLarge",
			nil,
		)
	}
	return result, nil
}

func temporalPayloadSize(value interface{}) (int, error) {
	payload, err := converter.GetDefaultDataConverter().ToPayload(value)
	if err != nil {
		return 0, err
	}
	return proto.Size(payload), nil
}
