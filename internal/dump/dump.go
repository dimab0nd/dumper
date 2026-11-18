// Package dump builds pg_dump commands and checks custom-format archives.
package dump

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// Magic is the header of a Postgres custom-format archive.
	Magic = "PGDMP"

	// ListDatabasesSQL returns every connectable non-template database.
	ListDatabasesSQL = "SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate ORDER BY datname"

	maintenanceDatabase = "postgres"
)

// PgDumpArgs returns the argv for a custom-format dump inside the container.
func PgDumpArgs(user, database string) []string {
	return []string{
		"pg_dump",
		"--no-password",
		"-Fc",
		"--lock-wait-timeout=60s",
		"-U", user,
		"-d", database,
	}
}

// ListDatabasesArgs returns the argv that prints database names, one per line.
func ListDatabasesArgs(user string) []string {
	return []string{
		"psql",
		"--no-password",
		"-U", user,
		"-d", maintenanceDatabase,
		"-v", "ON_ERROR_STOP=1",
		"-At",
		"-c", ListDatabasesSQL,
	}
}

// ExecEnv is the environment passed to pg_dump and psql.
// PGPASSWORD is omitted when password is empty so local auth can work.
func ExecEnv(password string) []string {
	env := []string{"PGCONNECT_TIMEOUT=30"}
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return env
}

// ParseDatabaseList splits psql -At output into database names.
func ParseDatabaseList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

// ValidateDatabaseName rejects names that would break the object key layout.
func ValidateDatabaseName(name string) error {
	if name == "" || name == "." || name == ".." || name == "*" {
		return fmt.Errorf("invalid database name %q", name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("invalid database name %q", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid database name %q", name)
		}
	}
	return nil
}

// ValidateDumpHeader checks that r starts with a custom-format archive.
// size is the full object size, not the number of bytes left in r.
func ValidateDumpHeader(r io.Reader, size int64) error {
	if size < int64(len(Magic)) {
		return errors.New("dump file is too small")
	}
	header := make([]byte, len(Magic))
	if _, err := io.ReadFull(r, header); err != nil {
		return fmt.Errorf("read dump header: %w", err)
	}
	if string(header) != Magic {
		return fmt.Errorf("unexpected dump header %q", header)
	}
	return nil
}
