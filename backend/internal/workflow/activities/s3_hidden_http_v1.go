package activities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	s3HiddenHTTPResolvePathV1   = "/v1/resolve-suite"
	s3HiddenHTTPExecutePathV1   = "/v1/execute"
	s3HiddenHTTPMaxRequestV1    = 32 << 10
	s3HiddenHTTPMaxResponseV1   = 32 << 10
	s3HiddenHTTPExecuteSchemaV1 = "algoforge.hidden-executor-request.v1"
)

// HTTPHiddenQualityClientV1 is the production adapter for a separately
// deployed hidden-suite registry/runner. Only opaque suite identity and a
// minimal candidate CAS locator cross the boundary; hidden inputs and seeds
// never enter this process or Temporal history.
type HTTPHiddenQualityClientV1 struct {
	baseURL     *url.URL
	bearerToken string
	client      *http.Client
}

func NewHTTPHiddenQualityClientV1(rawURL, bearerToken string, timeout time.Duration, allowHTTP bool) (*HTTPHiddenQualityClientV1, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("hidden quality HTTP timeout must be positive")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("hidden quality URL must be an absolute credential-free endpoint")
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return nil, fmt.Errorf("hidden quality URL requires HTTPS")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	if strings.ContainsAny(bearerToken, "\r\n") || len(bearerToken) > 4096 {
		return nil, fmt.Errorf("hidden quality bearer token is invalid")
	}
	return &HTTPHiddenQualityClientV1{
		baseURL: parsed, bearerToken: bearerToken,
		client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *HTTPHiddenQualityClientV1) ResolveHiddenSuiteV1(ctx context.Context, in HiddenSuiteResolutionRequestV1) (HiddenSuiteRefV1, error) {
	if c == nil || c.baseURL == nil || in.SchemaVersion != S3HiddenSuiteSchemaV1 || !safeOpaqueS3TokenV1(in.SubjectID, 128) || !isManifestSHA256(in.SemanticSpecSHA256) {
		return HiddenSuiteRefV1{}, fmt.Errorf("invalid hidden-suite resolution request")
	}
	var out HiddenSuiteRefV1
	if err := c.postJSON(ctx, s3HiddenHTTPResolvePathV1, in, &out); err != nil {
		return HiddenSuiteRefV1{}, err
	}
	if out.SchemaVersion != S3HiddenSuiteSchemaV1 || !safeOpaqueS3TokenV1(out.SuiteID, 128) || !isManifestSHA256(out.RevisionSHA256) {
		return HiddenSuiteRefV1{}, fmt.Errorf("hidden registry returned an invalid opaque suite reference")
	}
	return out, nil
}

type s3HiddenHTTPCandidateRefV1 struct {
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}

type s3HiddenHTTPExecuteRequestV1 struct {
	SchemaVersion string                     `json:"schema_version"`
	Suite         HiddenSuiteRefV1           `json:"suite"`
	Candidate     s3HiddenHTTPCandidateRefV1 `json:"candidate"`
}

func (c *HTTPHiddenQualityClientV1) ExecuteHiddenRegressionV1(ctx context.Context, in HiddenRegressionExecutionRequestV1) (HiddenRegressionExecutionResponseV1, error) {
	if c == nil || c.baseURL == nil || in.SchemaVersion != S3HiddenSuiteSchemaV1 ||
		in.Suite.SchemaVersion != S3HiddenSuiteSchemaV1 || !safeOpaqueS3TokenV1(in.Suite.SuiteID, 128) || !isManifestSHA256(in.Suite.RevisionSHA256) {
		return HiddenRegressionExecutionResponseV1{}, fmt.Errorf("invalid hidden execution request")
	}
	if err := in.CandidateArtifact.Validate(in.CandidateArtifact.Bucket); err != nil {
		return HiddenRegressionExecutionResponseV1{}, fmt.Errorf("invalid hidden candidate CAS: %w", err)
	}
	request := s3HiddenHTTPExecuteRequestV1{
		SchemaVersion: s3HiddenHTTPExecuteSchemaV1, Suite: in.Suite,
		Candidate: s3HiddenHTTPCandidateRefV1{Bucket: in.CandidateArtifact.Bucket, Key: in.CandidateArtifact.Key, SHA256: in.CandidateArtifact.SHA256, SizeBytes: in.CandidateArtifact.SizeBytes, ContentType: in.CandidateArtifact.ContentType},
	}
	var out HiddenRegressionExecutionResponseV1
	if err := c.postJSON(ctx, s3HiddenHTTPExecutePathV1, request, &out); err != nil {
		return HiddenRegressionExecutionResponseV1{}, err
	}
	return out, nil
}

func (c *HTTPHiddenQualityClientV1) postJSON(ctx context.Context, path string, input, output interface{}) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal hidden quality request: %w", err)
	}
	if len(body) > s3HiddenHTTPMaxRequestV1 {
		return fmt.Errorf("hidden quality request exceeds %d bytes", s3HiddenHTTPMaxRequestV1)
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create hidden quality request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("execute hidden quality request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("hidden quality service returned HTTP %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return fmt.Errorf("hidden quality service returned a non-JSON response")
	}
	limited := io.LimitReader(response.Body, s3HiddenHTTPMaxResponseV1+1)
	responseBytes, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read hidden quality response: %w", err)
	}
	if len(responseBytes) == 0 || len(responseBytes) > s3HiddenHTTPMaxResponseV1 {
		return fmt.Errorf("hidden quality response is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("decode strict hidden quality response: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode strict hidden quality response: %w", err)
	}
	return nil
}
