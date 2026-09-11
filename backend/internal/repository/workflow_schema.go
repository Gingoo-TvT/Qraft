package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

const MinimumWorkflowDurabilityMigration = 16

var requiredWorkflowDurabilityRelations = []string{
	"public.schema_migrations",
	"public.provenance_artifacts",
	"public.provenance_artifact_bindings",
	"public.workflow_operations",
	"public.workflow_operation_steps",
	"public.workflow_outbox",
	"public.provider_effects",
}

// VerifyWorkflowDurabilitySchema prevents a worker from making provider or
// external side effects when its idempotency/provenance foundation is absent.
func VerifyWorkflowDurabilitySchema(ctx context.Context, db *pgxpool.Pool) error {
	for _, relation := range requiredWorkflowDurabilityRelations {
		var exists bool
		if err := db.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&exists); err != nil {
			return fmt.Errorf("checking required workflow relation %s: %w", relation, err)
		}
		if !exists {
			return fmt.Errorf("required workflow relation %s is missing", relation)
		}
	}
	var migration int
	if err := db.QueryRow(ctx, `SELECT COALESCE(MAX(version),0) FROM public.schema_migrations`).Scan(&migration); err != nil {
		return fmt.Errorf("checking workflow durability migration version: %w", err)
	}
	if migration < MinimumWorkflowDurabilityMigration {
		return fmt.Errorf("workflow durability requires migration %d, database is at %d", MinimumWorkflowDurabilityMigration, migration)
	}
	return nil
}
