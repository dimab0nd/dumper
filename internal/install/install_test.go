package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/dimab0nd/dumper/deploy"
)

func TestInstallFresh(t *testing.T) {
	root := t.TempDir()
	exe := writeExe(t, "dumper-v1")
	opts, buf, calls := testOptions(root, exe)

	if err := Install(opts); err != nil {
		t.Fatal(err)
	}

	bin := abs(root, binRel)
	if string(readFile(t, bin)) != "dumper-v1" {
		t.Fatalf("binary = %q", readFile(t, bin))
	}
	if perm(t, bin) != 0o755 {
		t.Fatalf("binary mode = %o", perm(t, bin))
	}
	if perm(t, abs(root, etcRel)) != 0o700 {
		t.Fatalf("config dir mode = %o", perm(t, abs(root, etcRel)))
	}
	if perm(t, abs(root, stateRel)) != 0o700 {
		t.Fatalf("state dir mode = %o", perm(t, abs(root, stateRel)))
	}

	cfg := abs(root, configRel)
	example := asset(t, exampleName)
	if !bytes.Equal(readFile(t, cfg), example) {
		t.Fatal("config does not match the embedded example")
	}
	if perm(t, cfg) != 0o600 {
		t.Fatalf("config mode = %o", perm(t, cfg))
	}

	servicePath := filepath.Join(abs(root, unitDirRel), serviceName)
	timerPath := filepath.Join(abs(root, unitDirRel), timerName)
	service := asset(t, serviceName)
	if !bytes.Equal(readFile(t, servicePath), service) {
		t.Fatal("service unit does not match the embedded file")
	}
	if !bytes.Equal(readFile(t, timerPath), asset(t, timerName)) {
		t.Fatal("timer unit does not match the embedded file")
	}
	if perm(t, servicePath) != 0o644 || perm(t, timerPath) != 0o644 {
		t.Fatalf("unit modes = %o %o", perm(t, servicePath), perm(t, timerPath))
	}
	if !bytes.Contains(service, []byte("ExecStart="+abs("/", binRel)+" ")) {
		t.Fatal("service ExecStart does not match the install path")
	}
	if !bytes.Contains(service, []byte("-config "+abs("/", configRel))) {
		t.Fatal("service config path does not match the install path")
	}

	want := fmt.Sprintf("Installed dumper.\nEdit %s\nThen:\n  dumper check -config %s\n  systemctl enable --now dumper.timer\n", cfg, cfg)
	if buf.String() != want {
		t.Fatalf("stdout = %q", buf.String())
	}
	if len(*calls) != 1 || (*calls)[0][0] != "systemctl" || len((*calls)[0]) != 2 || (*calls)[0][1] != "daemon-reload" {
		t.Fatalf("systemctl calls = %#v", *calls)
	}
}

func TestInstallKeepsConfigAndReplacesUnits(t *testing.T) {
	root := t.TempDir()
	exe := writeExe(t, "dumper-v1")
	opts, buf, calls := testOptions(root, exe)
	if err := Install(opts); err != nil {
		t.Fatal(err)
	}

	cfg := abs(root, configRel)
	if err := os.WriteFile(cfg, []byte("secret: keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cfg, 0o640); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(abs(root, unitDirRel), serviceName)
	if err := os.WriteFile(servicePath, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	*calls = nil
	opts.Executable = writeExe(t, "dumper-v2")
	if err := Install(opts); err != nil {
		t.Fatal(err)
	}

	if string(readFile(t, cfg)) != "secret: keep\n" {
		t.Fatalf("config = %q", readFile(t, cfg))
	}
	if perm(t, cfg) != 0o640 {
		t.Fatalf("config mode = %o", perm(t, cfg))
	}
	if !bytes.Equal(readFile(t, servicePath), asset(t, serviceName)) {
		t.Fatal("service unit was not replaced")
	}
	if string(readFile(t, abs(root, binRel))) != "dumper-v2" {
		t.Fatalf("binary = %q", readFile(t, abs(root, binRel)))
	}
	want := fmt.Sprintf("Installed dumper.\nLeft existing config %s in place.\nThen:\n  dumper check -config %s\n  systemctl enable --now dumper.timer\n", cfg, cfg)
	if buf.String() != want {
		t.Fatalf("stdout = %q", buf.String())
	}
	if len(*calls) != 1 || (*calls)[0][1] != "daemon-reload" {
		t.Fatalf("systemctl calls = %#v", *calls)
	}
}

func TestInstallSameBinaryKeepsInode(t *testing.T) {
	root := t.TempDir()
	bin := abs(root, binRel)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("running"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := inode(t, bin)
	opts, _, _ := testOptions(root, bin)
	if err := Install(opts); err != nil {
		t.Fatal(err)
	}
	if inode(t, bin) != before {
		t.Fatal("rewrote the running binary inode")
	}
	if string(readFile(t, bin)) != "running" {
		t.Fatalf("binary = %q", readFile(t, bin))
	}
	if perm(t, bin) != 0o755 {
		t.Fatalf("binary mode = %o", perm(t, bin))
	}
}

func TestInstallRejectsNonRoot(t *testing.T) {
	root := t.TempDir()
	called := false
	err := Install(Options{
		Root:       root,
		Executable: "missing",
		Euid:       func() int { return 1 },
		Run: func(string, ...string) error {
			called = true
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("Install() error = %v", err)
	}
	if called {
		t.Fatal("systemctl was called")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("created files without root: %v", entries)
	}
}

func TestInstallSystemctlError(t *testing.T) {
	root := t.TempDir()
	exe := writeExe(t, "dumper-v1")
	buf := &bytes.Buffer{}
	err := Install(Options{
		Root:       root,
		Executable: exe,
		Euid:       func() int { return 0 },
		Stdout:     buf,
		Run: func(name string, args ...string) error {
			return errors.New("boom")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Install() error = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("stdout = %q", buf.String())
	}
	if _, err := os.Stat(abs(root, binRel)); err != nil {
		t.Fatal(err)
	}
}

func testOptions(root, exe string) (Options, *bytes.Buffer, *[][]string) {
	buf := &bytes.Buffer{}
	var calls [][]string
	opts := Options{
		Root:       root,
		Executable: exe,
		Euid:       func() int { return 0 },
		Stdout:     buf,
		Run: func(name string, args ...string) error {
			calls = append(calls, append([]string{name}, args...))
			return nil
		},
	}
	return opts, buf, &calls
}

func writeExe(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dumper")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("file info has no stat")
	}
	return stat.Ino
}

func asset(t *testing.T, name string) []byte {
	t.Helper()
	data, err := deploy.Files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
