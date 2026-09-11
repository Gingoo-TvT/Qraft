// Package qualitygate implements the isolated, deterministic S3 quality-gate
// contracts. It deliberately has no workflow, provider, database, or service
// dependencies so the same inputs can be recomputed offline.
package qualitygate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	ReviewAssessmentSchemaVersionV1 = "algoforge.quality-review-assessment.v1"
	ReviewScoreMinimumV1            = 0
	ReviewScoreMaximumV1            = 10
	ReviewPassingScoreV1            = 7
	maxReviewAssessmentBytesV1      = 64 << 10
)

var stableCodePatternV1 = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ReviewAssessmentV1 is model-supplied evidence. Approved is explicitly
// advisory: RecomputeVerdictV1 never treats it as the final gate decision.
// Pointers distinguish required false/zero values from omitted JSON fields.
type ReviewAssessmentV1 struct {
	SchemaVersion string              `json:"schema_version"`
	Approved      *bool               `json:"approved"`
	Dimensions    *ReviewDimensionsV1 `json:"dimensions"`
	Blockers      []ReviewBlockerV1   `json:"blockers"`
}

type ReviewDimensionsV1 struct {
	Clarity               *ReviewDimensionV1 `json:"clarity"`
	Correctness           *ReviewDimensionV1 `json:"correctness"`
	TestCoverage          *ReviewDimensionV1 `json:"test_coverage"`
	DifficultyCalibration *ReviewDimensionV1 `json:"difficulty_calibration"`
	TagAccuracy           *ReviewDimensionV1 `json:"tag_accuracy"`
}

type ReviewDimensionV1 struct {
	Score *int   `json:"score"`
	Notes string `json:"notes"`
}

// ReviewBlockerV1 binds every model-raised blocker to the asset that must be
// changed and to a bounded, machine-runnable witness descriptor. The witness
// is data for a trusted runner, never an arbitrary shell command.
type ReviewBlockerV1 struct {
	Code             string              `json:"code"`
	ResponsibleAsset string              `json:"responsible_asset"`
	Witness          ExecutableWitnessV1 `json:"witness"`
}

type ExecutableWitnessV1 struct {
	Runner     string `json:"runner"`
	FixtureRef string `json:"fixture_ref"`
	Assertion  string `json:"assertion"`
}

func BoolV1(value bool) *bool {
	copyValue := value
	return &copyValue
}

func ScoreV1(value int) *int {
	copyValue := value
	return &copyValue
}

// DecodeReviewAssessmentV1 accepts exactly one JSON object. It rejects
// unknown fields, duplicate keys at any nesting depth, and trailing values.
func DecodeReviewAssessmentV1(data []byte) (ReviewAssessmentV1, error) {
	if len(data) == 0 {
		return ReviewAssessmentV1{}, errors.New("review assessment JSON is empty")
	}
	if len(data) > maxReviewAssessmentBytesV1 {
		return ReviewAssessmentV1{}, fmt.Errorf("review assessment JSON exceeds %d bytes", maxReviewAssessmentBytesV1)
	}
	if err := validateSingleJSONValueNoDuplicateKeysV1(data); err != nil {
		return ReviewAssessmentV1{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var assessment ReviewAssessmentV1
	if err := decoder.Decode(&assessment); err != nil {
		return ReviewAssessmentV1{}, fmt.Errorf("decode review assessment: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ReviewAssessmentV1{}, errors.New("review assessment has a trailing JSON value")
		}
		return ReviewAssessmentV1{}, fmt.Errorf("decode trailing review assessment data: %w", err)
	}
	if err := assessment.Validate(); err != nil {
		return ReviewAssessmentV1{}, err
	}
	return assessment, nil
}

func (assessment ReviewAssessmentV1) Validate() error {
	if assessment.SchemaVersion != ReviewAssessmentSchemaVersionV1 {
		return fmt.Errorf("review schema_version %q, want %q", assessment.SchemaVersion, ReviewAssessmentSchemaVersionV1)
	}
	if assessment.Approved == nil {
		return errors.New("review approved is required")
	}
	if assessment.Dimensions == nil {
		return errors.New("review dimensions are required")
	}
	if assessment.Blockers == nil {
		return errors.New("review blockers must be an explicit array")
	}

	dimensions := []struct {
		name  string
		value *ReviewDimensionV1
	}{
		{name: "clarity", value: assessment.Dimensions.Clarity},
		{name: "correctness", value: assessment.Dimensions.Correctness},
		{name: "test_coverage", value: assessment.Dimensions.TestCoverage},
		{name: "difficulty_calibration", value: assessment.Dimensions.DifficultyCalibration},
		{name: "tag_accuracy", value: assessment.Dimensions.TagAccuracy},
	}
	for _, dimension := range dimensions {
		if dimension.value == nil {
			return fmt.Errorf("review dimension %s is required", dimension.name)
		}
		if err := dimension.value.validate(dimension.name); err != nil {
			return err
		}
	}

	seen := make(map[string]struct{}, len(assessment.Blockers))
	for index, blocker := range assessment.Blockers {
		if err := blocker.Validate(); err != nil {
			return fmt.Errorf("review blocker %d: %w", index, err)
		}
		identity := strings.Join([]string{
			blocker.Code,
			blocker.ResponsibleAsset,
			blocker.Witness.Runner,
			blocker.Witness.FixtureRef,
			blocker.Witness.Assertion,
		}, "\x00")
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("review blocker %d duplicates an earlier blocker", index)
		}
		seen[identity] = struct{}{}
	}
	return nil
}

func (dimension ReviewDimensionV1) validate(name string) error {
	if dimension.Score == nil {
		return fmt.Errorf("review dimension %s score is required", name)
	}
	if *dimension.Score < ReviewScoreMinimumV1 || *dimension.Score > ReviewScoreMaximumV1 {
		return fmt.Errorf("review dimension %s score %d is outside [%d,%d]", name, *dimension.Score, ReviewScoreMinimumV1, ReviewScoreMaximumV1)
	}
	if strings.TrimSpace(dimension.Notes) == "" {
		return fmt.Errorf("review dimension %s notes are required", name)
	}
	if len(dimension.Notes) > 4096 {
		return fmt.Errorf("review dimension %s notes exceed 4096 bytes", name)
	}
	return nil
}

func (blocker ReviewBlockerV1) Validate() error {
	if !stableCodePatternV1.MatchString(blocker.Code) || len(blocker.Code) > 128 {
		return fmt.Errorf("invalid blocker code %q", blocker.Code)
	}
	if strings.TrimSpace(blocker.ResponsibleAsset) == "" || len(blocker.ResponsibleAsset) > 512 {
		return errors.New("responsible_asset is required and must not exceed 512 bytes")
	}
	if err := blocker.Witness.Validate(); err != nil {
		return err
	}
	return nil
}

func (witness ExecutableWitnessV1) Validate() error {
	if !stableCodePatternV1.MatchString(witness.Runner) || len(witness.Runner) > 128 {
		return fmt.Errorf("invalid witness runner %q", witness.Runner)
	}
	if strings.TrimSpace(witness.FixtureRef) == "" || len(witness.FixtureRef) > 512 {
		return errors.New("witness fixture_ref is required and must not exceed 512 bytes")
	}
	if strings.TrimSpace(witness.Assertion) == "" || len(witness.Assertion) > 2048 {
		return errors.New("witness assertion is required and must not exceed 2048 bytes")
	}
	return nil
}

func CanonicalReviewAssessmentV1(assessment ReviewAssessmentV1) ([]byte, string, error) {
	if err := assessment.Validate(); err != nil {
		return nil, "", err
	}
	canonical := assessment
	canonical.Blockers = append([]ReviewBlockerV1(nil), assessment.Blockers...)
	sort.Slice(canonical.Blockers, func(i, j int) bool {
		return reviewBlockerSortKeyV1(canonical.Blockers[i]) < reviewBlockerSortKeyV1(canonical.Blockers[j])
	})
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical review assessment: %w", err)
	}
	return encoded, sha256HexV1(encoded), nil
}

func reviewBlockerSortKeyV1(blocker ReviewBlockerV1) string {
	return strings.Join([]string{
		blocker.Code,
		blocker.ResponsibleAsset,
		blocker.Witness.Runner,
		blocker.Witness.FixtureRef,
		blocker.Witness.Assertion,
	}, "\x00")
}

func validateSingleJSONValueNoDuplicateKeysV1(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValueV1(decoder, "$"); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON token %v", token)
		}
		return fmt.Errorf("decode trailing JSON data: %w", err)
	}
	return nil
}

func scanJSONValueV1(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode JSON at %s: %w", path, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("decode object key at %s: %w", path, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("non-string object key at %s", path)
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValueV1(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := scanJSONValueV1(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}

func sha256HexV1(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
