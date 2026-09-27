package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/Gingoo-TvT/Qraft/backend/internal/workflow/activities"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

type testingExportSource struct {
	problem *domain.Problem
	tests   []*domain.TestCase
}

func (s *testingExportSource) GetProblem(context.Context, uuid.UUID) (*domain.Problem, error) {
	return s.problem, nil
}
func (s *testingExportSource) GetTestCases(context.Context, uuid.UUID) ([]*domain.TestCase, error) {
	return s.tests, nil
}

type testingExportObjects struct {
	files map[string][]byte
	reads map[string]int
}

func (o *testingExportObjects) DownloadFile(_ context.Context, key string) ([]byte, error) {
	o.reads[key]++
	data, ok := o.files[key]
	if !ok {
		return nil, fmt.Errorf("missing fixture object")
	}
	return append([]byte(nil), data...), nil
}
func testingExportFixture(t *testing.T) (*HydroExportService, *testingExportSource, *testingExportObjects) {
	t.Helper()
	id := uuid.New()
	p := &domain.Problem{ID: id, Title: "Synthetic addition", Statement: "Read a number and print it.", Status: domain.ProblemStatusDraft, TimeLimit: 1000, MemoryLimit: 128}
	tests := []*domain.TestCase{
		{ID: uuid.New(), ProblemID: id, TestIndex: 0, GroupID: 1, IsSample: true, InputPath: "sample.in", OutputPath: "sample.out"},
		{ID: uuid.New(), ProblemID: id, TestIndex: 1, GroupID: 1, InputPath: "secret.in", OutputPath: "secret.out"},
	}
	objects := &testingExportObjects{files: map[string][]byte{"sample.in": []byte("1\n"), "sample.out": []byte("1\n"), "secret.in": []byte("7\n"), "secret.out": []byte("7\n")}, reads: map[string]int{}}
	identity := activities.TestManifestSandboxIdentity{ManifestDigest: strings.Repeat("a", 64), ImageDigest: strings.Repeat("b", 64), ToolchainManifestDigest: strings.Repeat("c", 64), SeccompPolicyDigest: strings.Repeat("d", 64), LimitProfile: "synthetic-tests-only"}
	manifest := activities.TestManifestV1{
		SchemaVersion: activities.TestManifestSchemaVersion, ComparisonMode: activities.TestManifestComparisonMode, TestCount: 2, DifferentialCheckedCount: 2,
		MainSolutionSHA256: strings.Repeat("e", 64), BruteSolutionSHA256: strings.Repeat("f", 64), MainSandbox: identity, BruteSandbox: identity,
	}
	for _, tc := range tests {
		manifest.Cases = append(manifest.Cases, activities.TestManifestCase{TestIndex: tc.TestIndex, GroupID: tc.GroupID, IsSample: tc.IsSample, Purpose: "synthetic", Origin: activities.TestCaseOriginLLMInline, InputSHA256: sha256Hex(objects.files[tc.InputPath]), ExpectedOutputSHA256: sha256Hex(objects.files[tc.OutputPath]), DifferentialChecked: true, DifferentialMatch: true, BruteOutputSHA256: sha256Hex(objects.files[tc.OutputPath])})
	}
	encoded, digest, err := activities.CanonicalTestManifestJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("problems/%s/test_manifest.v1.json", id)
	objects.files[path] = encoded
	p.MetadataJSON, _ = json.Marshal(map[string]interface{}{
		"activity_payload_version": activities.StoreProblemTestManifestPayloadVersion,
		"test_manifest":            map[string]string{"schema_version": activities.TestManifestSchemaVersion, "sha256": digest, "path": path},
		"import_source":            map[string]string{"final_sha256": sha256Hex([]byte(p.Statement))},
		"private-marker":           "must-not-leak",
	})
	source := &testingExportSource{p, tests}
	return &HydroExportService{problems: source, objects: objects}, source, objects
}
func TestProblemSetTestingExportsMixedGenericAndHydroDirectories(t *testing.T) {
	svc, source, objects := testingExportFixture(t)
	q := publicQuizFixture(t)
	set := &domain.ProblemSet{Code: "synthetic", Title: "Practice", Items: []domain.ProblemSetItem{{Quiz: &q, Score: 25}, {Problem: source.problem, Score: 75}}}
	pkg, err := buildProblemSetTestingPackage(context.Background(), set, "generic", svc)
	if err != nil {
		t.Fatal(err)
	}
	files := readPublicPackage(t, pkg.Content)
	for _, path := range []string{"problem-set.json", "README.txt", "problems.xlsx", "export-notes.json", "problems/001/question.json", "problems/002/statement.md", "problems/002/judge.json", "datas/" + testingOJCode(set, 1, domain.QuizTypeProgramming) + "/2.in", "datas/" + testingOJCode(set, 1, domain.QuizTypeProgramming) + "/2.out"} {
		if files[path] == nil {
			t.Errorf("missing %s", path)
		}
	}
	var manifest testingSetManifest
	if err := json.Unmarshal(files["problem-set.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != "testing" || len(manifest.Items) != 2 || manifest.Items[0].Score != 25 || manifest.Items[1].Position != 2 || manifest.Items[1].Validation.DifferentialCheckedCount != 2 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
	for name, data := range files {
		if bytes.Contains(data, []byte("must-not-leak")) {
			t.Fatalf("private metadata leaked to %s", name)
		}
	}
	if _, err := buildProblemSetTestingPackage(context.Background(), set, "hydro", svc); err == nil || !strings.Contains(err.Error(), "第 1 题") {
		t.Fatalf("mixed Hydro not rejected: %v", err)
	}
	set.Items = []domain.ProblemSetItem{{Problem: source.problem, Score: 60}, {Problem: source.problem, Score: 40}}
	objects.reads = map[string]int{}
	pkg, err = buildProblemSetTestingPackage(context.Background(), set, "hydro", svc)
	if err != nil {
		t.Fatal(err)
	}
	files = readPublicPackage(t, pkg.Content)
	for _, prefix := range []string{"001/", "002/"} {
		for _, name := range []string{"problem.yaml", "problem_zh.md", "testdata/config.yaml", "testdata/2.in", "testdata/2.out", "additional_file/qraft_testing.json"} {
			if files[prefix+name] == nil {
				t.Errorf("missing %s%s", prefix, name)
			}
		}
		var problemYAML hydroProblemYAML
		if err := yaml.Unmarshal(files[prefix+"problem.yaml"], &problemYAML); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(problemYAML.PID, "P"+strings.TrimSuffix(prefix, "/")) {
			t.Fatalf("Hydro ID does not preserve order: %s", problemYAML.PID)
		}
		var config hydroConfigYAML
		if err := yaml.Unmarshal(files[prefix+"testdata/config.yaml"], &config); err != nil {
			t.Fatal(err)
		}
		if config.Time != "1s" || config.Memory != "128m" || config.CheckerType != "default" || len(config.Subtasks) == 0 {
			t.Errorf("config=%+v", config)
		}
	}
	// Each problem occurrence has its own frozen snapshot, without fetching
	// inputs again during Hydro's second verification/writing pass.
	if objects.reads["secret.in"] != 2 {
		t.Errorf("re-read verified object: %v", objects.reads)
	}
	if _, err := svc.BuildProblemPackage(context.Background(), source.problem.ID); err == nil {
		t.Fatal("testing download made draft formally exportable")
	}
}
func TestProblemSetTestingRejectsIncompleteOrChangedData(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testingExportSource, *testingExportObjects)
	}{
		{"missing testdata", func(s *testingExportSource, o *testingExportObjects) { s.tests = nil }},
		{"missing output", func(s *testingExportSource, o *testingExportObjects) { delete(o.files, "secret.out") }},
		{"changed input", func(s *testingExportSource, o *testingExportObjects) { o.files["secret.in"] = []byte("altered") }},
		{"changed output", func(s *testingExportSource, o *testingExportObjects) { o.files["secret.out"] = []byte("altered") }},
		{"cross problem", func(s *testingExportSource, o *testingExportObjects) { s.tests[1].ProblemID = uuid.New() }},
		{"changed group", func(s *testingExportSource, o *testingExportObjects) { s.tests[1].GroupID = 2 }},
		{"changed sample", func(s *testingExportSource, o *testingExportObjects) { s.tests[1].IsSample = true }},
		{"duplicate index", func(s *testingExportSource, o *testingExportObjects) { s.tests[1].TestIndex = 0 }},
		{"changed statement", func(s *testingExportSource, o *testingExportObjects) { s.problem.Statement += "changed" }},
		{"stale", func(s *testingExportSource, o *testingExportObjects) {
			var m map[string]interface{}
			json.Unmarshal(s.problem.MetadataJSON, &m)
			m["stale"] = true
			s.problem.MetadataJSON, _ = json.Marshal(m)
		}},
		{"quarantined", func(s *testingExportSource, o *testingExportObjects) {
			s.problem.Status = domain.ProblemStatusQuarantined
		}},
		{"generating", func(s *testingExportSource, o *testingExportObjects) {
			s.problem.Status = domain.ProblemStatusGenerating
		}},
		{"no evidence", func(s *testingExportSource, o *testingExportObjects) { s.problem.MetadataJSON = []byte("{}") }},
		{"wrong manifest bytes", func(s *testingExportSource, o *testingExportObjects) {
			for name := range o.files {
				if strings.HasSuffix(name, ".json") {
					o.files[name] = []byte("{}")
				}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, s, o := testingExportFixture(t)
			tc.change(s, o)
			if _, err := svc.loadTestingProblem(context.Background(), s.problem.ID); !errors.Is(err, ErrConflict) {
				t.Fatalf("want conflict got %v", err)
			}
		})
	}
}
func TestProblemSetTestingRejectsNoDifferentialEvidence(t *testing.T) {
	svc, s, o := testingExportFixture(t)
	var metadata map[string]interface{}
	json.Unmarshal(s.problem.MetadataJSON, &metadata)
	ref := metadata["test_manifest"].(map[string]interface{})
	path := ref["path"].(string)
	m, err := activities.ParseTestManifestJSON(o.files[path])
	if err != nil {
		t.Fatal(err)
	}
	m.DifferentialCheckedCount = 0
	for i := range m.Cases {
		m.Cases[i].DifferentialChecked = false
		m.Cases[i].DifferentialMatch = false
		m.Cases[i].BruteOutputSHA256 = ""
	}
	data, digest, err := activities.CanonicalTestManifestJSON(*m)
	if err != nil {
		t.Fatal(err)
	}
	o.files[path] = data
	ref["sha256"] = digest
	s.problem.MetadataJSON, _ = json.Marshal(metadata)
	if _, err := svc.loadTestingProblem(context.Background(), s.problem.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("unverified data accepted: %v", err)
	}
}
func TestProblemSetTestingRetainsS3V2Binding(t *testing.T) {
	f := qg15AS3ExportFixture(t)
	svc := &HydroExportService{problems: &qg15AHydroProblemSource{problem: f.request.Problem, testCases: f.request.TestCases}, objects: f.reader}
	set := &domain.ProblemSet{Code: "v2", Items: []domain.ProblemSetItem{{Problem: f.request.Problem}}}
	if _, err := buildProblemSetTestingPackage(context.Background(), set, "hydro", svc); err != nil {
		t.Fatal(err)
	}
	f.reader.data[f.request.TestCases[0].InputPath] = []byte("changed")
	if _, err := buildProblemSetTestingPackage(context.Background(), set, "hydro", svc); err == nil {
		t.Fatal("tampered V2 data accepted")
	}
}

type testingSetStore struct {
	problemSetStore
	set           *domain.ProblemSet
	ledger        []*domain.ProblemSetLedgerEntry
	statusUpdates int
}

func (s *testingSetStore) GetByID(context.Context, uuid.UUID) (*domain.ProblemSet, error) {
	return s.set, nil
}
func (s *testingSetStore) CreateLedgerEntry(_ context.Context, e *domain.ProblemSetLedgerEntry) error {
	s.ledger = append(s.ledger, e)
	return nil
}
func (s *testingSetStore) UpdateStatusAndScore(context.Context, uuid.UUID, domain.ProblemSetStatus, int) error {
	s.statusUpdates++
	return nil
}
func TestProblemSetTestingServiceDoesNotPublishOrRequirePlannedQuota(t *testing.T) {
	svc, source, _ := testingExportFixture(t)
	id := source.problem.ID
	set := &domain.ProblemSet{ID: uuid.New(), Code: "test", Status: domain.ProblemSetStatusDraft, DesiredItemCount: 10, MinItemCount: 10, MaxItemCount: 20, CooldownSets: 5, Items: []domain.ProblemSetItem{{ProblemID: &id, Score: 100}}}
	repo := &testingSetStore{set: set}
	sets := NewProblemSetServiceWithDeps(repo, source, nil, svc, nil)
	result, err := sets.ExportTesting(context.Background(), set.ID, "generic")
	if err != nil {
		t.Fatal(err)
	}
	if result.Package == nil || result.Quality.ReadyForExport || len(repo.ledger) != 1 || repo.ledger[0].EventType != "validated" || repo.ledger[0].OverlapReport["operation"] != "testing_exported" || repo.statusUpdates != 0 || set.Status != domain.ProblemSetStatusDraft {
		t.Fatal("testing mutated publication lifecycle")
	}
	sets.SetWorkflowAccess(&WorkflowAccess{})
	if _, err := sets.ExportTesting(context.Background(), set.ID, "generic"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing identity accepted: %v", err)
	}
}

func TestProblemSetTestingHandlesObjectiveOnlyAndCancellation(t *testing.T) {
	q := publicQuizFixture(t)
	set := &domain.ProblemSet{Code: "objective", Items: []domain.ProblemSetItem{{Quiz: &q, Score: 10}}}
	if _, err := buildProblemSetTestingPackage(context.Background(), set, "generic", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buildProblemSetTestingPackage(ctx, set, "generic", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	set.Items = nil
	if _, err := buildProblemSetTestingPackage(context.Background(), set, "generic", nil); err == nil {
		t.Fatal("empty set accepted")
	}
}

func TestProblemSetTestingFreezesVerifiedBytes(t *testing.T) {
	svc, source, objects := testingExportFixture(t)
	assets, err := svc.loadTestingProblem(context.Background(), source.problem.ID)
	if err != nil {
		t.Fatal(err)
	}
	objects.files["secret.in"] = []byte("changed after validation")
	objects.files["secret.out"] = []byte("changed after validation")
	pkg, err := BuildHydroProblemPackage(context.Background(), assets.request)
	if err != nil {
		t.Fatal(err)
	}
	files := readPublicPackage(t, pkg.Content)
	if string(files["testdata/2.in"]) != "7\n" || string(files["testdata/2.out"]) != "7\n" {
		t.Fatal("package re-read changed storage")
	}
	if objects.reads["secret.in"] != 1 || objects.reads["secret.out"] != 1 {
		t.Fatal("verified objects fetched twice")
	}
}
