package repository

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/rating"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRatingFeedbackRejectsContradictoryObservation(t *testing.T) {
	assistAt := 10
	cf := 1500
	future := time.Now().Add(48 * time.Hour)
	cases := []rating.FeedbackInput{
		{Outcome: "unsolved", ElapsedMinutes: 10, ObservedFullWindow: true},
		{Outcome: "not_attempted", ElapsedMinutes: 1},
		{Outcome: "solved", ElapsedMinutes: 20, IndependentMinutes: 30},
		{Outcome: "solved", ElapsedMinutes: 20, IndependentMinutes: 15, Assistance: []string{"hint"}, AssistanceAfterMinutes: &assistAt},
		{Outcome: "solved", Assistance: []string{"magic"}},
		{Outcome: "solved", CFRating: &cf},
		{Outcome: "solved", CFRating: &cf, CFRatingAt: &future},
	}
	for _, f := range cases {
		f.ResultSource = "self_report"
		require.ErrorIs(t, validateRatingFeedback(f, 60), rating.ErrInvalid)
	}
	require.NoError(t, validateRatingFeedback(rating.FeedbackInput{Outcome: "stopped", ElapsedMinutes: 10, IndependentMinutes: 10, ResultSource: "self_report"}, 60))
	require.NoError(t, validateRatingFeedback(rating.FeedbackInput{Outcome: "unsolved", ElapsedMinutes: 60, IndependentMinutes: 60, ObservedFullWindow: true, ResultSource: "self_report"}, 60))
}
