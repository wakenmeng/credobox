package guestagent_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	gav1 "gpubox/gen/guestagent/v1"
	"gpubox/internal/guestagent"
)

// sandboxFS is the fake image filesystem an in-process Guest Agent sees: an
// /etc with the `user` account mapped to whoever runs the tests (a switch to
// any other uid needs root), and that account's home.
type sandboxFS struct {
	etc  string
	home string
}

func newSandboxFS(t *testing.T) sandboxFS {
	t.Helper()
	fs := sandboxFS{etc: t.TempDir(), home: t.TempDir()}
	uid, gid := os.Getuid(), os.Getgid()
	passwd := fmt.Sprintf("root:x:0:0:root:/root:/bin/sh\nuser:x:%d:%d::%s:/bin/sh\n", uid, gid, fs.home)
	group := fmt.Sprintf("root:x:0:\nuser:x:%d:\n", gid)
	for name, body := range map[string]string{"passwd": passwd, "group": group} {
		if err := os.WriteFile(filepath.Join(fs.etc, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return fs
}

// startGuestAgent serves a Guest Agent on a fresh unix socket and returns a
// client connected to it, the way the Host Agent reaches it through the bridge.
func startGuestAgent(t *testing.T) gav1.GuestAgentClient {
	t.Helper()
	return startGuestAgentOn(t, newSandboxFS(t))
}

func startGuestAgentOn(t *testing.T, fs sandboxFS) gav1.GuestAgentClient {
	t.Helper()
	// t.TempDir() paths can exceed the 104-byte unix socket limit on macOS.
	dir, err := os.MkdirTemp("", "ga")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ga.sock")

	agent := guestagent.New(guestagent.Options{EtcDir: fs.etc})
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	go agent.Serve(lis)
	t.Cleanup(agent.Stop)

	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return gav1.NewGuestAgentClient(conn)
}

// execResult is everything one Exec sent back.
type execResult struct {
	pid            int32
	stdout, stderr []byte
	exited         *gav1.Exited
}

// runExec runs an Exec to its end and collects its events.
func runExec(c context.Context, ga gav1.GuestAgentClient, req *gav1.ExecRequest) (execResult, error) {
	var res execResult
	stream, err := ga.Exec(c, req)
	if err != nil {
		return res, err
	}
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			if res.exited == nil {
				return res, fmt.Errorf("stream ended without Exited")
			}
			return res, nil
		}
		if err != nil {
			return res, err
		}
		switch e := ev.Event.(type) {
		case *gav1.ExecEvent_Started:
			res.pid = e.Started.Pid
		case *gav1.ExecEvent_Stdout:
			res.stdout = append(res.stdout, e.Stdout...)
		case *gav1.ExecEvent_Stderr:
			res.stderr = append(res.stderr, e.Stderr...)
		case *gav1.ExecEvent_Exited:
			res.exited = e.Exited
		}
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestExecBeforeInitIsRefused(t *testing.T) {
	ga := startGuestAgent(t)

	_, err := runExec(ctx(t), ga, &gav1.ExecRequest{Command: "echo hi"})

	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Exec before Init: got %v, want FailedPrecondition", err)
	}
}

func withToken(c context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(c, "authorization", "Bearer "+token)
}

func TestExecWithInitTokenRunsTheCommand(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}

	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: "echo hi; echo oops >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}

	if string(res.stdout) != "hi\n" || string(res.stderr) != "oops\n" || res.exited.ExitCode != 3 || res.exited.TimedOut {
		t.Fatalf("got stdout=%q stderr=%q exited=%v, want \"hi\\n\" \"oops\\n\" exit 3", res.stdout, res.stderr, res.exited)
	}
}

func TestExecWithoutInitTokenIsRefused(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]context.Context{
		"no token":    ctx(t),
		"wrong token": withToken(ctx(t), "not-t1"),
	} {
		_, err := runExec(c, ga, &gav1.ExecRequest{Command: "echo hi"})
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: got %v, want Unauthenticated", name, err)
		}
	}
}

func TestTokenFromPreviousInitIsRefused(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}
	// A new incarnation (a restore, later a Fork) is Init-ed again by the
	// Host Agent, which proves itself with the current token.
	if _, err := ga.Init(withToken(ctx(t), "t1"), &gav1.InitRequest{Token: "t2"}); err != nil {
		t.Fatal(err)
	}

	_, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: "echo hi"})

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Exec with the previous token: got %v, want Unauthenticated", err)
	}
}

func TestReInitWithoutCurrentTokenIsRefused(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}

	// Code inside the Sandbox that reaches the socket must not be able to
	// replace the token with one it knows.
	_, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "attacker"})

	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("re-Init without the current token: got %v, want Unauthenticated", err)
	}
}

func TestBufferedExecOutputIsCappedAndMarkedTruncated(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}

	// 1.5 MiB on stdout: more than the cap, so the rest must be dropped while
	// the command still runs to completion.
	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{
		Command: "head -c 1572864 /dev/zero; echo small >&2", MaxOutputBytes: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.stdout) != 1<<20 || !res.exited.StdoutTruncated {
		t.Errorf("stdout: %d bytes, truncated=%v; want %d, true", len(res.stdout), res.exited.StdoutTruncated, 1<<20)
	}
	if string(res.stderr) != "small\n" || res.exited.StderrTruncated || res.exited.ExitCode != 0 {
		t.Errorf("stderr=%q exited=%v; want \"small\\n\", not truncated, exit 0", res.stderr, res.exited)
	}
}

// initT Inits ga with token t1 and the given Sandbox default environment.
func initT(t *testing.T, ga gav1.GuestAgentClient, env map[string]string) {
	t.Helper()
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1", Env: env}); err != nil {
		t.Fatal(err)
	}
}

func TestExecRunsAsUserInItsHomeByDefault(t *testing.T) {
	fs := newSandboxFS(t)
	ga := startGuestAgentOn(t, fs)
	initT(t, ga, nil)

	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: `pwd -P; echo "$HOME"`})
	if err != nil {
		t.Fatal(err)
	}

	home, _ := filepath.EvalSymlinks(fs.home)
	if want := home + "\n" + fs.home + "\n"; string(res.stdout) != want {
		t.Fatalf("got %q, want the `user` account's home as cwd and HOME: %q", res.stdout, want)
	}
}

func TestExecEnvironmentLayersImageThenSandboxDefaultsThenExec(t *testing.T) {
	// The Guest Agent's own environment stands in for the image's Env.
	t.Setenv("LAYER_IMAGE", "image")
	t.Setenv("LAYER_ALL", "image")
	ga := startGuestAgent(t)
	initT(t, ga, map[string]string{"LAYER_SANDBOX": "sandbox", "LAYER_ALL": "sandbox", "LAYER_EXEC": "sandbox"})

	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{
		Command: `echo "$LAYER_IMAGE $LAYER_SANDBOX $LAYER_EXEC $LAYER_ALL"`,
		Env:     map[string]string{"LAYER_EXEC": "exec", "LAYER_ALL": "exec"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(res.stdout) != "image sandbox exec exec\n" {
		t.Fatalf("got %q, want \"image sandbox exec exec\\n\"", res.stdout)
	}
}

func TestExecInAnExplicitWorkingDirectory(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)
	dir, _ := filepath.EvalSymlinks(t.TempDir())

	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: "pwd -P", Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}

	if string(res.stdout) != dir+"\n" {
		t.Fatalf("got %q, want %q", res.stdout, dir+"\n")
	}
}

func TestExecWithAnUnknownUserOrMissingDirectoryIsRefused(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)

	for name, req := range map[string]*gav1.ExecRequest{
		"unknown user":      {Command: "true", User: "nobody-here"},
		"missing directory": {Command: "true", Cwd: "/no/such/dir"},
	} {
		_, err := runExec(withToken(ctx(t), "t1"), ga, req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: got %v, want InvalidArgument", name, err)
		}
	}
}

// assertGone waits up to 5 s for pid to be gone. A killed process can linger
// briefly as a zombie until its new parent reaps it.
func assertGone(t *testing.T, what string, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Errorf("%s (pid %d) is still running", what, pid)
}

// A shell with a background child: both are in the Exec's process group.
const shellWithChild = "sleep 30 & echo $!; wait"

func TestExecTimeoutKillsTheWholeProcessGroupAndSaysSo(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)

	began := time.Now()
	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: shellWithChild, TimeoutMs: 300})
	if err != nil {
		t.Fatal(err)
	}

	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("Exec took %s, want about the 300 ms timeout", took)
	}
	if !res.exited.TimedOut || res.exited.ExitCode != 128+int32(syscall.SIGKILL) {
		t.Errorf("exited=%v, want timed_out and exit code 137", res.exited)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(res.stdout)))
	if err != nil {
		t.Fatalf("child pid: %q", res.stdout)
	}
	assertGone(t, "the shell", int(res.pid))
	assertGone(t, "its background child", child)
}

func TestCancelledExecKillsTheWholeProcessGroup(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)
	c, cancel := context.WithCancel(withToken(ctx(t), "t1"))
	defer cancel()

	stream, err := ga.Exec(c, &gav1.ExecRequest{Command: shellWithChild})
	if err != nil {
		t.Fatal(err)
	}
	var shell int32
	var out []byte
	for !strings.Contains(string(out), "\n") {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		shell = max(shell, ev.GetStarted().GetPid())
		out = append(out, ev.GetStdout()...)
	}
	// The client goes away mid-Exec, as when an HTTP client disconnects.
	cancel()

	child, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	assertGone(t, "the shell", int(shell))
	assertGone(t, "its background child", child)
}

func TestStreamedExecHasNoOutputCap(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)

	// 3 MiB, three times the buffered cap; MaxOutputBytes 0 is streaming.
	res, err := runExec(withToken(ctx(t), "t1"), ga, &gav1.ExecRequest{Command: "head -c 3145728 /dev/zero"})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.stdout) != 3<<20 || res.exited.StdoutTruncated {
		t.Fatalf("got %d bytes, truncated=%v; want all %d", len(res.stdout), res.exited.StdoutTruncated, 3<<20)
	}
}

func TestExecOutputArrivesWhileTheCommandRuns(t *testing.T) {
	ga := startGuestAgent(t)
	initT(t, ga, nil)

	stream, err := ga.Exec(withToken(ctx(t), "t1"), &gav1.ExecRequest{Command: "echo first; sleep 1; echo second"})
	if err != nil {
		t.Fatal(err)
	}
	var firstAt, exitedAt time.Time
	for exitedAt.IsZero() {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if string(ev.GetStdout()) == "first\n" {
			firstAt = time.Now()
		}
		if ev.GetExited() != nil {
			exitedAt = time.Now()
		}
	}

	if firstAt.IsZero() || exitedAt.Sub(firstAt) < 800*time.Millisecond {
		t.Fatalf("\"first\" arrived %s before the end, want about 1 s (while the command was still running)", exitedAt.Sub(firstAt))
	}
}
