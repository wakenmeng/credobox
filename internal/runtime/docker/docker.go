// Package docker is the Docker Runtime. It drives the docker CLI, so every
// step is a command you can also run by hand.
//
// What a Docker Sandbox is: one container whose entrypoint is the platform's
// Guest Agent, bind-mounted read-only from the Host (ADR 0003). The image's
// own Entrypoint and Cmd become the Guest Agent's arguments, and it starts
// them after Init (ADR 0008). No platform environment variable is put in the
// container config, so `docker commit` can never capture one.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"gpubox/internal/runtime"
)

// Paths inside the Sandbox.
const (
	guestAgentMount = "/.gpubox"
	guestAgentPath  = guestAgentMount + "/guestagent"
	// The Guest Agent's root-only socket lives on a tmpfs owned by the
	// container: unix sockets don't cross the macOS/VM boundary (ticket 00),
	// and tmpfs contents are never captured by `docker commit`.
	guestAgentRunDir = "/run/gpubox"

	// LabelSandboxID marks every container the platform owns.
	LabelSandboxID = "gpubox.sandbox"
)

// Config configures the Docker Runtime.
type Config struct {
	// GuestAgentDir is a directory on the Host holding the `guestagent`
	// binary built for the Docker VM's architecture.
	GuestAgentDir string
}

// Runtime is the Docker Runtime.
type Runtime struct{ cfg Config }

var _ runtime.Runtime = (*Runtime)(nil)

// New returns a Docker Runtime.
func New(cfg Config) *Runtime { return &Runtime{cfg: cfg} }

func containerName(sandboxID string) string { return "gpubox-" + sandboxID }

// imageConfig is the part of the image config the Template record keeps.
type imageConfig struct {
	Entrypoint []string
	Cmd        []string
}

func (r *Runtime) Start(ctx context.Context, spec runtime.StartSpec) error {
	img, err := r.templateConfig(ctx, spec.TemplateImage)
	if err != nil {
		return err
	}
	args := []string{
		"run", "--detach",
		"--name", containerName(spec.SandboxID),
		"--label", LabelSandboxID + "=" + spec.SandboxID,
		// No network until ticket 12 gives each Sandbox its own --internal,
		// isolated network. The Guest Agent channel needs none.
		"--network", "none",
		// The Guest Agent is PID 1 and needs root; it applies the image's
		// own config to the processes it starts.
		"--user", "0:0",
		"--entrypoint", guestAgentPath,
		"--mount", "type=bind,source=" + r.cfg.GuestAgentDir + ",target=" + guestAgentMount + ",readonly",
		"--tmpfs", guestAgentRunDir + ":rw,nosuid,nodev,noexec,mode=0700",
		spec.TemplateImage,
		"init", "--",
	}
	args = append(args, img.Entrypoint...)
	args = append(args, img.Cmd...)
	if _, err := docker(ctx, args...); err != nil {
		return fmt.Errorf("start sandbox: %w", err)
	}
	return nil
}

// templateConfig reads the image's original Entrypoint and Cmd, pulling the
// image first if it is not on the Host. Until Templates are registered
// (ticket 13), the image itself is the Template record.
func (r *Runtime) templateConfig(ctx context.Context, image string) (imageConfig, error) {
	out, err := docker(ctx, "image", "inspect", "--format", "{{json .Config}}", image)
	if err != nil {
		if _, perr := docker(ctx, "pull", "--quiet", image); perr != nil {
			return imageConfig{}, fmt.Errorf("pull template image %q: %w", image, perr)
		}
		if out, err = docker(ctx, "image", "inspect", "--format", "{{json .Config}}", image); err != nil {
			return imageConfig{}, fmt.Errorf("inspect template image %q: %w", image, err)
		}
	}
	var cfg imageConfig
	if err := json.Unmarshal(out, &cfg); err != nil {
		return imageConfig{}, fmt.Errorf("decode image config: %w", err)
	}
	return cfg, nil
}

func (r *Runtime) Destroy(ctx context.Context, sandboxID string) error {
	name := containerName(sandboxID)
	_, err := docker(ctx, "rm", "--force", "--volumes", name)
	switch {
	case err == nil, isNoSuchContainer(err):
		return nil
	case strings.Contains(err.Error(), "already in progress"):
		// Another caller is removing it (for example the Host Agent's own
		// cleanup after a timed-out create). Return only once it is gone.
		for {
			if _, err := docker(ctx, "container", "inspect", "--format", "{{.Id}}", name); isNoSuchContainer(err) {
				return nil
			} else if ctx.Err() != nil {
				return ctx.Err()
			}
			time.Sleep(50 * time.Millisecond)
		}
	default:
		return err
	}
}

func isNoSuchContainer(err error) bool {
	return err != nil && strings.Contains(err.Error(), "No such container")
}

// DialGuestAgent opens the phase 1 channel: a long-lived `docker exec -i`
// running `guestagent bridge`, which relays its stdin and stdout to the
// Guest Agent's socket (ADR 0002). Measured at 0.25 ms per round trip.
func (r *Runtime) DialGuestAgent(ctx context.Context, sandboxID string) (net.Conn, error) {
	// The process must outlive ctx, which only bounds the dial.
	cmd := exec.Command("docker", "exec", "--interactive", "--user", "0:0", containerName(sandboxID), guestAgentPath, "bridge")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start bridge: %w", err)
	}
	return &execConn{cmd: cmd, r: stdout, w: stdin, stderr: stderr}, nil
}

func docker(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("docker %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// execConn is a net.Conn made of a `docker exec -i` process's stdout and
// stdin. gRPC runs HTTP/2 over it as it would over a socket.
type execConn struct {
	cmd    *exec.Cmd
	r      io.ReadCloser
	w      io.WriteCloser
	stderr *lockedBuffer
}

func (c *execConn) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if errors.Is(err, io.EOF) {
		if msg := c.stderr.String(); msg != "" {
			return n, fmt.Errorf("bridge closed: %s", strings.TrimSpace(msg))
		}
	}
	return n, err
}

// lockedBuffer collects the bridge's stderr, which os/exec writes from its
// own goroutine while Read may look at it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (c *execConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *execConn) Close() error {
	c.w.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
	return nil
}

type execAddr struct{}

func (execAddr) Network() string { return "docker-exec" }
func (execAddr) String() string  { return "docker-exec" }

func (c *execConn) LocalAddr() net.Addr                { return execAddr{} }
func (c *execConn) RemoteAddr() net.Addr               { return execAddr{} }
func (c *execConn) SetDeadline(t time.Time) error      { return nil }
func (c *execConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *execConn) SetWriteDeadline(t time.Time) error { return nil }
