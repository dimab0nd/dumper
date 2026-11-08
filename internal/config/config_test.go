package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.S3.Bucket != "backups" || cfg.S3.Prefix != "vps-1" || !cfg.S3.ForcePathStyle {
		t.Fatalf("s3 = %+v", cfg.S3)
	}
	if cfg.Dump.TmpDir != DefaultTmpDir {
		t.Fatalf("tmp dir = %q", cfg.Dump.TmpDir)
	}
	if cfg.S3.RetentionDays != 0 {
		t.Fatalf("retention = %d", cfg.S3.RetentionDays)
	}
	if len(cfg.Targets) != 1 || cfg.Targets[0].Name != "myapp" || cfg.Targets[0].Database != "myapp" {
		t.Fatalf("targets = %+v", cfg.Targets)
	}
}

func TestLoadDefaultsAndWildcard(t *testing.T) {
	cfg := writeConfig(t, `
s3:
  region: eu-central-1
  bucket: backups
targets:
  - name: myapp
    container: myapp-postgres-1
    database: "*"
    user: postgres
`)
	if cfg.Dump.TmpDir != DefaultTmpDir {
		t.Fatalf("tmp dir = %q", cfg.Dump.TmpDir)
	}
	if cfg.Targets[0].Database != "*" {
		t.Fatalf("database = %q", cfg.Targets[0].Database)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "region",
			yaml: "s3:\n  bucket: b\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n",
			want: "s3.region",
		},
		{
			name: "bucket",
			yaml: "s3:\n  region: r\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n",
			want: "s3.bucket",
		},
		{
			name: "retention",
			yaml: "s3:\n  region: r\n  bucket: b\n  retention_days: -1\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n",
			want: "retention_days",
		},
		{
			name: "endpoint",
			yaml: "s3:\n  endpoint: s3.example.com\n  region: r\n  bucket: b\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n",
			want: "s3.endpoint",
		},
		{
			name: "half credentials",
			yaml: "s3:\n  region: r\n  bucket: b\n  access_key_id: id\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n",
			want: "secret_access_key",
		},
		{
			name: "no targets",
			yaml: "s3:\n  region: r\n  bucket: b\n",
			want: "at least one target",
		},
		{
			name: "duplicate",
			yaml: "s3:\n  region: r\n  bucket: b\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n  - name: a\n    container: c2\n    database: d\n    user: u\n",
			want: "duplicate",
		},
		{
			name: "bad target name",
			yaml: "s3:\n  region: r\n  bucket: b\ntargets:\n  - name: a/b\n    container: c\n    database: d\n    user: u\n",
			want: "target name",
		},
		{
			name: "newline password",
			yaml: "s3:\n  region: r\n  bucket: b\ntargets:\n  - name: a\n    container: c\n    database: d\n    user: u\n    password: \"bad\\npass\"\n",
			want: "password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestPermWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if PermWarning(path) == "" {
		t.Fatal("expected a permission warning")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if PermWarning(path) != "" {
		t.Fatal("expected no permission warning")
	}
}

func writeConfig(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
