package dump

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestPgDumpArgs(t *testing.T) {
	got := PgDumpArgs("app", "appdb")
	want := []string{
		"pg_dump",
		"--no-password",
		"-Fc",
		"--lock-wait-timeout=60s",
		"-U", "app",
		"-d", "appdb",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("PgDumpArgs() = %#v, want %#v", got, want)
	}
}

func TestListDatabasesArgs(t *testing.T) {
	got := ListDatabasesArgs("postgres")
	if got[0] != "psql" || got[len(got)-1] != ListDatabasesSQL {
		t.Fatalf("ListDatabasesArgs() = %#v", got)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-d "+maintenanceDatabase) {
		t.Fatalf("list command does not use the postgres database: %#v", got)
	}
	if !strings.Contains(ListDatabasesSQL, "NOT datistemplate") || !strings.Contains(ListDatabasesSQL, "datallowconn") {
		t.Fatalf("SQL = %q", ListDatabasesSQL)
	}
}

func TestExecEnv(t *testing.T) {
	withPassword := ExecEnv("secret")
	if !slices.Contains(withPassword, "PGPASSWORD=secret") {
		t.Fatalf("env = %#v", withPassword)
	}
	if !slices.Contains(withPassword, "PGCONNECT_TIMEOUT=30") {
		t.Fatalf("env = %#v", withPassword)
	}
	without := ExecEnv("")
	for _, item := range without {
		if strings.HasPrefix(item, "PGPASSWORD=") {
			t.Fatalf("empty password should omit PGPASSWORD, got %#v", without)
		}
	}
}

func TestParseDatabaseList(t *testing.T) {
	got := ParseDatabaseList(" app \n\npostgres\n  \nanalytics\n")
	want := []string{"app", "postgres", "analytics"}
	if !slices.Equal(got, want) {
		t.Fatalf("ParseDatabaseList() = %#v, want %#v", got, want)
	}
	if ParseDatabaseList(" \n\n") != nil {
		t.Fatal("expected nil for empty output")
	}
}

func TestValidateDatabaseName(t *testing.T) {
	valid := []string{"app", "my-db", "App DB"}
	for _, name := range valid {
		if err := ValidateDatabaseName(name); err != nil {
			t.Fatalf("ValidateDatabaseName(%q) unexpected error: %v", name, err)
		}
	}
	invalid := []string{"", ".", "..", "*", "a/b", `a\b`, "bad\nname"}
	for _, name := range invalid {
		if err := ValidateDatabaseName(name); err == nil {
			t.Fatalf("ValidateDatabaseName(%q) expected error", name)
		}
	}
}

func TestValidateDumpHeader(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		size    int64
		wantErr bool
	}{
		{name: "custom format", data: "PGDMPxxxx", size: 9},
		{name: "exact header", data: Magic, size: int64(len(Magic))},
		{name: "empty", data: "", size: 0, wantErr: true},
		{name: "short", data: "PG", size: 2, wantErr: true},
		{name: "wrong header", data: "HELLO", size: 5, wantErr: true},
		{name: "truncated reader", data: "PG", size: 10, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDumpHeader(bytes.NewReader([]byte(tt.data)), tt.size)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatal(err)
			}
		})
	}
}
