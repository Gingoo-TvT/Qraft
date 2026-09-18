package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/Gingoo-TvT/Qraft/backend/internal/sandbox"
	"go.temporal.io/sdk/activity"
)

const ratingMaxCases = 32
const ratingMaxCaseBytes = 256 << 10

type ratingCaseEvidence struct {
	TestID         string                      `json:"test_id"`
	InputSHA256    string                      `json:"input_sha256"`
	ExpectedSHA256 string                      `json:"expected_sha256"`
	ActualSHA256   string                      `json:"actual_sha256"`
	Verdict        string                      `json:"verdict"`
	LegalStatus    string                      `json:"legal_status"`
	Audit          sandbox.RemoteAuditMetadata `json:"audit"`
}
type ratingExecutionEvidence struct {
	Compile       *sandbox.RemoteCompileResult `json:"compile,omitempty"`
	Cases         []ratingCaseEvidence         `json:"cases"`
	SuiteCases    int                          `json:"suite_cases"`
	SelectedCases int                          `json:"selected_cases"`
	Scope         string                       `json:"scope"`
}

func ratingOutput(s string) string {
	// Same byte-oriented normalization used by the existing exact comparator.
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n\r ")
}
func ratingPathEvidence(p *rating.Path, status, summary string, details any) {
	ev := rating.Evidence{ID: rating.Digest(rating.StableJSON(details)), Kind: "sandbox_test", Status: status, Summary: summary, Details: rating.StableJSON(details)}
	p.Evidence = append(p.Evidence, ev)
}
func (a *Activities) RatingVerifyActivity(ctx context.Context, in RatingVerifyInput) (*RatingReportResult, error) {
	var snap RatingSnapshot
	if err := a.ratingRead(ctx, in.Snapshot, &snap); err != nil {
		return nil, err
	}
	var report rating.Report
	if err := a.ratingRead(ctx, in.Report, &report); err != nil {
		return nil, err
	}
	if report.SnapshotHash != snap.Subject.Hash {
		return nil, ratingInvalid(fmt.Errorf("verification snapshot mismatch"))
	}
	if len(report.Paths) > 5 {
		return nil, ratingInvalid(fmt.Errorf("too many verification paths"))
	}
	for i := range report.Paths {
		p := &report.Paths[i]
		if p.Validation == "tested" || p.Validation == "refuted" {
			continue
		}
		if p.Code == "" || p.Language != "cpp" || snap.Subject.JudgeMode != "exact_normalized" || len(snap.Subject.Tests) == 0 {
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "缺少可执行代码、冻结测试或已支持的判题语义；没有运行，不作通过判断。", map[string]string{"judge_mode": snap.Subject.JudgeMode})
			continue
		}
		// Existing cases have frozen provenance; model-proposed new inputs do not
		// have a machine-checkable legality witness in this pilot. They remain
		// review candidates rather than silently becoming trusted counterexamples.
		for _, c := range p.Counterexamples {
			p.Evidence = append(p.Evidence, rating.Evidence{ID: rating.Digest([]byte(c.Input)), Kind: "proposed_counterexample", Status: "not_run", Summary: "候选反例尚未独立验证输入合法性和标准输出；不计为击穿。", Details: rating.StableJSON(c)})
		}
		evidence := ratingExecutionEvidence{SuiteCases: len(snap.Subject.Tests), Scope: "bounded_frozen_suite", Cases: []ratingCaseEvidence{}}
		inputs := []string{}
		expected := []string{}
		tests := []rating.TestArtifact{}
		// Stable stratified positions span the entire suite rather than always
		// selecting its first (often sample-only) prefix.
		indexes := ratingCaseIndexes(len(snap.Subject.Tests))
		for _, idx := range indexes {
			t := snap.Subject.Tests[idx]
			input, output, err := a.ratingTestBytes(ctx, t)
			if err != nil {
				return nil, err
			}
			if len(input) > ratingMaxCaseBytes || len(output) > ratingMaxCaseBytes {
				continue
			}
			inputs = append(inputs, input)
			expected = append(expected, output)
			tests = append(tests, t)
		}
		evidence.SelectedCases = len(inputs)
		if len(inputs) == 0 {
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "冻结用例超出本轮传输预算，没有运行。", evidence)
			continue
		}
		executor, err := a.remoteSandboxExecutor(a.sandboxBatchTimeout(len(inputs), snap.Subject.TimeLimit))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "沙箱不可用，未执行候选代码。", evidence)
			continue
		}
		stop := func() {}
		if activity.IsActivity(ctx) {
			stop = heartbeatWhile(ctx, "verifying rating path "+p.ID, 10*time.Second)
		}
		result, err := executor.Execute(ctx, p.Language, p.Code, inputs, sandbox.NewRemoteLimits(snap.Subject.TimeLimit, snap.Subject.MemoryLimit))
		stop()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "沙箱未返回可用执行结果，不能判断候选是否通过。", evidence)
			continue
		}
		if result == nil {
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "沙箱返回为空，不能判断候选是否通过。", evidence)
			continue
		}
		evidence.Compile = &result.Compile
		if !result.Compile.Success {
			p.Validation = "needs_review"
			ratingPathEvidence(p, "compile_error", "候选编译失败；这是实现问题，不能据此否定算法或确认误导路线。", evidence)
			continue
		}
		if len(result.Results) != len(inputs) {
			p.Validation = "needs_review"
			ratingPathEvidence(p, "not_run", "沙箱用例数量不完整，不能汇总为通过。", evidence)
			continue
		}
		killed := false
		unknown := false
		for j, item := range result.Results {
			verdict := string(item.Verdict)
			if item.Index != j {
				unknown = true
				verdict = "invalid_case_index"
			}
			if item.Verdict == sandbox.VerdictOK {
				if ratingOutput(item.Stdout) != ratingOutput(expected[j]) {
					verdict = "wrong_answer"
					killed = true
				} else {
					verdict = "matched_frozen_output"
				}
			} else if item.Verdict == sandbox.VerdictTLE || item.Verdict == sandbox.VerdictMLE || item.Verdict == sandbox.VerdictRE {
				killed = true
			} else {
				unknown = true
			}
			evidence.Cases = append(evidence.Cases, ratingCaseEvidence{TestID: tests[j].ID, InputSHA256: tests[j].InputSHA256, ExpectedSHA256: tests[j].OutputSHA256, ActualSHA256: rating.Digest([]byte(item.Stdout)), Verdict: verdict, LegalStatus: "frozen_suite_case", Audit: result.Audit})
		}
		// The remote service's audit identity is necessary for a reusable receipt.
		if result.Audit.RunID == "" || result.Audit.ManifestDigest == "" || result.Audit.ImageDigest == "" || result.Audit.ToolchainManifestDigest == "" || result.Audit.SeccompPolicyDigest == "" {
			unknown = true
		}
		switch {
		case unknown:
			p.Validation = "needs_review"
			ratingPathEvidence(p, "needs_review", "执行回执缺失或判定不完整；保留原始结果待复核。", evidence)
		case killed:
			p.Validation = "refuted"
			ratingPathEvidence(p, "killed", "此候选代码实例在冻结测试上实际输出不符或超出资源限制；算法本身是否有误仍需论证复核。", evidence)
		default:
			p.Validation = "tested"
			ratingPathEvidence(p, "passed_tests", "候选通过本轮选中的冻结测试；不能据此断言完整正确或 KC 已绕过。", evidence)
		}
		if p.ConstraintScope != "full" || p.SemanticReview == "equivalent_dependency" {
			// Runtime PASS says nothing about avoiding a semantic dependency.
			p.Evidence = append(p.Evidence, rating.Evidence{ID: p.ID + "-semantic", Kind: "kc_dependency_review", Status: "needs_review", Summary: "测试结果不证明完整范围可行性或语义知识绕过。"})
		}
	}
	rating.NormalizeReport(&report)
	report.Limitations = append(report.Limitations, "执行最多 32 个分层选取的冻结测试；未新增未经独立合法性验证的测试或更改正式测试集。")
	ref, err := a.ratingWrite(ctx, report, "verified_report")
	if err != nil {
		return nil, err
	}
	return &RatingReportResult{Report: *ref, NeedsAdditionalRound: rating.NeedsAdditionalRound(report) && report.AdditionalRounds == 0}, nil
}
func ratingCaseIndexes(count int) []int {
	if count <= 0 {
		return nil
	}
	n := count
	if n > ratingMaxCases {
		n = ratingMaxCases
	}
	out := make([]int, n)
	if n == 1 {
		return []int{0}
	}
	for i := range out {
		out[i] = i * (count - 1) / (n - 1)
	}
	return out
}
