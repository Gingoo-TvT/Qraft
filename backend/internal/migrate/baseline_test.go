package migrate

import (
	"context"
	"strings"
	"testing"
)

func TestBaselineRequiresExplicitBoundedOptions(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	tests := []struct {
		name    string
		options BaselineOptions
		max     int64
		wantErr string
	}{
		{
			name:    "through zero",
			options: BaselineOptions{ExpectedSchemaSHA256: validHash, Actor: "operator", Acknowledged: true},
			max:     3,
			wantErr: "--through must be positive",
		},
		{
			name:    "through after local chain",
			options: BaselineOptions{Through: 4, ExpectedSchemaSHA256: validHash, Actor: "operator", Acknowledged: true},
			max:     3,
			wantErr: "exceeds highest local migration",
		},
		{
			name:    "invalid expected hash",
			options: BaselineOptions{Through: 3, ExpectedSchemaSHA256: "not-a-hash", Actor: "operator", Acknowledged: true},
			max:     3,
			wantErr: "exactly 64 hexadecimal",
		},
		{
			name:    "missing actor",
			options: BaselineOptions{Through: 3, ExpectedSchemaSHA256: validHash, Acknowledged: true},
			max:     3,
			wantErr: "--actor is required",
		},
		{
			name:    "missing acknowledgement",
			options: BaselineOptions{Through: 3, ExpectedSchemaSHA256: validHash, Actor: "operator"},
			max:     3,
			wantErr: "--acknowledge-pre-ledger-adoption",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.options.Validate(tt.max)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestBaselineAdoptsPartialChainAndUpContinues(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;", "SELECT 3;")
	options := validBaselineOptions(state, 2)

	result, err := runner.Baseline(context.Background(), options)
	if err != nil {
		t.Fatalf("Baseline() error = %v", err)
	}
	if result.ThroughVersion != 2 || result.Catalog.SHA256 != options.ExpectedSchemaSHA256 {
		t.Fatalf("Baseline() result = %#v", result)
	}
	if len(result.MigrationChainSHA256) != 64 {
		t.Fatalf("chain hash length = %d, want 64", len(result.MigrationChainSHA256))
	}

	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	assertStates(t, status, StateApplied, StateApplied, StatePending)

	state.mu.Lock()
	if len(state.baseline) != 1 {
		state.mu.Unlock()
		t.Fatalf("baseline event count = %d, want 1", len(state.baseline))
	}
	event := state.baseline[0]
	state.mu.Unlock()
	if event.Through != 2 || event.Actor != options.Actor || event.Ack != BaselineAcknowledgement {
		t.Fatalf("baseline event = %#v", event)
	}

	if _, err := runner.Baseline(context.Background(), options); err == nil || !strings.Contains(err.Error(), "requires an empty ledger") {
		t.Fatalf("second Baseline() error = %v, want non-empty ledger error", err)
	}
	applied, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("Up() after baseline error = %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 3 {
		t.Fatalf("Up() applied = %#v, want migration 3", applied)
	}
}

func TestBaselineRejectsHashMismatchWithoutLedgerWrites(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")
	options := validBaselineOptions(state, 2)
	options.ExpectedSchemaSHA256 = strings.Repeat("0", 64)

	_, err := runner.Baseline(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "catalog checksum mismatch") {
		t.Fatalf("Baseline() error = %v, want hash mismatch", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.applied) != 0 || len(state.baseline) != 0 {
		t.Fatalf("mismatch wrote migrations/events = %d/%d", len(state.applied), len(state.baseline))
	}
}

func TestBaselineRejectsNonEmptyMigrationLedger(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;")
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	_, err := runner.Baseline(context.Background(), validBaselineOptions(state, 1))
	if err == nil || !strings.Contains(err.Error(), "schema_migrations=1") {
		t.Fatalf("Baseline() error = %v, want non-empty migration ledger", err)
	}
}

func TestBaselineAuditFailureRollsBackMigrationRows(t *testing.T) {
	db, state := newFakeDatabase(t)
	state.failBaseline = true
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")

	_, err := runner.Baseline(context.Background(), validBaselineOptions(state, 2))
	if err == nil || !strings.Contains(err.Error(), "record baseline audit event") {
		t.Fatalf("Baseline() error = %v, want audit failure", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.applied) != 0 || len(state.baseline) != 0 {
		t.Fatalf("failed transaction wrote migrations/events = %d/%d", len(state.applied), len(state.baseline))
	}
}

func TestBaselineAdvisoryLockAllowsOneConcurrentAdoption(t *testing.T) {
	db, state := newFakeDatabase(t)
	runnerA := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")
	runnerB := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")
	options := validBaselineOptions(state, 2)

	start := make(chan struct{})
	errors := make(chan error, 2)
	for _, runner := range []*Runner{runnerA, runnerB} {
		go func(runner *Runner) {
			<-start
			_, err := runner.Baseline(context.Background(), options)
			errors <- err
		}(runner)
	}
	close(start)

	successes := 0
	nonEmptyFailures := 0
	for range 2 {
		err := <-errors
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "requires an empty ledger"):
			nonEmptyFailures++
		default:
			t.Fatalf("unexpected concurrent Baseline() error = %v", err)
		}
	}
	if successes != 1 || nonEmptyFailures != 1 {
		t.Fatalf("concurrent results success/non-empty = %d/%d, want 1/1", successes, nonEmptyFailures)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.applied) != 2 || len(state.baseline) != 1 {
		t.Fatalf("concurrent baseline wrote migrations/events = %d/%d, want 2/1", len(state.applied), len(state.baseline))
	}
}

func TestMigrationControlDDLProtectsBaselineAuditEvents(t *testing.T) {
	for _, fragment := range []string{
		"BEFORE UPDATE OR DELETE OR TRUNCATE",
		"schema_migration_baseline_events is append-only",
	} {
		if !strings.Contains(ensureMigrationControlSQL, fragment) {
			t.Fatalf("migration control DDL does not contain %q", fragment)
		}
	}
}

func validBaselineOptions(state *fakeDBState, through int64) BaselineOptions {
	state.mu.Lock()
	catalog := append([]catalogObject(nil), state.catalog...)
	state.mu.Unlock()
	return BaselineOptions{
		Through:              through,
		ExpectedSchemaSHA256: hashCatalogObjects(catalog).SHA256,
		Actor:                "test-operator/change-123",
		Acknowledged:         true,
	}
}
