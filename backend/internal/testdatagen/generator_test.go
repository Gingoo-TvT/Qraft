package testdatagen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	remotesandbox "github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
)

const testCode = "void generate(long long i, long long g, af::Random& rng, std::ostream& out) { out << rng.integer(1,100) << '\\n'; }"

func TestLibraryProperties(t *testing.T) {
	compiler, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("g++ required for C++ primitive property tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "properties")
	cmd := exec.CommandContext(ctx, compiler, "-std=c++20", "-O1", "-fsanitize=undefined", "-fno-sanitize-recover=all", "-I.", "testdata/properties.cpp", "-o", binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(ctx, binary).CombinedOutput(); err != nil {
		t.Fatalf("properties: %v\n%s", err, out)
	} else {
		t.Log(string(out))
	}
}
func TestRecipeHarnessAndIdentity(t *testing.T) {
	recipe := Recipe{Version: Version, Code: testCode}
	a, err := Build(recipe)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build(recipe)
	if !reflect.DeepEqual(a, b) || a.SHA256 != SHA256(a.Source) {
		t.Fatal("recipe is not reproducible")
	}
	b, _ = Build(Recipe{Version: Version, Code: testCode + "\n"})
	if a.SHA256 == b.SHA256 {
		t.Fatal("source change did not change identity")
	}
	if Seed(a.Source, 0, 1) == Seed(a.Source, 1, 1) {
		t.Fatal("case identity lost")
	}
	for _, bad := range []Recipe{{}, {Version: Version}, {Version: "future", Code: testCode}, {Version: Version, Code: strings.Repeat("x", MaxCodeBytes+1)}} {
		if _, err := Build(bad); err == nil {
			t.Fatal("invalid recipe accepted")
		}
	}
	compiler, err := exec.LookPath("g++")
	if err != nil {
		t.Skip("g++ unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "generator.cpp")
	binary := filepath.Join(dir, "generator")
	if err = os.WriteFile(source, []byte(a.Source), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, compiler, "-std=c++20", "-O2", source, "-o", binary).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	run := func(input string) (string, error) {
		cmd := exec.CommandContext(ctx, binary)
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	first, err := run("0 1 42\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := run("0 1 42\n")
	if err != nil || first != second {
		t.Fatal("seed replay mismatch")
	}
	for _, input := range []string{"", "0 1", "-1 0 0", "0 0 -1", "0 0 1 extra"} {
		if _, err := run(input); err == nil {
			t.Fatalf("accepted malformed control input %q", input)
		}
	}
}

type fakeExecutor struct {
	calls   int
	sizes   []int
	execute func(string, []string, remotesandbox.RemoteLimits) *remotesandbox.RemoteExecuteResult
}

func (f *fakeExecutor) Compile(context.Context, string, string) (*remotesandbox.RemoteCompileResult, error) {
	return &remotesandbox.RemoteCompileResult{Success: true}, nil
}
func (f *fakeExecutor) Execute(_ context.Context, _ string, source string, inputs []string, limits remotesandbox.RemoteLimits) (*remotesandbox.RemoteExecuteResult, error) {
	f.calls++
	f.sizes = append(f.sizes, len(inputs))
	if f.execute != nil {
		return f.execute(source, inputs, limits), nil
	}
	return okBatch(inputs), nil
}
func okBatch(inputs []string) *remotesandbox.RemoteExecuteResult {
	result := &remotesandbox.RemoteExecuteResult{Compile: remotesandbox.RemoteCompileResult{Success: true}}
	for i, input := range inputs {
		result.Results = append(result.Results, remotesandbox.RemoteCaseResult{Index: i, Verdict: remotesandbox.VerdictOK, Stdout: input})
	}
	return result
}
func TestToolsGenerateWithoutModelAndFailAtomically(t *testing.T) {
	fake := &fakeExecutor{}
	tools := Tools{fake}
	recipe := Recipe{Version: Version, Code: testCode}
	if _, err := tools.BuildGenerator(context.Background(), recipe); err != nil {
		t.Fatal(err)
	}
	cases := make([]Case, 20)
	for i := range cases {
		cases[i] = Case{Index: i, GroupID: 3, Purpose: "boundary"}
	}
	a, err := tools.GenerateCases(context.Background(), recipe, cases, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.sizes, []int{8, 8, 4}) {
		t.Fatalf("batch sizes=%v", fake.sizes)
	}
	b, err := tools.GenerateCases(context.Background(), recipe, cases, 0)
	if err != nil || !reflect.DeepEqual(a.Cases, b.Cases) {
		t.Fatal("replay mismatch")
	}
	if cases[0].Seed != nil {
		t.Fatal("mutated caller plan")
	}
	explicit := int64(42)
	cases[0].Seed = &explicit
	c, err := tools.GenerateCases(context.Background(), recipe, cases[:1], 0)
	if err != nil || !strings.Contains(c.Cases[0].Input, "42\n") {
		t.Fatal("explicit seed ignored")
	}
	fake.execute = func(_ string, inputs []string, _ remotesandbox.RemoteLimits) *remotesandbox.RemoteExecuteResult {
		r := okBatch(inputs)
		if fake.calls%2 == 0 {
			r.Results[0].Verdict = remotesandbox.VerdictRE
		}
		return r
	}
	fake.calls = 0
	if result, err := tools.GenerateCases(context.Background(), recipe, cases, 0); err == nil || result != nil {
		t.Fatal("partial generation escaped")
	}
	fake.execute = func(_ string, inputs []string, _ remotesandbox.RemoteLimits) *remotesandbox.RemoteExecuteResult {
		r := okBatch(inputs)
		r.Results[0].Index = 99
		return r
	}
	if _, err := tools.GenerateCases(context.Background(), recipe, cases[:1], 0); err == nil {
		t.Fatal("incorrect index accepted")
	}
}
func TestVerifyCasesUsesExecutableFailures(t *testing.T) {
	fake := &fakeExecutor{execute: func(source string, inputs []string, _ remotesandbox.RemoteLimits) *remotesandbox.RemoteExecuteResult {
		r := okBatch(inputs)
		if source == "wrong" {
			r.Results[0].Stdout = "incorrect"
		}
		if source == "reject" {
			r.Results[0].Verdict = remotesandbox.VerdictRE
			r.Results[0].ExitCode = 1
		}
		return r
	}}
	tools := Tools{fake}
	req := VerifyRequest{Inputs: []string{"1\n", "2\n"}, Validator: "validator", Reference: "reference", Brute: "brute", BruteIndices: []int{1}, WrongPrograms: []WrongProgram{{ID: "bad", Source: "wrong"}, {ID: "survivor", Source: "reference"}}}
	result, err := tools.VerifyCases(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || !result.DifferentialChecked || result.DifferentialCaseCount != 1 || !reflect.DeepEqual(result.KilledWrongIDs, []string{"bad"}) || !reflect.DeepEqual(result.SurvivingWrongIDs, []string{"survivor"}) {
		t.Fatalf("verification=%+v", result)
	}
	req.Validator = "reject"
	fake.calls = 0
	result, err = tools.VerifyCases(context.Background(), req)
	if err != nil || result.Passed || fake.calls != 1 || len(result.Outputs) != 0 {
		t.Fatal("illegal input reached reference")
	}
	req.Validator = "validator"
	req.Brute = "wrong"
	result, err = tools.VerifyCases(context.Background(), req)
	if err != nil || result.Passed {
		t.Fatal("brute mismatch not rejected")
	}
	req.Brute = ""
	req.BruteIndices = nil
	req.WrongPrograms = nil
	result, err = tools.VerifyCases(context.Background(), req)
	if err != nil || result.DifferentialChecked {
		t.Fatal("missing oracle claimed as checked")
	}
	req.Comparison = "custom"
	if _, err := tools.VerifyCases(context.Background(), req); err == nil {
		t.Fatal("unsupported comparison accepted")
	}
}
func TestToolInputBudgets(t *testing.T) {
	fake := &fakeExecutor{}
	tools := Tools{fake}
	recipe := Recipe{Version: Version, Code: testCode}
	for _, cases := range [][]Case{nil, {{Index: -1, Purpose: "x"}}, {{Index: 0}}, {{Index: 0, Purpose: "x"}, {Index: 0, Purpose: "y"}}} {
		if _, err := tools.GenerateCases(context.Background(), recipe, cases, 0); err == nil {
			t.Fatal("bad plan accepted")
		}
	}
	for _, budget := range []int64{-1, MaxCaseBytes + 1} {
		if _, err := tools.GenerateCases(context.Background(), recipe, []Case{{Purpose: "x"}}, budget); err == nil {
			t.Fatal("bad budget accepted")
		}
	}
	if fake.calls != 0 {
		t.Fatal("invalid request used sandbox")
	}
}

func TestTokenComparisonDoesNotCollapseTokenBoundaries(t *testing.T) {
	fake := &fakeExecutor{execute: func(source string, inputs []string, _ remotesandbox.RemoteLimits) *remotesandbox.RemoteExecuteResult {
		result := okBatch(inputs)
		result.Results[0].Stdout = "a b\n"
		if source == "brute" {
			result.Results[0].Stdout = "a\x00b\n"
		}
		return result
	}}
	result, err := (Tools{fake}).VerifyCases(context.Background(), VerifyRequest{Inputs: []string{"1\n"}, Validator: "validator", Reference: "reference", Brute: "brute", Comparison: "tokens"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.Findings[0].Verdict != "WA" || result.Findings[0].Actual != "a\x00b\n" || result.Findings[0].Expected != "a b\n" {
		t.Fatal("token mismatch or executable witness lost")
	}
}
