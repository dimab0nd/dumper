// Command dumper dumps Postgres databases from Docker containers to S3.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dimab0nd/dumper/internal/backup"
	"github.com/dimab0nd/dumper/internal/config"
	"github.com/dimab0nd/dumper/internal/docker"
	"github.com/dimab0nd/dumper/internal/install"
	"github.com/dimab0nd/dumper/internal/store"
)

const defaultConfigPath = "/etc/dumper/config.yaml"

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "backup":
		os.Exit(runBackup(os.Args[2:]))
	case "check":
		os.Exit(runCheck(os.Args[2:]))
	case "install":
		os.Exit(runInstall(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  dumper backup [-config path] [-only name]
  dumper check [-config path]
  dumper install

Commands:
  backup   dump configured databases and upload them to S3
  check    verify the config, running containers, and bucket access
  install  install the binary, example config, and systemd units
`)
}

func runBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to YAML config")
	only := fs.String("only", "", "back up a single target by name")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: dumper backup [-config path] [-only name]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	return execute(*configPath, func(ctx context.Context, cfg *config.Config, rt *docker.Client, st *store.Store) error {
		return backup.Run(ctx, cfg, rt, st, *only, nil)
	})
}

func runInstall(args []string) int {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: dumper install\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if err := install.Install(install.Options{}); err != nil {
		log.Printf("install: %v", err)
		return 1
	}
	return 0
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", defaultConfigPath, "path to YAML config")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: dumper check [-config path]\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	return execute(*configPath, func(ctx context.Context, cfg *config.Config, rt *docker.Client, st *store.Store) error {
		return backup.Check(ctx, cfg, rt, st)
	})
}

func execute(configPath string, fn func(context.Context, *config.Config, *docker.Client, *store.Store) error) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("config: %v", err)
		return 1
	}
	if msg := config.PermWarning(configPath); msg != "" {
		log.Printf("warning: %s", msg)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dc, err := docker.New()
	if err != nil {
		log.Printf("%v", err)
		return 1
	}
	defer dc.Close()

	st, err := store.New(cfg.S3)
	if err != nil {
		log.Printf("%v", err)
		return 1
	}
	if err := fn(ctx, cfg, dc, st); err != nil {
		log.Printf("%v", err)
		return 1
	}
	return 0
}
