package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var migrationFilePattern = regexp.MustCompile(`^([0-9]+)_([A-Za-z0-9][A-Za-z0-9_-]*)\.sql$`)

// Migration is one immutable, checksummed SQL migration.
type Migration struct {
	Version  int64
	Name     string
	Checksum string
	SQL      string
}

// Discover reads and validates all *.sql migrations in dir.
func Discover(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %q: %w", dir, err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("migration %q is not a regular file", entry.Name())
		}

		matches := migrationFilePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration filename %q: expected <version>_<name>.sql", entry.Name())
		}

		version, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}

		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}

		migrations = append(migrations, newMigration(version, entry.Name(), string(contents)))
	}

	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}
	return migrations, nil
}

func newMigration(version int64, name, sql string) Migration {
	sum := sha256.Sum256([]byte(sql))
	return Migration{
		Version:  version,
		Name:     name,
		Checksum: hex.EncodeToString(sum[:]),
		SQL:      sql,
	}
}

func validateMigrations(migrations []Migration) error {
	if len(migrations) == 0 {
		return fmt.Errorf("no migration files found")
	}

	sort.Slice(migrations, func(i, j int) bool {
		if migrations[i].Version == migrations[j].Version {
			return migrations[i].Name < migrations[j].Name
		}
		return migrations[i].Version < migrations[j].Version
	})

	for i := 1; i < len(migrations); i++ {
		if migrations[i].Version == migrations[i-1].Version {
			return fmt.Errorf("duplicate migration version %d: %q and %q", migrations[i].Version, migrations[i-1].Name, migrations[i].Name)
		}
	}

	for i, migration := range migrations {
		expectedVersion := int64(i + 1)
		if migration.Version != expectedVersion {
			return fmt.Errorf("missing migration version %d before %q", expectedVersion, migration.Name)
		}

		matches := migrationFilePattern.FindStringSubmatch(migration.Name)
		if matches == nil {
			return fmt.Errorf("invalid migration filename %q: expected <version>_<name>.sql", migration.Name)
		}
		nameVersion, err := strconv.ParseInt(matches[1], 10, 64)
		if err != nil || nameVersion != migration.Version {
			return fmt.Errorf("migration %q version does not match %d", migration.Name, migration.Version)
		}
		if strings.TrimSpace(migration.SQL) == "" {
			return fmt.Errorf("migration %q is empty", migration.Name)
		}
		calculated := newMigration(migration.Version, migration.Name, migration.SQL).Checksum
		if migration.Checksum != calculated {
			return fmt.Errorf("migration %q has invalid checksum: got %s, calculated %s", migration.Name, migration.Checksum, calculated)
		}
	}

	return nil
}
