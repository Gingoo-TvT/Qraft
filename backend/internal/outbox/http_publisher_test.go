package outbox

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
	"github.com/google/uuid"
)

func testEvent() repository.OutboxEvent {
	return repository.OutboxEvent{
		EventID:       uuid.New(),
		OperationKey:  "workflow/1/store",
		EventType:     "problem.published.v1",
		AggregateType: "problem",
		AggregateID:   "problem-1",
		PayloadJSON:   json.RawMessage(`{"problem_id":"problem-1"}`),
		AttemptCount:  2,
	}
}

func testHTTPPublisher(t *testing.T, endpoint string, mutate func(*HTTPPublisherOptions)) *HTTPPublisher {
	t.Helper()
	options := HTTPPublisherOptions{
		Endpoint:         endpoint,
		BearerToken:      "secret-token",
		HMACSecret:       strings.Repeat("s", 32),
		AllowHTTP:        true,
		ConnectTimeout:   time.Second,
		RequestTimeout:   time.Second,
		MaxRequestBytes:  4096,
		MaxResponseBytes: 1024,
	}
	if mutate != nil {
		mutate(&options)
	}
	publisher, err := NewHTTPPublisher(options)
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	return publisher
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func publisherResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestHTTPPublisherUsesFixedEnvelopeAndIdempotencyHeaders(t *testing.T) {
	event := testEvent()
	publisher := testHTTPPublisher(t, "https://events.example.test/fixed-destination", nil)
	publisher.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/fixed-destination" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Idempotency-Key"); got != event.EventID.String() {
			t.Errorf("idempotency key = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("X-Algoforge-Event-ID"); got != event.EventID.String() {
			t.Errorf("event ID header = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		var envelope deliveryEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("decode envelope: %v", err)
		}
		if envelope.EventID != event.EventID.String() || envelope.EventType != event.EventType || envelope.Attempt != 2 {
			t.Errorf("unexpected envelope: %+v", envelope)
		}
		if string(envelope.Payload) != string(event.PayloadJSON) {
			t.Errorf("payload = %s", envelope.Payload)
		}
		timestamp := r.Header.Get("X-Algoforge-Timestamp")
		if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
			t.Errorf("timestamp header = %q: %v", timestamp, err)
		}
		mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
		_, _ = mac.Write([]byte(timestamp + "\n" + event.EventID.String() + "\n"))
		_, _ = mac.Write(body)
		expectedSignature := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-Algoforge-Signature"); !hmac.Equal([]byte(got), []byte(expectedSignature)) {
			t.Errorf("signature = %q, want %q", got, expectedSignature)
		}
		return publisherResponse(http.StatusNoContent, ""), nil
	})

	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestHTTPPublisherRejectsNonSuccessRedirectAndOversizedResponse(t *testing.T) {
	event := testEvent()
	tests := []struct {
		name       string
		status     int
		body       string
		maxResp    int64
		wantDetail string
		location   string
	}{
		{
			name:       "non-success",
			status:     http.StatusServiceUnavailable,
			body:       "private-response-token",
			maxResp:    1024,
			wantDetail: "returned 503",
		},
		{
			name:       "redirect is not followed",
			status:     http.StatusTemporaryRedirect,
			location:   "https://other.example.test/elsewhere",
			maxResp:    1024,
			wantDetail: "returned 307",
		},
		{
			name:       "oversized response",
			status:     http.StatusOK,
			body:       strings.Repeat("x", 32),
			maxResp:    8,
			wantDetail: "exceeded 8 bytes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := testHTTPPublisher(t, "https://events.example.test/deliver", func(options *HTTPPublisherOptions) {
				options.MaxResponseBytes = tt.maxResp
			})
			calls := 0
			publisher.client.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				calls++
				response := publisherResponse(tt.status, tt.body)
				if tt.location != "" {
					response.Header.Set("Location", tt.location)
				}
				return response, nil
			})
			err := publisher.Publish(context.Background(), event)
			if err == nil || !strings.Contains(err.Error(), tt.wantDetail) {
				t.Fatalf("publish error = %v, want %q", err, tt.wantDetail)
			}
			if strings.Contains(err.Error(), "private-response-token") {
				t.Fatalf("response body leaked in error: %v", err)
			}
			if calls != 1 {
				t.Fatalf("transport calls = %d, want 1", calls)
			}
		})
	}
}

func TestHTTPPublisherBoundsRequestsAndTimeouts(t *testing.T) {
	event := testEvent()

	tooSmall := testHTTPPublisher(t, "https://events.example.test/deliver", func(options *HTTPPublisherOptions) {
		options.MaxRequestBytes = 8
	})
	if err := tooSmall.Publish(context.Background(), event); err == nil || !strings.Contains(err.Error(), "envelope is") {
		t.Fatalf("request limit error = %v", err)
	}

	timed := testHTTPPublisher(t, "https://events.example.test/deliver", func(options *HTTPPublisherOptions) {
		options.ConnectTimeout = 10 * time.Millisecond
		options.RequestTimeout = 20 * time.Millisecond
	})
	timed.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	if err := timed.Publish(context.Background(), event); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestHTTPPublisherRejectsInsecureEndpointByDefault(t *testing.T) {
	_, err := NewHTTPPublisher(HTTPPublisherOptions{
		Endpoint:         "http://events.example.test",
		ConnectTimeout:   time.Second,
		RequestTimeout:   time.Second,
		MaxRequestBytes:  1024,
		MaxResponseBytes: 1024,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("insecure endpoint error = %v", err)
	}
}
