package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSandboxInputCapacityContract(t *testing.T) {
	baseURL := contractURL(t)
	source := "int main(void){return 0;}"
	limits := executionLimits{
		TimeLimitMS: 2000, MemoryLimitMB: 128, OutputLimitBytes: 64 << 10, MaxProcesses: 32,
	}
	exactCase := strings.Repeat("x", maxInputBytes)
	response := contractExecute(t, baseURL, "c", source, []string{exactCase}, limits)
	if !response.Compile.Success || len(response.Results) != 1 || response.Results[0].Verdict != verdictOK {
		t.Fatalf("exact 8 MiB input failed: %+v", response)
	}

	exactBatch := []string{exactCase, exactCase, exactCase, exactCase}
	response = contractExecute(t, baseURL, "c", source, exactBatch, limits)
	if !response.Compile.Success || len(response.Results) != 4 {
		t.Fatalf("exact 32 MiB batch failed: %+v", response)
	}

	contractExpectRequestTooLarge(t, baseURL, executeRequest{
		Version: apiVersion, Language: "c", Source: source,
		Inputs: []string{exactCase + "x"}, Limits: limits,
	})
	contractExpectRequestTooLarge(t, baseURL, executeRequest{
		Version: apiVersion, Language: "c", Source: source,
		Inputs: append(exactBatch, "x"), Limits: limits,
	})
	t.Log("real HTTP service accepted exact 8/32 MiB inputs and rejected both +1 byte boundaries")
}

func contractExpectRequestTooLarge(t *testing.T, baseURL string, request executeRequest) {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/execute", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Minute, Transport: &http.Transport{Proxy: nil}}).Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(string(data), `"code":"invalid_request"`) {
		t.Fatalf("capacity +1 response HTTP %d: %s", response.StatusCode, data)
	}
}
