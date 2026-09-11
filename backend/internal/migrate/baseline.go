package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	BaselineAcknowledgement = "pre-ledger schema adoption explicitly acknowledged"

	selectBaselineLedgerCountsSQL = `
SELECT
    (SELECT count(*) FROM public.schema_migrations),
    (SELECT count(*) FROM public.schema_migration_baseline_events)`
	insertBaselineEventSQL = `
INSERT INTO public.schema_migration_baseline_events (
    event_id, through_version, expected_schema_sha256, actual_schema_sha256,
    catalog_algorithm, catalog_object_count, migration_chain_sha256,
    actor, acknowledgement
) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8)`
)

// BaselineOptions are deliberately explicit because baseline adopts schema
// changes that were applied before the migration ledger existed.
type BaselineOptions struct {
	Through              int64
	ExpectedSchemaSHA256 string
	Actor                string
	Acknowledged         bool
}

// BaselineResult records the exact schema and migration chain adopted.
type BaselineResult struct {
	ThroughVersion       int64
	Catalog              CatalogHashResult
	MigrationChainSHA256 string
	Actor                string
}

// Validate checks baseline options without touching the database.
func (options BaselineOptions) Validate(maxVersion int64) error {
	if options.Through <= 0 {
		return fmt.Errorf("baseline --through must be positive")
	}
	if options.Through > maxVersion {
		return fmt.Errorf("baseline --through %d exceeds highest local migration %d", options.Through, maxVersion)
	}
	expected := strings.ToLower(strings.TrimSpace(options.ExpectedSchemaSHA256))
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("baseline --expected-schema-sha256 must be exactly 64 hexadecimal characters")
	}
	if strings.TrimSpace(options.Actor) == "" {
		return fmt.Errorf("baseline --actor is required")
	}
	if !options.Acknowledged {
		return fmt.Errorf("baseline requires --acknowledge-pre-ledger-adoption")
	}
	return nil
}

// Baseline adopts migrations 1..Through only when an empty ledger's catalog
// exactly matches the caller-supplied trusted reference hash.
func (r *Runner) Baseline(ctx context.Context, options BaselineOptions) (BaselineResult, error) {
	maxVersion := r.migrations[len(r.migrations)-1].Version
	if err := options.Validate(maxVersion); err != nil {
		return BaselineResult{}, err
	}
	options.ExpectedSchemaSHA256 = strings.ToLower(strings.TrimSpace(options.ExpectedSchemaSHA256))
	options.Actor = strings.TrimSpace(options.Actor)

	var result BaselineResult
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
		if err != nil {
			return fmt.Errorf("begin baseline transaction: %w", err)
		}
		defer tx.Rollback()

		var migrationCount, eventCount int64
		if err := tx.QueryRowContext(ctx, selectBaselineLedgerCountsSQL).Scan(&migrationCount, &eventCount); err != nil {
			return fmt.Errorf("inspect baseline ledger: %w", err)
		}
		if migrationCount != 0 || eventCount != 0 {
			return fmt.Errorf("baseline requires an empty ledger: schema_migrations=%d baseline_events=%d", migrationCount, eventCount)
		}

		catalog, err := catalogHash(ctx, tx)
		if err != nil {
			return err
		}
		if catalog.SHA256 != options.ExpectedSchemaSHA256 {
			return fmt.Errorf("catalog checksum mismatch: expected=%s actual=%s algorithm=%s objects=%d", options.ExpectedSchemaSHA256, catalog.SHA256, catalog.Algorithm, catalog.ObjectCount)
		}

		adopted := r.migrations[:options.Through]
		for _, migration := range adopted {
			if _, err := tx.ExecContext(ctx, insertAppliedMigrationSQL, migration.Version, migration.Name, migration.Checksum); err != nil {
				return fmt.Errorf("record baselined migration %d (%s): %w", migration.Version, migration.Name, err)
			}
		}

		chainHash := migrationChainHash(adopted)
		if _, err := tx.ExecContext(
			ctx,
			insertBaselineEventSQL,
			options.Through,
			options.ExpectedSchemaSHA256,
			catalog.SHA256,
			catalog.Algorithm,
			catalog.ObjectCount,
			chainHash,
			options.Actor,
			BaselineAcknowledgement,
		); err != nil {
			return fmt.Errorf("record baseline audit event: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit baseline transaction: %w", err)
		}

		result = BaselineResult{
			ThroughVersion:       options.Through,
			Catalog:              catalog,
			MigrationChainSHA256: chainHash,
			Actor:                options.Actor,
		}
		return nil
	})
	return result, err
}

func migrationChainHash(migrations []Migration) string {
	digest := sha256.New()
	writeHashField(digest, "migration-chain-v1")
	for _, migration := range migrations {
		var version [8]byte
		binary.BigEndian.PutUint64(version[:], uint64(migration.Version))
		digest.Write(version[:])
		writeHashField(digest, migration.Name)
		writeHashField(digest, migration.Checksum)
	}
	return hex.EncodeToString(digest.Sum(nil))
}
