package prompts

import (
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestRegistryResolvesCanonicalAndLegacyNames(t *testing.T) {
	r := NewRegistry()
	cases := []struct {
		legacy, canonical string
	}{
		{"statement", TemplateNameStatement},
		{"solution", TemplateNameSolution},
		{"testdata", TemplateNameTestData},
		{"review", TemplateNameReview},
		{"editorial", TemplateNameEditorial},
	}
	for _, tc := range cases {
		tmpl, err := r.Get(tc.legacy)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.legacy, err)
		}
		if tmpl.Name != tc.canonical {
			t.Fatalf("Get(%q) returned %q, want %q", tc.legacy, tmpl.Name, tc.canonical)
		}
		canonical, err := r.Get(tc.canonical)
		if err != nil || canonical != tmpl {
			t.Fatalf("canonical lookup for %q did not resolve the same template", tc.canonical)
		}
	}
}

func TestStatementTemplateRendersItsDeclaredInput(t *testing.T) {
	input := NewStatementInput(domain.DefaultProblemGenParams(), nil)
	if _, err := StatementTemplate().Render(input); err != nil {
		t.Fatalf("statement template should render StatementInput: %v", err)
	}
}

func TestBuiltInTemplatesRenderDeclaredInputs(t *testing.T) {
	statement := NewStatementInput(domain.DefaultProblemGenParams(), nil)
	solution := NewSolutionInput("title", 1200, []string{"dp"}, 2000, 256, "statement", "C++17", "hint")
	testdata := TestDataInput{Statement: "statement", Config: domain.DefaultTestDataConfig(), PreferGenerator: true}
	review := ReviewInput{Level: "algorithm", Statement: "statement", MainSolutionCode: "int main(){}", MainSolutionLanguage: "C++17"}
	editorial := EditorialInput{Statement: "statement", SolutionCode: "int main(){}", SolutionLanguage: "C++17"}
	cases := []struct {
		name string
		tmpl *PromptTemplate
		data interface{}
	}{
		{"statement", StatementTemplate(), statement},
		{"solution", SolutionTemplate(), solution},
		{"testdata", TestDataTemplate(), testdata},
		{"review", ReviewTemplate(), review},
		{"editorial", EditorialTemplate(), editorial},
	}
	for _, tc := range cases {
		req, err := tc.tmpl.Render(tc.data)
		if err != nil {
			t.Fatalf("%s template should render its declared input: %v", tc.name, err)
		}
		if req.PromptID != tc.tmpl.Name || req.PromptVersion != PromptVersionV1 {
			t.Fatalf("%s request identity = %s/%s, want %s/%s", tc.name, req.PromptID, req.PromptVersion, tc.tmpl.Name, PromptVersionV1)
		}
	}
}

func TestRegistryDescriptorIsStableAndBoundToSource(t *testing.T) {
	r := NewRegistry()
	first, err := r.Descriptor(TemplateNameStatement)
	if err != nil {
		t.Fatalf("Descriptor: %v", err)
	}
	second, err := r.Descriptor("statement")
	if err != nil || first != second {
		t.Fatalf("canonical and legacy descriptors differ: %+v / %+v", first, second)
	}
	if first.ID != TemplateNameStatement || first.Version != PromptVersionV1 || len(first.Digest) != 64 {
		t.Fatalf("invalid descriptor: %+v", first)
	}
	if len(r.Descriptors()) != len(r.Names()) {
		t.Fatalf("descriptor count does not match registry count")
	}
}
