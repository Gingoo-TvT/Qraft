package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSandboxBusyWaitsForCapacity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"version":"v1","error":{"code":"sandbox_busy","message":"sandbox concurrency limit reached"}}`)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	c, _ := NewHTTPClient(server.URL, time.Second)
	var result struct {
		OK bool `json:"ok"`
	}
	if err := c.post(context.Background(), "/v1/execute", struct{}{}, &result); err != nil || !result.OK || calls != 2 {
		t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
	}
}
func TestSandboxBusyWaitRespectsCancellation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		fmt.Fprint(w, `{"version":"v1","error":{"code":"sandbox_busy","message":"busy"}}`)
	}))
	defer server.Close()
	c, _ := NewHTTPClient(server.URL, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	var result struct{}
	if err := c.post(ctx, "/v1/execute", struct{}{}, &result); !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
