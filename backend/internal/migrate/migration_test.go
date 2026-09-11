package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverOrdersAndChecksumsMigrations(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "002_second.sql", "SELECT 2;\n")
	writeMigration(t, dir, "README.md", "ignored")
	writeMigration(t, dir, "001_first.sql", "SELECT 1;\n")

	migrations, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("len(migrations) = %d, want 2", len(migrations))
	}
	if migrations[0].Version != 1 || migrations[0].Name != "001_first.sql" {
		t.Fatalf("first migration = %#v", migrations[0])
	}
	if migrations[1].Version != 2 || migrations[1].Name != "002_second.sql" {
		t.Fatalf("second migration = %#v", migrations[1])
	}
	if len(migrations[0].Checksum) != 64 {
		t.Fatalf("checksum length = %d, want 64", len(migrations[0].Checksum))
	}
	if migrations[0].Checksum == migrations[1].Checksum {
		t.Fatal("different SQL produced the same checksum")
	}
}

func TestDiscoverRejectsInvalidChains(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		wantErr string
	}{
		{
			name:    "no migrations",
			files:   map[string]string{"README.md": "none"},
			wantErr: "no migration files found",
		},
		{
			name:    "malformed filename",
			files:   map[string]string{"migration.sql": "SELECT 1;"},
			wantErr: "invalid migration filename",
		},
		{
			name: "duplicate version",
			files: map[string]string{
				"001_first.sql": "SELECT 1;",
				"001_other.sql": "SELECT 2;",
			},
			wantErr: "duplicate migration version 1",
		},
		{
			name: "missing version",
			files: map[string]string{
				"001_first.sql": "SELECT 1;",
				"003_third.sql": "SELECT 3;",
			},
			wantErr: "missing migration version 2",
		},
		{
			name:    "does not start at one",
			files:   map[string]string{"002_second.sql": "SELECT 2;"},
			wantErr: "missing migration version 1",
		},
		{
			name:    "empty migration",
			files:   map[string]string{"001_empty.sql": " \n\t"},
			wantErr: "is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, contents := range tt.files {
				writeMigration(t, dir, name, contents)
			}
			_, err := Discover(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Discover() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRepositoryMigrationChainIsContinuous(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	if len(migrations) < 13 {
		t.Fatalf("repository contains %d migrations, want at least the existing 13", len(migrations))
	}
	if migrations[len(migrations)-1].Version != int64(len(migrations)) {
		t.Fatalf("last migration version = %d, migration count = %d", migrations[len(migrations)-1].Version, len(migrations))
	}
}

func TestRepositoryMigrationChainIncludesReviewQuarantineLedger(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	latest := migrations[len(migrations)-1]
	if latest.Version < 18 {
		t.Fatalf("latest migration version = %d, want at least 18", latest.Version)
	}

	data, err := os.ReadFile("../../migrations/018_create_problem_quarantine_records.sql")
	if err != nil {
		t.Fatalf("read review quarantine migration: %v", err)
	}
	sql := string(data)
	for _, required := range []string{
		"CREATE TABLE problem_quarantine_records",
		"REFERENCES problems(id) ON DELETE RESTRICT",
		"operation_key          VARCHAR(512) NOT NULL UNIQUE",
		"review_result          JSONB NOT NULL",
		"review_text            TEXT NOT NULL",
		"source_ancestry        JSONB NOT NULL",
		"review_gate_change_id",
		"review_gate_version",
		"review_result_sha256",
		"'source_artifacts'",
		"trg_force_problem_review_quarantine_status",
		"trg_prevent_problem_review_quarantine_release",
		"problem_review_quarantine_status_immutable",
		"trg_prevent_problem_quarantine_record_mutation",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("review quarantine migration is missing %q", required)
		}
	}
}

func TestRepositoryMigrationChainIncludesEmbeddingVersionRegistry(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	latest := migrations[len(migrations)-1]
	if latest.Version < 19 {
		t.Fatalf("latest migration version = %d, want at least 19", latest.Version)
	}

	data, err := os.ReadFile("../../migrations/019_create_embedding_version_registry.sql")
	if err != nil {
		t.Fatalf("read embedding version registry migration: %v", err)
	}
	sql := string(data)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS embedding_model_versions",
		"CREATE TABLE IF NOT EXISTS embedding_active_pointers",
		"model_version_id UUID",
		"embedding_kind TEXT",
		"content_hash TEXT",
		"UNIQUE (problem_id, model_version_id, embedding_kind, content_hash)",
		"embedding_statement_content_hash",
		"trg_validate_problem_embedding_version",
		"trg_validate_embedding_active_pointer",
		"idx_problem_embeddings_statement_embedding",
		"idx_problem_embeddings_solution_embedding",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("embedding version registry migration is missing %q", required)
		}
	}
}

func TestRepositoryMigrationChainRoutesFindSimilarThroughVersionedEmbeddings(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	latest := migrations[len(migrations)-1]
	if latest.Version < 20 {
		t.Fatalf("latest migration version = %d, want at least 20", latest.Version)
	}

	data, err := os.ReadFile("../../migrations/020_route_find_similar_to_versioned_embeddings.sql")
	if err != nil {
		t.Fatalf("read versioned find_similar migration: %v", err)
	}
	sql := string(data)
	for _, required := range []string{
		"CREATE OR REPLACE FUNCTION find_similar_problems",
		"embedding_active_pointers",
		"problem_embeddings pe",
		"pe.model_version_id = active.model_version_id",
		"pe.embedding_kind = 'statement'",
		"problem_quarantine_records",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("versioned find_similar migration is missing %q", required)
		}
	}
	if strings.Contains(sql, "p.embedding") {
		t.Fatalf("versioned find_similar migration still reads problems.embedding:\n%s", sql)
	}
}

func TestRepositoryMigrationChainIncludesEmbeddingBackfillLedger(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	latest := migrations[len(migrations)-1]
	if latest.Version < 21 {
		t.Fatalf("latest migration version = %d, want at least 21", latest.Version)
	}

	data, err := os.ReadFile("../../migrations/021_create_embedding_backfill_runs.sql")
	if err != nil {
		t.Fatalf("read embedding backfill migration: %v", err)
	}
	sql := string(data)
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS embedding_backfill_runs",
		"from_model_version_id",
		"to_model_version_id",
		"embedding_kind",
		"request_sha256",
		"last_problem_id",
		"CREATE TABLE IF NOT EXISTS embedding_backfill_failures",
		"PRIMARY KEY (run_key, problem_id, content_hash, embedding_kind)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("embedding backfill migration is missing %q", required)
		}
	}
}

func TestRepositoryMigrationChainIncludesProblemEditStaleness(t *testing.T) {
	migrations, err := Discover("../../migrations")
	if err != nil {
		t.Fatalf("Discover(repository migrations) error = %v", err)
	}
	latest := migrations[len(migrations)-1]
	if latest.Version < 22 {
		t.Fatalf("latest migration version = %d, want at least 22", latest.Version)
	}

	data, err := os.ReadFile("../../migrations/022_create_problem_edit_staleness.sql")
	if err != nil {
		t.Fatalf("read problem edit staleness migration: %v", err)
	}
	sql := string(data)
	for _, required := range []string{
		"ALTER TABLE solutions",
		"ADD COLUMN IF NOT EXISTS metadata_json",
		"ALTER TABLE testcases",
		"idx_solutions_problem_stale",
		"idx_testcases_problem_stale",
		"idx_problem_embeddings_problem_stale",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("problem edit staleness migration is missing %q", required)
		}
	}
}

func writeMigration(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
