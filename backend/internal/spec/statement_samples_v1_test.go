package spec

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
)

func TestStatementSamplesV1RoundTripsZeroOneAndManyDeterministically(t *testing.T) {
	variants := []struct {
		name    string
		samples []StatementSampleV1
	}{
		{name: "zero"},
		{name: "one", samples: []StatementSampleV1{{Input: "1\n5\n", Output: "25"}}},
		{name: "many", samples: []StatementSampleV1{
			{Input: "1\n-7\n", Output: "49"},
			{Input: "3\n1 2 3\n", Output: "6\n```\nstill output"},
			{Input: "4\n4 3 2 1\n", Output: "10"},
		}},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			draft := "before\n" + StatementSamplesPlaceholderV1 + "\nafter\n"
			first, err := RenderStatementSamplesV1(draft, variant.samples)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := RenderStatementSamplesV1(draft, variant.samples)
			if err != nil || second != first {
				t.Fatalf("render is not deterministic: err=%v", err)
			}
			if strings.Contains(first, StatementSamplesPlaceholderV1) {
				t.Fatal("render retained placeholder")
			}
			parsed, err := ParseStatementSamplesV1(first)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(parsed) != len(variant.samples) ||
				(len(parsed) > 0 && !reflect.DeepEqual(parsed, variant.samples)) {
				t.Fatalf("round trip mismatch: got=%+v want=%+v", parsed, variant.samples)
			}
		})
	}
}

func TestStatementSamplesV1RejectsManualEditOrderingAndMarkerDefects(t *testing.T) {
	draft := "before\n" + StatementSamplesPlaceholderV1 + "\nafter\n"
	markdown, err := RenderStatementSamplesV1(draft, []StatementSampleV1{
		{Input: "1\n5\n", Output: "25"},
		{Input: "1\n7\n", Output: "49"},
	})
	if err != nil {
		t.Fatal(err)
	}
	reordered := strings.Replace(markdown, "#### input1", "#### input9", 1)
	cases := map[string]string{
		"manual output edit":  strings.Replace(markdown, "\n25\n```", "\n26\n```", 1),
		"reordered label":     reordered,
		"duplicate section":   markdown + "\n" + markdown,
		"placeholder remains": markdown + StatementSamplesPlaceholderV1,
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseStatementSamplesV1(value); err == nil {
				t.Fatal("expected fail-closed parse error")
			}
		})
	}
	if _, err := RenderStatementSamplesV1("no marker", nil); err == nil {
		t.Fatal("missing marker was accepted")
	}
	if _, err := RenderStatementSamplesV1(draft, []StatementSampleV1{{Input: "1\r\n", Output: "ok"}}); err == nil {
		t.Fatal("bare/CRLF input was accepted by the fenced-text contract")
	}
}

func TestValidateSampleInputProfileV1IsIntegerOnlyEvenWithoutSamples(t *testing.T) {
	one := int64(1)
	base := domain.SemanticSpecV1{
		SchemaVersion: domain.SemanticSpecSchemaV1,
		Symbols: []domain.SemanticSymbolV1{{
			Name: "n", Type: domain.SemanticSymbolInteger, Scope: domain.SemanticSymbolScopeInput,
		}},
		InputGrammar: domain.SemanticGrammarV1{
			Profile: domain.SemanticGrammarTokenLinesV1,
			Lines: []domain.SemanticGrammarLineV1{{
				ID:     "line_n",
				Repeat: domain.SemanticExpressionV1{Kind: domain.SemanticExpressionInteger, Value: &one},
				Fields: []domain.SemanticGrammarFieldV1{{Symbol: "n", Mode: domain.SemanticGrammarFieldScalar}},
			}},
		},
	}
	if err := ValidateSampleInputProfileV1(base); err != nil {
		t.Fatalf("integer profile rejected: %v", err)
	}
	unsupported := base
	unsupported.Symbols = append([]domain.SemanticSymbolV1(nil), base.Symbols...)
	unsupported.Symbols[0].Type = domain.SemanticSymbolString
	if err := ValidateSampleInputProfileV1(unsupported); err == nil || !strings.Contains(err.Error(), "unsupported parser type") {
		t.Fatalf("unsupported grammar did not fail closed: %v", err)
	}
}
