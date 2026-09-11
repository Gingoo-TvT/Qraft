package outbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/repository"
)

type HTTPPublisherOptions struct {
	Endpoint         string
	BearerToken      string
	HMACSecret       string
	AllowHTTP        bool
	ConnectTimeout   time.Duration
	RequestTimeout   time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
}

type HTTPPublisher struct {
	endpoint         string
	bearerToken      string
	hmacSecret       []byte
	maxRequestBytes  int64
	maxResponseBytes int64
	client           *http.Client
}

type deliveryEnvelope struct {
	SchemaVersion string          `json:"schema_version"`
	EventID       string          `json:"event_id"`
	OperationKey  string          `json:"operation_key"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   string          `json:"aggregate_id"`
	Attempt       int             `json:"attempt"`
	Payload       json.RawMessage `json:"payload"`
}

func NewHTTPPublisher(options HTTPPublisherOptions) (*HTTPPublisher, error) {
	endpoint, err := url.ParseRequestURI(strings.TrimSpace(options.Endpoint))
	if err != nil || endpoint.Host == "" {
		return nil, fmt.Errorf("outbox endpoint must be an absolute URL")
	}
	if endpoint.Scheme != "https" && !(options.AllowHTTP && endpoint.Scheme == "http") {
		return nil, fmt.Errorf("outbox endpoint must use HTTPS")
	}
	if endpoint.User != nil || endpoint.Fragment != "" {
		return nil, fmt.Errorf("outbox endpoint must not contain user info or a fragment")
	}
	if options.ConnectTimeout <= 0 || options.RequestTimeout <= 0 || options.ConnectTimeout > options.RequestTimeout {
		return nil, fmt.Errorf("invalid outbox HTTP timeouts")
	}
	if options.MaxRequestBytes <= 0 || options.MaxResponseBytes <= 0 {
		return nil, fmt.Errorf("outbox HTTP size limits must be positive")
	}
	if len(options.HMACSecret) < 32 {
		return nil, fmt.Errorf("outbox HMAC secret must contain at least 32 bytes")
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: options.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   options.ConnectTimeout,
		ResponseHeaderTimeout: options.RequestTimeout,
		ExpectContinueTimeout: time.Second,
	}
	return &HTTPPublisher{
		endpoint:         endpoint.String(),
		bearerToken:      strings.TrimSpace(options.BearerToken),
		hmacSecret:       []byte(options.HMACSecret),
		maxRequestBytes:  options.MaxRequestBytes,
		maxResponseBytes: options.MaxResponseBytes,
		client: &http.Client{
			Transport: transport,
			Timeout:   options.RequestTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (p *HTTPPublisher) Publish(ctx context.Context, event repository.OutboxEvent) error {
	if len(event.PayloadJSON) == 0 || !json.Valid(event.PayloadJSON) {
		return fmt.Errorf("outbox event %s has invalid payload JSON", event.EventID)
	}
	body, err := json.Marshal(deliveryEnvelope{
		SchemaVersion: "algoforge.outbox/v1",
		EventID:       event.EventID.String(),
		OperationKey:  event.OperationKey,
		EventType:     event.EventType,
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		Attempt:       event.AttemptCount,
		Payload:       event.PayloadJSON,
	})
	if err != nil {
		return fmt.Errorf("encode outbox event %s: %w", event.EventID, err)
	}
	if int64(len(body)) > p.maxRequestBytes {
		return fmt.Errorf("outbox event %s envelope is %d bytes, limit is %d", event.EventID, len(body), p.maxRequestBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create outbox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "algoforge-outbox/1")
	req.Header.Set("Idempotency-Key", event.EventID.String())
	req.Header.Set("X-Algoforge-Event-ID", event.EventID.String())
	req.Header.Set("X-Algoforge-Event-Type", event.EventType)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	req.Header.Set("X-Algoforge-Timestamp", timestamp)
	req.Header.Set("X-Algoforge-Signature", signDelivery(p.hmacSecret, timestamp, event.EventID.String(), body))
	if p.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.bearerToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("deliver outbox event %s: %w", event.EventID, err)
	}
	defer resp.Body.Close()

	limited := &io.LimitedReader{R: resp.Body, N: p.maxResponseBytes + 1}
	responseBody, readErr := io.ReadAll(limited)
	if readErr != nil {
		return fmt.Errorf("read outbox response for event %s: %w", event.EventID, readErr)
	}
	if int64(len(responseBody)) > p.maxResponseBytes {
		return fmt.Errorf("outbox response for event %s exceeded %d bytes", event.EventID, p.maxResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		digest := sha256.Sum256(responseBody)
		return fmt.Errorf(
			"outbox endpoint returned %d for event %s (response_bytes=%d response_sha256=%s)",
			resp.StatusCode, event.EventID, len(responseBody), hex.EncodeToString(digest[:]),
		)
	}
	return nil
}

// signDelivery binds freshness, identity, and the exact envelope bytes. The
// receiver should enforce a bounded timestamp skew and deduplicate EventID.
func signDelivery(secret []byte, timestamp, eventID string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = io.WriteString(mac, timestamp)
	_, _ = io.WriteString(mac, "\n")
	_, _ = io.WriteString(mac, eventID)
	_, _ = io.WriteString(mac, "\n")
	_, _ = mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
