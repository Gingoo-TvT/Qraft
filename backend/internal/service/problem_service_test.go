package service

import (
	"testing"
)

func TestProblemRepositoryListFilterExcludesQuarantineAndRejectedByDefault(t *testing.T) {
	defaultFilter := problemRepositoryListFilter(ListProblemsFilter{}, 20, 0)
	if !defaultFilter.ExcludeQuarantined {
		t.Fatal("default problem list must exclude quarantined candidates")
	}
	if !defaultFilter.ExcludeRejected {
		t.Fatal("default problem list must exclude rejected candidates")
	}

	status := "quarantined"
	operatorFilter := problemRepositoryListFilter(ListProblemsFilter{Status: &status}, 20, 0)
	if operatorFilter.ExcludeQuarantined || operatorFilter.ExcludeRejected || operatorFilter.Status == nil || *operatorFilter.Status != status {
		t.Fatalf("explicit quarantine operator filter was not preserved: %+v", operatorFilter)
	}

	status = "rejected"
	operatorFilter = problemRepositoryListFilter(ListProblemsFilter{Status: &status}, 20, 0)
	if operatorFilter.ExcludeQuarantined || operatorFilter.ExcludeRejected || operatorFilter.Status == nil || *operatorFilter.Status != status {
		t.Fatalf("explicit rejected operator filter was not preserved: %+v", operatorFilter)
	}
}
