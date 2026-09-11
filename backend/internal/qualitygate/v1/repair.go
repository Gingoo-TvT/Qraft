package qualitygate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	RepairStateSchemaVersionV1 = "algoforge.s3-repair-state.v1"
	MaxRepairRoundsV1          = 3

	RepairDecisionActive    = "active"
	RepairDecisionSucceeded = "succeeded"
	RepairDecisionNoGo      = "no_go"

	RepairStopResolved   = "resolved"
	RepairStopNewBlocker = "new_blocker"
	RepairStopNoProgress = "no_progress_two_rounds"
	RepairStopMaxRounds  = "max_rounds"
)

type RepairRoundV1 struct {
	Round        int      `json:"round"`
	ParentSHA256 string   `json:"parent_sha256"`
	ResultSHA256 string   `json:"result_sha256"`
	BlockerCodes []string `json:"blocker_codes"`
	Progress     bool     `json:"progress"`
}

type RepairStateV1 struct {
	SchemaVersion       string          `json:"schema_version"`
	InitialSHA256       string          `json:"initial_sha256"`
	CurrentSHA256       string          `json:"current_sha256"`
	InitialBlockerCodes []string        `json:"initial_blocker_codes"`
	CurrentBlockerCodes []string        `json:"current_blocker_codes"`
	Rounds              []RepairRoundV1 `json:"rounds"`
	ActivityRetryCount  int             `json:"activity_retry_count"`
	NoProgressRounds    int             `json:"no_progress_rounds"`
	Decision            string          `json:"decision"`
	StopReason          string          `json:"stop_reason,omitempty"`
}

type RepairRoundInputV1 struct {
	ParentSHA256 string   `json:"parent_sha256"`
	ResultSHA256 string   `json:"result_sha256"`
	BlockerCodes []string `json:"blocker_codes"`
}

func NewRepairStateV1(initialSHA256 string, blockerCodes []string) (RepairStateV1, error) {
	if !lowerSHA256PatternV1.MatchString(initialSHA256) {
		return RepairStateV1{}, errors.New("initial repair SHA-256 is invalid")
	}
	canonicalBlockers, err := canonicalBlockerCodesV1(blockerCodes)
	if err != nil {
		return RepairStateV1{}, err
	}
	if len(canonicalBlockers) == 0 {
		return RepairStateV1{}, errors.New("repair requires at least one initial blocker")
	}
	state := RepairStateV1{
		SchemaVersion:       RepairStateSchemaVersionV1,
		InitialSHA256:       initialSHA256,
		CurrentSHA256:       initialSHA256,
		InitialBlockerCodes: append([]string(nil), canonicalBlockers...),
		CurrentBlockerCodes: append([]string(nil), canonicalBlockers...),
		Rounds:              []RepairRoundV1{},
		Decision:            RepairDecisionActive,
	}
	return state, nil
}

// RecordActivityRetryV1 accounts for infrastructure/provider retries without
// consuming a logical repair round.
func RecordActivityRetryV1(state RepairStateV1) (RepairStateV1, error) {
	if err := state.Validate(); err != nil {
		return RepairStateV1{}, err
	}
	if state.Decision != RepairDecisionActive {
		return RepairStateV1{}, errors.New("cannot retry a terminal repair state")
	}
	next := cloneRepairStateV1(state)
	next.ActivityRetryCount++
	return next, nil
}

func ApplyRepairRoundV1(state RepairStateV1, input RepairRoundInputV1) (RepairStateV1, error) {
	if err := state.Validate(); err != nil {
		return RepairStateV1{}, err
	}
	if state.Decision != RepairDecisionActive {
		return RepairStateV1{}, errors.New("cannot append to a terminal repair state")
	}
	if len(state.Rounds) >= MaxRepairRoundsV1 {
		return RepairStateV1{}, errors.New("repair round limit already reached")
	}
	if input.ParentSHA256 != state.CurrentSHA256 {
		return RepairStateV1{}, fmt.Errorf("repair parent %q does not match immutable current revision %q", input.ParentSHA256, state.CurrentSHA256)
	}
	if !lowerSHA256PatternV1.MatchString(input.ResultSHA256) {
		return RepairStateV1{}, errors.New("repair result SHA-256 is invalid")
	}
	currentBlockers, err := canonicalBlockerCodesV1(input.BlockerCodes)
	if err != nil {
		return RepairStateV1{}, err
	}

	progress, newBlocker := repairRoundProgressV1(
		state.CurrentBlockerCodes,
		currentBlockers,
		input.ParentSHA256,
		input.ResultSHA256,
	)

	next := cloneRepairStateV1(state)
	next.CurrentSHA256 = input.ResultSHA256
	next.CurrentBlockerCodes = append([]string{}, currentBlockers...)
	next.Rounds = append(next.Rounds, RepairRoundV1{
		Round:        len(next.Rounds) + 1,
		ParentSHA256: input.ParentSHA256,
		ResultSHA256: input.ResultSHA256,
		BlockerCodes: append([]string{}, currentBlockers...),
		Progress:     progress,
	})
	if progress {
		next.NoProgressRounds = 0
	} else {
		next.NoProgressRounds++
	}

	switch {
	case newBlocker:
		next.Decision = RepairDecisionNoGo
		next.StopReason = RepairStopNewBlocker
	case len(currentBlockers) == 0:
		next.Decision = RepairDecisionSucceeded
		next.StopReason = RepairStopResolved
	case next.NoProgressRounds >= 2:
		next.Decision = RepairDecisionNoGo
		next.StopReason = RepairStopNoProgress
	case len(next.Rounds) >= MaxRepairRoundsV1:
		next.Decision = RepairDecisionNoGo
		next.StopReason = RepairStopMaxRounds
	}
	if err := next.Validate(); err != nil {
		return RepairStateV1{}, err
	}
	return next, nil
}

func (state RepairStateV1) Validate() error {
	if state.SchemaVersion != RepairStateSchemaVersionV1 {
		return fmt.Errorf("repair schema_version %q, want %q", state.SchemaVersion, RepairStateSchemaVersionV1)
	}
	if !lowerSHA256PatternV1.MatchString(state.InitialSHA256) || !lowerSHA256PatternV1.MatchString(state.CurrentSHA256) {
		return errors.New("repair initial/current SHA-256 is invalid")
	}
	initial, err := canonicalBlockerCodesV1(state.InitialBlockerCodes)
	if err != nil || len(initial) == 0 || !equalStringsV1(initial, state.InitialBlockerCodes) {
		return errors.New("repair initial blockers are invalid or non-canonical")
	}
	current, err := canonicalBlockerCodesV1(state.CurrentBlockerCodes)
	if err != nil || !equalStringsV1(current, state.CurrentBlockerCodes) {
		return errors.New("repair current blockers are invalid or non-canonical")
	}
	if state.Rounds == nil || len(state.Rounds) > MaxRepairRoundsV1 {
		return fmt.Errorf("repair rounds must be explicit and at most %d", MaxRepairRoundsV1)
	}
	if state.ActivityRetryCount < 0 || state.NoProgressRounds < 0 || state.NoProgressRounds > 2 {
		return errors.New("repair retry/no-progress counters are invalid")
	}
	parent := state.InitialSHA256
	previousBlockers := initial
	expectedNoProgress := 0
	expectedDecision := RepairDecisionActive
	expectedStopReason := ""
	for index, round := range state.Rounds {
		if round.Round != index+1 || round.ParentSHA256 != parent || !lowerSHA256PatternV1.MatchString(round.ResultSHA256) {
			return fmt.Errorf("repair round %d breaks immutable parent lineage", index+1)
		}
		codes, err := canonicalBlockerCodesV1(round.BlockerCodes)
		if err != nil || !equalStringsV1(codes, round.BlockerCodes) {
			return fmt.Errorf("repair round %d blocker codes are invalid or non-canonical", index+1)
		}
		progress, newBlocker := repairRoundProgressV1(previousBlockers, codes, round.ParentSHA256, round.ResultSHA256)
		if round.Progress != progress {
			return fmt.Errorf("repair round %d progress flag is not reproducible", index+1)
		}
		if progress {
			expectedNoProgress = 0
		} else {
			expectedNoProgress++
		}
		parent = round.ResultSHA256
		previousBlockers = codes
		switch {
		case newBlocker:
			expectedDecision = RepairDecisionNoGo
			expectedStopReason = RepairStopNewBlocker
		case len(codes) == 0:
			expectedDecision = RepairDecisionSucceeded
			expectedStopReason = RepairStopResolved
		case expectedNoProgress >= 2:
			expectedDecision = RepairDecisionNoGo
			expectedStopReason = RepairStopNoProgress
		case index+1 >= MaxRepairRoundsV1:
			expectedDecision = RepairDecisionNoGo
			expectedStopReason = RepairStopMaxRounds
		}
		if expectedDecision != RepairDecisionActive && index != len(state.Rounds)-1 {
			return fmt.Errorf("repair round %d reached terminal state before later rounds", index+1)
		}
	}
	if parent != state.CurrentSHA256 {
		return errors.New("repair current SHA-256 does not match round lineage")
	}
	if !equalStringsV1(previousBlockers, state.CurrentBlockerCodes) {
		return errors.New("repair current blockers do not match round lineage")
	}
	if expectedNoProgress != state.NoProgressRounds {
		return fmt.Errorf("repair no_progress_rounds=%d, want reproducible value %d", state.NoProgressRounds, expectedNoProgress)
	}
	if state.Decision != expectedDecision || state.StopReason != expectedStopReason {
		return fmt.Errorf("repair terminal state %q/%q, want %q/%q", state.Decision, state.StopReason, expectedDecision, expectedStopReason)
	}
	return nil
}

func CanonicalRepairStateV1(state RepairStateV1) ([]byte, string, error) {
	if err := state.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return nil, "", fmt.Errorf("encode canonical repair state: %w", err)
	}
	return encoded, sha256HexV1(encoded), nil
}

func canonicalBlockerCodesV1(codes []string) ([]string, error) {
	if codes == nil {
		return nil, errors.New("blocker codes must be an explicit array")
	}
	result := append([]string(nil), codes...)
	if result == nil {
		result = []string{}
	}
	sort.Strings(result)
	for index, code := range result {
		if !stableCodePatternV1.MatchString(code) || len(code) > 128 {
			return nil, fmt.Errorf("invalid repair blocker code %q", code)
		}
		if index > 0 && result[index-1] == code {
			return nil, fmt.Errorf("duplicate repair blocker code %q", code)
		}
	}
	return result, nil
}

func stringSetV1(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func repairRoundProgressV1(previousBlockers, currentBlockers []string, parentSHA256, resultSHA256 string) (bool, bool) {
	previousSet := stringSetV1(previousBlockers)
	newBlocker := false
	for _, code := range currentBlockers {
		if _, existed := previousSet[code]; !existed {
			newBlocker = true
			break
		}
	}
	strictReduction := len(currentBlockers) < len(previousBlockers)
	progress := resultSHA256 != parentSHA256 && strictReduction && !newBlocker
	return progress, newBlocker
}

func equalStringsV1(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneRepairStateV1(state RepairStateV1) RepairStateV1 {
	clone := state
	clone.InitialBlockerCodes = append([]string(nil), state.InitialBlockerCodes...)
	clone.CurrentBlockerCodes = append([]string(nil), state.CurrentBlockerCodes...)
	clone.Rounds = make([]RepairRoundV1, len(state.Rounds))
	for index, round := range state.Rounds {
		clone.Rounds[index] = round
		clone.Rounds[index].BlockerCodes = append([]string(nil), round.BlockerCodes...)
	}
	return clone
}
