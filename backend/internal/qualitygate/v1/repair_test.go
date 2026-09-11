package qualitygate

import (
	"bytes"
	"strings"
	"testing"
)

func TestRepairControllerV1ActivityRetryDoesNotConsumeRound(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.correctness"})
	next, err := RecordActivityRetryV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Rounds) != 0 || next.ActivityRetryCount != 1 {
		t.Fatalf("retry state = %+v", next)
	}
	if len(state.Rounds) != 0 || state.ActivityRetryCount != 0 {
		t.Fatalf("retry mutated prior state: %+v", state)
	}
}

func TestRepairControllerV1StopsAfterTwoNoProgressRounds(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.correctness"})
	first, err := ApplyRepairRoundV1(state, RepairRoundInputV1{
		ParentSHA256: state.CurrentSHA256,
		ResultSHA256: strings.Repeat("2", 64),
		BlockerCodes: []string{"review.correctness"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Decision != RepairDecisionActive || first.NoProgressRounds != 1 || first.Rounds[0].Progress {
		t.Fatalf("first no-progress round = %+v", first)
	}
	second, err := ApplyRepairRoundV1(first, RepairRoundInputV1{
		ParentSHA256: first.CurrentSHA256,
		ResultSHA256: strings.Repeat("3", 64),
		BlockerCodes: []string{"review.correctness"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Decision != RepairDecisionNoGo || second.StopReason != RepairStopNoProgress || len(second.Rounds) != 2 {
		t.Fatalf("second no-progress round = %+v", second)
	}
}

func TestRepairControllerV1StopsOnNewBlocker(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.correctness"})
	next, err := ApplyRepairRoundV1(state, RepairRoundInputV1{
		ParentSHA256: state.CurrentSHA256,
		ResultSHA256: strings.Repeat("2", 64),
		BlockerCodes: []string{"review.correctness", "review.test_coverage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Decision != RepairDecisionNoGo || next.StopReason != RepairStopNewBlocker || len(next.Rounds) != 1 {
		t.Fatalf("new-blocker state = %+v", next)
	}
}

func TestRepairControllerV1StopsAtThreeRoundsAndPreservesParents(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.clarity", "review.correctness", "review.test_coverage"})
	inputs := []RepairRoundInputV1{
		{ParentSHA256: strings.Repeat("1", 64), ResultSHA256: strings.Repeat("2", 64), BlockerCodes: []string{"review.correctness", "review.test_coverage"}},
		{ParentSHA256: strings.Repeat("2", 64), ResultSHA256: strings.Repeat("3", 64), BlockerCodes: []string{"review.test_coverage"}},
		{ParentSHA256: strings.Repeat("3", 64), ResultSHA256: strings.Repeat("4", 64), BlockerCodes: []string{"review.test_coverage"}},
	}
	var err error
	for _, input := range inputs {
		state, err = ApplyRepairRoundV1(state, input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Decision != RepairDecisionNoGo || state.StopReason != RepairStopMaxRounds || len(state.Rounds) != MaxRepairRoundsV1 {
		t.Fatalf("three-round state = %+v", state)
	}
	for index, round := range state.Rounds {
		if round.ParentSHA256 != inputs[index].ParentSHA256 || round.ResultSHA256 != inputs[index].ResultSHA256 {
			t.Fatalf("round %d lineage changed: %+v", index+1, round)
		}
	}
}

func TestRepairControllerV1RejectsParentRewriteWithoutMutatingPriorState(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.correctness"})
	before, beforeSHA, err := CanonicalRepairStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ApplyRepairRoundV1(state, RepairRoundInputV1{
		ParentSHA256: strings.Repeat("9", 64),
		ResultSHA256: strings.Repeat("2", 64),
		BlockerCodes: []string{},
	})
	if err == nil || !strings.Contains(err.Error(), "immutable current revision") {
		t.Fatalf("parent rewrite error = %v", err)
	}
	after, afterSHA, err := CanonicalRepairStateV1(state)
	if err != nil {
		t.Fatal(err)
	}
	if beforeSHA != afterSHA || !bytes.Equal(before, after) {
		t.Fatal("failed parent rewrite mutated prior state")
	}
}

func TestRepairControllerV1CanResolveBeforeLimit(t *testing.T) {
	state := newRepairFixtureStateV1(t, []string{"review.correctness"})
	next, err := ApplyRepairRoundV1(state, RepairRoundInputV1{
		ParentSHA256: state.CurrentSHA256,
		ResultSHA256: strings.Repeat("2", 64),
		BlockerCodes: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Decision != RepairDecisionSucceeded || next.StopReason != RepairStopResolved || len(next.Rounds) != 1 {
		t.Fatalf("resolved state = %+v", next)
	}
}

func newRepairFixtureStateV1(t *testing.T, blockerCodes []string) RepairStateV1 {
	t.Helper()
	state, err := NewRepairStateV1(strings.Repeat("1", 64), blockerCodes)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
