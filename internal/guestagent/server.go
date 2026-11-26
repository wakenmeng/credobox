// Package guestagent is the Guest Agent: the process inside every Sandbox and
// its only operational entry point (ADR 0002). The Host Agent is its only
// client.
package guestagent

import (
	"context"
	"crypto/subtle"
	"log"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gav1 "gpubox/gen/guestagent/v1"
	"gpubox/internal/utf8cut"
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

// Agent serves the GuestAgent gRPC service.
type Agent struct {
	gav1.UnimplementedGuestAgentServer
	opts Options
	grpc *grpc.Server

	mu sync.Mutex
	// token is this incarnation's token from Init, kept only in memory.
	// Empty until Init completes.
	token string
	// env is the Sandbox's default environment, from Init.
	env map[string]string
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
	if first {
		// Before any user process: Execs run as `user` (ADR 0009).
		if err := ensureUserAccount(a.etcDir()); err != nil {
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
	}
	a.token, a.env = req.Token, req.Env
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
	id, err := a.identity(a.opts.User)
	if err != nil {
		return err
	}
	cmd := exec.Command(a.opts.Workload[0], a.opts.Workload[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	env := []map[string]string{a.env}
	if id.cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: id.cred}
		env = []map[string]string{{"HOME": id.home}, a.env}
	}
	cmd.Env = mergeEnv(os.Environ(), env...)
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

// etcDir is where accounts are looked up.
func (a *Agent) etcDir() string {
	if a.opts.EtcDir == "" {
		return "/etc"
	}
	return a.opts.EtcDir
}

// identity resolves an account the way Docker resolves USER. A process
// that is already that uid and gid needs no switch, so it gets none.
func (a *Agent) identity(spec string) (identity, error) {
	id, err := resolveUser(spec, a.etcDir())
	if err != nil {
		return id, err
	}
	if c := id.cred; c != nil && int(c.Uid) == os.Geteuid() && int(c.Gid) == os.Getegid() {
		id.cred = nil
	}
	return id, nil
}

// mergeEnv returns base with each layer's variables set on top, later
// layers winning.
func mergeEnv(base []string, layers ...map[string]string) []string {
	set := map[string]string{}
	for _, layer := range layers {
		for k, v := range layer {
			set[k] = v
		}
	}
	out := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; !ok {
			out = append(out, kv)
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+set[k])
	}
	return out
}

// Workload returns the running workload process, or nil.
func (a *Agent) Workload() *os.Process {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workload
}

func (a *Agent) Exec(req *gav1.ExecRequest, stream gav1.GuestAgent_ExecServer) error {
	ctx := stream.Context()
	if err := a.authorize(ctx); err != nil {
		return err
	}
	cmd, err := a.execCommand(req)
	if err != nil {
		return err
	}

	outR, outW, err := os.Pipe()
	if err != nil {
		return status.Errorf(codes.Internal, "pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return status.Errorf(codes.Internal, "pipe: %v", err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	exited, err := start(cmd)
	// The child holds its own copies of the write ends; close ours so the
	// reads below end when the child (and anything it spawned) closes them.
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return status.Errorf(codes.InvalidArgument, "start command: %v", err)
	}
	pid := cmd.Process.Pid
	// The shell leads its own process group, and everything it starts
	// stays in it unless it moves out on purpose (setsid). Killing the group
	// kills them all; killing only the shell would leave its children running.
	//
	// In theory, once every member has exited and only a process that left
	// the group (setsid) still holds the pipes, the pgid could be reused by
	// an unrelated new group before the kill. That needs the pid counter to
	// wrap around within the timeout, so it is ignored.
	killGroup := func() { syscall.Kill(-pid, syscall.SIGKILL) }
	if err := stream.Send(&gav1.ExecEvent{Event: &gav1.ExecEvent_Started{Started: &gav1.Started{Pid: int32(pid)}}}); err != nil {
		killGroup()
		outR.Close()
		errR.Close()
		return err
	}

	out := newOutput(outR, errR, req.OutputHeadBytes, req.OutputTailBytes)
	defer out.close()

	var timeout, drain <-chan time.Time
	if req.TimeoutMs > 0 {
		t := time.NewTimer(time.Duration(req.TimeoutMs) * time.Millisecond)
		defer t.Stop()
		timeout = t.C
	}
	var ws syscall.WaitStatus
	timedOut := false
	// The Exec ends when the shell has exited and the output has ended. A
	// background child that keeps stdout or stderr open keeps it going until
	// the timeout.
	for leaderDone, outputDone := false, false; !leaderDone || !outputDone; {
		select {
		case c, ok := <-out.chunks:
			if !ok {
				outputDone = true
				out.chunks = nil
				continue
			}
			if err := stream.Send(c.event()); err != nil {
				killGroup()
				return err
			}
		case ws = <-exited:
			leaderDone = true
			exited = nil
		case <-timeout:
			timedOut, timeout = true, nil
			killGroup()
			drain = time.After(time.Second)
		case <-drain:
			// Something that left the group still holds the pipes.
			out.close()
		case <-ctx.Done():
			// The client went away (the Host data proxy cancels the call
			// when its HTTP client disconnects).
			killGroup()
			return status.FromContextError(ctx.Err()).Err()
		}
	}
	stdout, stderr := out.tails[0], out.tails[1]
	return stream.Send(&gav1.ExecEvent{Event: &gav1.ExecEvent_Exited{Exited: &gav1.Exited{
		ExitCode:           int32(exitCode(ws)),
		TimedOut:           timedOut,
		StdoutTruncated:    stdout.omitted > 0,
		StderrTruncated:    stderr.omitted > 0,
		StdoutTail:         stdout.tail,
		StderrTail:         stderr.tail,
		StdoutOmittedBytes: stdout.omitted,
		StderrOmittedBytes: stderr.omitted,
	}}})
}

// execCommand builds the Exec's process: /bin/sh -c as the requested
// account (`user` by default, ADR 0009), in its working directory, with the
// image's, the Sandbox's and the Exec's environment layered in that order.
func (a *Agent) execCommand(req *gav1.ExecRequest) (*exec.Cmd, error) {
	user := req.User
	if user == "" {
		user = DefaultUser
	}
	id, err := a.identity(user)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	cwd := req.Cwd
	if cwd == "" {
		cwd = "/"
		if fi, err := os.Stat(id.home); err == nil && fi.IsDir() {
			cwd = id.home
		}
	}
	a.mu.Lock()
	sandboxEnv := a.env
	a.mu.Unlock()

	cmd := exec.Command("/bin/sh", "-c", req.Command)
	// The child changes into Dir after it has switched to the account, so
	// the directory is resolved with that account's permissions.
	cmd.Dir = cwd
	cmd.Env = mergeEnv(os.Environ(), map[string]string{"HOME": id.home}, sandboxEnv, req.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: id.cred, Setpgid: true}
	return cmd, nil
}

// chunk is a piece of one output stream.
type chunk struct {
	stderr bool
	data   []byte
}

func (c chunk) event() *gav1.ExecEvent {
	if c.stderr {
		return &gav1.ExecEvent{Event: &gav1.ExecEvent_Stderr{Stderr: c.data}}
	}
	return &gav1.ExecEvent{Event: &gav1.ExecEvent_Stdout{Stdout: c.data}}
}

// output reads an Exec's stdout and stderr into chunks, which is closed once
// both have ended. With a cap (head or tail > 0), each stream sends only its
// first head bytes as chunks and keeps the last tail bytes of the rest in
// tails, which are complete once chunks is closed; the middle is read and
// dropped.
type output struct {
	chunks    chan chunk
	files     [2]*os.File
	tails     [2]tail
	quit      chan struct{}
	closeOnce sync.Once
}

// tail is what a capped stream kept after its head.
type tail struct {
	tail    []byte
	omitted int64
}

func newOutput(stdout, stderr *os.File, head, tail int64) *output {
	o := &output{chunks: make(chan chunk), files: [2]*os.File{stdout, stderr}, quit: make(chan struct{})}
	var wg sync.WaitGroup
	for i, f := range o.files {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o.read(f, i == 1, head, tail, &o.tails[i])
		}()
	}
	chunks := o.chunks
	go func() { wg.Wait(); close(chunks) }()
	return o
}

func (o *output) read(f *os.File, stderr bool, head, tailMax int64, kept *tail) {
	capped := head > 0 || tailMax > 0
	var sent, after int64
	headFull := head == 0
	// rest holds the latest output after the head, trimmed to tailMax now
	// and then, so it never grows past twice that plus one read.
	var rest []byte
	defer func() {
		if !capped {
			return
		}
		if int64(len(rest)) > tailMax {
			rest = rest[int64(len(rest))-tailMax:]
		}
		omitted := after - int64(len(rest))
		if omitted > 0 {
			// The tail may start inside a character whose start was dropped.
			trimmed := utf8cut.TrimPartialStart(rest)
			omitted += int64(len(rest) - len(trimmed))
			rest = trimmed
		}
		kept.tail, kept.omitted = append([]byte(nil), rest...), omitted
	}()
	for {
		buf := make([]byte, 32<<10)
		n, err := f.Read(buf)
		data := buf[:n]
		if capped && !headFull && len(data) > 0 {
			h := data
			if room := head - sent; int64(len(h)) >= room {
				// The head ends in this read, on a character boundary.
				h, _ = utf8cut.SplitIncomplete(h[:room])
				headFull = true
			}
			if !o.send(chunk{stderr: stderr, data: h}) {
				return
			}
			sent += int64(len(h))
			data = data[len(h):]
		}
		if capped && headFull && len(data) > 0 {
			after += int64(len(data))
			rest = append(rest, data...)
			if int64(len(rest)) > 2*tailMax {
				rest = append(rest[:0], rest[int64(len(rest))-tailMax:]...)
			}
		} else if !capped && len(data) > 0 {
			if !o.send(chunk{stderr: stderr, data: data}) {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (o *output) send(c chunk) bool {
	if len(c.data) == 0 {
		return true
	}
	select {
	case o.chunks <- c:
		return true
	case <-o.quit:
		return false
	}
}

// close stops the readers and closes the read ends, so a reader blocked on a
// pipe some escaped process still holds returns too.
func (o *output) close() {
	o.closeOnce.Do(func() {
		close(o.quit)
		for _, f := range o.files {
			f.Close()
		}
	})
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
