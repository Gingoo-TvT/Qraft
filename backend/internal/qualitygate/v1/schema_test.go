package qualitygate

import (
	"bytes"
	"strings"
	"testing"
)

const validReviewJSONV1 = `{"schema_version":"algoforge.quality-review-assessment.v1","approved":true,"dimensions":{"clarity":{"score":7,"notes":"clear"},"correctness":{"score":8,"notes":"correct"},"test_coverage":{"score":9,"notes":"covered"},"difficulty_calibration":{"score":7,"notes":"calibrated"},"tag_accuracy":{"score":10,"notes":"accurate"}},"blockers":[]}`

func TestDecodeReviewAssessmentV1StrictContract(t *testing.T) {
	if _, err := DecodeReviewAssessmentV1([]byte(validReviewJSONV1)); err != nil {
		t.Fatalf("valid review assessment rejected: %v", err)
	}

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "unknown field",
			raw:  strings.Replace(validReviewJSONV1, `"approved":true`, `"unexpected":1,"approved":true`, 1),
			want: "unknown field",
		},
		{
			name: "nested duplicate field",
			raw:  strings.Replace(validReviewJSONV1, `"score":7`, `"score":7,"score":8`, 1),
			want: "duplicate JSON field",
		},
		{
			name: "trailing value",
			raw:  validReviewJSONV1 + `{}`,
			want: "trailing",
		},
		{
			name: "missing dimension",
			raw:  strings.Replace(validReviewJSONV1, `"clarity":{"score":7,"notes":"clear"},`, ``, 1),
			want: "clarity is required",
		},
		{
			name: "score below range",
			raw:  strings.Replace(validReviewJSONV1, `"score":7`, `"score":-1`, 1),
			want: "outside [0,10]",
		},
		{
			name: "score above range",
			raw:  strings.Replace(validReviewJSONV1, `"score":10`, `"score":11`, 1),
			want: "outside [0,10]",
		},
		{
			name: "missing explicit blockers",
			raw:  strings.Replace(validReviewJSONV1, `,"blockers":[]`, ``, 1),
			want: "explicit array",
		},
		{
			name: "blocker missing responsible asset",
			raw: strings.Replace(validReviewJSONV1, `"blockers":[]`,
				`"blockers":[{"code":"review.correctness","responsible_asset":"","witness":{"runner":"fixture.runner","fixture_ref":"cas://fixture","assertion":"must pass"}}]`, 1),
			want: "responsible_asset",
		},
		{
			name: "blocker missing executable witness",
			raw: strings.Replace(validReviewJSONV1, `"blockers":[]`,
				`"blockers":[{"code":"review.correctness","responsible_asset":"cas://statement","witness":{"runner":"","fixture_ref":"cas://fixture","assertion":"must pass"}}]`, 1),
			want: "witness runner",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeReviewAssessmentV1([]byte(test.raw))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCanonicalReviewAssessmentV1SortsBlockers(t *testing.T) {
	assessment := passingReviewAssessmentV1(true)
	assessment.Blockers = []ReviewBlockerV1{
		fixtureBlockerV1("review.z", "cas://z"),
		fixtureBlockerV1("review.a", "cas://a"),
	}
	first, firstSHA, err := CanonicalReviewAssessmentV1(assessment)
	if err != nil {
		t.Fatal(err)
	}
	assessment.Blockers[0], assessment.Blockers[1] = assessment.Blockers[1], assessment.Blockers[0]
	second, secondSHA, err := CanonicalReviewAssessmentV1(assessment)
	if err != nil {
		t.Fatal(err)
	}
	if firstSHA != secondSHA || !bytes.Equal(first, second) {
		t.Fatalf("canonical review changed with blocker order: %s/%s", firstSHA, secondSHA)
	}
}
