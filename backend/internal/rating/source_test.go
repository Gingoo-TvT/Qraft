package rating

import (
	"encoding/json"
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func testSourceReference() *SourceReference {
	return &SourceReference{Status: "verified", StatementSHA256: Digest([]byte("synthetic")), Difficulty: &domain.SourceDifficulty{Platform: "codeforces", Scale: "codeforces_rating", Value: "800", Label: "Codeforces 800", SourceURL: "https://codeforces.com/problemset/problem/900001/A", FetchedAt: time.Unix(100, 0)}}
}
func TestSourceRatingPrecedenceAndNativeCategoryBoundary(t *testing.T) {
	source := testSourceReference()
	report := Report{SourceReference: source, Paths: []Path{{Kind: "intended", Validation: "tested", ConstraintScope: "full"}}, Estimate: ReferenceEstimate{Status: "provisional", Representative: new(int)}}
	NormalizeReport(&report)
	require.Equal(t, "source_reference", report.Estimate.Status)
	require.Equal(t, 800, *report.Estimate.Representative)
	source.Difficulty.Platform, source.Difficulty.Scale, source.Difficulty.Value, source.Difficulty.SourceURL = "luogu", "luogu_level", "3", "https://www.luogu.com.cn/problem/P900001"
	_, ok := NativeRating(source)
	require.False(t, ok, "native ordinal is not a CF rating")
	report.SourceReference = testSourceReference()
	report.Disagreements = []string{"algorithm correctness unresolved"}
	NormalizeReport(&report)
	require.Nil(t, report.Estimate.Representative)
	require.NotNil(t, report.SourceReference, "source evidence is still visible while approval remains blocked")
	report.Disagreements = nil
	report.RuleVersion = LegacyRuleVersion
	NormalizeReport(&report)
	require.Equal(t, LegacyRuleVersion, report.RuleVersion)
	require.Nil(t, report.Estimate.Representative, "old reports must not acquire new scoring semantics")
}
func TestSourceSnapshotInvalidatesOnEditAndRejectsForgedHosts(t *testing.T) {
	source := testSourceReference()
	raw, _ := json.Marshal(map[string]any{"import_difficulty": map[string]any{"source_reference": source}})
	require.Equal(t, "verified", SourceFromMetadata(raw, "synthetic").Status)
	require.Equal(t, "stale", SourceFromMetadata(raw, "changed").Status)
	source.Difficulty.SourceURL = "https://codeforces.com.evil.test/problem/1"
	_, ok := NativeRating(source)
	require.False(t, ok)
}
func TestReferencePoolDoesNotLearnModelGuessesOrDuplicateSources(t *testing.T) {
	source := testSourceReference()
	good := Anchor{ID: uuid.New(), Basis: "external_source", SourceReference: source, Rating: 800, SourceURL: source.Difficulty.SourceURL, Family: "contest-one"}
	guessed := Anchor{ID: uuid.New(), Rating: 1800, SourceURL: "https://example.test/guess", Basis: "model"}
	duplicate := good
	duplicate.ID = uuid.New()
	require.Len(t, SelectAnchors([]Anchor{good, guessed, duplicate}, "", 6), 1)
	require.Empty(t, SelectAnchors([]Anchor{good}, good.SourceURL, 6), "target must not be its own anchor")
	require.Len(t, SelectAnchors([]Anchor{good}, "", 1), 1)
}

func TestNativeCategoryLearnsOnlyFromIndependentHumanDecisions(t *testing.T) {
	source := testSourceReference()
	source.Difficulty.Platform, source.Difficulty.Scale, source.Difficulty.Value, source.Difficulty.SourceURL = "luogu", "luogu_level", "3", "https://www.luogu.com.cn/problem/P900001"
	anchors := []Anchor{}
	for i, n := range []int{1200, 1300, 1400} {
		reference := *source
		d := *source.Difficulty
		d.SourceURL += "-" + string(rune('a'+i))
		reference.Difficulty = &d
		anchors = append(anchors, Anchor{ID: uuid.New(), Basis: "admin_decision", Rating: n, DecisionID: uuid.New(), SubjectHash: "current", ReviewedBy: "synthetic", ReviewedAt: time.Unix(100, 0), SourceURL: d.SourceURL, SourceReference: &reference, Family: d.SourceURL})
	}
	require.Nil(t, EstimateNativeCategory(source, anchors[:2]).Representative)
	result := EstimateNativeCategory(source, anchors)
	require.Equal(t, 1200, *result.Lower)
	require.Equal(t, 1400, *result.Upper)
	anchors[2].Basis = "model"
	require.Nil(t, EstimateNativeCategory(source, anchors).Representative)
}

func TestSourceIdentityDeduplicatesHumanAndUpstreamReferences(t *testing.T) {
	source := testSourceReference()
	upstream := Anchor{ID: uuid.New(), Basis: "external_source", SourceReference: source, Rating: 800, SourceURL: source.Difficulty.SourceURL, Family: "cf-contest"}
	human := Anchor{ID: uuid.New(), Basis: "admin_decision", SourceReference: source, Rating: 1000, SourceURL: "qraft://problems/synthetic", Family: "reviewed-arithmetic", DecisionID: uuid.New(), SubjectHash: "current", ReviewedBy: "synthetic", ReviewedAt: time.Unix(100, 0)}
	selected := SelectAnchors([]Anchor{upstream, human}, "", 6)
	require.Len(t, selected, 1)
	require.Equal(t, 1000, selected[0].Rating)
	require.Equal(t, "admin_decision", selected[0].Basis)
	alias := "https://codeforces.com/contest/900001/problem/A?locale=en"
	require.Empty(t, SelectAnchors([]Anchor{upstream, human}, alias, 6))
}
