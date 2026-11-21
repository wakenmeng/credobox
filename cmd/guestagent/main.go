// Command guestagent is the Guest Agent. The platform bind-mounts it
// read-only into every Sandbox (ADR 0003); the image never contains it.
//
//	guestagent init [-- entrypoint args...]
//	    Run as PID 1: serve the GuestAgent gRPC service on a root-only unix
//	    socket, reap orphans, and start the image's entrypoint after Init.
//	guestagent bridge
//	    Relay stdin/stdout to that socket. The Host Agent runs this through
//	    one long-lived `docker exec -i` per Sandbox (ADR 0002).
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpubox/internal/guestagent"
)

// SocketPath is the Guest Agent's socket, on a mode-0700 tmpfs.
const SocketPath = "/run/gpubox/guestagent.sock"

func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: guestagent init [-- cmd...] | guestagent bridge")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = runInit(workloadArgs(os.Args[2:]))
	case "bridge":
		err = runBridge()
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		log.Fatalf("guestagent %s: %v", os.Args[1], err)
	}
}

func workloadArgs(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

func runInit(workload []string) error {
	if os.Getpid() != 1 {
		log.Printf("guestagent: warning: running as pid %d, not PID 1", os.Getpid())
	}
	// Signals: the kernel never delivers a signal to PID 1 unless PID 1 has
	// a handler for it (SIGKILL from outside the container aside), so
	// without these `docker stop` would wait its full timeout. Forward the
	// signal to the image's entrypoint, then exit.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	os.Remove(SocketPath)
	lis, err := net.Listen("unix", SocketPath)
	if err != nil {
		return err
	}
	// Root-only: an ordinary user in the Sandbox can't even connect.
	if err := os.Chmod(SocketPath, 0o600); err != nil {
		return err
	}
	agent := guestagent.New(guestagent.Options{Workload: workload})
	go func() {
		sig := <-signals
		if p := agent.Workload(); p != nil {
			p.Signal(sig)
		}
		agent.Stop()
		os.Exit(0)
	}()
	return agent.Serve(lis)
}

// runBridge relays stdin and stdout to the Guest Agent's socket until either
// side closes. It waits for the socket to appear, because the Host Agent
// dials as soon as the container has started.
func runBridge() error {
	var conn net.Conn
	var err error
	for deadline := time.Now().Add(30 * time.Second); ; {
		conn, err = net.Dial("unix", SocketPath)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	done := make(chan struct{}, 2)
	go func() { io.Copy(conn, os.Stdin); done <- struct{}{} }()
	go func() { io.Copy(os.Stdout, conn); done <- struct{}{} }()
	<-done
	return nil
}
