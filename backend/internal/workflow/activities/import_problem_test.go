package activities

import (
	"bytes"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"testing"
	"time"
)

func TestImportedStatementPreservesClearSourceBytes(t *testing.T) {
	original := domain.SourceProblem{ItemID: "a", Title: "Original", Statement: "# Task\n\nCompute $a+b$.\n```text\n1 2\n```\n```text\n3\n```\n"}
	out, e := applyImportClarifications(original, importAnalysis{Samples: []ImportedSample{{Input: "1 2", Output: "3"}}})
	if e != nil {
		t.Fatal(e)
	}
	if out.Statement.Statement != original.Statement || out.Statement.Title != original.Title || out.Evidence.FinalSHA256 != original.Hash() {
		t.Fatal("clear statement mutated")
	}
	if _, e = applyImportClarifications(original, importAnalysis{Changes: []domain.ImportClarification{{Original: "Compute", Replacement: "Calculate"}}}); e == nil {
		t.Fatal("unjustified rewrite accepted")
	}
	if _, e = applyImportClarifications(original, importAnalysis{Samples: []ImportedSample{{Input: "invented", Output: "3"}}}); e == nil {
		t.Fatal("invented sample accepted")
	}
}
func TestImportedStatementClarificationRequiresExactEvidence(t *testing.T) {
	source := domain.SourceProblem{ItemID: "a", Title: "T", Statement: "Return a rounding of x."}
	change := domain.ImportClarification{Original: "a rounding", Replacement: "the floor"}
	out, e := applyImportClarifications(source, importAnalysis{Ambiguous: true, Reason: "Rounding direction is undefined.", Changes: []domain.ImportClarification{change}})
	if e != nil {
		t.Fatal(e)
	}
	if out.Statement.Statement != "Return the floor of x." || out.Evidence.Original.Statement != source.Statement || out.Evidence.FinalSHA256 == out.Evidence.OriginalSHA256 {
		t.Fatal("clarification evidence lost")
	}
	for _, a := range []importAnalysis{{Ambiguous: true, Changes: []domain.ImportClarification{change}}, {Ambiguous: true, Reason: "ambiguous"}, {Ambiguous: true, Reason: "ambiguous", Changes: []domain.ImportClarification{{Original: "missing", Replacement: "new"}}}} {
		if _, e := applyImportClarifications(source, a); e == nil {
			t.Fatal("unsupported clarification accepted")
		}
	}
}
func TestImportedStoreDoesNotForgeGenerationOrPublicationEvidence(t *testing.T) {
	source := domain.SourceProblem{ItemID: "a", Title: "T", Statement: "original bytes"}
	evidence := domain.ImportSourceEvidence{Original: source, OriginalSHA256: source.Hash(), FinalSHA256: source.Hash()}
	in := StoreInput{PayloadVersion: StoreProblemTestManifestPayloadVersion, Statement: StatementResult{Title: source.Title, Statement: source.Statement}, ImportSource: &evidence, TestManifest: &TestManifestV1{DifferentialCheckedCount: 1}}
	if e := validateImportStoreEvidence(in); e != nil {
		t.Fatal(e)
	}
	if initialStoreProblemStatus(in) != domain.ProblemStatusDraft {
		t.Fatal("import should remain draft")
	}
	in.Statement.Statement = "silently rewritten"
	if validateImportStoreEvidence(in) == nil {
		t.Fatal("unbound source accepted")
	}
	in.Statement.Statement = source.Statement
	in.TestManifest.DifferentialCheckedCount = 0
	if validateImportStoreEvidence(in) == nil {
		t.Fatal("unvalidated dataset accepted")
	}
}

func TestImportedStoreMetadataStableAcrossCreateAndRetry(t *testing.T) {
	input := StoreInput{Statement: StatementResult{Title: "Original", Statement: "Print n.", Tags: nil}}
	originalHash, err := storeInputHash(input)
	if err != nil {
		t.Fatal(err)
	}
	makeRecord := func() *domain.Problem {
		p := &domain.Problem{Title: input.Statement.Title, Statement: input.Statement.Statement, Tags: input.Statement.Tags, CreatedAt: time.Unix(100, 0)}
		prepareImportedProblemRecord(p)
		return p
	}
	first := makeRecord()
	// First creation enforces the database NOT NULL array; a retry skips it.
	if first.Tags == nil {
		first.Tags = []string{}
	}
	retry := makeRecord()
	main := &domain.Solution{SolutionType: domain.SolutionTypeMain, Language: "cpp"}
	brute := &domain.Solution{SolutionType: domain.SolutionTypeBrute, Language: "cpp"}
	firstMetadata := buildFullMetadata(first, main, brute, nil, input.Params)
	retryMetadata := buildFullMetadata(retry, main, brute, nil, input.Params)
	if !bytes.Equal(firstMetadata, retryMetadata) || !bytes.Contains(retryMetadata, []byte(`"tags": []`)) {
		t.Fatalf("metadata effect bytes changed on retry: first=%s retry=%s", firstMetadata, retryMetadata)
	}
	retryHash, err := storeInputHash(input)
	if err != nil || originalHash != retryHash || input.Statement.Tags != nil {
		t.Fatalf("stored defaults changed historical input identity: %s / %s: %v", originalHash, retryHash, err)
	}
	if first.Source != "qraft_import" || retry.Source != first.Source {
		t.Fatal("source defaults differ")
	}
}
