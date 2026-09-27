package activities

import (
	"github.com/Gingoo-TvT/Qraft/backend/internal/domain"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImportOJFormattingBoundToOriginalAndStoredStatement(t *testing.T) {
	source := domain.SourceProblem{ItemID: "fixture", Title: "Add", Statement: "Add 1 and 2 to get 3."}
	candidate := ojAnalysis{OJStatement: domain.OJStatement{Version: 1, Description: "求和。", Input: "两个整数。", Output: "和。", Constraints: "原题未指定额外数据范围。", Reason: "统一格式", Samples: []domain.OJSample{{Input: "1 2", Output: "3", Origin: "generated"}}}}
	out, err := applyOJAnalysis(source, candidate)
	require.NoError(t, err)
	require.Equal(t, source.Statement, out.Evidence.Original.Statement)
	in := StoreInput{PayloadVersion: StoreProblemTestManifestPayloadVersion, Statement: out.Statement, ImportSource: &out.Evidence, TestManifest: &TestManifestV1{DifferentialCheckedCount: 1}}
	require.NoError(t, validateImportStoreEvidence(in))
	in.Statement.Statement += "silently changed"
	in.ImportSource.FinalSHA256 = sha256Bytes([]byte(in.Statement.Statement))
	require.ErrorContains(t, validateImportStoreEvidence(in), "normalized source evidence")
}
