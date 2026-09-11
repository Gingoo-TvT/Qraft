package activities

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCanonicalAuthoringBriefV1NormalizesOrderAndLineEndingsDeterministically(t *testing.T) {
	first := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  "  Count paths.\r\nKeep the core mechanism.  ",
		RequiredFacts:  []CanonicalAuthoringBriefFactV1{{Key: "zeta", Value: " last "}, {Key: "alpha", Value: " first\r\nline "}},
	}
	second := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  "Count paths.\nKeep the core mechanism.",
		RequiredFacts:  []CanonicalAuthoringBriefFactV1{{Key: "alpha", Value: "first\nline"}, {Key: "zeta", Value: "last"}},
	}
	activities := &Activities{}
	one, err := activities.BuildCanonicalAuthoringBriefActivityV1(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	two, err := activities.BuildCanonicalAuthoringBriefActivityV1(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if one.CanonicalBrief != two.CanonicalBrief || one.CanonicalBriefSHA256 != two.CanonicalBriefSHA256 || one.BuilderInputSHA256 != two.BuilderInputSHA256 {
		t.Fatal("normalized equivalent inputs did not produce one identity")
	}
	if one.CanonicalBriefSHA256 != sha256Hex([]byte(one.CanonicalBrief)) {
		t.Fatal("canonical brief hash mismatch")
	}
	var brief CanonicalAuthoringBriefV1
	if err := json.Unmarshal([]byte(one.CanonicalBrief), &brief); err != nil {
		t.Fatal(err)
	}
	if brief.SchemaVersion != CanonicalAuthoringBriefSchemaV1 || brief.InputGrammarProfile == "" ||
		len(brief.SupportedInputSymbolTypes) != 2 || brief.RequiredFacts[0].Key != "alpha" {
		t.Fatalf("unexpected canonical brief: %+v", brief)
	}
}

func TestBuildCanonicalAuthoringBriefV1RejectsAmbiguousOrInvalidSources(t *testing.T) {
	base := BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  "Count paths.", RequiredFacts: []CanonicalAuthoringBriefFactV1{{Key: "rule", Value: "one"}},
	}
	cases := map[string]BuildCanonicalAuthoringBriefInput{
		"empty concept": {PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion},
		"duplicate key": {PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion, FrozenConcept: "x", RequiredFacts: []CanonicalAuthoringBriefFactV1{{Key: "same", Value: "1"}, {Key: "same", Value: "2"}}},
		"control":       {PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion, FrozenConcept: "x\x00y"},
		"bad version":   func() BuildCanonicalAuthoringBriefInput { value := base; value.PayloadVersion++; return value }(),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := (&Activities{}).BuildCanonicalAuthoringBriefActivityV1(context.Background(), input); err == nil {
				t.Fatal("invalid source was accepted")
			}
		})
	}
	result, err := (&Activities{}).BuildCanonicalAuthoringBriefActivityV1(context.Background(), base)
	if err != nil || strings.TrimSpace(result.CanonicalBrief) != result.CanonicalBrief {
		t.Fatalf("valid source failed: result=%+v err=%v", result, err)
	}
	emptyFacts, err := (&Activities{}).BuildCanonicalAuthoringBriefActivityV1(context.Background(), BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion, FrozenConcept: "No extra facts.", RequiredFacts: []CanonicalAuthoringBriefFactV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded CanonicalAuthoringBriefV1
	if err := json.Unmarshal([]byte(emptyFacts.CanonicalBrief), &decoded); err != nil || decoded.RequiredFacts == nil {
		t.Fatalf("required_facts must remain a canonical empty array: decoded=%+v err=%v", decoded, err)
	}
}
