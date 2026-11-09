// Package install copies dumper, an example config, and systemd units onto the local host.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dimab0nd/dumper/deploy"
)

const (
	binRel      = "usr/local/bin/dumper"
	configRel   = "etc/dumper/config.yaml"
	etcRel      = "etc/dumper"
	stateRel    = "var/lib/dumper"
	unitDirRel  = "etc/systemd/system"
	serviceName = "dumper.service"
	timerName   = "dumper.timer"
	exampleName = "config.example.yaml"
)

// Options configures a host install. Zero values install onto the real
// system paths, using the current executable and systemctl.
type Options struct {
	// Root is the filesystem prefix. Empty means "/".
	Root string
	// Executable is the binary to copy. Empty means the current process.
	Executable string
	// Euid reports the effective user id. Empty means os.Geteuid.
	Euid func() int
	// Run executes systemctl. Empty means exec.Command.
	Run func(name string, args ...string) error
	// Stdout receives the operator instructions.
	Stdout io.Writer
}

// Install copies the binary, systemd units, and an example config onto the host.
// An existing config file is left unchanged. The timer is not enabled.
func Install(opts Options) error {
	if opts.euid() != 0 {
		return errors.New("must be root")
	}
	exe, err := opts.executable()
	if err != nil {
		return err
	}
	info, err := os.Stat(exe)
	if err != nil {
		return fmt.Errorf("executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", exe)
	}
	service, err := deploy.Files.ReadFile(serviceName)
	if err != nil {
		return fmt.Errorf("read %s: %w", serviceName, err)
	}
	timer, err := deploy.Files.ReadFile(timerName)
	if err != nil {
		return fmt.Errorf("read %s: %w", timerName, err)
	}
	example, err := deploy.Files.ReadFile(exampleName)
	if err != nil {
		return fmt.Errorf("read %s: %w", exampleName, err)
	}

	if err := mkdirPrivate(abs(opts.Root, etcRel)); err != nil {
		return err
	}
	if err := mkdirPrivate(abs(opts.Root, stateRel)); err != nil {
		return err
	}
	binPath := abs(opts.Root, binRel)
	if err := copyFile(exe, binPath, 0o755); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	unitDir := abs(opts.Root, unitDirRel)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", unitDir, err)
	}
	if err := writeAtomic(filepath.Join(unitDir, serviceName), service, 0o644); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(unitDir, timerName), timer, 0o644); err != nil {
		return err
	}
	configPath := abs(opts.Root, configRel)
	created, err := writeNewFile(configPath, example, 0o600)
	if err != nil {
		return err
	}
	run := opts.Run
	if run == nil {
		run = runCommand
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w", err)
	}
	if _, err := io.WriteString(opts.stdout(), instructions(configPath, created)); err != nil {
		return fmt.Errorf("write instructions: %w", err)
	}
	return nil
}

func (o Options) euid() int {
	if o.Euid != nil {
		return o.Euid()
	}
	return os.Geteuid()
}

func (o Options) executable() (string, error) {
	if o.Executable != "" {
		return o.Executable, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("executable: %w", err)
	}
	return exe, nil
}

func (o Options) stdout() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return os.Stdout
}

func instructions(configPath string, created bool) string {
	if created {
		return fmt.Sprintf("Installed dumper.\nEdit %s\nThen:\n  dumper check -config %s\n  systemctl enable --now dumper.timer\n", configPath, configPath)
	}
	return fmt.Sprintf("Installed dumper.\nLeft existing config %s in place.\nThen:\n  dumper check -config %s\n  systemctl enable --now dumper.timer\n", configPath, configPath)
}

func abs(root, rel string) string {
	if root == "" {
		root = string(os.PathSeparator)
	}
	return filepath.Join(root, rel)
}

func mkdirPrivate(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// copyFile installs src at dst. When both paths are the same file, it only
// fixes the mode: the running binary's inode is not rewritten in place.
func copyFile(src, dst string, mode os.FileMode) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", src)
	}
	dstInfo, err := os.Stat(dst)
	if err == nil && os.SameFile(srcInfo, dstInfo) {
		if err := os.Chmod(dst, mode); err != nil {
			return fmt.Errorf("chmod %s: %w", dst, err)
		}
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", dst, err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	return writeAtomicReader(dst, in, mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	return writeAtomicReader(path, bytes.NewReader(data), mode)
}

func writeAtomicReader(path string, r io.Reader, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := writeTemp(dir, r, mode)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

func writeTemp(dir string, r io.Reader, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, ".dumper-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmp := f.Name()
	success := false
	defer func() {
		if !success {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := io.Copy(f, r); err != nil {
		return "", fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Chmod(mode); err != nil {
		return "", fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", tmp, err)
	}
	success = true
	return tmp, nil
}

func writeNewFile(path string, data []byte, mode os.FileMode) (created bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			os.Remove(path)
		}
	}()
	if _, err = f.Write(data); err != nil {
		f.Close()
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return false, fmt.Errorf("chmod %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", path, err)
	}
	return true, nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
