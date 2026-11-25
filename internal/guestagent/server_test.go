package guestagent_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
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

// startGuestAgent serves a Guest Agent on a fresh unix socket and returns a
// client connected to it, the way the Host Agent reaches it through the bridge.
func startGuestAgent(t *testing.T) gav1.GuestAgentClient {
	t.Helper()
	// t.TempDir() paths can exceed the 104-byte unix socket limit on macOS.
	dir, err := os.MkdirTemp("", "ga")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ga.sock")

	agent := guestagent.New(guestagent.Options{})
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

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestExecBeforeInitIsRefused(t *testing.T) {
	ga := startGuestAgent(t)

	_, err := ga.Exec(ctx(t), &gav1.ExecRequest{Command: "echo hi"})

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

	res, err := ga.Exec(withToken(ctx(t), "t1"), &gav1.ExecRequest{Command: "echo hi; echo oops >&2; exit 3"})
	if err != nil {
		t.Fatal(err)
	}

	if string(res.Stdout) != "hi\n" || string(res.Stderr) != "oops\n" || res.ExitCode != 3 {
		t.Fatalf("got stdout=%q stderr=%q exit=%d, want \"hi\\n\" \"oops\\n\" 3", res.Stdout, res.Stderr, res.ExitCode)
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
		_, err := ga.Exec(c, &gav1.ExecRequest{Command: "echo hi"})
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

	_, err := ga.Exec(withToken(ctx(t), "t1"), &gav1.ExecRequest{Command: "echo hi"})

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

func TestExecOutputIsCappedAndMarkedTruncated(t *testing.T) {
	ga := startGuestAgent(t)
	if _, err := ga.Init(ctx(t), &gav1.InitRequest{Token: "t1"}); err != nil {
		t.Fatal(err)
	}

	// 1.5 MiB on stdout: more than the cap, so the rest must be dropped while
	// the command still runs to completion.
	res, err := ga.Exec(withToken(ctx(t), "t1"), &gav1.ExecRequest{Command: "head -c 1572864 /dev/zero; echo small >&2"})
	if err != nil {
		t.Fatal(err)
	}

	if len(res.Stdout) != guestagent.MaxOutputBytes || !res.StdoutTruncated {
		t.Errorf("stdout: %d bytes, truncated=%v; want %d, true", len(res.Stdout), res.StdoutTruncated, guestagent.MaxOutputBytes)
	}
	if string(res.Stderr) != "small\n" || res.StderrTruncated || res.ExitCode != 0 {
		t.Errorf("stderr=%q truncated=%v exit=%d; want \"small\\n\", false, 0", res.Stderr, res.StderrTruncated, res.ExitCode)
	}
}
