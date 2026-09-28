package rating

import (
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func analysisPromptTemplate(t *testing.T) Analysis {
	t.Helper()
	var out Analysis
	decoder := json.NewDecoder(strings.NewReader(analysisOutputTemplate))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("template is not exactly one JSON object")
	}
	return out
}

func assertPromptJSONFields(t *testing.T, actual map[string]json.RawMessage, typ reflect.Type, excluded ...string) {
	t.Helper()
	omit := map[string]bool{}
	for _, key := range excluded {
		omit[key] = true
	}
	expected := []string{}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" && !omit[name] {
			expected = append(expected, name)
		}
	}
	found := []string{}
	for key := range actual {
		found = append(found, key)
	}
	sort.Strings(expected)
	sort.Strings(found)
	if !reflect.DeepEqual(found, expected) {
		t.Fatalf("template fields=%v actual contract=%v", found, expected)
	}
}

func TestAnalysisPromptTemplateMatchesStrictRuntimeContract(t *testing.T) {
	out := analysisPromptTemplate(t)
	if err := NormalizeAnalysis(&out); err != nil {
		t.Fatalf("prompt contradicts runtime validator: %v", err)
	}
	if len(out.Paths) != 2 || out.Paths[0].ID != "blind_a" || out.Paths[1].ID != "blind_b" {
		t.Fatal("template lost independently identified blind paths")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(analysisOutputTemplate), &raw); err != nil {
		t.Fatal(err)
	}
	assertPromptJSONFields(t, raw, reflect.TypeOf(Analysis{}))
	for _, item := range []struct {
		name     string
		typ      reflect.Type
		excluded []string
	}{
		{"kcs", reflect.TypeOf(KC{}), nil},
		{"paths", reflect.TypeOf(Path{}), []string{"evidence", "validation", "human_observations"}},
	} {
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw[item.name], &rows); err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			assertPromptJSONFields(t, row, item.typ, item.excluded...)
		}
	}
	if !strings.Contains(AnalysisSystemPrompt, analysisOutputTemplate) {
		t.Fatal("tested template is not sent to the model")
	}
}

func TestAnalysisPromptPinsTypesEnumsAndReferenceConstraints(t *testing.T) {
	for _, text := range []string{
		"conditions:string", "related_tags:string[]", "kc_ids:string[]", "bypasses:string[]", "disagreements:string[]", "limitations:string[]",
		`kind 只能是 "intended"、"alternative"、"misleading"`, `禁止 kind="blind"`,
		`constraint_scope 只能是 "full"、"restricted"、"uncertain"`,
		`semantic_review 只能是 "candidate"、"equivalent_dependency"、"needs_review"`,
		`relation 只能是 "easier"、"similar"、"harder"、"incomparable"`,
		"本次 kcs 中已有的 id", "路径 id 唯一", "anchor_rating:integer", "reviewed_anchors 中实际给定的 UUID",
		"input:string、legality_argument:string、failure_reason:string", "counterexamples 最多 3 项", "最多 16 个 KC、5 条路径",
		"不输出 evidence、validation、human_observations", "不能由角色名猜测", "不得为满足格式而编造", "原样保留",
	} {
		if !strings.Contains(AnalysisSystemPrompt, text) {
			t.Fatalf("output contract missing %q", text)
		}
	}
	for _, required := range []string{"不按 KC 加权", "不按多模型投票给分", "语义结论等待人工复核", "不能因为有人自报采用就认定算法正确", "允许 unresolved disagreements"} {
		if !strings.Contains(AnalysisSystemPrompt, required) {
			t.Fatalf("analysis requirement was dropped: %s", required)
		}
	}
}

func TestAnalysisPromptEnumContractDoesNotRelaxValidation(t *testing.T) {
	for _, kind := range []string{"intended", "alternative", "misleading"} {
		out := analysisPromptTemplate(t)
		out.Paths[0].Kind = kind
		if err := NormalizeAnalysis(&out); err != nil {
			t.Fatalf("declared kind %s rejected: %v", kind, err)
		}
	}
	for _, kind := range []string{"blind", "blind_a", "blind_b", "", "invented"} {
		out := analysisPromptTemplate(t)
		out.Paths[0].Kind = kind
		if err := NormalizeAnalysis(&out); err == nil {
			t.Fatalf("invalid kind %q silently mapped", kind)
		}
	}
	out := analysisPromptTemplate(t)
	out.Paths[0].KCIDs = []string{"not-in-kcs"}
	if err := NormalizeAnalysis(&out); err == nil {
		t.Fatal("unknown KC reference accepted")
	}
	out = analysisPromptTemplate(t)
	out.Paths[1].ID = out.Paths[0].ID
	if err := NormalizeAnalysis(&out); err == nil {
		t.Fatal("duplicate path identity accepted")
	}
	if ModelPromptVersion != "kc-rating-prompt-v3" || LegacyRuleVersion != "kc-rating-pilot-v1" || RuleVersion == LegacyRuleVersion {
		t.Fatal("output contract revision changed scoring rules")
	}
}
