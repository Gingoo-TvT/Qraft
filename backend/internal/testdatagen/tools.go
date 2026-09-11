package testdatagen

import (
	"context"
	"fmt"
	"slices"
	"strings"

	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
)

type Case struct {
	Index   int    `json:"index"`
	GroupID int    `json:"group_id"`
	Seed    *int64 `json:"seed,omitempty"`
	Purpose string `json:"purpose"`
}
type GeneratedCase struct {
	Case
	Input  string `json:"input"`
	SHA256 string `json:"sha256"`
}
type BuildResult struct {
	Generator *Generator                         `json:"generator"`
	Compile   *remotesandbox.RemoteCompileResult `json:"compile"`
}
type GenerateResult struct {
	Generator *Generator                          `json:"generator"`
	Cases     []GeneratedCase                     `json:"cases"`
	Audits    []remotesandbox.RemoteAuditMetadata `json:"audits"`
}
type Tools struct{ Executor remotesandbox.RemoteExecutor }

func (t Tools) BuildGenerator(ctx context.Context, recipe Recipe) (*BuildResult, error) {
	generator, err := Build(recipe)
	if err != nil {
		return nil, err
	}
	if t.Executor == nil {
		return nil, fmt.Errorf("remote sandbox is required")
	}
	result, err := t.Executor.Compile(ctx, "cpp", generator.Source)
	if err != nil {
		return nil, err
	}
	if result == nil || !result.Success {
		return nil, fmt.Errorf("generator compilation failed: %s", compileError(result))
	}
	return &BuildResult{Generator: generator, Compile: result}, nil
}
func compileError(result *remotesandbox.RemoteCompileResult) string {
	if result == nil {
		return "sandbox returned no compilation result"
	}
	return bounded(result.Stderr)
}
func bounded(s string) string {
	if len(s) > 2048 {
		return s[:2048]
	}
	return s
}

func (t Tools) GenerateCases(ctx context.Context, recipe Recipe, cases []Case, outputLimit int64) (*GenerateResult, error) {
	generator, err := Build(recipe)
	if err != nil {
		return nil, err
	}
	if t.Executor == nil {
		return nil, fmt.Errorf("remote sandbox is required")
	}
	if len(cases) == 0 || len(cases) > MaxCases {
		return nil, fmt.Errorf("case count must be in [1,%d]", MaxCases)
	}
	if outputLimit == 0 {
		outputLimit = DefaultCaseBytes
	}
	batchSize, err := BatchSize(outputLimit)
	if err != nil {
		return nil, err
	}
	seen := map[[2]int]bool{}
	for _, c := range cases {
		key := [2]int{c.Index, c.GroupID}
		if c.Index < 0 || strings.TrimSpace(c.Purpose) == "" || (c.Seed != nil && *c.Seed < 0) || seen[key] {
			return nil, fmt.Errorf("cases require nonnegative index/seed, purpose and unique index/group")
		}
		seen[key] = true
	}
	result := &GenerateResult{Generator: generator, Cases: make([]GeneratedCase, 0, len(cases))}
	var total int64
	limits := remotesandbox.NewRemoteLimits(3000, 256)
	limits.MaxProcesses = 16
	limits.OutputLimitBytes = outputLimit
	limits.Seed = Seed(generator.Source, -1, -1)
	for start := 0; start < len(cases); start += batchSize {
		end := min(start+batchSize, len(cases))
		inputs := make([]string, end-start)
		batch := make([]GeneratedCase, end-start)
		for j, c := range cases[start:end] {
			seed := Seed(generator.Source, c.Index, c.GroupID)
			if c.Seed != nil {
				seed = *c.Seed
			}
			c.Seed = &seed
			inputs[j] = fmt.Sprintf("%d %d %d\n", c.Index, c.GroupID, seed)
			batch[j] = GeneratedCase{Case: c}
		}
		executed, err := t.Executor.Execute(ctx, "cpp", generator.Source, inputs, limits)
		if err != nil {
			return nil, err
		}
		if err = checkBatch(executed, len(inputs)); err != nil {
			return nil, err
		}
		for j, item := range executed.Results {
			if item.Verdict != remotesandbox.VerdictOK || item.ExitCode != 0 || item.Signal != "" {
				return nil, fmt.Errorf("generator case %d failed (%s): %s", start+j, item.Verdict, bounded(item.Stderr))
			}
			n := int64(len(item.Stdout))
			total += n
			if n == 0 || n > outputLimit || total > MaxTotalBytes {
				return nil, fmt.Errorf("generator case %d violates input byte budget", start+j)
			}
			batch[j].Input = item.Stdout
			batch[j].SHA256 = SHA256(item.Stdout)
		}
		result.Cases = append(result.Cases, batch...)
		result.Audits = append(result.Audits, executed.Audit)
	}
	return result, nil
}
func checkBatch(result *remotesandbox.RemoteExecuteResult, n int) error {
	if result == nil {
		return fmt.Errorf("sandbox returned no execution result")
	}
	if !result.Compile.Success {
		return fmt.Errorf("compilation failed: %s", bounded(result.Compile.Stderr))
	}
	if len(result.Results) != n {
		return fmt.Errorf("sandbox result count mismatch: got %d want %d", len(result.Results), n)
	}
	for i, item := range result.Results {
		if item.Index != i {
			return fmt.Errorf("sandbox case index mismatch at %d", i)
		}
	}
	return nil
}

type WrongProgram struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}
type VerifyRequest struct {
	Inputs    []string `json:"inputs"`
	Validator string   `json:"validator"`
	Reference string   `json:"reference"`
	Brute     string   `json:"brute,omitempty"`
	// nil selects all cases; a provided list selects small cases for the brute.
	BruteIndices  []int          `json:"brute_indices,omitempty"`
	WrongPrograms []WrongProgram `json:"wrong_programs,omitempty"`
	Comparison    string         `json:"comparison,omitempty"`
	TimeLimitMS   int            `json:"time_limit_ms,omitempty"`
}
type Finding struct {
	Program   string `json:"program"`
	CaseIndex int    `json:"case_index"`
	Verdict   string `json:"verdict"`
	Detail    string `json:"detail,omitempty"`
	Expected  string `json:"expected,omitempty"`
	Actual    string `json:"actual,omitempty"`
}
type VerifyResult struct {
	Passed                bool                                `json:"passed"`
	DifferentialChecked   bool                                `json:"differential_checked"`
	DifferentialCaseCount int                                 `json:"differential_case_count"`
	Outputs               []string                            `json:"outputs,omitempty"`
	InputSHA256           []string                            `json:"input_sha256"`
	Findings              []Finding                           `json:"findings"`
	KilledWrongIDs        []string                            `json:"killed_wrong_ids"`
	SurvivingWrongIDs     []string                            `json:"surviving_wrong_ids"`
	Audits                []remotesandbox.RemoteAuditMetadata `json:"audits"`
}

// VerifyCases checks executable facts. It is a tool result, not publication approval.
// A validator reads one input and returns exit 0 for legal, nonzero for illegal.
func (t Tools) VerifyCases(ctx context.Context, req VerifyRequest) (*VerifyResult, error) {
	if t.Executor == nil {
		return nil, fmt.Errorf("remote sandbox is required")
	}
	if len(req.Inputs) == 0 || len(req.Inputs) > MaxCases {
		return nil, fmt.Errorf("invalid verification case count")
	}
	if strings.TrimSpace(req.Validator) == "" || strings.TrimSpace(req.Reference) == "" {
		return nil, fmt.Errorf("independent validator and reference source are required")
	}
	if req.Comparison == "" {
		req.Comparison = "exact"
	}
	if req.Comparison != "exact" && req.Comparison != "tokens" {
		return nil, fmt.Errorf("comparison must be exact or tokens; custom checkers require the existing quality pipeline")
	}
	if req.TimeLimitMS == 0 {
		req.TimeLimitMS = 3000
	}
	if req.TimeLimitMS < 1 || req.TimeLimitMS > 10000 {
		return nil, fmt.Errorf("time_limit_ms must be in [1,10000]")
	}
	if len(req.WrongPrograms) > 4 {
		return nil, fmt.Errorf("at most four wrong programs per request")
	}
	seen := map[string]bool{}
	for _, p := range req.WrongPrograms {
		if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Source) == "" || seen[p.ID] {
			return nil, fmt.Errorf("wrong programs require unique IDs and source")
		}
		seen[p.ID] = true
	}
	indexes := req.BruteIndices
	if req.Brute == "" && len(indexes) > 0 {
		return nil, fmt.Errorf("brute source is required for brute_indices")
	}
	if req.Brute != "" && indexes == nil {
		indexes = make([]int, len(req.Inputs))
		for i := range indexes {
			indexes[i] = i
		}
	}
	used := map[int]bool{}
	for _, i := range indexes {
		if i < 0 || i >= len(req.Inputs) || used[i] {
			return nil, fmt.Errorf("brute_indices must be distinct valid case positions")
		}
		used[i] = true
	}
	result := &VerifyResult{Passed: true, Findings: []Finding{}, KilledWrongIDs: []string{}, SurvivingWrongIDs: []string{}}
	var total int64
	for _, input := range req.Inputs {
		total += int64(len(input))
		if len(input) == 0 || int64(len(input)) > MaxCaseBytes || total > MaxTotalBytes {
			return nil, fmt.Errorf("invalid verification input byte budget")
		}
		result.InputSHA256 = append(result.InputSHA256, SHA256(input))
	}
	limits := remotesandbox.NewRemoteLimits(req.TimeLimitMS, 256)
	limits.OutputLimitBytes = DefaultCaseBytes
	run := func(source string, inputs []string) ([]remotesandbox.RemoteCaseResult, error) {
		if len(source) > 256<<10 {
			return nil, fmt.Errorf("program source is too large")
		}
		all := make([]remotesandbox.RemoteCaseResult, 0, len(inputs))
		for start := 0; start < len(inputs); start += 8 {
			end := min(start+8, len(inputs))
			execution, err := t.Executor.Execute(ctx, "cpp", source, inputs[start:end], limits)
			if err != nil {
				return nil, err
			}
			if err = checkBatch(execution, end-start); err != nil {
				return nil, err
			}
			for _, item := range execution.Results {
				if string(item.Verdict) != "OK" && string(item.Verdict) != "RE" && string(item.Verdict) != "TLE" && string(item.Verdict) != "MLE" && string(item.Verdict) != "OLE" {
					return nil, fmt.Errorf("sandbox execution unavailable: %s", item.Verdict)
				}
				if item.Verdict == remotesandbox.VerdictOK && (item.ExitCode != 0 || item.Signal != "") {
					return nil, fmt.Errorf("sandbox returned inconsistent successful verdict")
				}
				if int64(len(item.Stdout)) > DefaultCaseBytes {
					return nil, fmt.Errorf("sandbox output budget exceeded")
				}
			}
			all = append(all, execution.Results...)
			result.Audits = append(result.Audits, execution.Audit)
		}
		return all, nil
	}
	validation, err := run(req.Validator, req.Inputs)
	if err != nil {
		return nil, err
	}
	for i, item := range validation {
		if item.Verdict != remotesandbox.VerdictOK {
			result.Passed = false
			result.Findings = append(result.Findings, Finding{Program: "validator", CaseIndex: i, Verdict: string(item.Verdict), Detail: bounded(item.Stderr)})
		}
	}
	if !result.Passed {
		return result, nil
	}
	reference, err := run(req.Reference, req.Inputs)
	if err != nil {
		return nil, err
	}
	for i, item := range reference {
		if item.Verdict != remotesandbox.VerdictOK {
			result.Passed = false
			result.Findings = append(result.Findings, Finding{Program: "reference", CaseIndex: i, Verdict: string(item.Verdict), Detail: bounded(item.Stderr)})
		}
		result.Outputs = append(result.Outputs, item.Stdout)
	}
	if !result.Passed {
		return result, nil
	}
	equal := func(a, b string) bool {
		if req.Comparison == "tokens" {
			return slices.Equal(strings.Fields(a), strings.Fields(b))
		}
		return a == b
	}
	if req.Brute != "" && len(indexes) > 0 {
		inputs := make([]string, len(indexes))
		for j, i := range indexes {
			inputs[j] = req.Inputs[i]
		}
		brute, err := run(req.Brute, inputs)
		if err != nil {
			return nil, err
		}
		result.DifferentialChecked = true
		result.DifferentialCaseCount = len(indexes)
		for j, item := range brute {
			i := indexes[j]
			if item.Verdict != remotesandbox.VerdictOK || !equal(item.Stdout, result.Outputs[i]) {
				result.Passed = false
				verdict := string(item.Verdict)
				if item.Verdict == remotesandbox.VerdictOK {
					verdict = "WA"
				}
				result.Findings = append(result.Findings, Finding{Program: "brute", CaseIndex: i, Verdict: verdict, Detail: "reference/brute mismatch or brute execution failure", Expected: bounded(result.Outputs[i]), Actual: bounded(item.Stdout)})
			}
		}
		if !result.Passed {
			return result, nil
		}
	}
	for _, wrong := range req.WrongPrograms {
		execution, err := run(wrong.Source, req.Inputs)
		if err != nil {
			return nil, fmt.Errorf("wrong program %s: %w", wrong.ID, err)
		}
		killed := false
		for i, item := range execution {
			if item.Verdict != remotesandbox.VerdictOK || !equal(item.Stdout, result.Outputs[i]) {
				killed = true
				verdict := string(item.Verdict)
				if item.Verdict == remotesandbox.VerdictOK {
					verdict = "WA"
				}
				result.Findings = append(result.Findings, Finding{Program: wrong.ID, CaseIndex: i, Verdict: verdict, Detail: "wrong program rejected by this case", Expected: bounded(result.Outputs[i]), Actual: bounded(item.Stdout)})
				break
			}
		}
		if killed {
			result.KilledWrongIDs = append(result.KilledWrongIDs, wrong.ID)
		} else {
			result.SurvivingWrongIDs = append(result.SurvivingWrongIDs, wrong.ID)
		}
	}
	return result, nil
}
