// Package guestagent is the Guest Agent: the process inside every Sandbox and
// its only operational entry point (ADR 0002). The Host Agent is its only
// client.
package guestagent

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gav1 "gpubox/gen/guestagent/v1"
)

// Options configures a Guest Agent.
type Options struct {
	// Workload is the image's original Entrypoint followed by its Cmd. The
	// Guest Agent starts it once the first Init completes, so no user process
	// runs before the Sandbox has its identity (ADR 0008). Empty means none.
	Workload []string
	// User is the image's USER, which the workload runs as. Empty means root.
	User string
	// EtcDir is where User is looked up (passwd, group). Default /etc.
	EtcDir string
}

// MaxOutputBytes caps what one Exec keeps of stdout, and of stderr. The rest
// is read and dropped, so the command still runs to completion and the Guest
// Agent's memory stays bounded; the response says what was cut. Streaming
// (ticket 04) lifts the cap.
const MaxOutputBytes = 1 << 20

// Agent serves the GuestAgent gRPC service.
type Agent struct {
	gav1.UnimplementedGuestAgentServer
	opts Options
	grpc *grpc.Server

	mu sync.Mutex
	// token is this incarnation's token from Init, kept only in memory.
	// Empty until Init completes.
	token string
	// workload is the image's entrypoint process, once started.
	workload *os.Process
}

// New returns an Agent that has not been Init-ed yet.
func New(opts Options) *Agent {
	a := &Agent{opts: opts}
	a.grpc = grpc.NewServer()
	gav1.RegisterGuestAgentServer(a.grpc, a)
	return a
}

// Serve accepts connections on lis until Stop is called.
func (a *Agent) Serve(lis net.Listener) error { return a.grpc.Serve(lis) }

// Stop closes every connection and the listener.
func (a *Agent) Stop() { a.grpc.Stop() }

func (a *Agent) Init(ctx context.Context, req *gav1.InitRequest) (*gav1.InitResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// The first Init is open: the socket is root-only and the Host Agent
	// sends it before any user process runs. Every later Init must prove it
	// comes from the Host Agent by carrying the current token.
	if a.token != "" && !hasToken(ctx, a.token) {
		return nil, status.Error(codes.Unauthenticated, "re-Init needs the current token")
	}
	first := a.token == ""
	a.token = req.Token
	if first && len(a.opts.Workload) > 0 {
		if err := a.startWorkload(); err != nil {
			return nil, status.Errorf(codes.Internal, "start the image's entrypoint: %v", err)
		}
	}
	return &gav1.InitResponse{}, nil
}

// startWorkload starts the image's own entrypoint as the image's USER, with
// the image's Env and WorkingDir, which it inherits from PID 1. PID 1 itself
// runs as root (the Runtime starts it with --user 0:0), so the USER is applied
// here, the way Docker would have. Its output goes to PID 1's, so `docker
// logs` shows it. When it exits the Sandbox keeps running: many images'
// default command (a shell, a REPL) exits at once without a TTY.
func (a *Agent) startWorkload() error {
	etc := a.opts.EtcDir
	if etc == "" {
		etc = "/etc"
	}
	id, err := resolveUser(a.opts.User, etc)
	if err != nil {
		return err
	}
	cmd := exec.Command(a.opts.Workload[0], a.opts.Workload[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if id.cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: id.cred}
		cmd.Env = withHome(os.Environ(), id.home)
	}
	exited, err := start(cmd)
	if err != nil {
		return err
	}
	a.workload = cmd.Process
	go func() {
		ws := <-exited
		log.Printf("guestagent: workload %q exited with code %d", a.opts.Workload, exitCode(ws))
	}()
	return nil
}

// withHome sets HOME, which Docker derives from USER and PID 1 got as root's.
func withHome(env []string, home string) []string {
	out := []string{"HOME=" + home}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "HOME=") {
			out = append(out, kv)
		}
	}
	return out
}

// Workload returns the running workload process, or nil.
func (a *Agent) Workload() *os.Process {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workload
}

func (a *Agent) Exec(ctx context.Context, req *gav1.ExecRequest) (*gav1.ExecResponse, error) {
	if err := a.authorize(ctx); err != nil {
		return nil, err
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, status.Errorf(codes.Internal, "pipe: %v", err)
	}
	cmd := exec.Command("/bin/sh", "-c", req.Command)
	cmd.Stdout, cmd.Stderr = outW, errW
	exited, err := start(cmd)
	// The child holds its own copies of the write ends; close ours so the
	// reads below end when the child (and anything it spawned) closes them.
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, status.Errorf(codes.Internal, "start command: %v", err)
	}

	var wg sync.WaitGroup
	stdout, stderr := &cappedBuffer{max: MaxOutputBytes}, &cappedBuffer{max: MaxOutputBytes}
	for _, p := range []struct {
		r   *os.File
		buf *cappedBuffer
	}{{outR, stdout}, {errR, stderr}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer p.r.Close()
			io.Copy(p.buf, p.r)
		}()
	}

	var ws syscall.WaitStatus
	select {
	case ws = <-exited:
	case <-ctx.Done():
		cmd.Process.Kill()
		<-exited
		// Don't wait for the readers: a background child may still hold the
		// pipes. Killing the whole process group comes with ticket 04.
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	wg.Wait()
	return &gav1.ExecResponse{
		Stdout:          stdout.buf.Bytes(),
		Stderr:          stderr.buf.Bytes(),
		ExitCode:        int32(exitCode(ws)),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

// cappedBuffer keeps the first max bytes written to it and drops the rest.
type cappedBuffer struct {
	max       int
	buf       bytes.Buffer
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); len(p) > room {
		b.buf.Write(p[:max(room, 0)])
		b.truncated = true
	} else {
		b.buf.Write(p)
	}
	return len(p), nil
}

// authorize admits a call only after Init, and only if it carries this
// incarnation's token.
func (a *Agent) authorize(ctx context.Context) error {
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	if token == "" {
		return status.Error(codes.FailedPrecondition, "Init has not completed")
	}
	if !hasToken(ctx, token) {
		return status.Error(codes.Unauthenticated, "missing or wrong token")
	}
	return nil
}

func hasToken(ctx context.Context, token string) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if subtle.ConstantTimeCompare([]byte(v), []byte("Bearer "+token)) == 1 {
			return true
		}
	}
	return false
}
