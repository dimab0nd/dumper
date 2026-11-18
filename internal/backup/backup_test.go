package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dimab0nd/dumper/internal/config"
	"github.com/dimab0nd/dumper/internal/docker"
	"github.com/dimab0nd/dumper/internal/dump"
)

func TestRunUploadsDump(t *testing.T) {
	rt := &fakeRuntime{
		env: map[string]string{"db/POSTGRES_PASSWORD": "s3cret-password"},
	}
	st := &fakeStorage{}
	cfg := testConfig(t)
	cfg.S3.RetentionDays = 14
	now := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	err := Run(context.Background(), cfg, rt, st, "", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if len(st.keys) != 1 || st.keys[0] != "vps-1/myapp/myapp/20261017T010203Z.dump" {
		t.Fatalf("keys = %#v", st.keys)
	}
	if !bytes.HasPrefix(st.bodies[0], []byte(dump.Magic)) {
		t.Fatalf("body = %q", st.bodies[0])
	}
	if len(st.prefixes) != 1 || st.prefixes[0] != "vps-1/myapp/myapp/" {
		t.Fatalf("prefixes = %#v", st.prefixes)
	}
	wantCutoff := now.Add(-14 * 24 * time.Hour)
	if !st.cutoffs[0].Equal(wantCutoff) {
		t.Fatalf("cutoff = %s, want %s", st.cutoffs[0], wantCutoff)
	}
	if len(rt.calls) != 1 || rt.calls[0].cmd[0] != "pg_dump" {
		t.Fatalf("calls = %#v", rt.calls)
	}
	foundPassword := false
	for _, item := range rt.calls[0].env {
		if item == "PGPASSWORD=s3cret-password" {
			foundPassword = true
		}
	}
	if !foundPassword {
		t.Fatalf("env = %#v", rt.calls[0].env)
	}
	if strings.Contains(logs.String(), "s3cret-password") {
		t.Fatalf("password leaked into logs: %s", logs.String())
	}
	if entries, err := os.ReadDir(cfg.Dump.TmpDir); err != nil || len(entries) != 0 {
		t.Fatalf("temp dir entries = %#v, err = %v", entries, err)
	}
}

func TestRunContinuesAfterTargetFailure(t *testing.T) {
	rt := &fakeRuntime{
		execFn: func(cmd []string, stdout io.Writer) (docker.Result, error) {
			if cmd[len(cmd)-1] == "broken" {
				return docker.Result{ExitCode: 1, Stderr: "lock timeout"}, nil
			}
			_, _ = stdout.Write([]byte(dump.Magic + "ok"))
			return docker.Result{}, nil
		},
	}
	st := &fakeStorage{}
	cfg := testConfig(t)
	cfg.Targets = []config.Target{
		{Name: "broken", Container: "db", Database: "broken", User: "app"},
		{Name: "ok", Container: "db", Database: "ok", User: "app"},
	}
	err := Run(context.Background(), cfg, rt, st, "", time.Now)
	if err == nil || !strings.Contains(err.Error(), "1 target(s) failed") {
		t.Fatalf("error = %v", err)
	}
	if len(st.keys) != 1 || !strings.Contains(st.keys[0], "/ok/") {
		t.Fatalf("keys = %#v", st.keys)
	}
}

func TestRunOnly(t *testing.T) {
	rt := &fakeRuntime{}
	st := &fakeStorage{}
	cfg := testConfig(t)
	cfg.Targets = []config.Target{
		{Name: "one", Container: "db", Database: "one", User: "app"},
		{Name: "two", Container: "db", Database: "two", User: "app"},
	}
	if err := Run(context.Background(), cfg, rt, st, "two", time.Now); err != nil {
		t.Fatal(err)
	}
	if len(st.keys) != 1 || !strings.Contains(st.keys[0], "/two/") {
		t.Fatalf("keys = %#v", st.keys)
	}
	if len(st.prefixes) != 0 {
		t.Fatalf("retention ran with retention_days=0: %#v", st.prefixes)
	}
	if err := Run(context.Background(), cfg, rt, st, "missing", time.Now); err == nil {
		t.Fatal("expected unknown target error")
	}
}

func TestRunWildcardDatabases(t *testing.T) {
	rt := &fakeRuntime{
		execFn: func(cmd []string, stdout io.Writer) (docker.Result, error) {
			if cmd[0] == "psql" {
				_, _ = io.WriteString(stdout, "alpha\nbeta\n")
				return docker.Result{}, nil
			}
			_, _ = stdout.Write([]byte(dump.Magic + "ok"))
			return docker.Result{}, nil
		},
	}
	st := &fakeStorage{}
	cfg := testConfig(t)
	cfg.Targets[0].Database = "*"
	if err := Run(context.Background(), cfg, rt, st, "", time.Now); err != nil {
		t.Fatal(err)
	}
	if len(st.keys) != 2 {
		t.Fatalf("keys = %#v", st.keys)
	}
	if !strings.Contains(st.keys[0], "/alpha/") || !strings.Contains(st.keys[1], "/beta/") {
		t.Fatalf("keys = %#v", st.keys)
	}
}

func TestRunRejectsBadHeader(t *testing.T) {
	rt := &fakeRuntime{
		execFn: func(cmd []string, stdout io.Writer) (docker.Result, error) {
			_, _ = stdout.Write([]byte("NOPE!"))
			return docker.Result{}, nil
		},
	}
	st := &fakeStorage{}
	cfg := testConfig(t)
	if err := Run(context.Background(), cfg, rt, st, "", time.Now); err == nil {
		t.Fatal("expected header error")
	}
	if len(st.keys) != 0 {
		t.Fatalf("uploaded keys = %#v", st.keys)
	}
}

func TestRunCleansTempFileWhenUploadFails(t *testing.T) {
	rt := &fakeRuntime{}
	st := &fakeStorage{uploadErr: errors.New("s3 down")}
	cfg := testConfig(t)
	if err := Run(context.Background(), cfg, rt, st, "", time.Now); err == nil {
		t.Fatal("expected upload error")
	}
	entries, err := os.ReadDir(cfg.Dump.TmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %d", len(entries))
	}
}

func TestCheck(t *testing.T) {
	cfg := testConfig(t)
	rt := &fakeRuntime{}
	st := &fakeStorage{}
	if err := Check(context.Background(), cfg, rt, st); err != nil {
		t.Fatal(err)
	}
	rt.running = map[string]error{"db": errors.New("container db is not running")}
	if err := Check(context.Background(), cfg, rt, st); err == nil {
		t.Fatal("expected running error")
	}
	rt.running = nil
	st.headErr = errors.New("denied")
	if err := Check(context.Background(), cfg, rt, st); err == nil {
		t.Fatal("expected bucket error")
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		S3: config.S3Config{
			Region: "us-east-1",
			Bucket: "backups",
			Prefix: "vps-1",
		},
		Dump: config.DumpConfig{TmpDir: t.TempDir()},
		Targets: []config.Target{{
			Name:      "myapp",
			Container: "db",
			Database:  "myapp",
			User:      "app",
		}},
	}
}

type execCall struct {
	container string
	env       []string
	cmd       []string
}

type fakeRuntime struct {
	running map[string]error
	env     map[string]string
	calls   []execCall
	execFn  func(cmd []string, stdout io.Writer) (docker.Result, error)
}

func (f *fakeRuntime) Running(_ context.Context, container string) error {
	if f.running == nil {
		return nil
	}
	return f.running[container]
}

func (f *fakeRuntime) Env(_ context.Context, container, key string) (string, bool, error) {
	value, ok := f.env[container+"/"+key]
	return value, ok, nil
}

func (f *fakeRuntime) Exec(_ context.Context, container string, env, cmd []string, stdout io.Writer) (docker.Result, error) {
	f.calls = append(f.calls, execCall{container: container, env: append([]string(nil), env...), cmd: append([]string(nil), cmd...)})
	if f.execFn != nil {
		return f.execFn(cmd, stdout)
	}
	_, _ = stdout.Write([]byte(dump.Magic + "payload"))
	return docker.Result{}, nil
}

type fakeStorage struct {
	keys      []string
	bodies    [][]byte
	prefixes  []string
	cutoffs   []time.Time
	headErr   error
	uploadErr error
}

func (f *fakeStorage) Upload(_ context.Context, key string, body io.Reader) error {
	data, _ := io.ReadAll(body)
	f.keys = append(f.keys, key)
	f.bodies = append(f.bodies, data)
	return f.uploadErr
}

func (f *fakeStorage) DeleteOlderThan(_ context.Context, prefix string, cutoff time.Time) (int, error) {
	f.prefixes = append(f.prefixes, prefix)
	f.cutoffs = append(f.cutoffs, cutoff)
	return 1, nil
}

func (f *fakeStorage) HeadBucket(context.Context) error {
	return f.headErr
}
