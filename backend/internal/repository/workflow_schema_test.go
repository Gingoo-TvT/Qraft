package repository

import "testing"

func TestWorkflowDurabilitySchemaContractTracksMigration(t *testing.T) {
	if MinimumWorkflowDurabilityMigration != 16 {
		t.Fatalf("minimum workflow durability migration = %d, want 16", MinimumWorkflowDurabilityMigration)
	}
	want := map[string]bool{
		"public.schema_migrations":            true,
		"public.provenance_artifacts":         true,
		"public.provenance_artifact_bindings": true,
		"public.workflow_operations":          true,
		"public.workflow_operation_steps":     true,
		"public.workflow_outbox":              true,
		"public.provider_effects":             true,
	}
	for _, relation := range requiredWorkflowDurabilityRelations {
		delete(want, relation)
	}
	if len(want) != 0 {
		t.Fatalf("schema preflight is missing relations: %v", want)
	}
}
