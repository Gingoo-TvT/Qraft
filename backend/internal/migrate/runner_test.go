package migrate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunnerEmptyDatabaseUpStatusAndIdempotency(t *testing.T) {
	db, state := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")

	status, err := runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() before Up error = %v", err)
	}
	assertStates(t, status, StatePending, StatePending)

	applied, err := runner.Up(context.Background())
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("Up() applied %d migrations, want 2", len(applied))
	}

	applied, err = runner.Up(context.Background())
	if err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("second Up() applied %d migrations, want 0", len(applied))
	}

	status, err = runner.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() after Up error = %v", err)
	}
	assertStates(t, status, StateApplied, StateApplied)

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lockCount != 4 || state.unlockCount != 4 {
		t.Fatalf("advisory lock/unlock counts = %d/%d, want 4/4", state.lockCount, state.unlockCount)
	}
}

func TestRunnerFailedTransactionIsNotRecorded(t *testing.T) {
	db, state := newFakeDatabase(t)
	state.failSubstring = "FAIL MIGRATION"
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "FAIL MIGRATION;", "SELECT 3;")

	applied, err := runner.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "execute migration 2") {
		t.Fatalf("Up() error = %v, want migration 2 execution failure", err)
	}
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Fatalf("applied before failure = %#v, want only migration 1", applied)
	}

	state.mu.Lock()
	if len(state.applied) != 1 {
		state.mu.Unlock()
		t.Fatalf("database recorded %d migrations, want 1", len(state.applied))
	}
	if _, exists := state.applied[2]; exists {
		state.mu.Unlock()
		t.Fatal("failed migration was recorded")
	}
	state.failSubstring = ""
	state.mu.Unlock()

	applied, err = runner.Up(context.Background())
	if err != nil {
		t.Fatalf("Up() after fixing failure error = %v", err)
	}
	if len(applied) != 2 || applied[0].Version != 2 || applied[1].Version != 3 {
		t.Fatalf("applied after retry = %#v, want migrations 2 and 3", applied)
	}
}

func TestRunnerAdvisoryLockSerializesConcurrentUp(t *testing.T) {
	db, state := newFakeDatabase(t)
	runnerA := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")
	runnerB := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")

	type result struct {
		count int
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for _, runner := range []*Runner{runnerA, runnerB} {
		go func(runner *Runner) {
			<-start
			applied, err := runner.Up(context.Background())
			results <- result{count: len(applied), err: err}
		}(runner)
	}
	close(start)

	totalApplied := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent Up() error = %v", result.err)
		}
		totalApplied += result.count
	}
	if totalApplied != 2 {
		t.Fatalf("concurrent runners applied %d migrations in total, want 2", totalApplied)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.applied) != 2 {
		t.Fatalf("database contains %d migration records, want 2", len(state.applied))
	}
}

func TestRunnerRejectsChecksumAndNameDrift(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]Migration) []Migration
		wantErr string
	}{
		{
			name: "checksum drift",
			mutate: func(migrations []Migration) []Migration {
				migrations[0] = newMigration(1, "001_migration.sql", "SELECT 999;")
				return migrations
			},
			wantErr: "checksum drift",
		},
		{
			name: "name drift",
			mutate: func(migrations []Migration) []Migration {
				migrations[0] = newMigration(1, "001_renamed.sql", migrations[0].SQL)
				return migrations
			},
			wantErr: "name drift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, _ := newFakeDatabase(t)
			original := testMigrations("SELECT 1;")
			runner, err := NewRunner(db, original)
			if err != nil {
				t.Fatalf("NewRunner() error = %v", err)
			}
			if _, err := runner.Up(context.Background()); err != nil {
				t.Fatalf("initial Up() error = %v", err)
			}

			changed, err := NewRunner(db, tt.mutate(append([]Migration(nil), original...)))
			if err != nil {
				t.Fatalf("NewRunner(changed) error = %v", err)
			}
			_, err = changed.Status(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Status() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestRunnerRejectsAppliedVersionWithoutLocalFile(t *testing.T) {
	db, _ := newFakeDatabase(t)
	runner := newRunnerWithDatabase(t, db, "SELECT 1;", "SELECT 2;")
	if _, err := runner.Up(context.Background()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	shortRunner, err := NewRunner(db, testMigrations("SELECT 1;"))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	_, err = shortRunner.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "has no local file") {
		t.Fatalf("Status() error = %v, want missing local file error", err)
	}
}

func TestRunnerRejectsOutOfOrderAppliedState(t *testing.T) {
	db, state := newFakeDatabase(t)
	migrations := testMigrations("SELECT 1;", "SELECT 2;")
	state.applied[2] = AppliedMigration{
		Version: 2, Name: migrations[1].Name, Checksum: migrations[1].Checksum, AppliedAt: time.Now(),
	}

	runner, err := NewRunner(db, migrations)
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	_, err = runner.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "follows an unapplied earlier version") {
		t.Fatalf("Status() error = %v, want out-of-order error", err)
	}
}

func newRunnerWithDatabase(t *testing.T, db *sql.DB, statements ...string) *Runner {
	t.Helper()
	runner, err := NewRunner(db, testMigrations(statements...))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}
	return runner
}

func testMigrations(statements ...string) []Migration {
	migrations := make([]Migration, 0, len(statements))
	for i, statement := range statements {
		version := int64(i + 1)
		migrations = append(migrations, newMigration(version, migrationName(version), statement))
	}
	return migrations
}

func migrationName(version int64) string {
	return fmt.Sprintf("%03d_migration.sql", version)
}

func assertStates(t *testing.T, entries []StatusEntry, want ...State) {
	t.Helper()
	if len(entries) != len(want) {
		t.Fatalf("status length = %d, want %d", len(entries), len(want))
	}
	for i := range want {
		if entries[i].State != want[i] {
			t.Fatalf("status[%d] = %s, want %s", i, entries[i].State, want[i])
		}
	}
}

type fakeDBState struct {
	mu            sync.Mutex
	advisory      sync.Mutex
	applied       map[int64]AppliedMigration
	baseline      []fakeBaselineEvent
	catalog       []catalogObject
	failSubstring string
	failBaseline  bool
	lockCount     int
	unlockCount   int
}

type fakeConnector struct {
	state *fakeDBState
}

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakeConn{state: c.state}, nil
}

func (c *fakeConnector) Driver() driver.Driver {
	return fakeDriver{state: c.state}
}

type fakeDriver struct {
	state *fakeDBState
}

func (d fakeDriver) Open(string) (driver.Conn, error) {
	return &fakeConn{state: d.state}, nil
}

type fakeConn struct {
	state *fakeDBState
	tx    *fakeTx
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported by fake database")
}

func (c *fakeConn) Close() error { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.tx != nil {
		return nil, errors.New("transaction already active")
	}
	c.tx = &fakeTx{conn: c}
	return c.tx, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	normalized := strings.TrimSpace(query)
	switch {
	case strings.HasPrefix(normalized, "SELECT pg_advisory_lock"):
		c.state.advisory.Lock()
		c.state.mu.Lock()
		c.state.lockCount++
		c.state.mu.Unlock()
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(normalized, "SELECT pg_advisory_unlock"):
		c.state.mu.Lock()
		c.state.unlockCount++
		c.state.mu.Unlock()
		c.state.advisory.Unlock()
		return driver.RowsAffected(1), nil
	case normalized == strings.TrimSpace(ensureMigrationControlSQL):
		return driver.RowsAffected(0), nil
	case normalized == strings.TrimSpace(insertAppliedMigrationSQL):
		if c.tx == nil {
			return nil, errors.New("migration record inserted outside transaction")
		}
		if len(args) != 3 {
			return nil, errors.New("unexpected migration record arguments")
		}
		c.tx.pending = append(c.tx.pending, AppliedMigration{
			Version: args[0].Value.(int64), Checksum: args[2].Value.(string), Name: args[1].Value.(string), AppliedAt: time.Now().UTC(),
		})
		return driver.RowsAffected(1), nil
	case normalized == strings.TrimSpace(insertBaselineEventSQL):
		if c.tx == nil {
			return nil, errors.New("baseline event inserted outside transaction")
		}
		c.state.mu.Lock()
		failBaseline := c.state.failBaseline
		c.state.mu.Unlock()
		if failBaseline {
			return nil, errors.New("injected baseline audit failure")
		}
		if len(args) != 8 {
			return nil, errors.New("unexpected baseline event arguments")
		}
		c.tx.baseline = &fakeBaselineEvent{
			Through:   args[0].Value.(int64),
			Expected:  args[1].Value.(string),
			Actual:    args[2].Value.(string),
			Algorithm: args[3].Value.(string),
			Objects:   args[4].Value.(int64),
			Chain:     args[5].Value.(string),
			Actor:     args[6].Value.(string),
			Ack:       args[7].Value.(string),
		}
		return driver.RowsAffected(1), nil
	default:
		if c.tx == nil {
			return nil, errors.New("migration SQL executed outside transaction")
		}
		c.state.mu.Lock()
		failSubstring := c.state.failSubstring
		c.state.mu.Unlock()
		if failSubstring != "" && strings.Contains(query, failSubstring) {
			return nil, errors.New("injected migration failure")
		}
		return driver.RowsAffected(0), nil
	}
}

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	normalized := strings.TrimSpace(query)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()

	switch normalized {
	case strings.TrimSpace(selectAppliedMigrationsSQL):
		records := make([]AppliedMigration, 0, len(c.state.applied))
		for _, record := range c.state.applied {
			records = append(records, record)
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Version < records[j].Version })
		rows := make([][]driver.Value, 0, len(records))
		for _, record := range records {
			rows = append(rows, []driver.Value{record.Version, record.Name, record.Checksum, record.AppliedAt})
		}
		return &fakeRows{columns: []string{"version", "name", "checksum", "applied_at"}, rows: rows}, nil
	case strings.TrimSpace(selectBaselineLedgerCountsSQL):
		return &fakeRows{
			columns: []string{"migration_count", "event_count"},
			rows:    [][]driver.Value{{int64(len(c.state.applied)), int64(len(c.state.baseline))}},
		}, nil
	case strings.TrimSpace(catalogSnapshotSQL):
		objects := append([]catalogObject(nil), c.state.catalog...)
		sort.Slice(objects, func(i, j int) bool {
			if objects[i].Kind != objects[j].Kind {
				return objects[i].Kind < objects[j].Kind
			}
			return objects[i].Identity < objects[j].Identity
		})
		rows := make([][]driver.Value, 0, len(objects))
		for _, object := range objects {
			rows = append(rows, []driver.Value{object.Kind, object.Identity, object.Definition})
		}
		return &fakeRows{columns: []string{"kind", "identity", "definition"}, rows: rows}, nil
	default:
		return nil, errors.New("unexpected query")
	}
}

type fakeTx struct {
	conn     *fakeConn
	pending  []AppliedMigration
	baseline *fakeBaselineEvent
	closed   bool
}

func (tx *fakeTx) Commit() error {
	if tx.closed {
		return errors.New("transaction already closed")
	}
	tx.conn.state.mu.Lock()
	for _, migration := range tx.pending {
		tx.conn.state.applied[migration.Version] = migration
	}
	if tx.baseline != nil {
		tx.conn.state.baseline = append(tx.conn.state.baseline, *tx.baseline)
	}
	tx.conn.state.mu.Unlock()
	tx.closed = true
	tx.conn.tx = nil
	return nil
}

func (tx *fakeTx) Rollback() error {
	if tx.closed {
		return sql.ErrTxDone
	}
	tx.closed = true
	tx.conn.tx = nil
	return nil
}

type fakeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *fakeRows) Columns() []string {
	return r.columns
}

func (r *fakeRows) Close() error { return nil }

func (r *fakeRows) Next(values []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	row := r.rows[r.index]
	r.index++
	copy(values, row)
	return nil
}

type fakeBaselineEvent struct {
	Through   int64
	Expected  string
	Actual    string
	Algorithm string
	Objects   int64
	Chain     string
	Actor     string
	Ack       string
}

func newFakeDatabase(t *testing.T) (*sql.DB, *fakeDBState) {
	t.Helper()
	state := &fakeDBState{
		applied: make(map[int64]AppliedMigration),
		catalog: []catalogObject{
			{Kind: "column", Identity: "public.example.1.id", Definition: "type=uuid|not_null=true"},
			{Kind: "type", Identity: "public.problem_level", Definition: "kind=e|enum={syntax,algorithm}"},
		},
	}
	db := sql.OpenDB(&fakeConnector{state: state})
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	return db, state
}
