package repository

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"testing"
)

type jsonTagsRegressionRows struct {
	value string
	read  bool
}

func (r *jsonTagsRegressionRows) Next() bool {
	if r.read {
		return false
	}
	r.read = true
	return true
}
func (r *jsonTagsRegressionRows) Err() error { return nil }
func (r *jsonTagsRegressionRows) Scan(dest ...interface{}) error {
	return pgtype.NewMap().Scan(pgtype.JSONBOID, pgtype.BinaryFormatCode, append([]byte{1}, []byte(r.value)...), dest[8])
}
func TestSimilarProblemReadsNativeJSONBTags(t *testing.T) {
	for _, raw := range []string{`["数组","标签,逗号"]`, `[]`, `null`} {
		t.Run(raw, func(t *testing.T) {
			problems, _, err := scanSimilarProblemRows(&jsonTagsRegressionRows{value: raw})
			require.NoError(t, err)
			require.Len(t, problems, 1)
			if raw[1:2] == `"` {
				require.Equal(t, []string{"数组", "标签,逗号"}, problems[0].Tags)
			} else {
				require.Empty(t, problems[0].Tags)
			}
		})
	}
}
