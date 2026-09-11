package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolDescriptionAndStrictRequests(t *testing.T) {
	var object map[string]any
	if err := json.Unmarshal([]byte(description), &object); err != nil {
		t.Fatal(err)
	}
	tools := object["tools"].([]any)
	if len(tools) != 3 {
		t.Fatal("missing tool descriptions")
	}
	for _, raw := range []string{
		"{}",
		"{\"tool\":\"unknown\"}",
		"{\"tool\":\"build_generator\",\"unexpected\":true}",
		"{\"tool\":\"build_generator\"} {}",
		"{\"tool\":\"verify_cases\"}",
	} {
		var output bytes.Buffer
		if err := run(context.Background(), strings.NewReader(raw), &output, "http://127.0.0.1:1"); err == nil {
			t.Fatalf("invalid request accepted: %s", raw)
		}
		if output.Len() != 0 {
			t.Fatal("invalid request emitted success")
		}
	}
}
