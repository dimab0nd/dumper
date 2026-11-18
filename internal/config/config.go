// Package config loads the dumper YAML configuration.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/dimab0nd/dumper/internal/dump"
	"gopkg.in/yaml.v3"
)

const DefaultTmpDir = "/var/lib/dumper"

// Config is the on-disk configuration. Do not log it: it may contain secrets.
type Config struct {
	S3      S3Config   `yaml:"s3"`
	Dump    DumpConfig `yaml:"dump"`
	Targets []Target   `yaml:"targets"`
}

// S3Config describes an S3-compatible bucket.
type S3Config struct {
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	Prefix          string `yaml:"prefix"`
	ForcePathStyle  bool   `yaml:"force_path_style"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	RetentionDays   int    `yaml:"retention_days"`
}

// DumpConfig controls local dump files.
type DumpConfig struct {
	TmpDir string `yaml:"tmp_dir"`
}

// Target is one database, or every non-template database when Database is "*".
type Target struct {
	Name      string `yaml:"name"`
	Container string `yaml:"container"`
	Database  string `yaml:"database"`
	User      string `yaml:"user"`
	Password  string `yaml:"password"`
}

// Load reads and validates a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// PermWarning reports a world- or group-readable config file.
func PermWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("config %s is readable by group or others; mode 0600 is recommended", path)
	}
	return ""
}

func (c *Config) normalize() error {
	c.S3.Endpoint = strings.TrimSpace(c.S3.Endpoint)
	c.S3.Region = strings.TrimSpace(c.S3.Region)
	c.S3.Bucket = strings.TrimSpace(c.S3.Bucket)
	c.S3.Prefix = strings.TrimSpace(c.S3.Prefix)
	c.Dump.TmpDir = strings.TrimSpace(c.Dump.TmpDir)
	if c.Dump.TmpDir == "" {
		c.Dump.TmpDir = DefaultTmpDir
	}
	if c.S3.Region == "" {
		return errors.New("s3.region is required")
	}
	if c.S3.Bucket == "" {
		return errors.New("s3.bucket is required")
	}
	if c.S3.RetentionDays < 0 {
		return errors.New("s3.retention_days must be >= 0")
	}
	if c.S3.Endpoint != "" {
		if err := validateEndpoint(c.S3.Endpoint); err != nil {
			return err
		}
	}
	if (c.S3.AccessKeyID == "") != (c.S3.SecretAccessKey == "") {
		return errors.New("s3.access_key_id and s3.secret_access_key must both be set")
	}
	if err := usableSecret(c.S3.SecretAccessKey); err != nil {
		return fmt.Errorf("s3.secret_access_key: %w", err)
	}
	if len(c.Targets) == 0 {
		return errors.New("at least one target is required")
	}
	seen := make(map[string]struct{}, len(c.Targets))
	for i := range c.Targets {
		target := &c.Targets[i]
		target.Name = strings.TrimSpace(target.Name)
		target.Container = strings.TrimSpace(target.Container)
		target.Database = strings.TrimSpace(target.Database)
		target.User = strings.TrimSpace(target.User)
		if err := validateTarget(target); err != nil {
			return fmt.Errorf("targets[%d]: %w", i, err)
		}
		if _, ok := seen[target.Name]; ok {
			return fmt.Errorf("duplicate target name %q", target.Name)
		}
		seen[target.Name] = struct{}{}
	}
	return nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("s3.endpoint must be an absolute http(s) URL")
	}
	return nil
}

func validateTarget(target *Target) error {
	if target.Name == "" {
		return errors.New("name is required")
	}
	if err := validateTargetName(target.Name); err != nil {
		return err
	}
	if target.Container == "" {
		return errors.New("container is required")
	}
	if err := rejectControlChars("container", target.Container); err != nil {
		return err
	}
	if target.Database == "" {
		return errors.New("database is required")
	}
	if target.Database != "*" {
		if err := dump.ValidateDatabaseName(target.Database); err != nil {
			return fmt.Errorf("database: %w", err)
		}
	}
	if target.User == "" {
		return errors.New("user is required")
	}
	if err := rejectControlChars("user", target.User); err != nil {
		return err
	}
	if err := usableSecret(target.Password); err != nil {
		return fmt.Errorf("password: %w", err)
	}
	return nil
}

func validateTargetName(name string) error {
	if name == "." || name == ".." {
		return fmt.Errorf("invalid target name %q", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return fmt.Errorf("target name %q must contain only letters, digits, '.', '_' and '-'", name)
		}
	}
	return nil
}

func rejectControlChars(field, value string) error {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}

func usableSecret(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("contains a forbidden character")
	}
	return nil
}
