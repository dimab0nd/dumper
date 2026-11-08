// Package docker runs commands inside containers through the Docker Engine API.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

const stderrLimit = 64 * 1024

// Result is the outcome of a finished exec.
type Result struct {
	ExitCode int
	Stderr   string
}

// Client talks to the local Docker daemon.
type Client struct {
	api *client.Client
}

// New connects to the Docker daemon.
// The default socket is unix:///var/run/docker.sock; set DOCKER_HOST to override it.
func New() (*Client, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{api: cli}, nil
}

// Close releases the client.
func (c *Client) Close() error {
	return c.api.Close()
}

// Running reports whether the container exists and is running.
func (c *Client) Running(ctx context.Context, name string) error {
	info, err := c.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Container.State == nil || !info.Container.State.Running {
		return fmt.Errorf("container %s is not running", name)
	}
	return nil
}

// Env returns one environment variable from the container config.
func (c *Client) Env(ctx context.Context, name, key string) (string, bool, error) {
	info, err := c.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return "", false, fmt.Errorf("inspect %s: %w", name, err)
	}
	if info.Container.Config == nil {
		return "", false, nil
	}
	prefix := key + "="
	for _, item := range info.Container.Config.Env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix), true, nil
		}
	}
	return "", false, nil
}

// Exec runs cmd inside the container.
// The command's stdout is copied to stdout. stderr is returned on Result and capped at 64 KiB.
func (c *Client) Exec(ctx context.Context, name string, env, cmd []string, stdout io.Writer) (Result, error) {
	if stdout == nil {
		stdout = io.Discard
	}
	created, err := c.api.ExecCreate(ctx, name, client.ExecCreateOptions{
		AttachStdout: true,
		AttachStderr: true,
		Env:          env,
		Cmd:          cmd,
	})
	if err != nil {
		return Result{}, fmt.Errorf("exec create: %w", err)
	}
	attach, err := c.api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("exec attach: %w", err)
	}
	defer attach.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			attach.Close()
		case <-done:
		}
	}()

	var stderr cappedBuffer
	if _, err := stdcopy.StdCopy(stdout, &stderr, attach.Reader); err != nil {
		if ctx.Err() != nil {
			return Result{Stderr: stderr.String()}, ctx.Err()
		}
		return Result{Stderr: stderr.String()}, fmt.Errorf("exec stream: %w", err)
	}
	inspect, err := c.api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return Result{Stderr: stderr.String()}, fmt.Errorf("exec inspect: %w", err)
	}
	if inspect.Running {
		return Result{Stderr: stderr.String()}, errors.New("exec still running")
	}
	return Result{ExitCode: inspect.ExitCode, Stderr: stderr.String()}, nil
}

type cappedBuffer struct {
	buf bytes.Buffer
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	space := stderrLimit - c.buf.Len()
	if space > 0 {
		if len(p) > space {
			_, _ = c.buf.Write(p[:space])
		} else {
			_, _ = c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	return c.buf.String()
}
