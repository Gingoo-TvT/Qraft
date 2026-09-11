package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	// AdvisoryLockID is stable across all AlgoForge migration runner instances.
	AdvisoryLockID int64 = 0x416c676f466f7267 // "AlgoForg"

	ensureMigrationControlSQL = `
CREATE TABLE IF NOT EXISTS public.schema_migrations (
    version    BIGINT PRIMARY KEY CHECK (version > 0),
    name       TEXT NOT NULL UNIQUE,
    checksum   TEXT NOT NULL CHECK (length(checksum) = 64),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS public.schema_migration_baseline_events (
    event_id                 SMALLINT PRIMARY KEY DEFAULT 1 CHECK (event_id = 1),
    through_version          BIGINT NOT NULL CHECK (through_version > 0),
    expected_schema_sha256   TEXT NOT NULL CHECK (length(expected_schema_sha256) = 64),
    actual_schema_sha256     TEXT NOT NULL CHECK (length(actual_schema_sha256) = 64),
    catalog_algorithm        TEXT NOT NULL,
    catalog_object_count     BIGINT NOT NULL CHECK (catalog_object_count >= 0),
    migration_chain_sha256   TEXT NOT NULL CHECK (length(migration_chain_sha256) = 64),
    actor                     TEXT NOT NULL CHECK (length(actor) > 0),
    acknowledgement          TEXT NOT NULL CHECK (length(acknowledgement) > 0),
    recorded_at               TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION public.prevent_schema_migration_baseline_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $migration_control$
BEGIN
    RAISE EXCEPTION 'schema_migration_baseline_events is append-only';
END;
$migration_control$;

DO $migration_control$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_catalog.pg_trigger
        WHERE tgname = 'trg_schema_migration_baseline_events_append_only'
          AND tgrelid = 'public.schema_migration_baseline_events'::regclass
          AND NOT tgisinternal
    ) THEN
        EXECUTE 'CREATE TRIGGER trg_schema_migration_baseline_events_append_only
                 BEFORE UPDATE OR DELETE OR TRUNCATE
                 ON public.schema_migration_baseline_events
                 FOR EACH STATEMENT
                 EXECUTE FUNCTION public.prevent_schema_migration_baseline_event_mutation()';
    END IF;
END;
$migration_control$;`
	selectAppliedMigrationsSQL = `
SELECT version, name, checksum, applied_at
FROM public.schema_migrations
ORDER BY version`
	insertAppliedMigrationSQL = `
INSERT INTO public.schema_migrations (version, name, checksum)
VALUES ($1, $2, $3)`
)

// AppliedMigration is the audit record stored in schema_migrations.
type AppliedMigration struct {
	Version   int64
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// State identifies whether a local migration has been applied.
type State string

const (
	StatePending State = "pending"
	StateApplied State = "applied"
)

// StatusEntry combines the immutable local migration with its database state.
type StatusEntry struct {
	Migration
	State     State
	AppliedAt time.Time
}

// Runner applies a validated migration chain to PostgreSQL.
type Runner struct {
	db         *sql.DB
	migrations []Migration
	lockID     int64
}

// NewRunner validates migrations before any database operation is attempted.
func NewRunner(db *sql.DB, migrations []Migration) (*Runner, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}

	chain := append([]Migration(nil), migrations...)
	if err := validateMigrations(chain); err != nil {
		return nil, fmt.Errorf("validate migrations: %w", err)
	}

	return &Runner{db: db, migrations: chain, lockID: AdvisoryLockID}, nil
}

// Up applies every pending migration in its own transaction. The migration
// audit row is committed in the same transaction as the SQL it describes.
func (r *Runner) Up(ctx context.Context) ([]Migration, error) {
	appliedNow := make([]Migration, 0)
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := r.validateApplied(applied); err != nil {
			return err
		}

		appliedByVersion := make(map[int64]struct{}, len(applied))
		for _, migration := range applied {
			appliedByVersion[migration.Version] = struct{}{}
		}

		for _, migration := range r.migrations {
			if _, ok := appliedByVersion[migration.Version]; ok {
				continue
			}
			if err := applyMigration(ctx, conn, migration); err != nil {
				return err
			}
			appliedNow = append(appliedNow, migration)
		}
		return nil
	})
	return appliedNow, err
}

// Status returns the reconciled state of every local migration. Drift and
// out-of-order database state are errors rather than informational output.
func (r *Runner) Status(ctx context.Context) ([]StatusEntry, error) {
	var result []StatusEntry
	err := r.withLock(ctx, func(conn *sql.Conn) error {
		applied, err := loadApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := r.validateApplied(applied); err != nil {
			return err
		}

		appliedByVersion := make(map[int64]AppliedMigration, len(applied))
		for _, migration := range applied {
			appliedByVersion[migration.Version] = migration
		}

		result = make([]StatusEntry, 0, len(r.migrations))
		for _, migration := range r.migrations {
			entry := StatusEntry{Migration: migration, State: StatePending}
			if record, ok := appliedByVersion[migration.Version]; ok {
				entry.State = StateApplied
				entry.AppliedAt = record.AppliedAt
			}
			result = append(result, entry)
		}
		return nil
	})
	return result, err
}

func (r *Runner) withLock(ctx context.Context, operation func(*sql.Conn) error) (err error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire database connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, r.lockID); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, unlockErr := conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, r.lockID); unlockErr != nil && err == nil {
			err = fmt.Errorf("release migration advisory lock: %w", unlockErr)
		}
	}()

	if _, err := conn.ExecContext(ctx, ensureMigrationControlSQL); err != nil {
		return fmt.Errorf("ensure migration control tables: %w", err)
	}
	return operation(conn)
}

func loadApplied(ctx context.Context, conn *sql.Conn) ([]AppliedMigration, error) {
	rows, err := conn.QueryContext(ctx, selectAppliedMigrationsSQL)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()

	var migrations []AppliedMigration
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.Checksum, &migration.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		migrations = append(migrations, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return migrations, nil
}

func (r *Runner) validateApplied(applied []AppliedMigration) error {
	localByVersion := make(map[int64]Migration, len(r.migrations))
	for _, migration := range r.migrations {
		localByVersion[migration.Version] = migration
	}

	appliedByVersion := make(map[int64]AppliedMigration, len(applied))
	for _, record := range applied {
		if _, exists := appliedByVersion[record.Version]; exists {
			return fmt.Errorf("duplicate applied migration version %d", record.Version)
		}
		local, ok := localByVersion[record.Version]
		if !ok {
			return fmt.Errorf("applied migration version %d (%s) has no local file", record.Version, record.Name)
		}
		if record.Name != local.Name {
			return fmt.Errorf("migration %d name drift: database=%q local=%q", record.Version, record.Name, local.Name)
		}
		if record.Checksum != local.Checksum {
			return fmt.Errorf("migration %d checksum drift: database=%s local=%s", record.Version, record.Checksum, local.Checksum)
		}
		appliedByVersion[record.Version] = record
	}

	pendingSeen := false
	for _, local := range r.migrations {
		_, isApplied := appliedByVersion[local.Version]
		if !isApplied {
			pendingSeen = true
			continue
		}
		if pendingSeen {
			return fmt.Errorf("applied migration %d follows an unapplied earlier version", local.Version)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, migration Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("execute migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx, insertAppliedMigrationSQL, migration.Version, migration.Name, migration.Checksum); err != nil {
		return fmt.Errorf("record migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d (%s): %w", migration.Version, migration.Name, err)
	}
	return nil
}
