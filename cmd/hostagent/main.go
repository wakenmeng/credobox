// Command hostagent runs the Host Agent with the Docker Runtime: the HostAgent
// gRPC service on a unix socket for the Control Plane, and the Host data
// proxy for clients.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gpubox/internal/hostagent"
	"gpubox/internal/runtime/docker"
)

func main() {
	socket := flag.String("socket", filepath.Join(os.TempDir(), "gpubox", "hostagent.sock"), "unix socket for the HostAgent gRPC service")
	dataAddr := flag.String("data-addr", "127.0.0.1:8081", "TCP address of the Host data proxy")
	gaDir := flag.String("guestagent-dir", "bin/linux", "directory holding the linux guestagent binary (bind-mounted read-only into every Sandbox)")
	flag.Parse()

	dir, err := filepath.Abs(*gaDir)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "guestagent")); err != nil {
		log.Fatalf("no guestagent binary in %s (run `make guestagent`): %v", dir, err)
	}
	ha, err := hostagent.Start(hostagent.Config{
		SocketPath: *socket,
		DataAddr:   *dataAddr,
		Runtime:    docker.New(docker.Config{GuestAgentDir: dir}),
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("hostagent: control socket %s, data proxy %s", *socket, ha.DataEndpoint())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ha.Stop()
}
