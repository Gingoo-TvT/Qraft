package activities

import (
	"strings"
	"testing"

	"go.temporal.io/sdk/testsuite"
)

func TestValidateActivityFailsClosedOnOutputMismatch(t *testing.T) {
	activities := New(&Dependencies{})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.ValidateActivity)
	encoded, err := env.ExecuteActivity(
		activities.ValidateActivity,
		SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"42\n"}},
		SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"41\n"}},
	)
	if err != nil {
		t.Fatalf("ValidateActivity() error = %v", err)
	}
	var result ValidationResult
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	if result.AllPassed || len(result.Mismatches) != 1 {
		t.Fatalf("ValidateActivity() result = %+v, want one mismatch and AllPassed=false", &result)
	}
	if result.Mismatches[0].TestIndex != 0 {
		t.Fatalf("mismatch test index = %d, want 0", result.Mismatches[0].TestIndex)
	}
}

func TestValidateActivityFailsClosedOnCountMismatch(t *testing.T) {
	activities := New(&Dependencies{})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.ValidateActivity)
	encoded, err := env.ExecuteActivity(
		activities.ValidateActivity,
		SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1\n", "2\n"}},
		SandboxResult{PayloadVersion: ActivityPayloadVersion, Outputs: []string{"1\n"}},
	)
	if err != nil {
		t.Fatalf("ValidateActivity() error = %v", err)
	}
	var result ValidationResult
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	if result.AllPassed || len(result.Mismatches) != 1 {
		t.Fatalf("ValidateActivity() result = %+v, want count mismatch and AllPassed=false", &result)
	}
	if result.Mismatches[0].TestIndex != -1 ||
		!strings.Contains(result.Mismatches[0].MainOutput, "count mismatch") {
		t.Fatalf("count mismatch payload = %+v", result.Mismatches[0])
	}
}

func TestValidateActivityFailsClosedOnEmptyComparison(t *testing.T) {
	activities := New(&Dependencies{})

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activities.ValidateActivity)
	encoded, err := env.ExecuteActivity(
		activities.ValidateActivity,
		SandboxResult{PayloadVersion: ActivityPayloadVersion},
		SandboxResult{PayloadVersion: ActivityPayloadVersion},
	)
	if err != nil {
		t.Fatalf("ValidateActivity() error = %v", err)
	}
	var result ValidationResult
	if err := encoded.Get(&result); err != nil {
		t.Fatal(err)
	}
	if result.AllPassed || len(result.Mismatches) != 1 {
		t.Fatalf("ValidateActivity() result = %+v, want empty-comparison mismatch and AllPassed=false", &result)
	}
	if result.Mismatches[0].TestIndex != -1 ||
		!strings.Contains(result.Mismatches[0].MainOutput, "no test cases") {
		t.Fatalf("empty-comparison payload = %+v", result.Mismatches[0])
	}
}
