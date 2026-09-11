package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/Gingoo-TvT/Qraft/backend/internal/migrate"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("dir", envOrDefault("MIGRATIONS_DIR", "migrations"), "directory containing migration SQL files")
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL; defaults to DATABASE_URL or POSTGRES_* variables")
	timeout := flags.Duration("timeout", 10*time.Minute, "overall command timeout, including advisory lock wait")
	through := flags.Int64("through", 0, "last migration version to adopt (baseline only)")
	expectedSchemaSHA256 := flags.String("expected-schema-sha256", "", "trusted clean-reference catalog SHA-256 (baseline only)")
	actor := flags.String("actor", "", "operator or change-ticket identity (baseline only)")
	acknowledgeBaseline := flags.Bool("acknowledge-pre-ledger-adoption", false, "explicitly acknowledge pre-ledger schema adoption (baseline only)")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: migrate [flags] <up|status|catalog-hash|catalog-manifest|baseline>")
		fmt.Fprintln(stderr, "   or: migrate <command> [flags]")
		flags.PrintDefaults()
	}

	command, flagArgs := splitCommand(args)
	if err := flags.Parse(flagArgs); err != nil {
		return 2
	}
	if command == "" {
		if flags.NArg() != 1 {
			flags.Usage()
			return 2
		}
		command = flags.Arg(0)
	} else if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		flags.Usage()
		return 2
	}
	if !isCommand(command) {
		fmt.Fprintf(stderr, "unsupported command %q\n", command)
		flags.Usage()
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "timeout must be positive")
		return 2
	}
	baselineOptions := migrate.BaselineOptions{
		Through:              *through,
		ExpectedSchemaSHA256: *expectedSchemaSHA256,
		Actor:                *actor,
		Acknowledged:         *acknowledgeBaseline,
	}
	migrations, err := migrate.Discover(*directory)
	if err != nil {
		fmt.Fprintf(stderr, "discover migrations: %v\n", err)
		return 1
	}
	if command == "baseline" {
		if err := baselineOptions.Validate(migrations[len(migrations)-1].Version); err != nil {
			fmt.Fprintf(stderr, "baseline options: %v\n", err)
			return 2
		}
	}

	dsn := *databaseURL
	if dsn == "" {
		dsn, err = databaseURLFromEnvironment()
		if err != nil {
			fmt.Fprintf(stderr, "database configuration: %v\n", err)
			return 1
		}
	}
	pgxConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		fmt.Fprintf(stderr, "parse database URL: %v\n", err)
		return 1
	}

	db := stdlib.OpenDB(*pgxConfig)
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintf(stderr, "connect to database: %v\n", err)
		return 1
	}

	runner, err := migrate.NewRunner(db, migrations)
	if err != nil {
		fmt.Fprintf(stderr, "create migration runner: %v\n", err)
		return 1
	}

	switch command {
	case "up":
		applied, err := runner.Up(ctx)
		for _, migration := range applied {
			fmt.Fprintf(stdout, "applied %03d %s %s\n", migration.Version, migration.Name, migration.Checksum)
		}
		if err != nil {
			fmt.Fprintf(stderr, "migrate up after %d successful migration(s): %v\n", len(applied), err)
			return 1
		}
		fmt.Fprintf(stdout, "migration complete: %d applied, %d total\n", len(applied), len(migrations))
	case "status":
		status, err := runner.Status(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "migration status: %v\n", err)
			return 1
		}
		printStatus(stdout, status)
	case "catalog-hash":
		catalog, err := runner.CatalogHash(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "catalog hash: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "schema_sha256=%s algorithm=%s objects=%d\n", catalog.SHA256, catalog.Algorithm, catalog.ObjectCount)
	case "catalog-manifest":
		manifest, err := runner.CatalogManifest(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "catalog manifest: %v\n", err)
			return 1
		}
		if err := printCatalogManifest(stdout, manifest); err != nil {
			fmt.Fprintf(stderr, "catalog manifest: %v\n", err)
			return 1
		}
	case "baseline":
		result, err := runner.Baseline(ctx, baselineOptions)
		if err != nil {
			fmt.Fprintf(stderr, "baseline: %v\n", err)
			return 1
		}
		fmt.Fprintf(
			stdout,
			"baseline recorded: through=%03d schema_sha256=%s chain_sha256=%s algorithm=%s objects=%d actor=%q\n",
			result.ThroughVersion,
			result.Catalog.SHA256,
			result.MigrationChainSHA256,
			result.Catalog.Algorithm,
			result.Catalog.ObjectCount,
			result.Actor,
		)
	}

	return 0
}

func splitCommand(args []string) (string, []string) {
	if len(args) > 0 && isCommand(args[0]) {
		return args[0], args[1:]
	}
	return "", args
}

func isCommand(value string) bool {
	switch value {
	case "up", "status", "catalog-hash", "catalog-manifest", "baseline":
		return true
	default:
		return false
	}
}

func printCatalogManifest(output io.Writer, entries []migrate.CatalogManifestEntry) error {
	encoder := json.NewEncoder(output)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return fmt.Errorf("encode JSONL: %w", err)
		}
	}
	return nil
}

func printStatus(output io.Writer, entries []migrate.StatusEntry) {
	w := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tSTATE\tNAME\tCHECKSUM\tAPPLIED_AT")
	for _, entry := range entries {
		appliedAt := "-"
		if !entry.AppliedAt.IsZero() {
			appliedAt = entry.AppliedAt.UTC().Format(time.RFC3339Nano)
		}
		fmt.Fprintf(w, "%03d\t%s\t%s\t%s\t%s\n", entry.Version, entry.State, entry.Name, entry.Checksum, appliedAt)
	}
	w.Flush()
}

func databaseURLFromEnvironment() (string, error) {
	host := envOrDefault("POSTGRES_HOST", "localhost")
	port := envOrDefault("POSTGRES_PORT", "5432")
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", fmt.Errorf("invalid POSTGRES_PORT %q", port)
	}
	user := envOrDefault("POSTGRES_USER", "algoforge")
	database := envOrDefault("POSTGRES_DB", "algoforge")
	if host == "" || user == "" || database == "" {
		return "", fmt.Errorf("POSTGRES_HOST, POSTGRES_USER, and POSTGRES_DB must not be empty")
	}

	u := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, port),
		Path:   database,
	}
	password := os.Getenv("POSTGRES_PASSWORD")
	if password == "" {
		u.User = url.User(user)
	} else {
		u.User = url.UserPassword(user, password)
	}
	query := u.Query()
	query.Set("sslmode", envOrDefault("POSTGRES_SSLMODE", "disable"))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
