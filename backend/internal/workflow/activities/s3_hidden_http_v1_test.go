package activities

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHiddenQualityClientResolvesOpaqueSuiteAndSendsMinimalCandidateCAS(t *testing.T) {
	var executeBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case s3HiddenHTTPResolvePathV1:
			if r.Header.Get("Authorization") != "Bearer worker-secret" {
				t.Errorf("resolve authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(HiddenSuiteRefV1{SchemaVersion: S3HiddenSuiteSchemaV1, SuiteID: "suite-prod:v1", RevisionSHA256: strings.Repeat("a", 64)})
		case s3HiddenHTTPExecutePathV1:
			body, _ := io.ReadAll(r.Body)
			executeBody = string(body)
			_ = json.NewEncoder(w).Encode(HiddenRegressionExecutionResponseV1{SchemaVersion: S3HiddenExecutorResponseSchemaV1, Passed: true, ExecutedCount: 17})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewHTTPHiddenQualityClientV1(server.URL, "worker-secret", time.Second, true)
	if err != nil {
		t.Fatalf("new hidden client: %v", err)
	}
	suite, err := client.ResolveHiddenSuiteV1(context.Background(), HiddenSuiteResolutionRequestV1{SchemaVersion: S3HiddenSuiteSchemaV1, SubjectID: "subject-20", SemanticSpecSHA256: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatalf("resolve suite: %v", err)
	}
	sha := strings.Repeat("c", 64)
	artifact := ArtifactRef{SchemaVersion: ArtifactRefSchemaVersion, PayloadVersion: ActivityPayloadVersion, Bucket: "artifacts", Key: artifactKey(sha), SHA256: sha, SizeBytes: 17, ContentType: "text/plain", Producer: "GenerateMainSolutionActivityV1", Provider: "provider", Model: "model", ModelRevision: "revision", WorkflowID: "workflow"}
	artifact.LLMCallReceipt = &LLMCallReceipt{SchemaVersion: 1, RequestedModel: "secret-model", ReturnedModel: "secret-returned", Provider: "secret-provider", EndpointID: "secret-endpoint", PromptHash: strings.Repeat("d", 64), RequestSHA256: strings.Repeat("e", 64)}
	response, err := client.ExecuteHiddenRegressionV1(context.Background(), HiddenRegressionExecutionRequestV1{SchemaVersion: S3HiddenSuiteSchemaV1, Suite: suite, CandidateArtifact: artifact})
	if err != nil {
		t.Fatalf("execute hidden: %v", err)
	}
	if !response.Passed || response.ExecutedCount != 17 {
		t.Fatalf("hidden response = %+v", response)
	}
	for _, forbidden := range []string{"llm_call_receipt", "prompt_hash", "secret-model", "secret-provider", "statement", "hidden_seed", "hidden_input"} {
		if strings.Contains(executeBody, forbidden) {
			t.Fatalf("execute request exposed %q: %s", forbidden, executeBody)
		}
	}
	for _, required := range []string{`"suite_id":"suite-prod:v1"`, `"sha256":"` + sha + `"`, `"bucket":"artifacts"`} {
		if !strings.Contains(executeBody, required) {
			t.Fatalf("execute request missing %s: %s", required, executeBody)
		}
	}
}

func TestHTTPHiddenQualityClientRejectsUnsafeConfigurationAndNonMinimalResponse(t *testing.T) {
	for _, input := range []struct {
		url       string
		token     string
		allowHTTP bool
	}{
		{url: "http://runner.example", allowHTTP: false},
		{url: "https://user:pass@runner.example", allowHTTP: false},
		{url: "https://runner.example?secret=x", allowHTTP: false},
		{url: "https://runner.example", token: "bad\r\ntoken", allowHTTP: false},
	} {
		if _, err := NewHTTPHiddenQualityClientV1(input.url, input.token, time.Second, input.allowHTTP); err == nil {
			t.Fatalf("unsafe hidden configuration accepted: %+v", input)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":"algoforge.hidden-suite-ref.v1","suite_id":"suite","revision_sha256":"` + strings.Repeat("a", 64) + `","hidden_seed":"leak"}`))
	}))
	defer server.Close()
	client, err := NewHTTPHiddenQualityClientV1(server.URL, "", time.Second, true)
	if err != nil {
		t.Fatalf("new hidden client: %v", err)
	}
	if _, err := client.ResolveHiddenSuiteV1(context.Background(), HiddenSuiteResolutionRequestV1{SchemaVersion: S3HiddenSuiteSchemaV1, SubjectID: "subject", SemanticSpecSHA256: strings.Repeat("b", 64)}); err == nil {
		t.Fatal("hidden registry response with secret field was accepted")
	}
}
