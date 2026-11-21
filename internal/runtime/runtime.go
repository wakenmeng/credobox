// Package runtime defines what a Runtime does for the Host Agent: turn a
// Template into a running Sandbox with the Guest Agent as PID 1, connect a
// channel to that Guest Agent, and destroy the Sandbox. Docker is the first
// Runtime; gVisor and Firecracker implement the same interface later.
package runtime

import (
	"context"
	"net"
)

// StartSpec says which Sandbox to start and from which Template.
type StartSpec struct {
	SandboxID     string
	TemplateImage string
}

// Runtime is one isolation technology.
type Runtime interface {
	// Start creates and starts the Sandbox. The Guest Agent runs as PID 1
	// and waits for Init before starting the image's own entrypoint.
	Start(ctx context.Context, spec StartSpec) error

	// DialGuestAgent opens a new channel to the Sandbox's Guest Agent. The
	// Host Agent runs gRPC over it. The connection is always opened from the
	// outside in (ADR 0005).
	DialGuestAgent(ctx context.Context, sandboxID string) (net.Conn, error)

	// Destroy removes the Sandbox and everything it holds. Destroying a
	// Sandbox that does not exist is not an error.
	Destroy(ctx context.Context, sandboxID string) error
}
