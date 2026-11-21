// Command controlplane runs the Control Plane: the public REST API, backed by
// SQLite, calling the Host Agent over its unix socket.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gpubox/internal/controlplane"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "TCP address of the public API")
	db := flag.String("db", "controlplane.db", "SQLite database path")
	socket := flag.String("hostagent-socket", filepath.Join(os.TempDir(), "gpubox", "hostagent.sock"), "the Host Agent's unix socket")
	timeout := flag.Duration("create-timeout", 60*time.Second, "how long POST /v1/sandboxes waits for Running")
	flag.Parse()

	cp, err := controlplane.Start(controlplane.Config{
		Addr: *addr, DBPath: *db, HostAgentSocket: *socket, CreateTimeout: *timeout,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("controlplane: public API on http://%s", cp.Addr())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cp.Stop()
}
