package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"go.temporal.io/sdk/temporal"
)

const (
	BuildCanonicalAuthoringBriefPayloadVersion = 1
	CanonicalAuthoringBriefSchemaV1            = "algoforge.canonical-authoring-brief.v1"
	canonicalAuthoringBriefContractErrorV1     = "CanonicalAuthoringBriefContractError"
)

type CanonicalAuthoringBriefFactV1 struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type BuildCanonicalAuthoringBriefInput struct {
	PayloadVersion int                             `json:"payload_version"`
	FrozenConcept  string                          `json:"frozen_concept"`
	RequiredFacts  []CanonicalAuthoringBriefFactV1 `json:"required_facts"`
}

type CanonicalAuthoringBriefV1 struct {
	SchemaVersion             string                          `json:"schema_version"`
	FrozenConcept             string                          `json:"frozen_concept"`
	RequiredFacts             []CanonicalAuthoringBriefFactV1 `json:"required_facts"`
	InputGrammarProfile       string                          `json:"input_grammar_profile"`
	SupportedInputSymbolTypes []string                        `json:"supported_input_symbol_types"`
}

type BuildCanonicalAuthoringBriefResult struct {
	PayloadVersion       int    `json:"payload_version"`
	BuilderInputSHA256   string `json:"builder_input_sha256"`
	CanonicalBrief       string `json:"canonical_brief"`
	CanonicalBriefSHA256 string `json:"canonical_brief_sha256"`
}

// BuildCanonicalAuthoringBriefActivityV1 is a server-owned deterministic
// projection. It fixes the currently supported QG-03 input profile in the
// brief and never trusts a caller-supplied brief hash.
func (a *Activities) BuildCanonicalAuthoringBriefActivityV1(
	_ context.Context,
	in BuildCanonicalAuthoringBriefInput,
) (*BuildCanonicalAuthoringBriefResult, error) {
	normalized, err := normalizeCanonicalAuthoringBriefInputV1(in)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), canonicalAuthoringBriefContractErrorV1, err)
	}
	inputSHA, err := canonicalJSONSHA256(normalized)
	if err != nil {
		return nil, fmt.Errorf("hash canonical brief builder input: %w", err)
	}
	canonicalFacts := make([]CanonicalAuthoringBriefFactV1, len(normalized.RequiredFacts))
	copy(canonicalFacts, normalized.RequiredFacts)
	brief := CanonicalAuthoringBriefV1{
		SchemaVersion:             CanonicalAuthoringBriefSchemaV1,
		FrozenConcept:             normalized.FrozenConcept,
		RequiredFacts:             canonicalFacts,
		InputGrammarProfile:       domain.SemanticGrammarTokenLinesV1,
		SupportedInputSymbolTypes: []string{domain.SemanticSymbolInteger, domain.SemanticSymbolIntegerSequence},
	}
	briefBytes, err := json.Marshal(brief)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical authoring brief: %w", err)
	}
	if len(briefBytes) > maxCanonicalBriefBytes {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("canonical brief exceeds %d bytes", maxCanonicalBriefBytes),
			canonicalAuthoringBriefContractErrorV1, nil,
		)
	}
	return &BuildCanonicalAuthoringBriefResult{
		PayloadVersion:     BuildCanonicalAuthoringBriefPayloadVersion,
		BuilderInputSHA256: inputSHA,
		CanonicalBrief:     string(briefBytes), CanonicalBriefSHA256: sha256Hex(briefBytes),
	}, nil
}

func normalizeCanonicalAuthoringBriefInputV1(in BuildCanonicalAuthoringBriefInput) (BuildCanonicalAuthoringBriefInput, error) {
	if in.PayloadVersion != BuildCanonicalAuthoringBriefPayloadVersion {
		return BuildCanonicalAuthoringBriefInput{}, fmt.Errorf("unsupported canonical brief payload version %d", in.PayloadVersion)
	}
	concept, err := normalizeCanonicalAuthoringTextV1("frozen concept", in.FrozenConcept)
	if err != nil || concept == "" {
		if err == nil {
			err = fmt.Errorf("frozen concept must not be empty")
		}
		return BuildCanonicalAuthoringBriefInput{}, err
	}
	if len(in.RequiredFacts) > 128 {
		return BuildCanonicalAuthoringBriefInput{}, fmt.Errorf("required fact count exceeds 128")
	}
	facts := make([]CanonicalAuthoringBriefFactV1, len(in.RequiredFacts))
	for index, fact := range in.RequiredFacts {
		key, err := normalizeCanonicalAuthoringTextV1("required fact key", fact.Key)
		if err != nil || key == "" || strings.Contains(key, "\n") {
			return BuildCanonicalAuthoringBriefInput{}, fmt.Errorf("required fact %d has an invalid key", index)
		}
		value, err := normalizeCanonicalAuthoringTextV1("required fact value", fact.Value)
		if err != nil || value == "" {
			return BuildCanonicalAuthoringBriefInput{}, fmt.Errorf("required fact %d has an invalid value", index)
		}
		facts[index] = CanonicalAuthoringBriefFactV1{Key: key, Value: value}
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Key == facts[j].Key {
			return facts[i].Value < facts[j].Value
		}
		return facts[i].Key < facts[j].Key
	})
	for index := 1; index < len(facts); index++ {
		if facts[index-1].Key == facts[index].Key {
			return BuildCanonicalAuthoringBriefInput{}, fmt.Errorf("required fact key %q is duplicated", facts[index].Key)
		}
	}
	return BuildCanonicalAuthoringBriefInput{
		PayloadVersion: BuildCanonicalAuthoringBriefPayloadVersion,
		FrozenConcept:  concept, RequiredFacts: facts,
	}, nil
}

func normalizeCanonicalAuthoringTextV1(name, value string) (string, error) {
	if !utf8.ValidString(value) || strings.HasPrefix(value, "\ufeff") {
		return "", fmt.Errorf("%s is not canonical UTF-8 text", name)
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	if strings.ContainsRune(value, '\r') {
		return "", fmt.Errorf("%s contains a bare carriage return", name)
	}
	value = strings.TrimSpace(value)
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", fmt.Errorf("%s contains forbidden control character U+%04X", name, character)
		}
	}
	return value, nil
}
