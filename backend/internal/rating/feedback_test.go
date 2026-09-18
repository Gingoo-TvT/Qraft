package rating

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func syntheticFeedback() Feedback {
	return Feedback{ID: uuid.New(), ProblemID: uuid.New(), SubjectHash: "version-one", ReviewerID: uuid.New(), Revision: 1, WindowMinutes: 60, Context: "practice", FeedbackInput: FeedbackInput{Outcome: "solved", IndependentMinutes: 30, ElapsedMinutes: 30}, UpdatedAt: time.Unix(10, 0)}
}
func TestFeedbackWindowCensoringAndAssistance(t *testing.T) {
	base := syntheticFeedback()
	at20, at80 := 20, 80
	cases := []struct {
		name   string
		change func(*Feedback)
		want   string
	}{
		{"independent within T", func(f *Feedback) {}, "solved"},
		{"late completion observed", func(f *Feedback) { f.ElapsedMinutes = 90; f.IndependentMinutes = 90; f.ObservedFullWindow = true }, "failure"},
		{"early departure", func(f *Feedback) { f.Outcome = "stopped" }, "censored"},
		{"not attempted", func(f *Feedback) { f.Outcome = "not_attempted"; f.ObservedFullWindow = true }, "censored"},
		{"ongoing incomplete", func(f *Feedback) { f.Outcome = "in_progress" }, "censored"},
		{"assisted completion", func(f *Feedback) {
			f.Assistance = []string{"hint"}
			f.AssistanceAfterMinutes = &at20
			f.IndependentMinutes = 20
		}, "censored"},
		{"help after window", func(f *Feedback) {
			f.Assistance = []string{"hint"}
			f.AssistanceAfterMinutes = &at80
			f.ElapsedMinutes = 100
			f.IndependentMinutes = 80
			f.ObservedFullWindow = true
		}, "failure"},
		{"self report failure without full observation", func(f *Feedback) { f.Outcome = "unsolved"; f.ElapsedMinutes = 60; f.IndependentMinutes = 60 }, "censored"},
		{"repeat", func(f *Feedback) { f.SeenBefore = true }, "censored"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.change(&f)
			if got := windowOutcome(f); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
func TestFeedbackUniquePeopleRevisionAndVersion(t *testing.T) {
	f := syntheticFeedback()
	old := f
	old.Outcome = "unsolved"
	old.Revision = 0
	inputs := []Feedback{old, f, f}
	s := SummarizeFeedback(inputs)
	if s.TotalReviewers != 1 || s.IndependentSolved != 1 || s.ReviewTriggered {
		t.Fatalf("%+v", s)
	}
	other := f
	other.SubjectHash = "version-two"
	if got := SummarizeFeedback(append(inputs, other)); got.EffectiveReviewers != 0 || got.ReviewTriggered {
		t.Fatalf("pooled versions %+v", got)
	}
}
func TestThirtyTriggerReviewNeverAutomaticNumericUpdate(t *testing.T) {
	base := syntheticFeedback()
	fs := []Feedback{}
	for i := 0; i < 30; i++ {
		f := base
		f.ReviewerID = uuid.New()
		fs = append(fs, f)
	}
	c := BuildCalibration(base.ProblemID, base.SubjectHash, fs)
	if c.Status != "needs_review" || !c.Summary.ReviewTriggered || c.SuggestedRating != nil {
		t.Fatalf("%+v", c)
	}
	repeated := BuildCalibration(base.ProblemID, base.SubjectHash, append(fs, fs...))
	if !reflect.DeepEqual(c, repeated) {
		t.Fatal("same snapshot counted twice or accumulated prior")
	}
	for i, j := 0, len(fs)-1; i < j; i, j = i+1, j-1 {
		fs[i], fs[j] = fs[j], fs[i]
	}
	if FeedbackSnapshotHash(fs) != c.FeedbackHash {
		t.Fatal("snapshot depends on input order")
	}
	if BuildCalibration(base.ProblemID, "other-version", fs).Summary.EffectiveReviewers != 0 {
		t.Fatal("wrong version pooled")
	}
}

func TestFeedbackSignalBudgetAndAttemptFiltering(t *testing.T) {
	base := syntheticFeedback()
	base.FirstRoute = strings.Repeat("推理", 1000)
	base.Notes = "not a model clue"
	fs := []Feedback{}
	for i := 0; i < 20; i++ {
		f := base
		f.ReviewerID = uuid.New()
		fs = append(fs, f)
	}
	got := FeedbackSignals(fs)
	if len(got) != 12 {
		t.Fatal(len(got))
	}
	for _, s := range got {
		if len([]rune(s.FirstRoute)) != 1000 || s.Source != "human_self_report" || s.Verification != "unverified" {
			t.Fatalf("%+v", s)
		}
	}
	base.SeenBefore = true
	if len(FeedbackSignals([]Feedback{base})) != 0 {
		t.Fatal("repeat promoted as first attempt")
	}
	base.SeenBefore = false
	base.Outcome = "not_attempted"
	if len(FeedbackSignals([]Feedback{base})) != 0 {
		t.Fatal("non-attempt promoted")
	}
}
