// Package backup dumps configured databases and uploads the archives.
package backup

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dimab0nd/dumper/internal/config"
	"github.com/dimab0nd/dumper/internal/docker"
	"github.com/dimab0nd/dumper/internal/dump"
	"github.com/dimab0nd/dumper/internal/store"
)

// Runtime runs commands in a database container.
type Runtime interface {
	Running(ctx context.Context, container string) error
	Env(ctx context.Context, container, key string) (string, bool, error)
	Exec(ctx context.Context, container string, env, cmd []string, stdout io.Writer) (docker.Result, error)
}

// Storage uploads archives and applies retention.
type Storage interface {
	Upload(ctx context.Context, key string, body io.Reader) error
	DeleteOlderThan(ctx context.Context, prefix string, cutoff time.Time) (int, error)
	HeadBucket(ctx context.Context) error
}

// Run dumps every selected target. One target failure does not stop the others.
// now may be nil; the current time is used then.
func Run(ctx context.Context, cfg *config.Config, rt Runtime, st Storage, only string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	targets, err := selectTargets(cfg.Targets, only)
	if err != nil {
		return err
	}
	if err := st.HeadBucket(ctx); err != nil {
		return err
	}
	log.Printf("backup started targets=%d", len(targets))
	failed := 0
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return finish(failed, err)
		}
		if err := backupTarget(ctx, cfg, target, rt, st, now); err != nil {
			if ctx.Err() != nil {
				return finish(failed, err)
			}
			log.Printf("target %s failed: %v", target.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d target(s) failed", failed)
	}
	log.Printf("backup finished targets=%d", len(targets))
	return nil
}

// Check verifies that every container is running and the bucket is reachable.
func Check(ctx context.Context, cfg *config.Config, rt Runtime, st Storage) error {
	failed := 0
	for _, target := range cfg.Targets {
		if err := rt.Running(ctx, target.Container); err != nil {
			log.Printf("check target=%s container=%s: %v", target.Name, target.Container, err)
			failed++
		}
	}
	if err := st.HeadBucket(ctx); err != nil {
		log.Printf("check bucket=%s: %v", cfg.S3.Bucket, err)
		failed++
	}
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	log.Printf("check ok bucket=%s targets=%d", cfg.S3.Bucket, len(cfg.Targets))
	return nil
}

func finish(failed int, err error) error {
	if failed > 0 {
		return fmt.Errorf("%d target(s) failed; %w", failed, err)
	}
	return err
}

func selectTargets(targets []config.Target, only string) ([]config.Target, error) {
	if only == "" {
		return targets, nil
	}
	for _, target := range targets {
		if target.Name == only {
			return []config.Target{target}, nil
		}
	}
	return nil, fmt.Errorf("unknown target %q", only)
}

func backupTarget(ctx context.Context, cfg *config.Config, target config.Target, rt Runtime, st Storage, now func() time.Time) error {
	if err := rt.Running(ctx, target.Container); err != nil {
		return err
	}
	password, err := resolvePassword(ctx, target, rt)
	if err != nil {
		return err
	}
	databases, err := resolveDatabases(ctx, target, password, rt)
	if err != nil {
		return err
	}
	failed := 0
	for _, database := range databases {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := backupDatabase(ctx, cfg, target, database, password, rt, st, now()); err != nil {
			if ctx.Err() != nil {
				return err
			}
			log.Printf("dump failed target=%s database=%s: %v", target.Name, database, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d database(s) failed", failed)
	}
	return nil
}

func resolvePassword(ctx context.Context, target config.Target, rt Runtime) (string, error) {
	if target.Password != "" {
		log.Printf("target=%s auth=config", target.Name)
		return target.Password, nil
	}
	value, ok, err := rt.Env(ctx, target.Container, "POSTGRES_PASSWORD")
	if err != nil {
		return "", err
	}
	if ok && value != "" {
		if strings.ContainsAny(value, "\r\n\x00") {
			return "", fmt.Errorf("container %s POSTGRES_PASSWORD contains a forbidden character", target.Container)
		}
		log.Printf("target=%s auth=container-env", target.Name)
		return value, nil
	}
	log.Printf("target=%s auth=local", target.Name)
	return "", nil
}

func resolveDatabases(ctx context.Context, target config.Target, password string, rt Runtime) ([]string, error) {
	if target.Database != "*" {
		return []string{target.Database}, nil
	}
	log.Printf("listing databases target=%s container=%s", target.Name, target.Container)
	var buf strings.Builder
	result, err := rt.Exec(ctx, target.Container, dump.ExecEnv(password), dump.ListDatabasesArgs(target.User), &buf)
	if err != nil {
		return nil, err
	}
	if err := commandError("psql", result); err != nil {
		return nil, err
	}
	names := dump.ParseDatabaseList(buf.String())
	if len(names) == 0 {
		return nil, fmt.Errorf("no databases to dump")
	}
	for _, name := range names {
		if err := dump.ValidateDatabaseName(name); err != nil {
			return nil, err
		}
	}
	return names, nil
}

func backupDatabase(ctx context.Context, cfg *config.Config, target config.Target, database, password string, rt Runtime, st Storage, at time.Time) error {
	key := store.ObjectKey(cfg.S3.Prefix, target.Name, database, at)
	if err := os.MkdirAll(cfg.Dump.TmpDir, 0o700); err != nil {
		return fmt.Errorf("tmp dir: %w", err)
	}
	file, err := os.CreateTemp(cfg.Dump.TmpDir, "dumper-*.dump")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()

	log.Printf("dumping target=%s database=%s container=%s", target.Name, database, target.Container)
	started := time.Now()
	result, err := rt.Exec(ctx, target.Container, dump.ExecEnv(password), dump.PgDumpArgs(target.User, database), file)
	if err != nil {
		return err
	}
	if err := commandError("pg_dump", result); err != nil {
		return err
	}
	if msg := strings.TrimSpace(result.Stderr); msg != "" {
		log.Printf("pg_dump warning target=%s database=%s: %s", target.Name, database, msg)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync dump: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat dump: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind dump: %w", err)
	}
	if err := dump.ValidateDumpHeader(file, info.Size()); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind dump: %w", err)
	}
	if err := st.Upload(ctx, key, file); err != nil {
		return err
	}
	log.Printf("uploaded target=%s database=%s key=%s bytes=%d duration=%s",
		target.Name, database, key, info.Size(), time.Since(started).Round(time.Millisecond))

	if cfg.S3.RetentionDays > 0 {
		prefix := store.DatabasePrefix(cfg.S3.Prefix, target.Name, database)
		cutoff := at.Add(-time.Duration(cfg.S3.RetentionDays) * 24 * time.Hour)
		removed, err := st.DeleteOlderThan(ctx, prefix, cutoff)
		if err != nil {
			return err
		}
		if removed > 0 {
			log.Printf("removed expired objects count=%d prefix=%s", removed, prefix)
		}
	}
	return nil
}

func commandError(command string, result docker.Result) error {
	if result.ExitCode == 0 {
		return nil
	}
	msg := strings.TrimSpace(result.Stderr)
	if msg == "" {
		return fmt.Errorf("%s exited with code %d", command, result.ExitCode)
	}
	return fmt.Errorf("%s exited with code %d: %s", command, result.ExitCode, msg)
}
