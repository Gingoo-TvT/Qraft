package activities

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

// heartbeatWhile starts a background goroutine that sends Temporal heartbeats
// every interval with the given message. It returns a cancel function that
// must be called when the long-running operation completes.
func heartbeatWhile(ctx context.Context, msg string, interval time.Duration) context.CancelFunc {
	hbCtx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, msg)
			}
		}
	}()
	return cancel
}
