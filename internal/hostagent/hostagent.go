// Package hostagent is the Host Agent: the process on each Host that manages
// that Host's Sandboxes through a Runtime. It serves two things:
//
//   - the HostAgent gRPC service, on a unix socket only the Control Plane
//     can reach (ADR 0001, ADR 0005);
//   - the Host data proxy, an HTTP endpoint that takes data-path calls
//     (Exec for now) straight from clients, authorized by each Sandbox's
//     Access Token, and forwards them to the Guest Agent (ADR 0007).
package hostagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gav1 "gpubox/gen/guestagent/v1"
	hav1 "gpubox/gen/hostagent/v1"
	"gpubox/internal/runtime"
)

// Config configures a Host Agent.
type Config struct {
	// SocketPath is where the HostAgent gRPC service listens.
	SocketPath string
	// DataAddr is the TCP address of the Host data proxy, e.g. 127.0.0.1:8081.
	DataAddr string
	Runtime  runtime.Runtime
}

// HostAgent is a running Host Agent.
type HostAgent struct {
	hav1.UnimplementedHostAgentServer
	cfg          Config
	grpc         *grpc.Server
	data         *http.Server
	dataEndpoint string

	mu        sync.Mutex
	sandboxes map[string]*sandbox
}

// sandbox is what the Host Agent holds for one Running Sandbox.
type sandbox struct {
	id              string
	accessTokenHash [sha256.Size]byte
	// guestToken is this incarnation's Guest Agent token, sent in Init.
	guestToken string
	conn       *grpc.ClientConn
	guest      gav1.GuestAgentClient
}

// Start starts serving both endpoints.
func Start(cfg Config) (*HostAgent, error) {
	h := &HostAgent{cfg: cfg, sandboxes: map[string]*sandbox{}}
	// sockaddr_un holds 104 bytes on macOS (108 on Linux), terminator included.
	if len(cfg.SocketPath) > 103 {
		return nil, fmt.Errorf("control socket path is %d bytes, longer than a unix socket path can be (103): %s", len(cfg.SocketPath), cfg.SocketPath)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0o700); err != nil {
		return nil, err
	}
	os.Remove(cfg.SocketPath)
	ctl, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on control socket: %w", err)
	}
	if err := os.Chmod(cfg.SocketPath, 0o600); err != nil {
		ctl.Close()
		return nil, err
	}
	dataLis, err := net.Listen("tcp", cfg.DataAddr)
	if err != nil {
		ctl.Close()
		return nil, fmt.Errorf("listen for the data proxy: %w", err)
	}
	h.dataEndpoint = "http://" + dataLis.Addr().String()

	h.grpc = grpc.NewServer()
	hav1.RegisterHostAgentServer(h.grpc, h)
	go h.grpc.Serve(ctl)

	h.data = &http.Server{Handler: h.dataProxy(), ReadHeaderTimeout: 10 * time.Second}
	go h.data.Serve(dataLis)
	return h, nil
}

// DataEndpoint is the base URL of this Host's data proxy.
func (h *HostAgent) DataEndpoint() string { return h.dataEndpoint }

// Stop stops serving. It leaves Sandboxes running; taking them back after a
// restart is reconciliation's job (ticket 10).
func (h *HostAgent) Stop() {
	h.grpc.Stop()
	h.data.Close()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, sb := range h.sandboxes {
		sb.conn.Close()
	}
}

func (h *HostAgent) CreateSandbox(ctx context.Context, req *hav1.CreateSandboxRequest) (*hav1.CreateSandboxResponse, error) {
	if req.SandboxId == "" || req.TemplateImage == "" || req.AccessToken == "" {
		return nil, status.Error(codes.InvalidArgument, "sandbox_id, template_image and access_token are required")
	}
	h.mu.Lock()
	_, exists := h.sandboxes[req.SandboxId]
	h.mu.Unlock()
	if exists {
		return nil, status.Errorf(codes.AlreadyExists, "sandbox %s already exists", req.SandboxId)
	}

	sb, err := h.startSandbox(ctx, req)
	if err != nil {
		// Remove whatever was started, even though ctx may be done.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if derr := h.cfg.Runtime.Destroy(cleanup, req.SandboxId); derr != nil {
			log.Printf("hostagent: cleaning up sandbox %s: %v", req.SandboxId, derr)
		}
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.Internal, err.Error())
	}

	h.mu.Lock()
	h.sandboxes[sb.id] = sb
	h.mu.Unlock()
	return &hav1.CreateSandboxResponse{DataEndpoint: h.dataEndpoint}, nil
}

// startSandbox starts the Sandbox, opens the channel to its Guest Agent and
// completes Init. Only then is the Sandbox Running.
func (h *HostAgent) startSandbox(ctx context.Context, req *hav1.CreateSandboxRequest) (*sandbox, error) {
	rt := h.cfg.Runtime
	if err := rt.Start(ctx, runtime.StartSpec{SandboxID: req.SandboxId, TemplateImage: req.TemplateImage}); err != nil {
		return nil, err
	}

	id := req.SandboxId
	// gRPC dials lazily, and redials through the same function if the
	// channel breaks, so a dropped `docker exec` is reopened on its own.
	conn, err := grpc.NewClient("passthrough:///"+id,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return rt.DialGuestAgent(ctx, id)
		}),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.Config{BaseDelay: 50 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: time.Second},
			MinConnectTimeout: 5 * time.Second,
		}),
	)
	if err != nil {
		return nil, err
	}
	sb := &sandbox{
		id:              id,
		accessTokenHash: sha256.Sum256([]byte(req.AccessToken)),
		guestToken:      randomToken(),
		conn:            conn,
		guest:           gav1.NewGuestAgentClient(conn),
	}
	if _, err := sb.guest.Init(ctx, &gav1.InitRequest{Token: sb.guestToken}, grpc.WaitForReady(true)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init: %w", err)
	}
	return sb, nil
}

func (h *HostAgent) DestroySandbox(ctx context.Context, req *hav1.DestroySandboxRequest) (*hav1.DestroySandboxResponse, error) {
	h.mu.Lock()
	sb := h.sandboxes[req.SandboxId]
	delete(h.sandboxes, req.SandboxId)
	h.mu.Unlock()
	if sb != nil {
		sb.conn.Close()
	}
	if err := h.cfg.Runtime.Destroy(ctx, req.SandboxId); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &hav1.DestroySandboxResponse{}, nil
}

// lookup returns the Sandbox if accessToken is its Access Token.
func (h *HostAgent) lookup(id, accessToken string) (*sandbox, error) {
	h.mu.Lock()
	sb := h.sandboxes[id]
	h.mu.Unlock()
	if sb == nil {
		return nil, errNotFound
	}
	sum := sha256.Sum256([]byte(accessToken))
	if accessToken == "" || subtle.ConstantTimeCompare(sum[:], sb.accessTokenHash[:]) != 1 {
		return nil, errBadToken
	}
	return sb, nil
}

var (
	errNotFound = errors.New("sandbox not found on this Host")
	errBadToken = errors.New("missing or wrong Access Token")
)

func (sb *sandbox) guestContext(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+sb.guestToken)
}

func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
