package repository

import (
	"strings"
	"testing"
)

func TestPublicationIntegrityBlockingIssues(t *testing.T) {
	counts := PublicationIntegrityViolationCounts{
		PublishedProblems:                         99,
		PublishedMissingPublicationPolicyVersion:  1,
		PublishedMissingCurrentDefinitionArtifact: 1,
		PublishedDefinitionDeniedByPolicy:         1,
		PublishedPolicyVersionMismatch:            1,
		PublishedMissingDetailedSolution:          1,
		PublishedMissingMainSolution:              1,
		PublishedMissingBruteSolution:             1,
		PublishedMissingRunnableTests:             1,
		PublishedMissingActiveStatementVector:     1,
		PublishedDuplicateActiveStatementVector:   1,
		PublishedWithQuarantineRecord:             1,
		PublishedStaleProblem:                     1,
		PublishedStaleActiveStatementVector:       1,
		PublishedStaleSolutions:                   1,
		PublishedStaleTestcases:                   1,
	}
	if got, want := counts.BlockingIssues(), int64(15); got != want {
		t.Fatalf("BlockingIssues() = %d, want %d", got, want)
	}

	coverage := counts.Coverage()
	if coverage.PolicyVersion >= 1 {
		t.Fatalf("PolicyVersion coverage = %f, want below 1", coverage.PolicyVersion)
	}
}

func TestPublicationIntegritySQLCoversPublishedInvariants(t *testing.T) {
	sql := scanPublicationIntegritySQL()
	for _, required := range []string{
		"WHERE status = 'published'",
		"publication_policy_version",
		"provenance_artifact_bindings",
		"provenance_artifact_use_allowed",
		"artifact_role = 'definition'",
		"solutions s",
		"s.solution_type = 'main'",
		"s.solution_type = 'brute'",
		"testcases tc",
		"embedding_active_pointers",
		"problem_embeddings pe",
		"embedding_statement_content_hash",
		"problem_quarantine_records",
		"metadata_json ->> 'stale'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("publication integrity SQL is missing %q:\n%s", required, sql)
		}
	}
}
