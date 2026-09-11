// testdata-tool is a one-request JSON CLI for agents and scripts.
// It uses SANDBOX_URL for every compilation/execution; no local code execution.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"github.com/Gingoo-TvT/Qraft/backend/internal/testdatagen"
)

type request struct {
	Tool             string                     `json:"tool"`
	Recipe           testdatagen.Recipe         `json:"recipe"`
	Cases            []testdatagen.Case         `json:"cases,omitempty"`
	OutputLimitBytes int64                      `json:"output_limit_bytes,omitempty"`
	Verify           *testdatagen.VerifyRequest `json:"verify,omitempty"`
}

func run(ctx context.Context, input io.Reader, output io.Writer, url string) error {
	var req request
	dec := json.NewDecoder(io.LimitReader(input, 40<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return fmt.Errorf("invalid tool request: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON request")
	}
	if req.Tool != "build_generator" && req.Tool != "generate_cases" && req.Tool != "verify_cases" {
		return fmt.Errorf("unknown tool %q; use -describe", req.Tool)
	}
	client, err := remotesandbox.NewHTTPClient(url, 2*time.Minute)
	if err != nil {
		return err
	}
	tools := testdatagen.Tools{Executor: client}
	var result any
	switch req.Tool {
	case "build_generator":
		result, err = tools.BuildGenerator(ctx, req.Recipe)
	case "generate_cases":
		result, err = tools.GenerateCases(ctx, req.Recipe, req.Cases, req.OutputLimitBytes)
	case "verify_cases":
		if req.Verify == nil {
			return fmt.Errorf("verify object is required")
		}
		result, err = tools.VerifyCases(ctx, *req.Verify)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
func main() {
	describe := flag.Bool("describe", false, "print tool descriptions and JSON request examples")
	flag.Parse()
	if *describe {
		fmt.Println(description)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := run(ctx, os.Stdin, os.Stdout, os.Getenv("SANDBOX_URL")); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
		os.Exit(1)
	}
}

const description = `{
  "version": "algoforge.testdata.v1",
  "transport": "one JSON request on stdin; JSON result on stdout; errors on stderr with exit code 1",
  "sandbox": "SANDBOX_URL is required. Generated code never executes on this CLI host.",
  "tools": [
    {
      "name":"build_generator",
      "description":"Build and sandbox-compile a C++ recipe. Returns recipe, source, source/library hashes and compile audit. Hash identifies source, not a persistent compiled binary.",
      "request":{"tool":"build_generator","recipe":{"version":"algoforge.testdata.v1","code":"void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << rng.integer(1, 100) << '\\n'; }"}}
    },
    {
      "name":"generate_cases",
      "description":"Run a saved recipe without LLM calls. Omitted seed derives from source/index/group. Up to 32 cases; default 1 MiB per input, 32 MiB total. Save recipe and seeds to reproduce.",
      "request":{"tool":"generate_cases","recipe":{"version":"algoforge.testdata.v1","code":"void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << rng.integer(1, 100) << '\\n'; }"},"cases":[{"index":0,"group_id":0,"seed":42,"purpose":"small boundary"}],"output_limit_bytes":1048576}
    },
    {
      "name":"verify_cases",
      "description":"Run independent C++ validator and reference; optionally brute-check selected positions and test up to 4 named wrong programs. Validator accepts by exiting 0. passed reports input/reference/brute facts, not publication readiness or wrong-program coverage; inspect differential_case_count and surviving_wrong_ids separately.",
      "request":{"tool":"verify_cases","verify":{"inputs":["1\n"],"validator":"C++ validator source","reference":"C++ reference source","brute":"C++ brute source","brute_indices":[0],"wrong_programs":[{"id":"overflow","source":"C++ wrong source"}],"comparison":"exact","time_limit_ms":3000}}
    }
  ]
}`
